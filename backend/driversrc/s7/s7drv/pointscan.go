// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package s7

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"iot-gateway/driver"
)

// ==================== 点位范围扫描 ====================
//
// 对一台**已存在的设备**扫一段地址，把读得到的地址列出来。
//
// **这个功能产出的是一份「候选清单」，不是这台设备的点位表。** S7 没有任何标准
// 手段能枚举设备上有哪些点位或哪些 DB（SZL 里没有可靠的 DB 清单），所以这里做的是
// 「在这个范围里，哪些地址读得到」——而"读得到"与"有意义"是两回事：
//
//   - 非零的地址不一定有意义（可能是标定表、计数器、别人的数据）；
//   - 读得到 0 的地址可能恰恰是有效值（还没开机的状态、本来就应该为 0 的量）。
//
// 因此插件**不做零值过滤**（那是前端的一枚开关）：在所有地址都是 0 的设备上，
// 插件侧过滤会返回一份空列表，用户据此得出"这里没有点位"——与"没扫到"同型的错误。
//
// **绝不走 drv.Read。** 那条路是为**采集**设计的：它读 d.config 做区间规划、按采集
// 语义回填结果（缺失点位一律 Quality=0），遇到网络错误还会顺手 MarkDisconnected
// 影响采集侧的重连判定。这里要的是"逐地址问设备在不在"，只能取驱动自己的
// *S7Client 直接 readRange —— 与 modbusdrv 的 scanSessionOf 是同一条取舍。
//
// 与 Modbus 的一处差异：S7 没有"配置模式"（地址的含义不由 protocol_json 决定，
// StringLen/MaxGap 只影响采集侧的区间规划），所以这里**没有**那条
// errConfigModeNotScannable 守卫。这是协议属性决定的，不是疏忽。

// 点位扫描的规模上限。
const (
	// pointScanChunkBytes 一次读取请求覆盖的字节数。
	//
	// 取值权衡：大了则"整块失败→对半降级"的层数多（最坏 2n-1 次请求），
	// 小了则请求数多。512 是"一个不可达的 /24 也扫得完"与"对半降级不至于打满
	// 请求配额"之间的折中。gos7 内部还会按协商出的 PDU 长度（典型 480，
	// 可用载荷 462）再切一次，因此这个值不必也不能按 PDU 来定。
	pointScanChunkBytes = 512

	// pointScanMaxRequests 单次扫描最多发多少次读取请求。
	//
	// 存在的意义是**给"整段都读不到"的设备一个确定性的上界**：对半降级在一段
	// 完全读不到的区间上会退化成一棵满二叉树，最坏 2n-1 次请求，光靠 deadline
	// 止损会让耗时随网络延迟漂移（本地 PLC 上 512 次请求可能只要几百毫秒，
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
)

// pointScanZeroNote 值为 0 的点位带的说明。
//
// 判据是**原始字节全 0**（位模式则是**该位**为 0），不是解析后的值：
// 假设类型是 Real 时 0x00000001 会解成一个极小的非零数，按解析值判"零"会漏掉它，
// "显示 0 值"这枚开关也就漏了点。
const pointScanZeroNote = "当前值为 0"

// scanWidth 点位粒度：每个点位读多少字节，决定地址后缀与解码类型。
type scanWidth string

const (
	widthBit   scanWidth = "bit"   // DB1.DBX0.1 / M0.1，1 位
	widthByte  scanWidth = "byte"  // DB1.DBB0 / MB0，1 字节
	widthWord  scanWidth = "word"  // DB1.DBW0 / MW0，2 字节
	widthDWord scanWidth = "dword" // DB1.DBD0 / MD0，4 字节
)

// unitBytes 本粒度下一个点位占多少字节。位占 1 字节（所在字节）。
func (w scanWidth) unitBytes() int {
	switch w {
	case widthWord:
		return 2
	case widthDWord:
		return 4
	default:
		return 1
	}
}

// internalType 解码用的**内部类型裸名**（本包注册进 TypeRegistry 的名字）。
func (w scanWidth) internalType() string {
	switch w {
	case widthBit:
		return "bool"
	case widthByte:
		return "byte"
	case widthDWord:
		return "dword"
	default:
		return "word"
	}
}

// commonType 结果里回给前端的**通用类型名**（TypeMap 的 key）。
func (w scanWidth) commonType() string {
	switch w {
	case widthBit:
		return driver.TypeBoolean
	case widthByte:
		return driver.TypeByte
	case widthDWord:
		return driver.TypeDWord
	default:
		return driver.TypeWord
	}
}

