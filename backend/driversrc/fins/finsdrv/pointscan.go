// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"iot-gateway/driver"
)

// ==================== 点位范围扫描 ====================
//
// 对一台**已存在的设备**扫一段内存区，把读得到的地址列出来。
//
// **这个功能产出的是一份「候选清单」，不是这台设备的点位表。** FINS 没有任何
// 标准手段能枚举 PLC 上有哪些点位，所以这里做的是「在这个范围里，哪些地址读得到」——
// 而"读得到"与"有意义"是两回事：
//
//   - 非零的地址不一定有意义（可能是标定表、计数器、别人的数据）；
//   - 读得到 0 的地址可能恰恰是有效值（还没开机的状态、本来就应该为 0 的量）。
//
// 因此插件**不做零值过滤**（那是前端的一枚开关）：在所有地址都是 0 的设备上，
// 插件侧过滤会返回一份空列表，用户据此得出"这里没有点位"——与"没扫到"同型的错误。
//
// **绝不走 drv.Read。** 那条路是为**采集**设计的：它按点位的 DataType 算区间、
// 按采集语义回填结果（缺失点位一律 Quality=0），遇到网络错误还会向上返回让采集
// 引擎判连。这里要的是"读这一片字"，只能取驱动自己的传输客户端直接 Read ——
// 与 s7drv / modbusdrv 的 scanSessionOf 是同一条取舍。
//
// 与 Modbus 的一处差异：FINS 没有"配置模式"（地址的含义不由 protocol_json 决定，
// MergeWindow/MaxReadWords/StringLen 只影响采集侧的区间规划），所以这里**没有**
// 那条 errConfigModeNotScannable 守卫。这是协议属性决定的，不是疏忽。

// 点位扫描的规模上限。
const (
	// pointScanMaxRequests 单次扫描最多发多少次读取请求。
	//
	// 存在的意义是**给"整段都读不到"的设备一个确定性的上界**：对半降级在一段
	// 完全读不到的区间上会退化成一棵满二叉树，最坏 2n-1 次请求，光靠 deadline
	// 止损会让耗时随网络延迟漂移（本地 PLC 上几百次请求可能只要几百毫秒，
	// 于是照样跑完，而在慢链路上又跑不完）。触顶时置 Incomplete 并返回已得结果。
	pointScanMaxRequests = 512

	// pointScanMaxPoints 单次扫描最多返回多少个点位。
	//
	// 上限存在的意义不是性能而是**事故半径**：pluginwire 写帧持插件进程的写锁，
	// 一份几百 KB 的应答会把该进程上所有协议的应答一起头阻塞。2000 个点位
	// （每个地址一行、值几十字节）是几十 KB 的量级，一次能过管道。
	//
	// 与 PointDiscovery().MaxPoints 必须一致 —— 后者只是把它报给网关与前端，
	// 由 driversdk.Meta.Validate 校验落在 [1,20000] 内。
	pointScanMaxPoints = 2000

	// pointScanZeroNote 值为 0 的点位带的说明。
	//
	// 判据是**原始字节全 0**（位模式则是**该位**为 0），不是解析后的值：
	// 假设类型是 DWord 时 0x00000001 会解成一个极小的非零数，按解析值判"零"
	// 会漏掉它，"显示 0 值"这枚开关也就漏了点。
	pointScanZeroNote = "当前值为 0"
)