// pointScanForm S7 的点位扫描参数表单（form-create schema）。
//
// 文案要把**这个功能做不到什么**说在前面：用户最容易的误解是"扫出来 = 这台设备的
// 点位表"，而实际上没被读到的地址既可能不存在、也可能存在但这一段被拒绝了。
// S7 还多一条独有的坑必须写在表单里 —— TIA Portal 里默认勾选的「优化的块访问」
// 让 DB 无法按绝对地址读，那种 DB 整段都会读不到。
const pointScanForm = `{
  "rule": [
    {"type":"select","field":"area","title":"区域","$required":false,"value":"DB",
     "info":"决定读哪一片地址空间，也决定生成的地址写法：DB 为 DB1.DBW0，M 为 MW0，I 为 IW0，Q 为 QW0",
     "options":[{"label":"DB 数据块","value":"DB"},{"label":"M 标志位区","value":"M"},
                {"label":"I 输入区","value":"I"},{"label":"Q 输出区","value":"Q"}]},
    {"type":"inputNumber","field":"dbNumber","title":"DB 号","$required":false,"value":1,
     "info":"仅「区域」选 DB 时有效。DB 必须是 PLC 里已存在的非空块；若该 DB 在 TIA Portal 里勾了「优化的块访问」（1200/1500 默认勾选），则无法按绝对地址读取，整段都会读不到，需要先取消勾选并重新下载"},
    {"type":"inputNumber","field":"startOffset","title":"起始字节偏移","$required":true,"value":0,
     "info":"0-based 字节偏移，与点位地址写法一致（起始 100 对应 DB1.DBW100）。既不是 1-based，也不是位偏移"},
    {"type":"select","field":"width","title":"粒度","$required":false,"value":"word",
     "info":"每个点位读多少字节，也决定生成的地址后缀（B/W/D）。位模式下每个字节展开成 8 个点位",
     "options":[{"label":"位 BOOL（DB1.DBX0.1）","value":"bit"},{"label":"字节 BYTE（DB1.DBB0）","value":"byte"},
                {"label":"字 WORD（DB1.DBW0）","value":"word"},{"label":"双字 DWORD（DB1.DBD0）","value":"dword"}]},
    {"type":"inputNumber","field":"count","title":"点位数","$required":true,"value":64,
     "info":"要扫多少个点位（位模式下即位数）。数量越大耗时可观：整段扫描有预算与请求数两条上限，触顶会返回已完成的部分并把「未扫完」标出来"}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// PointDiscovery 返回 S7 的点位扫描能力声明，供 main.go 填进 Meta.Protocols。
//
// 本插件只有 ISO-on-TCP 一种传输，不存在"串口协议可以但网络协议不行"这类分歧，
// 因此与 Discovery 一样直接就声明了。
func PointDiscovery() *driver.PointDiscoverySpec {
	return &driver.PointDiscoverySpec{
		Title: "扫描 S7 地址范围",
		Description: "按区域扫一段地址，列出**读得到**的地址与当前值。" +
			"设备不会告诉你它有哪些点位：非零的地址不一定有意义，读得到 0 的也可能是有效值，" +
			"没列出来的地址既可能不存在、也可能只是这一段被设备拒绝了。" +
			"S7-1200/1500 上勾了「优化的块访问」的 DB 无法按绝对地址读取，会整段读不到。" +
			"请把结果当作候选清单，而不是点位表",
		FormJSON: json.RawMessage(pointScanForm),
		// "Word" 是 TypeMap 的键（通用类型名）—— 写成内部裸名 "word" 会被
		// Meta.Validate 拒掉。字是最常见的 S7 点位粒度，故用作下拉初值。
		DefaultCommonDataType: driver.TypeWord,
		// 20000ms 是留给"大范围 + 慢链路"的空间，但注意**网关侧还会把它钳到
		// 7000ms**（pluginhost 的 pointScanGatewayBudgetCapMs，因为这条链路是
		// 同步 HTTP，超时会拖住网关停机）。实际生效的总是被钳过的那一个。
		MaxBudgetMs: 20000,
		MaxPoints:   pointScanMaxPoints,
	}
}

// pointScanOptions 解析后的扫描参数。
type pointScanOptions struct {
	area  s7Area
	db    int
	start int // 起始字节偏移（0-based）
	width scanWidth
	count int // 点位数
}

// isBit 本位模式。
func (o pointScanOptions) isBit() bool { return o.width == widthBit }

// pointScanOptionsRaw 扫描选项的原始形态。键名与 pointScanForm 的 field 一一对应。
//
// 数值字段一律用 rawInt：form-create 的 select 控件产出的是**字符串**
// （"word" 那项不算，"64" 那种是），声明成 int 时一份带字符串的选项会让
// json.Unmarshal 整体报错 —— 不是那一个字段失效，而是整次扫描全失败。
type pointScanOptionsRaw struct {
	Area        string `json:"area"`
	DB          rawInt `json:"dbNumber"`
	StartOffset rawInt `json:"startOffset"`
	Width       string `json:"width"`
	Count       rawInt `json:"count"`
}

// parsePointScanOptions 解析扫描选项。
//
// 与 scanOptions 的取舍不同：那边缺项时静默回默认值（"没填机架/槽位"就是"按最常见的
// 两组试"），这里**缺失或不合法一律报错**。"扫多少"没有合理的默认值 —— 猜一个 64 会让
// 用户拿到一份他并没有要求的清单，而多扫的那部分在慢链路上要花掉好几秒。
func parsePointScanOptions(options json.RawMessage) (pointScanOptions, error) {
	opt := pointScanOptions{area: S7AreaDB, db: 1, width: widthWord}
	if len(options) == 0 {
		return opt, errors.New("扫描选项为空：需要指定区域、起始偏移、粒度与数量")
	}

	var raw pointScanOptionsRaw
	if err := json.Unmarshal(options, &raw); err != nil {
		return opt, fmt.Errorf("解析扫描选项失败: %w", err)
	}

	// 区域。大小写不敏感：表单给的是大写，protocol_json 里可能存着别的写法。
	switch strings.ToUpper(strings.TrimSpace(raw.Area)) {
	case "", "DB":
		opt.area = S7AreaDB
	case "M":
		opt.area = S7AreaM
	case "I":
		opt.area = S7AreaI
	case "Q":
		opt.area = S7AreaQ
	default:
		return opt, fmt.Errorf("区域 %q 不是 DB/M/I/Q 之一", raw.Area)
	}

	// DB 号。**只有 DB 区用它**：M/I/Q 区没有 DB 的概念，把配置里残留的 DB 号
	// 带过去会让 S7Range.DB 非零 —— gos7 只对 DB 区写这个字段，等于埋了个无声的
	// 不一致（同一个 DB 号出现在 M 区的区间里）。
	if opt.area == S7AreaDB {
		if raw.DB.set {
			if raw.DB.val < 1 || raw.DB.val > maxDB {
				return opt, fmt.Errorf("DB 号 %d 超出 [1,%d]", raw.DB.val, maxDB)
			}
			opt.db = raw.DB.val
		}
	} else {
		opt.db = 0
	}

	if raw.StartOffset.set {
		if raw.StartOffset.val < 0 || raw.StartOffset.val > maxS7ByteAddr {
			return opt, fmt.Errorf("起始字节偏移 %d 超出 [0,%d]", raw.StartOffset.val, maxS7ByteAddr)
		}
		opt.start = raw.StartOffset.val
	}

	// 粒度。同样大小写不敏感。
	switch scanWidth(strings.ToLower(strings.TrimSpace(raw.Width))) {
	case "":
		opt.width = widthWord
	case widthBit, widthByte, widthWord, widthDWord:
		opt.width = scanWidth(strings.ToLower(strings.TrimSpace(raw.Width)))
	default:
		return opt, fmt.Errorf("粒度 %q 不是 bit/byte/word/dword 之一", raw.Width)
	}

	if !raw.Count.set || raw.Count.val <= 0 {
		return opt, errors.New("点位数必须大于 0")
	}
	opt.count = raw.Count.val

	// 地址空间上限是 24 位（gos7 用 3 字节编码字节偏移，起点左移 3 位）。
	// 用户的 count 越过上限时**截断而不是报错**：意图很清楚，就是要扫到能扫的末尾 ——
	// 与 modbusdrv 里"从 60000 起扫 10000 个"的处理一致。
	if opt.start >= maxS7ByteAddr {
		return opt, fmt.Errorf("起始字节偏移 %d 之后已无可用地址空间", opt.start)
	}
	if maxN := opt.maxCount(); opt.count > maxN {
		opt.count = maxN
	}
	return opt, nil
}

// maxCount 起始偏移固定时，本粒度下最多还能扫多少个点位。
func (o pointScanOptions) maxCount() int {
	if o.isBit() {
		// 位：最后一个点位落在第 (n-1)/8 个字节里，该字节必须仍在地址空间内。
		return (maxS7ByteAddr - o.start) * 8
	}
	return (maxS7ByteAddr - o.start) / o.width.unitBytes()
}

// byteSpan 点位区间 [start, start+n) 对应的**连续字节区间**。
//
// 位模式是最小的一次读取单位是字节，所以 n 个位可能只覆盖 1 个字节。
func (o pointScanOptions) byteSpan(start, n int) (byteStart, byteLen int) {
	if o.isBit() {
		byteStart = o.start + start/8
		lastByte := o.start + (start+n-1)/8
		return byteStart, lastByte - byteStart + 1
	}
	ub := o.width.unitBytes()
	byteStart = o.start + start*ub
	return byteStart, n * ub
}

// pointName 第 idx 个点位的地址写法。
func (o pointScanOptions) pointName(idx int) string {
	if o.isBit() {
		return formatPointName(o.area, o.db, o.start+idx/8, idx%8, widthBit)
	}
	return formatPointName(o.area, o.db, o.start+idx*o.width.unitBytes(), -1, o.width)
}

// describe 把 [start, start+n) 渲染成一句用户看得懂的范围描述，用于中止性错误的文案。
func (o pointScanOptions) describe(start, n int) string {
	if n <= 1 {
		return o.pointName(start)
	}
	return o.pointName(start) + " … " + o.pointName(start+n-1)
}

// formatPointName 把 (区域, DB 号, 字节偏移, 位偏移) 渲染成 S7 地址写法。
//
// **生成的串必须能被 ParseS7Address 反向解析回同一个位置**，这条不变量比它看起来
// 重要得多：一个解析不出来的地址写进 device_address 之后，这个点位永远采不到值
// （planFor 在规划前就把它拆出去，只报 QualityBadAddress / 报警「地址无效」）。
// 由 pointscan_test.go 的 TestPointNameRoundTrip 逐组合钉住。
//
// 写法与前端 utils/addressSyntax.js 里 s7 规格的示例（DB1.DBW4 / DB1.DBD10 / M0.1）
// 保持一致 —— 那份文案是用户填地址时唯一的信息来源，两边不一致会让"扫出来的和我
// 平时写的不一样"变成一个没有答案的疑问。
func formatPointName(area s7Area, db, off, bit int, w scanWidth) string {
	if area == S7AreaDB {
		switch w {
		case widthBit:
			return fmt.Sprintf("DB%d.DBX%d.%d", db, off, bit)
		case widthByte:
			return fmt.Sprintf("DB%d.DBB%d", db, off)
		case widthWord:
			return fmt.Sprintf("DB%d.DBW%d", db, off)
		default:
			return fmt.Sprintf("DB%d.DBD%d", db, off)
		}
	}

	prefix := "M"
	switch area {
	case S7AreaI:
		prefix = "I"
	case S7AreaQ:
		prefix = "Q"
	}
	if w == widthBit {
		return fmt.Sprintf("%s%d.%d", prefix, off, bit)
	}
	suffix := "B"
	switch w {
	case widthWord:
		suffix = "W"
	case widthDWord:
		suffix = "D"
	}
	return fmt.Sprintf("%s%s%d", prefix, suffix, off)
}

// SessionAccess 与 driversdk.SessionAccess 同形，但**刻意本地声明而不 import
// driversdk**：与 probe.go 的 ProbeTCP 收 target/options/budget 三个原始值
// （而不是 driversdk.ProbeRequest）是同一条取舍 —— 协议实现只依赖 driver 这一份
// 契约，不被整套插件开发套件绑住，本包因此也能脱离插件进程单独测试。
type SessionAccess interface {
	Do(fn func(drv driver.Driver) error) error
}

// ScanPoints 扫描一段 S7 地址范围。
//
// sess 是读驱动实例的唯一入口（见 SessionAccess）：**每块读一次 Do**，
// 块与块之间锁是放开的，因此一次扫描不会把采集卡死几十秒。
//
// 返回的 error 与结果**可以同时非 nil**：中止性错误（设备掉线）发生时，
// 已经扫到的部分一并返回 —— 丢掉它们对用户没有任何好处。
func ScanPoints(sess SessionAccess, options json.RawMessage, deadline time.Time) (*driver.PointScanResult, error) {
	opt, err := parsePointScanOptions(options)
	if err != nil {
		return nil, err
	}

	// 预检：在**开始扫描之前**把"这台设备根本不能扫"判掉 —— 主要是"没有可用连接"。
	// 一次额外的 Do 换一条明确的报错，比让用户拿到一份空清单便宜得多。
	//
	// 结论走 preflightErr 而不是 Do 的返回值：那是**设备配置/连接状态**的问题，
	// 不是会话坏了，混在一起会被当成"插件调用失败"报上去，指向完全错误的方向。
	var preflightErr error
	if err := sess.Do(func(drv driver.Driver) error {
		_, err := scanSessionOf(drv)
		preflightErr = err
		return nil
	}); err != nil {
		return nil, err
	}
	if preflightErr != nil {
		return nil, preflightErr
	}

	read := func(byteStart int, buf []byte) error {
		// 每次现取驱动实例，**不缓存**：热刷新会重建实例、重连会换掉 client，
		// 缓存下来的指针读的是一个已经 Close 的句柄（拿回上一轮的缓存值，
		// 或不巧触发 nil 解引用把整个插件进程带走）。
		return sess.Do(func(drv driver.Driver) error {
			s, err := scanSessionOf(drv)
			if err != nil {
				return err
			}
			// 直接调 readRange，而不是驱动的 Read：见文件头的说明。
			// S7Range 只有这四个字段参与读取，AddressMap 是给采集侧解码用的，这里用不上。
			return s.client.readRange(S7Range{
				Area:        opt.area,
				DB:          opt.db,
				StartOffset: byteStart,
				EndOffset:   byteStart + len(buf),
			}, buf)
		})
	}

	return scanWith(read, opt, deadline)
}

// readFn 读取 [byteStart, byteStart+len(buf)) 的原始字节。
//
// 这是 pScanner 与"会话"之间唯一的缝：生产路径每次调用都经 SessionAccess.Do
// 重新取一次驱动实例，测试路径直接指向一个假函数。
//
// 之所以需要这条缝：对半降级、请求数上限、连接中断中止这几条路径都要靠
// "读到某个特定字节区间就报特定错误"来驱动，而假 PLC 只会一律成功 ——
// 没有这条缝，这三条最容易写错的路径就只能靠读代码来确认。
// 与 discovery 包把 Interface.Addrs 做成注入点（lanTargetsWith）是同一条取舍。
type readFn func(byteStart int, buf []byte) error

// scanWith 是 ScanPoints 的可测形态：读取原语以函数注入。
func scanWith(read readFn, opt pointScanOptions, deadline time.Time) (*driver.PointScanResult, error) {
	sc := &pScanner{
		opt:      opt,
		read:     read,
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
var errPointScanStop = errors.New("s7: point scan stop")

// pScanner 一次扫描的可变状态。
type pScanner struct {
	opt      pointScanOptions
	read     readFn
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
// 与 modbusdrv.ScanPoints 的契约一致，网关侧两者都会透给用户。
func (sc *pScanner) run() (*driver.PointScanResult, error) {
	for done := 0; done < sc.opt.count; {
		if sc.stopped() {
			break
		}
		n := sc.opt.count - done
		if per := sc.itemsPerChunk(); n > per {
			n = per
		}
		if err := sc.walk(done, n); err != nil {
			if errors.Is(err, errPointScanStop) {
				break
			}
			// 中止性错误：把已经拿到的部分一并返回 —— 丢掉它们对用户没有任何好处。
			return sc.finish(), err
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

// itemsPerChunk 一块最多覆盖多少个点位，使一次读取不超过 pointScanChunkBytes。
func (sc *pScanner) itemsPerChunk() int {
	if sc.opt.isBit() {
		return pointScanChunkBytes * 8
	}
	if n := pointScanChunkBytes / sc.opt.width.unitBytes(); n > 0 {
		return n
	}
	return 1
}

// walk 扫 [start, start+n) 这段点位，读不通就对半降级。
//
// 降级的形状是「整段 → 对半 → … → 单点」：S7 的 PLC 对含"不存在"地址的整个请求
// 会**整段拒绝**（项级错误码），只有一路拆到单点才能把"哪几个地址真的不存在"
// 摘出来。驱动的 Read 那条路做不到这件事 —— 它整段失败就把该区间所有点标
// Quality=0 了事。
func (sc *pScanner) walk(start, n int) error {
	if n <= 0 {
		return nil
	}
	if sc.stopped() {
		return errPointScanStop
	}

	byteStart, byteLen := sc.opt.byteSpan(start, n)
	if byteLen <= 0 || byteStart+byteLen > maxS7ByteAddr {
		// 越界不该发生（选项解析已按 24 位上限截断过 count），真发生了也不能把
		// 请求发出去：超 24 位地址的区间会被 gos7 静默回绕，读到的是**别处**的数据，
		// 而用户会把它当成这段地址的真实值。
		sc.result.Scanned += n
		sc.result.Unreadable += n
		return nil
	}

	sc.requests++
	buf := make([]byte, byteLen)
	err := sc.read(byteStart, buf)
	if err == nil {
		return sc.emit(start, n, byteStart, buf)
	}
	if !isUnreadableAddrErr(err) {
		// 连接类错误（超时、EOF、reset）或无法归类的协议错误：**中止而不是
		// 记成"地址不存在"**。把设备掉线记成"这里没有点位"，用户会从一份几乎
		// 全是空的清单里得出与事实完全相反的结论，而采集侧此时也已经在重连了 ——
		// 两边的结论会互相矛盾。
		return fmt.Errorf("扫描 %s 时连接中断: %w", sc.opt.describe(start, n), err)
	}

	if n == 1 {
		// 拆到单点了还是被拒：这个地址就是读不到。
		sc.result.Scanned++
		sc.result.Unreadable++
		return nil
	}
	half := n / 2
	if err := sc.walk(start, half); err != nil {
		return err
	}
	return sc.walk(start+half, n-half)
}

// emit 把一次成功读取的结果按点位逐个产出。
//
// 逐个产出而不是整段写进 Points，是为了让 MaxPoints 截断**精确**：达到上限时
// 立刻停手，超出的地址既不计入 Scanned/Readable，也不进列表 ——
// 这样"可读到 M 个"与列表长度始终自洽。
func (sc *pScanner) emit(start, n, byteStart int, buf []byte) error {
	for i := 0; i < n; i++ {
		if len(sc.result.Points) >= pointScanMaxPoints {
			sc.result.Truncated = true
			return errPointScanStop
		}
		idx := start + i
		sc.result.Scanned++
		sc.result.Readable++
		sc.result.Points = append(sc.result.Points, sc.buildPoint(idx, byteStart, buf))
	}
	return nil
}

// buildPoint 从读取缓冲区里取出第 idx 个点位的值。
func (sc *pScanner) buildPoint(idx, byteStart int, buf []byte) driver.DiscoveredPoint {
	name := sc.opt.pointName(idx)

	// 定位这个点位用到的字节。
	var (
		raw     []byte
		zero    bool
		bit     int
		absByte int
	)
	if sc.opt.isBit() {
		absByte = sc.opt.start + idx/8
		bit = idx % 8
		off := absByte - byteStart
		raw = buf[off : off+1]
		// 位点位的"零值"是**这一位**为 0，不是整字节为 0 ——
		// 同一字节里别的位为 1 不该让这个点位显示成有值。
		zero = (raw[0]>>uint(bit))&0x01 == 0
	} else {
		ub := sc.opt.width.unitBytes()
		absByte = sc.opt.start + idx*ub
		off := absByte - byteStart
		raw = buf[off : off+ub]
		zero = allZeroBytes(raw)
	}

	if zero {
		// Value 留空、只给 Note：零值点位在界面上是"没有值"的那一类，
		// 前端据此实现"显示 0 值"这枚开关（插件侧刻意不做过滤）。
		return driver.DiscoveredPoint{Name: name, Note: pointScanZeroNote}
	}

	// 位地址要把**整字节**交给 ParseS7Value：它自己按 BitOffset 取位（见 parser.go）。
	// 传一个预先取好的 0/1 字节再让它取位，取到的是 0 号位，值恒等于那一位。
	//
	// ByteOffset 与 Area/DB 在这里都不参与解码（ParseS7Value 只用 BitOffset 与
	// 类型表），填真实值只是为了这个结构体在调试时看起来是对的。
	addr := S7Address{
		Area:       sc.opt.area,
		DB:         sc.opt.db,
		ByteOffset: absByte,
		BitOffset:  -1,
	}
	if sc.opt.isBit() {
		addr.BitOffset = bit
	}
	v, err := ParseS7Value(raw, addr, sc.opt.width.internalType())
	if err != nil {
		// 解析失败不改判"读到了"：地址确实是读到了，只是值渲染不出来。
		return driver.DiscoveredPoint{Name: name, Note: "值解析失败：" + err.Error()}
	}
	return driver.DiscoveredPoint{
		Name:  name,
		Value: FormatS7Value(v, sc.opt.width.internalType()),
	}
}

// finish 收尾：填耗时与两个标志，并把 nil 切片规整成空切片。
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

// unreadableMarkers 设备在说"这个地址/对象我读不了"的错误文案。
//
// **为什么只能靠字符串匹配**：gos7 把 PLC 回的项级错误码经 CPUError→ErrorText
// 翻译成一句人话，一路都是 fmt.Errorf 的裸字符串，没有可 errors.As 的类型
// （modbusdrv 能按 ExceptionCode 判，是因为 goburrow 给了结构化的 ModbusError ——
// S7 这边没有等价物）。文案逐条取自 gos7 的 error.go 与 gos7.go：
//
//	"CPU : Address out of range"                      ← 0x05，地址超出 CPU 范围
//	"CPU : Item not available"                        ← 0x0A / 0xD209，DB 或对象不存在（300/400 最常见）
//	"CPU : Function not available"                    ← 0x8104，1200/1500 的"该区域/该功能不可用"
//	"CLI : function refused by CPU"                   ← CPUError 的**兜底桶**，0x810A/0x810B 等
//	                                                      S7-1500 项级错误码都落到这里
//	"address invalid"                                 ← 33029，S7Error 直译
//	"object doesn"                                    ← 33034，S7Error 直译"object doesn’t exist…"
//
// **把兜底桶也收进来是有意的**：能走到项级错误码，就说明 PLC 确实应答了（连接是活的），
// 此时剩余的歧义只是"这个错误码是什么意思"；而保守地把它判成中止，会让 S7-1200/1500 上
// 一份本来能扫出东西的清单直接变成一个错误。真正的连接失败根本走不到这里 ——
// 它们过不了 isConnErr。
//
// 后两条的原文里含 U+2019（’），所以用不带撇号的短前缀匹配。
var unreadableMarkers = []string{
	"CPU : Address out of range",
	"CPU : Item not available",
	"CPU : Function not available",
	"CLI : function refused by CPU",
	"address invalid",
	"object doesn",
}

// isConnErr 这条错误是否表示"链路断了"，而不是"设备拒绝了这次读取"。
//
// net.Error 一并覆盖了 *net.OpError 与 syscall.Errno（两者都实现了 Timeout/Temporary），
// 因此连接被 reset、拨号超时、写失败这几类都会命中。
func isConnErr(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed)
}

// isUnreadableAddrErr 这条错误是否等于设备在说"这个地址我读不了"。
//
// 只有白名单里的项级错误算。**其余一律判成"不是地址问题"**，
// 宁可少扫一段并如实报告，也不要把"从站忙/链路抖动"记成"这些地址不存在"，
// 后者会往用户的候选清单里塞进一批假的"读不到"。
func isUnreadableAddrErr(err error) bool {
	if err == nil || isConnErr(err) {
		return false
	}
	msg := err.Error()
	for _, m := range unreadableMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// ==================== 驱动内部资源的取用 ====================

// scanSession 一次点位扫描要用的驱动内部资源。
//
// 每次 Do 现取、用完即弃（见 ScanPoints 里 read 的注释）。
type scanSession struct {
	client *S7Client
}

// scanSessionOf 从驱动实例取出扫描要用的客户端。
//
// 用类型开关而不是给 driver.Driver 加方法：那会要求全部协议都实现它，
// 而这里只有 S7 需要 —— 与 modbusdrv.scanSessionOf 同一条取舍。
func scanSessionOf(drv driver.Driver) (*scanSession, error) {
	d, ok := drv.(*s7Driver)
	if !ok {
		return nil, fmt.Errorf("s7: 不支持的驱动类型 %T", drv)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.client == nil || d.config == nil || !d.client.IsConnected() {
		return nil, errors.New("s7: 设备未连接，无法扫描")
	}
	return &scanSession{client: d.client}, nil
}