// pointScanForm FINS 的点位扫描参数表单（form-create schema）。
//
// 文案要把两件用户最容易误解的事说在前面：
//  1. 扫出来的是**候选清单**，不是设备的点位表；
//  2. 扫到内存区末尾就会停 —— 而"停在哪里"本身是这份结果里最有用的信息之一。
const pointScanForm = `{
  "rule": [
    {"type":"select","field":"area","title":"内存区","$required":false,"value":"DM",
     "info":"决定读哪一片地址空间，也决定生成的地址写法：DM 为 D100，CIO 为 CIO100，WR 为 W100，HR 为 H100",
     "options":[{"label":"DM 数据内存（D）","value":"DM"},{"label":"CIO I/O 及内部继电器（CIO）","value":"CIO"},
                {"label":"WR 工作区（W）","value":"WR"},{"label":"HR 保持区（H）","value":"HR"}]},
    {"type":"inputNumber","field":"startWord","title":"起始字地址","$required":true,"value":0,
     "info":"0-based 字地址，与点位地址写法一致（起始 100 对应 D100）。既不是 1-based，也不带区前缀"},
    {"type":"select","field":"width","title":"粒度","$required":false,"value":"word",
     "info":"每个点位读多少字，也决定生成的地址写法：位模式下每个字展开成 16 个点位（D100.00 ~ D100.15）",
     "options":[{"label":"位 BOOL（D100.05）","value":"bit"},{"label":"字 WORD（D100）","value":"word"},
                {"label":"双字 DWORD（D100，占 D100~D101）","value":"dword"}]},
    {"type":"inputNumber","field":"count","title":"点位数","$required":true,"value":64,
     "info":"要扫多少个点位（位模式下即位数）。数量越大耗时可观：整段扫描有预算与请求数两条上限，触顶会返回已完成的部分并把「未扫完」标出来。读到该内存区末尾时会自动停下，后面的地址一律标为读不到"}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// PointDiscovery 返回 FINS 的点位扫描能力声明，供 main.go 填进 Meta.Protocols。
//
// **UDP 与 TCP 共用同一份**：这个功能走的地址解析、区域码与解码与传输层无关，
// 两边的表单、预算、上限因此逐字相同。串口与 HostLinkTCP 不声明它 ——
// 它们复用会话的路径不一样（见 specs/point-discovery.md §5 的回退表），
// 在没有真机验证之前不把这条能力挂上去。
func PointDiscovery() *driver.PointDiscoverySpec {
	return &driver.PointDiscoverySpec{
		Title: "扫描 FINS 内存区",
		Description: "按内存区扫一段字地址，列出**读得到**的地址与当前值。" +
			"设备不会告诉你它有哪些点位：非零的地址不一定有意义，读得到 0 的也可能是有效值，" +
			"没列出来的地址既可能不存在、也可能只是这一段被设备拒绝了。" +
			"读到一个地址读不到为止（同一内存区的地址是连续的），之后的地址一律按读不到计。" +
			"请把结果当作候选清单，而不是点位表",
		FormJSON: json.RawMessage(pointScanForm),
		// "Word" 是 TypeMap 的键（通用类型名）—— 写成内部裸名 "word" 会被
		// Meta.Validate 拒掉。字是最常见的 FINS 点位粒度，故用作下拉初值。
		DefaultCommonDataType: driver.TypeWord,
		// 20000ms 是留给"大范围 + 慢链路"的空间，但注意**网关侧还会把它钳到
		// 7000ms**（pluginhost 的 pointScanGatewayBudgetCapMs，因为这条链路是
		// 同步 HTTP，超时会拖住网关停机）。实际生效的总是被钳过的那一个。
		MaxBudgetMs: 20000,
		MaxPoints:   pointScanMaxPoints,
	}
}

// scanWidth 点位粒度：每个点位占多少字，决定地址后缀与解码类型。
type scanWidth string

const (
	widthBit   scanWidth = "bit"   // D100.05，1 位（16 个点位共用 1 个字）
	widthWord  scanWidth = "word"  // D100，1 字
	widthDWord scanWidth = "dword" // D100，2 字（D100~D101）
)

// unitWords 一个点位占多少字。位模式返回 1：它占的是**所在的那一个字**，
// 16 个位点位共用它，因此位模式的块划分走 pointsFor/wordsFor 两条互逆函数，
// 不能直接用这个值乘除。
func (w scanWidth) unitWords() int {
	if w == widthDWord {
		return 2
	}
	return 1
}

// wordSpan 覆盖点位 [start, start+n) 需要的字区间：相对起点的偏移与字数。
//
// **不能用 "start 字的偏移 + n 折算出的字数" 分别算**：位模式下对半降级会把
// 起点切到字中间（例如从第 5 位开始扫 8 个位），此时 "idx×unitWords" 算出来的
// 偏移落在别的字上，读到的是**别处**的数据 —— 而值看着完全合理，用户不会发现。
func (w scanWidth) wordSpan(start, n int) (offset, words int) {
	if n <= 0 {
		return 0, 0
	}
	if w == widthBit {
		first := start / 16
		last := (start + n - 1) / 16
		return first, last - first + 1
	}
	ub := w.unitWords()
	return start * ub, n * ub
}

// pointsFor words 个字里有多少个点位。与 wordSpan 互逆（向下取整）。
func (w scanWidth) pointsFor(words int) int {
	if words <= 0 {
		return 0
	}
	if w == widthBit {
		return words * 16
	}
	return words / w.unitWords()
}

// internalType 解码用的**内部类型裸名**（本包注册进 TypeRegistry 的名字）。
func (w scanWidth) internalType() string {
	switch w {
	case widthBit:
		return "bool"
	case widthDWord:
		return "uint32"
	default:
		return "word"
	}
}

// commonType 结果里回给前端的**通用类型名**（TypeMap 的 key）。
func (w scanWidth) commonType() string {
	switch w {
	case widthBit:
		return driver.TypeBoolean
	case widthDWord:
		return driver.TypeDWord
	default:
		return driver.TypeWord
	}
}

// areaPrefix 内存区的地址写法前缀。
//
// 与前端 utils/addressSyntax.js 里 fins 规格的示例（D100 / CIO0.00 / W1.2）
// 保持一致 —— 那份文案是用户填地址时唯一的信息来源，两边不一致会让
// "扫出来的和我平时写的不一样"变成一个没有答案的疑问。
//
// DM 用短写 "D" 而不是 "DM"：两者都能被 ParseFINSAddress 解析回来，
// 但 D100 是欧姆龙手册与现场习惯里更常见的写法，也更短。
func areaPrefix(area finsArea) string {
	switch area {
	case AreaDM:
		return "D"
	case AreaCIO:
		return "CIO"
	case AreaWR:
		return "W"
	case AreaHR:
		return "H"
	default:
		return "D"
	}
}

// pointScanOptions 解析后的扫描参数。
type pointScanOptions struct {
	area      finsArea
	startWord uint16 // 起始字地址（0-based）
	width     scanWidth
	count     int // 点位数
}

// pointScanOptionsRaw 扫描选项的原始形态。键名与 pointScanForm 的 field 一一对应。
//
// 数值字段一律用 rawInt：form-create 的 select 控件产出的是**字符串**
// （"word" 那项不算，"64" 那种是），声明成 int 时一份带字符串的选项会让
// json.Unmarshal 整体报错 —— 不是那一个字段失效，而是整次扫描全失败。
type pointScanOptionsRaw struct {
	Area      string `json:"area"`
	StartWord rawInt `json:"startWord"`
	Width     string `json:"width"`
	Count     rawInt `json:"count"`
}

// parsePointScanOptions 解析扫描选项。
//
// 与 scanOptions 的取舍不同：那边缺项时静默回默认值（"没填节点号"就是"按 IP
// 末段推导"），这里**缺失或不合法一律报错**。"扫多少"没有合理的默认值 ——
// 猜一个 64 会让用户拿到一份他并没有要求的清单，而多扫的那部分在慢链路上
// 要花掉好几秒。
func parsePointScanOptions(options json.RawMessage) (pointScanOptions, error) {
	opt := pointScanOptions{area: AreaDM, width: widthWord}
	if len(options) == 0 {
		return opt, errors.New("扫描选项为空：需要指定内存区、起始字地址、粒度与数量")
	}

	var raw pointScanOptionsRaw
	if err := json.Unmarshal(options, &raw); err != nil {
		return opt, fmt.Errorf("解析扫描选项失败: %w", err)
	}

	// 内存区。大小写不敏感：表单给的是大写，protocol_json 里可能存着别的写法。
	switch strings.ToUpper(strings.TrimSpace(raw.Area)) {
	case "", "DM", "D":
		opt.area = AreaDM
	case "CIO":
		opt.area = AreaCIO
	case "WR", "W":
		opt.area = AreaWR
	case "HR", "H":
		opt.area = AreaHR
	default:
		return opt, fmt.Errorf("内存区 %q 不是 DM/CIO/WR/HR 之一", raw.Area)
	}

	if raw.StartWord.set {
		if raw.StartWord.val < 0 || raw.StartWord.val > 65535 {
			return opt, fmt.Errorf("起始字地址 %d 超出 [0,65535]", raw.StartWord.val)
		}
		opt.startWord = uint16(raw.StartWord.val)
	}

	// 粒度。同样大小写不敏感。
	switch scanWidth(strings.ToLower(strings.TrimSpace(raw.Width))) {
	case "":
		opt.width = widthWord
	case widthBit, widthWord, widthDWord:
		opt.width = scanWidth(strings.ToLower(strings.TrimSpace(raw.Width)))
	default:
		return opt, fmt.Errorf("粒度 %q 不是 bit/word/dword 之一", raw.Width)
	}

	if !raw.Count.set || raw.Count.val <= 0 {
		return opt, errors.New("点位数必须大于 0")
	}
	opt.count = raw.Count.val

	// 字地址空间上限是 0xFFFF。用户的 count 越过上限时**截断而不是报错**：
	// 意图很清楚，就是要扫到能扫的末尾 —— 与 s7drv/modbusdrv 里
	// "从某处起扫 N 个"的处理一致。
	if maxN := opt.maxCount(); opt.count > maxN {
		opt.count = maxN
	}
	if opt.count <= 0 {
		return opt, fmt.Errorf("起始字地址 %d 之后已无可用地址空间", opt.startWord)
	}
	return opt, nil
}

// maxCount 起始字地址固定时，本粒度下最多还能扫多少个点位。
func (o pointScanOptions) maxCount() int {
	avail := 65536 - int(o.startWord)
	if o.width == widthBit {
		return avail * 16
	}
	return avail / o.width.unitWords()
}

// pointName 第 idx 个点位的地址写法。
//
// **生成的串必须能被 ParseFINSAddress 反向解析回同一个位置**，这条不变量比它
// 看起来重要得多：一个解析不出来的地址写进 device_address 之后，这个点位永远
// 采不到值（planFor 在规划前就把它拆出去，只报 QualityBadAddress / 报警「地址无效」）。
// 由 pointscan_test.go 的 TestPointNameRoundTrip 逐组合钉住。
func (o pointScanOptions) pointName(idx int) string {
	prefix := areaPrefix(o.area)
	if o.width == widthBit {
		// 位后缀补零到两位（D100.05 而不是 D100.5）：这是欧姆龙手册里
		// 位地址的惯用写法，前端给出的示例（CIO0.00）也是这个形态。
		// 两者 ParseFINSAddress 都收，选哪个不影响正确性，只影响可读性。
		return fmt.Sprintf("%s%d.%02d", prefix, int(o.startWord)+idx/16, idx%16)
	}
	return fmt.Sprintf("%s%d", prefix, int(o.startWord)+idx*o.width.unitWords())
}

// describe 把 [start, start+n) 渲染成一句用户看得懂的范围描述，用于中止性错误的文案。
func (o pointScanOptions) describe(start, n int) string {
	if n <= 1 {
		return o.pointName(start)
	}
	return o.pointName(start) + " … " + o.pointName(start+n-1)
}

// SessionAccess 与 driversdk.SessionAccess 同形，但**刻意本地声明而不 import
// driversdk**：与 probe.go 收 target/options/budget 三个原始值（而不是
// driversdk.ProbeRequest）是同一条取舍 —— 协议实现只依赖 driver 这一份契约，
// 不被整套插件开发套件绑住，本包因此也能脱离插件进程单独测试。
type SessionAccess interface {
	Do(fn func(drv driver.Driver) error) error
}

// ScanPoints 扫描一段 FINS 内存区。
//
// sess 是读驱动实例的唯一入口（见 SessionAccess）：**每块读一次 Do**，
// 块与块之间锁是放开的，因此一次扫描不会把采集卡死几十秒。
//
// 返回的 error 与结果**可以同时非 nil**：中止性错误（设备掉线、PLC 报错）发生时，
// 已经扫到的部分一并返回 —— 丢掉它们对用户没有任何好处。
func ScanPoints(sess SessionAccess, options json.RawMessage, deadline time.Time) (*driver.PointScanResult, error) {
	opt, err := parsePointScanOptions(options)
	if err != nil {
		return nil, err
	}

	// 预检：在**开始扫描之前**把"这台设备根本不能扫"判掉 —— 主要是"没有可用连接"。
	// 一次额外的 Do 换一条明确的报错，比让用户拿到一份空清单便宜得多。
	//
	// 解码配置（字节序/字序）也在这里取一次：它在一次扫描期间不会变
	//（改了配置意味着驱动要重连，那已经会让扫描中止），每次读都重取只是噪音。
	//
	// 结论走 preflightErr 而不是 Do 的返回值：那是**设备配置/连接状态**的问题，
	// 不是会话坏了，混在一起会被当成"插件调用失败"报上去，指向完全错误的方向。
	var (
		preflightErr error
		cfg          *FINSConfig
		maxWords     int
	)
	if err := sess.Do(func(drv driver.Driver) error {
		s, err := scanSessionOf(drv)
		if err != nil {
			preflightErr = err
			return nil
		}
		cfg = s.cfg
		maxWords = s.maxReadWords()
		return nil
	}); err != nil {
		return nil, err
	}
	if preflightErr != nil {
		return nil, preflightErr
	}

	read := func(word uint16, words int) ([]byte, error) {
		// 每次现取驱动实例，**不缓存**：热刷新会重建实例、重连会换掉 client，
		// 缓存下来的指针读的是一个已经 Close 的句柄（拿回上一轮的缓存值，
		// 或不巧触发 nil 解引用把整个插件进程带走）。
		var (
			out     []byte
			readErr error
		)
		if err := sess.Do(func(drv driver.Driver) error {
			s, err := scanSessionOf(drv)
			if err != nil {
				readErr = err
				return nil
			}
			// 直接调传输层的 Read，而不是驱动的 Read：见文件头的说明。
			data, err := s.client.Read(opt.area, word, uint16(words))
			if err != nil {
				readErr = err
				return nil
			}
			out = data
			return nil
		}); err != nil {
			return nil, err
		}
		return out, readErr
	}

	return scanWith(read, opt, cfg, maxWords, deadline)
}

// readFn 读取 [word, word+words) 的原始字节（每字 2 字节）。
//
// 这是 pScanner 与"会话"之间唯一的缝：生产路径每次调用都经 SessionAccess.Do
// 重新取一次驱动实例，测试路径直接指向一个假函数。
//
// 之所以需要这条缝：对半降级、请求数上限、连接中断中止、读到内存区末尾这几条
// 路径都要靠"读到某个特定字就报特定错误"来驱动，而假 PLC 只会一律成功 ——
// 没有这条缝，这几条最容易写错的路径就只能靠读代码来确认。
// 与 s7drv 的 readFn 是同一条取舍。
type readFn func(word uint16, words int) ([]byte, error)

// scanWith 是 ScanPoints 的可测形态：读取原语以函数注入。
func scanWith(read readFn, opt pointScanOptions, cfg *FINSConfig, maxWords int, deadline time.Time) (*driver.PointScanResult, error) {
	if cfg == nil {
		cfg = DefaultFINSConfig()
	}
	if maxWords <= 0 {
		maxWords = defaultMaxReadWords
	}
	sc := &pScanner{
		opt:      opt,
		read:     read,
		cfg:      cfg,
		maxWords: maxWords,
		deadline: deadline,
		started:  time.Now(),
		result: &driver.PointScanResult{
			AssumedCommonDataType: opt.width.commonType(),
		},
	}
	return sc.run()
}

// errPointScanStop 内部哨兵：停止扫描，但**不是错误**（达到 MaxPoints / 预算耗尽 /
// 请求数用尽）。
var errPointScanStop = errors.New("fins: point scan stop")

// pScanner 一次扫描的可变状态。
type pScanner struct {
	opt      pointScanOptions
	read     readFn
	cfg      *FINSConfig
	maxWords int // 单次请求最多读多少字（取自设备自己的 maxReadWords）
	deadline time.Time
	started  time.Time
	result   *driver.PointScanResult

	// requests 已发出的读取请求数（含降级途中那些）。
	requests int

	// incomplete 预算耗尽或请求数用尽。与 Truncated 是两回事：那个是"结果条数到上限"，
	// 这个是"范围还没扫完"，前端必须分开显示。
	incomplete bool
}

// run 逐块扫描，直到扫完、到上限或中止。
//
// 返回的 error 与结果**可以同时非 nil**（中止性错误发生时会带上已经扫到的部分）——
// 与 s7drv.ScanPoints 的契约一致，网关侧两者都会透给用户。
func (sc *pScanner) run() (*driver.PointScanResult, error) {
	for done := 0; done < sc.opt.count; {
		if sc.stopped() {
			break
		}
		n := sc.opt.count - done
		if per := sc.itemsPerChunk(); n > per {
			n = per
		}

		hitEnd, resumeAt, err := sc.walk(done, n)
		if err != nil {
			if errors.Is(err, errPointScanStop) {
				break
			}
			// 中止性错误：把已经拿到的部分一并返回 —— 丢掉它们对用户没有任何好处。
			return sc.finish(), err
		}
		if hitEnd {
			// 从 resumeAt 起这个内存区再也读不到了。**后面的地址不再逐个去问**：
			// 同一内存区里的字地址是连续的，越界之后全是越界 —— 逐个探测只会
			// 把一次"扫到末尾"变成几百次必然失败的请求，而结论一模一样。
			//
			// 这批点按"读不到"计而不是"没扫过"：用户需要知道它们**在这个范围里
			// 但读不出来**，否则他会以为范围根本没覆盖到那里。resumeAt 是
			// walk 已经交代过的下一个下标（它自己记到第一个读不到的那个为止），
			// 因此只补记它之后的部分，不然会多算一个。
			rest := sc.opt.count - resumeAt
			sc.result.Scanned += rest
			sc.result.Unreadable += rest
			break
		}
		done += n
	}
	return sc.finish(), nil
}

// stopped 该收工了：结果条数到上限 / 请求数用尽 / 预算耗尽。
//
// 三个上界分开置位（Truncated 与 incomplete 是给用户看的两个不同结论），
// 但都是"停止扫描"，所以合成一个判断放在每块与每次降级之前。
func (sc *pScanner) stopped() bool {
	if sc.result.Truncated || sc.incomplete {
		return true
	}
	if sc.requests >= pointScanMaxRequests {
		sc.incomplete = true
		return true
	}
	if time.Now().After(sc.deadline) {
		sc.incomplete = true
		return true
	}
	return false
}

// itemsPerChunk 一块最多覆盖多少个点位。
//
// 单次请求的字数上限取**设备自己的 maxReadWords**（默认 100）而不是写死一个值：
// 采集时的区间规划用的就是它，它对这台 PLC 是已知能用的；照搬一个更大的值
// 会让扫描在"单请求最大 100 字"的老型号上一片都读不到，而用户完全看不出
// 是扫描参数的问题还是设备的问题。
func (sc *pScanner) itemsPerChunk() int {
	n := sc.opt.width.pointsFor(sc.maxWords)
	if n < 1 {
		n = 1
	}
	return n
}

// walk 扫 [start, start+n) 这段点位，读不通就对半降级。
//
// 返回 (hitEnd, resumeAt, err)：hitEnd 为 true 表示**这个内存区到此为止**，
// resumeAt 是"下一个还没被记账的点位下标"（walk 自己已经产出过 [start, resumeAt)
// 里的可读点、也记过第一个读不到的点，再往后的由 run() 一次性补记）。
//
// 降级的形状是「整段 → 对半 → … → 单点」：FINS 对含"不存在地址"的整个请求会
// **整段拒绝**（结束码非 0），只有一路拆到单点才能把"哪里是末尾"摘出来。
// 驱动的 Read 那条路做不到这件事 —— 它整段失败就把该区间所有点标 Quality=0 了事。
//
// **为什么单点读不到就判定"后面全读不到"而不是继续往下扫**：FINS 的内存区是
// 连续的（DM 从 0 起、CIO 从 0 起，各自一整片），因此"某个字不存在"只可能是
// "已经越过这个区的末尾"。反过来，如果继续逐点去问，一次"count 填大了"的扫描
// 就会在末尾之后发出几百个必然失败的请求，最后撞上请求数上限、给用户一份标着
// "未扫完"的结果 —— 而它其实早就扫完了。
//
// 这条推断成立的前提是 isAddressEndCode 已经把**可恢复的节点/PLC 状态错误**
// 排除在外（它们不代表地址不存在）。两者缺一不可：少了那边的排除，
// 一次"目标节点忙"就会把清单从中间截断。
func (sc *pScanner) walk(start, n int) (hitEnd bool, resumeAt int, err error) {
	if n <= 0 {
		return false, 0, nil
	}
	if sc.stopped() {
		return false, 0, errPointScanStop
	}

	offset, words := sc.opt.width.wordSpan(start, n)
	if words <= 0 {
		// 不该发生（n >= 1 时 wordSpan 的 words 恒 >= 1），真发生了也不能把请求发出去。
		sc.result.Scanned += n
		sc.result.Unreadable += n
		return false, 0, nil
	}
	word := sc.opt.startWord + uint16(offset)
	// 越界不该发生（选项解析已按 0xFFFF 上限截断过 count），真发生了也不能把请求
	// 发出去：超上限的字地址会被回绕，读到的是**别处**的数据，而用户会把它当成
	// 这段地址的真实值 —— 与 s7drv 里那条越界守卫同一条理由。
	if int(word)+words-1 > 65535 {
		sc.result.Scanned += n
		sc.result.Unreadable += n
		return false, 0, nil
	}

	sc.requests++
	data, err := sc.read(word, words)
	if err == nil {
		return false, 0, sc.emit(start, n, word, data)
	}
	if !isAddressEndCode(err) {
		// 链路类错误（超时、EOF、reset、目标节点忙/无响应）或 PLC 自身报错：
		// **中止而不是记成"地址不存在"**。把设备掉线记成"这里没有点位"，
		// 用户会从一份几乎全是空的清单里得出与事实完全相反的结论，
		// 而采集侧此时也已经在重连了 —— 两边的结论会互相矛盾。
		return false, 0, fmt.Errorf("扫描 %s 时读取失败: %w", sc.opt.describe(start, n), err)
	}

	if n == 1 {
		// 拆到单点了还是被拒：这个地址就是读不到，且**这个内存区到此为止**。
		sc.result.Scanned++
		sc.result.Unreadable++
		return true, start + 1, nil
	}
	half := n / 2
	hit, resume, err := sc.walk(start, half)
	if err != nil || hit {
		// 左半边就已经越界：按连续内存区的性质，右半边及其后全都读不到，
		// 整棵右子树一次请求都不用发。
		return hit, resume, err
	}
	return sc.walk(start+half, n-half)
}

// emit 把一次成功读取的结果按点位逐个产出。
//
// 逐个产出而不是整段写进 Points，是为了让 MaxPoints 截断**精确**：达到上限时
// 立刻停手，超出的地址既不计入 Scanned/Readable，也不进列表 ——
// 这样"可读到 M 个"与列表长度始终自洽。
func (sc *pScanner) emit(start, n int, firstWord uint16, data []byte) error {
	for i := 0; i < n; i++ {
		if len(sc.result.Points) >= pointScanMaxPoints {
			sc.result.Truncated = true
			return errPointScanStop
		}
		idx := start + i
		sc.result.Scanned++
		sc.result.Readable++
		sc.result.Points = append(sc.result.Points, sc.buildPoint(idx, firstWord, data))
	}
	return nil
}

// buildPoint 从读取缓冲区里取出第 idx 个点位的值。
func (sc *pScanner) buildPoint(idx int, firstWord uint16, data []byte) driver.DiscoveredPoint {
	name := sc.opt.pointName(idx)

	var (
		raw      []byte
		zero     bool
		addrWord uint16
		bit      = -1
	)
	if sc.opt.width == widthBit {
		// 位点位：地址是它所在的那个字，外加字内的位号（0..15）。
		addrWord = sc.opt.startWord + uint16(idx/16)
		bit = idx % 16
		off := int(addrWord-firstWord) * 2
		if off < 0 || off+2 > len(data) {
			return driver.DiscoveredPoint{Name: name, Note: "值解析失败：读取数据不足"}
		}
		raw = data[off : off+2]
		// 位点位的"零值"是**这一位**为 0，不是整字为 0 ——
		// 同一个字里别的位为 1 不该让这个点位显示成有值。
		zero = finsBitValue(raw, bit) == 0
	} else {
		// 字/双字点位：地址是它起始的那个字，跨度由粒度决定。
		ub := sc.opt.width.unitWords()
		addrWord = sc.opt.startWord + uint16(idx*ub)
		off := int(addrWord-firstWord) * 2
		end := off + ub*2
		if off < 0 || end > len(data) {
			return driver.DiscoveredPoint{Name: name, Note: "值解析失败：读取数据不足"}
		}
		raw = data[off:end]
		zero = allZeroBytes(raw)
	}

	if zero {
		// Value 留空、只给 Note：零值点位在界面上是"没有值"的那一类，
		// 前端据此实现"显示 0 值"这枚开关（插件侧刻意不做过滤）。
		return driver.DiscoveredPoint{Name: name, Note: pointScanZeroNote}
	}

	addr := FINSAddress{Area: sc.opt.area, Word: addrWord, Bit: bit}
	internal := sc.opt.width.internalType()
	v, err := ParseFINSValue(raw, addr, internal, sc.cfg)
	if err != nil {
		// 解析失败不改判"读到了"：地址确实是读到了，只是值渲染不出来。
		return driver.DiscoveredPoint{Name: name, Note: "值解析失败：" + err.Error()}
	}
	return driver.DiscoveredPoint{Name: name, Value: FormatFINSValue(v, internal)}
}

// finish 收尾：填耗时与标志，并把 nil 切片规整成空切片。
func (sc *pScanner) finish() *driver.PointScanResult {
	if sc.incomplete {
		sc.result.Incomplete = true
	}
	sc.result.ElapsedMs = int(time.Since(sc.started).Milliseconds())
	if sc.result.Points == nil {
		// nil 切片过线是 JSON null，前端得为它多写一条分支。
		sc.result.Points = []driver.DiscoveredPoint{}
	}
	return sc.result
}

// allZeroBytes 原始字节是否全为 0。
func allZeroBytes(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// ==================== 错误分类 ====================

// isAddressEndCode 这条错误是否**只关于地址**（而不是节点或链路的状况）。
//
// FINS 的结束码是两字节：高字节是**主码**，说明"哪一类问题"，低字节是细分。
// 这里只按主码分类，而且用的是**排除法**，因为要判定的问题只有一个：
//
//	「这条错误能不能用来推断'这个内存区到此为止'？」
//
// 排除掉的三类恰恰是**可恢复的节点/设备状态**，与地址无关：
//
//	0x01 本机节点错误   （网关侧不在网络上、路由错……）
//	0x02 目标节点错误   （目标节点不在网络上、无此节点、**节点忙**、响应超时）
//	0x03 控制器错误     （PLC 自身处于错误状态）
//
// 拿它们做"到此为止"的推断是危险的：一次偶然的「目标节点忙」会把整份点位清单
// 从中间截断，而后面的地址其实完全正常 —— 用户看到的是一份短了一截、却看不出
// 任何异常的清单。这与 s7drv 里"宁可少扫一段并如实报告，也不要把链路抖动
// 记成地址不存在"是同一条取舍。
//
// 剩下的一律当作"地址问题"（0x04 不可执行 —— 地址范围越界就在这一类，
// 0x21 数据不可更改，以及其它）。**不逐条枚举正确的子码**：主码表是稳定的，
// 而某个具体子码到底是什么含义，各系列手册之间并不完全一致；枚举错了的表现是
// 拒绝对半降级（少扫一段）或者把节点忙当成地址不存在（截断），两种都难查。
func isAddressEndCode(err error) bool {
	var e *finsEndCodeError
	if !errors.As(err, &e) {
		return false
	}
	switch byte(e.code >> 8) {
	case 0x00, 0x01, 0x02, 0x03:
		// 0x00 不会走到这里（结束码为 0 时 parseCommandResponse 已经返回成功）。
		return false
	default:
		return true
	}
}

// ==================== 驱动内部资源的取用 ====================

// scanSession 一次点位扫描要用的驱动内部资源。
//
// 每次 Do 现取、用完即弃（见 ScanPoints 里 read 的注释）。
type scanSession struct {
	client finsTransport
	cfg    *FINSConfig
}

// maxReadWords 单次请求可读的字数上限（设备自己的配置，默认 100）。
func (s *scanSession) maxReadWords() int {
	if s.cfg == nil || s.cfg.MaxReadWords <= 0 {
		return defaultMaxReadWords
	}
	return s.cfg.MaxReadWords
}

// scanSessionOf 从驱动实例取出扫描要用的客户端。
//
// 用类型开关而不是给 driver.Driver 加方法：那会要求全部协议都实现它，
// 而这里只有 FINS 需要 —— 与 s7drv.scanSessionOf 同一条取舍。
func scanSessionOf(drv driver.Driver) (*scanSession, error) {
	d, ok := drv.(*finsDriver)
	if !ok {
		return nil, fmt.Errorf("fins: 不支持的驱动类型 %T", drv)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.client == nil || d.config == nil || !d.client.IsConnected() {
		return nil, errors.New("fins: 设备未连接，无法扫描")
	}
	return &scanSession{client: d.client, cfg: d.config}, nil
}
