// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"iot-gateway/driver"
)

// ==================== 点位范围扫描 ====================
//
// 对一台**已存在的设备**扫一段软元件地址，把读得到的地址列出来。
//
// **这个功能产出的是一份「候选清单」，不是这台设备的点位表。** MC 没有任何标准
// 手段能枚举 PLC 上有哪些点位，所以这里做的是「在这个范围里，哪些地址读得到」——
// 而"读得到"与"有意义"是两回事：
//
//   - 非零的地址不一定有意义（可能是标定表、计数器、别人的数据）；
//   - 读得到 0 的地址可能恰恰是有效值（还没开机的状态、本来就应该为 0 的量）；
//   - 没列出来的地址既可能不存在，也可能只是**这一段请求被设备整体拒绝了**。
//
// 因此插件**不做零值过滤**（那是前端的一枚开关）：在所有地址都是 0 的设备上，
// 插件侧过滤会返回一份空列表，用户据此得出"这里没有点位"——与"没扫到"同型的错误。
//
// **绝不走 drv.Read。** 那条路是为**采集**设计的：它按点位的 DataType 算区间、
// 按采集语义回填结果（缺失点位一律 Quality=0），遇到网络错误还会向上返回让采集
// 引擎判连。这里要的是"读这一片字"，只能取驱动自己的传输客户端直接 Read ——
// 与 s7drv / modbusdrv / finsdrv 的 scanSessionOf 是同一条取舍。
//
// **本协议没有「配置模式」，因此没有那条守卫。** Modbus 的
// `errConfigModeNotScannable` 防的是"地址的含义由 protocol_json 的 startAddress
// 决定"这一形态；MC 的 MCConfig 里没有这类字段，`maxGap`/`maxReadWords`/`stringLen`
// 只影响**采集侧**的区间规划，不改变任何地址的含义。照抄那条守卫只会凭空多出
// 一条拒绝条件（与 spec 里 S7 那一格同一条道理）。
//
// 三菱特有的两处，都写在用到它们的函数上，这里先点一句，免得漏读：
//
//  1. **单请求点数上限随「软元件类别 × 粒度」变**，不是一个常数（见 pointsPerRequest）；
//  2. **"读到第一个读不到就停"只对单片连续的软元件成立**，X/Y/B/W 的地址空间
//     按槽位/模块分配、中段可以有空洞（见 contiguous）。

// 点位扫描的规模上限。
const (
	// mcPointScanMaxRequests 单次扫描最多发多少次读取请求。
	//
	// 存在的意义是**给"整段都读不到"的设备一个确定性的上界**：对半降级在一段
	// 完全读不到的区间上会退化成一棵满二叉树，最坏 2n-1 次请求，光靠 deadline
	// 止损会让耗时随网络延迟漂移（本地 PLC 上几百次请求可能只要几百毫秒，
	// 于是照样跑完，而在慢链路上又跑不完）。触顶时置 Incomplete 并返回已得结果。
	//
	// 对 X/Y/B/W 这四种**非连续**软元件，这个上限是"中间有空洞"时唯一的兜底：
	// 那些设备上不做"读到末尾就停"的推断，请求数可以到 2n-1。
	mcPointScanMaxRequests = 512

	// mcPointScanMaxPoints 单次扫描最多返回多少个点位。
	//
	// 上限存在的意义不是性能而是**事故半径**：pluginwire 写帧持插件进程的写锁，
	// 一份几百 KB 的应答会把该进程上所有协议的应答一起头阻塞。2000 个点位
	// （每个地址一行、值几十字节）是几十 KB 的量级，一次能过管道。
	//
	// 与 PointDiscovery().MaxPoints 必须一致 —— 后者只是把它报给网关与前端，
	// 由 driversdk.Meta.Validate 校验落在 [1,20000] 内。
	mcPointScanMaxPoints = 2000

	// mcPointScanZeroNote 值为 0 的点位带的说明。
	//
	// 字点位的判据是**原始字节全 0**（位点位则是**该位**为 0），不是解析后的值：
	// 假设类型是 DWord 时 0x00000001 会解成一个极小的非零数，按解析值判"零"
	// 会漏掉它，"显示 0 值"这枚开关也就漏了点。
	mcPointScanZeroNote = "当前值为 0"
)

// pointScanDevices 点位扫描表单里提供的软元件（顺序即下拉顺序）。
//
// **只是下拉的候选，不是白名单**：parsePointScanOptions 用 lookupDevice 校验，
// 用户手工填别的软元件（S / V / F / Z / SD / SW / TC / CC …）同样能扫。
// 这里只列数采现场实际会扫的那几个 —— 22 个软元件全列进下拉，会把
// TC/CC/TN/CN/TS/CS 这些定时器/计数器元素被扫出一堆无意义地址的概率明显抬高。
//
// 与 mcDevices 的一致性由 pointscan_test.go 的 TestPointScanDeviceOptionsMatchDriver
// 钉住（存在性 + 位/字分类与 label 一致），否则这张清单会与驱动表静默漂移。
var pointScanDevices = []string{"D", "M", "X", "Y", "L", "B", "W", "R", "ZR"}

// pointScanForm MC 的点位扫描参数表单（form-create schema）。
//
// 文案要把三件用户最容易误解的事说在前面：
//
//  1. 扫出来的是**候选清单**，不是设备的点位表；
//  2. **X/Y 的起始地址是八进制**（与它们平时在图纸上的写法一致），其余软元件是十进制。
//     同一个数字在两类软元件上落在完全不同的位置，而两边看着都合理 —— 这是本表单
//     最容易出事的一栏，因此 start 用**文本框**而不是数字控件：只有文本能写出
//     "0010" 这种八进制写法，而且数字控件送出的是 JSON 数字，与"按八进制数字串解释"
//     的意图相冲突；
//  3. 位软元件（M/L/X/Y/B…）**只能用位粒度** —— 它们本身就是位，字粒度的读法虽然
//     驱动支持，但产出的是 M0/M16/M32 这种"看着像位、其实是 16 点一组"的名字。
const pointScanForm = `{
  "rule": [
    {"type":"select","field":"device","title":"软元件","$required":false,"value":"D",
     "info":"决定扫哪一片地址空间。带「位」的软元件只能用位粒度；X/Y 的地址按八进制解释，其余按十进制",
     "options":[{"label":"D 数据寄存器（字）","value":"D"},{"label":"M 内部继电器（位）","value":"M"},
                {"label":"X 输入继电器（位·八进制）","value":"X"},{"label":"Y 输出继电器（位·八进制）","value":"Y"},
                {"label":"L 锁存继电器（位）","value":"L"},{"label":"B 链接继电器（位）","value":"B"},
                {"label":"W 链接寄存器（字）","value":"W"},{"label":"R 文件寄存器（字）","value":"R"},
                {"label":"ZR 文件寄存器·扩展（字）","value":"ZR"}]},
    {"type":"input","field":"start","title":"起始地址","$required":true,"value":"0",
     "info":"只填地址数字，不带软元件前缀（起始 D100 填 100）。**X/Y 按八进制解释**（填 30 表示 X30，即十进制的 24），其余软元件按十进制"},
    {"type":"select","field":"width","title":"粒度","$required":false,"value":"word",
     "info":"每个点位占多少。位粒度下字软元件写成 D100.5（字内位号 0~15）；位软元件只能用位粒度",
     "options":[{"label":"位 BOOL（M10 / D100.5）","value":"bit"},{"label":"字 WORD（D100）","value":"word"},
                {"label":"双字 DWORD（D100，占 D100~D101）","value":"dword"}]},
    {"type":"inputNumber","field":"count","title":"点位数","$required":true,"value":64,
     "info":"要扫多少个点位。数量越大耗时可观：整段扫描有预算与请求数两条上限，触顶会返回已完成的部分并把「未扫完」标出来。在地址空间连续的软元件上（D/M/L/R/ZR…），读到第一个读不到的地址就会停下，后面的地址一律标为读不到；X/Y/B/W 的地址按槽位与模块分配、中段可能有空洞，因此会逐段降级继续往后扫"}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// PointDiscovery 返回 MC 的点位扫描能力声明，供 main.go 填进 Meta.Protocols。
//
// **TCP 与 Serial 共用同一份**：这个功能走的地址解析、软元件码与解码与传输层无关，
// 两边的表单、预算、上限因此逐字相同。会话复用两者都有（mcDriver.matchConfig
// 为串口而设，TCP 不走复用），所以串口协议声明它不需要任何额外的形态。
//
// 串口的实际差别只有速度：9600bps 上一帧几十字，网关侧 7000ms 的钳制会让大范围
// 扫描频繁落在 Incomplete 上。这条写进了 Description，因为它是最容易被误解成
// "这个功能坏了"的现象。
func PointDiscovery() *driver.PointDiscoverySpec {
	return &driver.PointDiscoverySpec{
		Title: "扫描 MC 软元件",
		Description: "按软元件扫一段地址，列出**读得到**的地址与当前值。" +
			"设备不会告诉你它有哪些点位：非零的地址不一定有意义，读得到 0 的也可能是有效值，" +
			"没列出来的地址既可能不存在、也可能只是这一段被设备拒绝了。" +
			"在 D/M/L/R/ZR 这类地址连续的软元件上，读到一个地址读不到为止，之后的地址一律按读不到计；" +
			"X/Y/B/W 按槽位与模块分配地址、中段可能有空洞，因此会逐段降级继续往后扫（更慢，但不会把清单从空洞处截断）。" +
			"位软元件（M/L/X/Y/B…）只有位粒度；X/Y 的地址按八进制解释。" +
			"串口设备在网关侧 7000ms 的预算钳制下，大范围扫描会经常标着「未扫完」返回。" +
			"请把结果当作候选清单，而不是点位表",
		FormJSON: json.RawMessage(pointScanForm),
		// "Word" 是 TypeMap 的键（通用类型名）—— 写成内部裸名 "word" 会被
		// Meta.Validate 拒掉。字是 MC 上最常见的点位粒度，故用作下拉初值。
		DefaultCommonDataType: driver.TypeWord,
		// 20000ms 是留给"大范围 + 慢链路（串口）"的空间，但注意**网关侧还会把它钳到
		// 7000ms**（pluginhost 的 pointScanGatewayBudgetCapMs，因为这条链路是
		// 同步 HTTP，超时会拖住网关停机）。实际生效的总是被钳过的那一个。
		MaxBudgetMs: 20000,
		MaxPoints:   mcPointScanMaxPoints,
	}
}

// scanWidth 点位粒度：每个点位占多少。
type scanWidth string

const (
	widthBit   scanWidth = "bit"   // M10（位软元件）/ D100.5（字软元件字内取位）
	widthWord  scanWidth = "word"  // D100，1 字
	widthDWord scanWidth = "dword" // D100，2 字（D100~D101）
)

// unitWords 一个点位占多少**字**。
//
// 位粒度返回 1：它占的是**所在的那一个字**（位软元件在位单位读取下是 1 位 1 字节，
// 但那个模式不按字算偏移）。字软元件 + 位粒度的偏移由 buildPoint 按**字地址之差**
// 换算，不能直接用这个值乘除 —— 见 buildPoint 的注释。
func (w scanWidth) unitWords() int {
	if w == widthDWord {
		return 2
	}
	return 1
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

// pointScanOptions 解析后的扫描参数。
type pointScanOptions struct {
	dev   mcDevice
	base  uint32 // 起始地址（X/Y 已按八进制解释成数值）
	width scanWidth
	count int // 点位数，已按地址空间上限截断
}

// pointScanOptionsRaw 扫描选项的原始形态。键名与 pointScanForm 的 field 一一对应。
//
// start 用 rawText（既收字符串也收数字）：表单里它是文本框，但客户端仍可能送
// JSON 数字，而声明成 string 时一个数字会让 json.Unmarshal **整体**报错 ——
// 不是那一栏失效，而是整次扫描全失败。count 用 rawInt，理由同 probe.go 里的说明。
//
// start 不能声明成 rawInt：X/Y 的地址要按**八进制数字串**解释，先 Atoi 成十进制
// 就把这个意图丢掉了（"30" 会变成十进制的三十，而它指的是八进制的三十）。
type pointScanOptionsRaw struct {
	Device string  `json:"device"`
	Start  rawText `json:"start"`
	Width  string  `json:"width"`
	Count  rawInt  `json:"count"`
}

// rawText 容错文本：同时接受 JSON 字符串与数字（数字取其字面量）。
type rawText struct {
	set bool
	val string
}

func (r *rawText) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	r.set = true
	r.val = s
	return nil
}

// parsePointScanOptions 解析扫描选项。
//
// 与 parseScanOptions（设备扫描）的取舍不同：那边缺项时静默回默认值，这里
// **缺失或不合法一律报错**。"扫多少"没有合理的默认值 —— 猜一个 64 会让用户
// 拿到一份他并没有要求的清单，而多扫的那部分在慢链路上要花掉好几秒。
//
// start 是个例外，缺项按 0 处理：起始地址为 0 是这个功能里唯一真正安全的默认值
// （它一定在范围内），而 count 缺失必须报错，两者并不矛盾。
func parsePointScanOptions(options json.RawMessage) (pointScanOptions, error) {
	opt := pointScanOptions{width: widthWord}
	if len(options) == 0 {
		return opt, errors.New("扫描选项为空：需要指定软元件、起始地址、粒度与数量")
	}

	var raw pointScanOptionsRaw
	if err := json.Unmarshal(options, &raw); err != nil {
		return opt, fmt.Errorf("解析扫描选项失败: %w", err)
	}

	// 软元件。大小写不敏感：表单给的是大写，protocol_json 与手工调用可能给别的写法。
	name := strings.ToUpper(strings.TrimSpace(raw.Device))
	if name == "" {
		name = "D"
	}
	dev, ok := lookupDevice(name)
	if !ok {
		return opt, fmt.Errorf("软元件 %q 不是 MC 支持的软元件（如 D/M/X/Y/L/B/W/R/ZR）", raw.Device)
	}
	opt.dev = dev

	// 粒度。同样大小写不敏感。
	switch w := scanWidth(strings.ToLower(strings.TrimSpace(raw.Width))); w {
	case "":
		opt.width = widthWord
	case widthBit, widthWord, widthDWord:
		opt.width = w
	default:
		return opt, fmt.Errorf("粒度 %q 不是 bit/word/dword 之一", raw.Width)
	}

	// 位软元件只能用位粒度。理由见 pointScanForm 的注释：字单位读位软元件是驱动
	// 支持的，但产出的是 M0/M16/M32 这种"看着像位、其实是 16 点一组"的名字，
	// 而它们的值会是整字 —— 用户不会发现，直到采出来的数据对不上。
	if dev.isBit && opt.width != widthBit {
		return opt, fmt.Errorf("软元件 %s 是位软元件，只能用位粒度（bit），不能用 %s", dev.name, opt.width)
	}

	// 起始地址。X/Y 按八进制数字串解释，其余按十进制 —— 与 ParseMCAddress 的
	// 判定完全一致（address.go 的 parseOctal 分支），否则生成的地址会解析到
	// **别的位置**，而名字看着完全合理。
	if raw.Start.set {
		num, err := parseDeviceNumber(dev, raw.Start.val)
		if err != nil {
			return opt, err
		}
		opt.base = num
	}

	if !raw.Count.set || raw.Count.val <= 0 {
		return opt, errors.New("点位数必须大于 0")
	}
	opt.count = raw.Count.val

	// 地址空间上限按粒度折算后**截断而不是报错**：用户的意图很清楚，就是要扫到
	// 能扫的末尾 —— 与 finsdrv / s7drv 里"从某处起扫 N 个"的处理一致。
	if maxN := opt.maxCount(); opt.count > maxN {
		opt.count = maxN
	}
	if opt.count <= 0 {
		return opt, fmt.Errorf("起始地址 %s 已到地址空间末尾，没有可扫的地址", opt.pointName(0))
	}
	return opt, nil
}

// parseDeviceNumber 把一个地址数字串按该软元件的进制解释成数值。
//
// 复用 address.go 的 parseOctal 而不是另写一遍：那份实现的溢出守卫就是这个功能
// 的上限依据（见 maxCount），两份实现一旦分叉，"生成的地址解析不回来"就会变成
// 一个只在边界上复现的问题。
//
// **错误必须原样带出去**：X/Y 填了 8 或 9 是用户的输入错误，静默按十进制解析
// 会让他扫到一段完全不同的地址，而结果看着完全正常。
func parseDeviceNumber(dev mcDevice, s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if dev.octal {
		num, err := parseOctal(s)
		if err != nil {
			return 0, fmt.Errorf("起始地址 %q 不是合法的八进制数字串（软元件 %s 的地址按八进制解释，只接受 0~7 的数字）", s, dev.name)
		}
		return uint32(num), nil
	}
	num, err := strconv.ParseUint(s, 10, 32)
	if err != nil || num > maxDeviceNumber {
		return 0, fmt.Errorf("起始地址 %q 不是合法的十进制地址（软元件 %s 的范围是 0~%d）", s, dev.name, maxDeviceNumber)
	}
	return uint32(num), nil
}

// maxCount 起始地址固定时，本粒度下最多还能扫多少个点位。
//
// **dword 那一格是差一陷阱**：一个双字点位占两个地址号，所以可扫点数只有
// 剩余地址数的一半。写成 `avail` 会让最后一个点位的第二个字越界，
// 而越界那半边读到的是**下一段**的数据。
func (o pointScanOptions) maxCount() int {
	avail := int(maxDeviceNumber) - int(o.base) + 1
	if avail <= 0 {
		return 0
	}
	switch {
	case o.dev.isBit:
		return avail
	case o.width == widthBit:
		// 每个字展开成 16 个位点位。count 到 2000 就截断了，这里返回的
		// 只是一个宽松的上界，不必担心它有多大。
		return avail * 16
	case o.width == widthDWord:
		return avail / 2
	default:
		return avail
	}
}

// pointsPerRequest 一块最多覆盖多少个点位。
//
// **这不是一个常数，随「软元件类别 × 粒度」变四档。** `cfg.MaxReadWords` 的语义
// 写死在 types.go 里是「单请求最大读取点数（字按字、位按位）」，也就是它衡量的
// 是**读出多少字/位**，而一个点位占多少字/位是另一回事：
//
//	位软元件 + bit    1 点 = 1 位   → 一块 = maxWords 点
//	字软元件 + bit    1 点 = 1 位   → 一块 = maxWords×16 点（maxWords 个字里的位）
//	字软元件 + word   1 点 = 1 字   → 一块 = maxWords 点
//	字软元件 + dword  1 点 = 2 字   → 一块 = maxWords/2 点
//
// 当成同一个常数会错两档，而且都是静默的：字软元件扫位时请求数会变成 16 倍
// （一段本来扫得完的地址会撞上请求数上限、拿到一份标着"未扫完"的清单），
// 双字时每帧字数翻倍（960 字上限的型号上直接超限）。
//
// 上限取**设备自己的 maxReadWords** 而不是写死一个值：采集时的区间规划用的就是
// 它，它对这台 PLC 是已知能用的；照搬一个更大的值会让扫描在"单请求最大 100 字"
// 的老型号上一片都读不到，而用户完全看不出是扫描参数的问题还是设备的问题。
//
// 字软元件 + 位粒度那一档是 `16*(maxWords-1)` 而不是 `maxWords*16`：位粒度下块头
// 要向下取整到字边界，块尾要向上取整，**最坏会多用掉一个字**（起始位落在字中间时）。
// 那多出来的一个字会让单请求字数刚好越过设备的 maxReadWords —— 在把配置调到
// 贴近型号上限的现场（maxReadWords=960）上会被设备回 0xC05B 拒绝，于是每一块都
// 白跑一轮对半降级。少要一个字，这条不变量就变成"恒不超限"，代价只是每块少读
// 16 个位。由 pointscan_test.go 的 TestSpanWithinSingleRequestLimit 钉住。
func (o pointScanOptions) pointsPerRequest(maxWords int) int {
	if maxWords < 1 {
		maxWords = defaultMaxReadWords
	}
	switch {
	case o.dev.isBit:
		return maxWords
	case o.width == widthBit:
		if maxWords < 2 {
			return 16 // 退化情形：至少给一个字能装下的位数
		}
		return 16 * (maxWords - 1)
	case o.width == widthDWord:
		n := maxWords / 2
		if n < 1 {
			n = 1
		}
		return n
	default:
		return maxWords
	}
}

// span 覆盖点位 [start, start+n) 需要的请求：块头、点数、以及是否位单位读取。
//
// **返回值直接就是 mcTransport.Read 的三个参数**，四种模式各一行：
//
//	位软元件 + bit    位地址、位数、bitMode=true
//	字软元件 + bit    起始字、字数、bitMode=false
//	字软元件 + word   起始字、字数、bitMode=false
//	字软元件 + dword  起始字、字数（2n）、bitMode=false
//
// 三处容易写错的地方：
//
//  1. **字软元件 + bit 的起始字是 `start/16`**，不是 start —— 对半降级会把起点
//     切到字中间（例如从第 5 位起扫 8 个位），用 16 位对齐的位地址当字地址发出去，
//     读到的是**别处**的字。
//  2. **点数不能用 uint32(n×unitWords) 表示成"字的个数"**，因为位粒度下
//     n 是位数而不是字数；四种模式的换算各不相同，只有显式分档才对。
//  3. 双字的起始字是 `base + 2*start`：start 是**点位下标**，而双字点位在地址
//     空间里每次跳两个字。漏乘 2 会让整块错位半个双字，值看着完全合理。
func (o pointScanOptions) span(start, n int) (head uint32, points uint16, bitMode bool) {
	switch {
	case o.dev.isBit:
		return o.base + uint32(start), uint16(n), true
	case o.width == widthBit:
		first := o.base + uint32(start/16)
		last := o.base + uint32((start+n-1)/16)
		return first, uint16(last - first + 1), false
	case o.width == widthDWord:
		return o.base + uint32(start*2), uint16(n * 2), false
	default:
		return o.base + uint32(start), uint16(n), false
	}
}

// contiguous 该软元件的地址空间是否**单片连续**。
//
// 这条判断决定能不能用「读到第一个读不到的点就收工」这个捷径（见 walk）。
// FINS 能把那条做成全局规则，是因为它的内存区各自是一整片；**MC 不是**：
//
//	X / Y  物理 I/O，按槽位分配（X0~X1F 是 1 号槽、X20~X3F 是 2 号槽……）。
//	       没插模块的槽位对应的地址读不到，而它后面的槽位可能是好的。
//	B / W  链接继电器 / 链接寄存器，按模块分配，同样中段可以有空洞。
//
// 在这四种软元件上照抄捷径的后果是：对半降级摘出空洞里的第一个单点、判定
// "到此为止"，于是**清单从空洞处断掉**，而 Truncated / Incomplete 都不置位 ——
// 用户看到一份短了一截、却看不出任何异常的清单。代价是请求数（最坏 2n-1 次），
// 由 mcPointScanMaxRequests 兜住。
//
// 其余软元件（D/R/ZR/M/L/S/V/F/Z 与各种特殊寄存器）是单片连续的：D0 与 D32767
// 之间不存在"没分配"的洞，读不到只可能是越过了这个区的末尾。
func (o pointScanOptions) contiguous() bool {
	switch o.dev.name {
	case "X", "Y", "B", "W":
		return false
	default:
		return true
	}
}

// pointName 第 idx 个点位的地址写法。
//
// **生成的串必须能被 ParseMCAddress 反向解析回同一个位置**，这条不变量比它
// 看起来重要得多：一个解析不出来的地址写进 device_address 之后，这个点位永远
// 采不到值（planFor 在规划前就把它拆出去，只报 QualityBadAddress / 报警「地址无效」）。
// 由 pointscan_test.go 的 TestPointNameRoundTrip 逐组合钉住。
//
// 写法与前端 utils/addressSyntax.js 里 mc 规格的示例一致（`D100` / `M0` / `X30`，
// X30 是八进制），也与该文件里两个陷阱 key 的文案对得上：
//
//	pitfallMcOctal      —— X/Y 是八进制，X10 不等于第十个点
//	pitfallMcBitDevice  —— 位软元件本身就是位，不能加 .bit 后缀
//
// 那份文案是用户填地址时唯一的信息来源，两边不一致会让"扫出来的和我平时写的
// 不一样"变成一个没有答案的疑问。
func (o pointScanOptions) pointName(idx int) string {
	switch {
	case o.dev.isBit:
		// 位软元件 + 位粒度：位地址自身就是名字。
		return o.dev.name + formatDeviceNumber(o.dev, o.base+uint32(idx))
	case o.width == widthBit:
		// 字软元件 + 位粒度：字地址 + 字内位号（0~15）。
		// **位号是字内的**（parser.go 的 IsBit 分支按 Bit%8 取字节、按 Bit%8 取位），
		// 因此这里的 idx%16 与它是同一个基准，不需要任何换算。
		word := o.base + uint32(idx/16)
		return fmt.Sprintf("%s%d.%d", o.dev.name, word, idx%16)
	case o.width == widthDWord:
		return fmt.Sprintf("%s%d", o.dev.name, o.base+uint32(idx*2))
	default:
		return fmt.Sprintf("%s%d", o.dev.name, o.base+uint32(idx))
	}
}

// formatDeviceNumber 把一个地址号渲染成该软元件自己的写法。
func formatDeviceNumber(dev mcDevice, num uint32) string {
	if dev.octal {
		return octalString(num)
	}
	return strconv.FormatUint(uint64(num), 10)
}

// octalString 十进制数值 → 八进制数字串（无前缀、无前导零、0 渲染成 "0"）。
//
// 与 address.go 的 parseOctal **在值上严格互逆**：任何一个 [0, 0xFFFFFF] 里的值，
// 经它渲染再经 parseOctal 解析都回到原值。串上的互逆不成立（"X0" 与 "X00"
// 都解析成 0，而这里只产出 "X0"），但那不影响位置 —— 契约要求往返的是位置。
//
// 不用 strconv.FormatUint(v, 8) 只是为了让"不产生前导零"这条约束在代码里可见：
// 它本来就是那个函数的行为，但这里是地址生成路径，值得写死。
func octalString(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [11]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%8)
		v /= 8
	}
	return string(buf[i:])
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

// ScanPoints 扫描一段 MC 软元件地址。
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

	// 预检：在**开始扫描之前**把"这台设备根本不能扫"判掉 —— 没有可用连接，
	// 或配置的字数上限连一个双字都放不下。
	//
	// 结论走 preflightErr 而不是 Do 的返回值：那是**设备配置/连接状态**的问题，
	// 不是会话坏了，混在一起会被当成"插件调用失败"报上去，指向完全错误的方向。
	var (
		preflightErr error
		maxWords     int
	)
	if err := sess.Do(func(drv driver.Driver) error {
		s, err := scanSessionOf(drv)
		if err != nil {
			preflightErr = err
			return nil
		}
		maxWords = s.maxReadWords()
		return nil
	}); err != nil {
		return nil, err
	}
	if preflightErr != nil {
		return nil, preflightErr
	}

	// maxReadWords < 2 时产出双字点位是**在出厂前就该拦住的事**：那个点位的跨度
	// 是 2 字，而 CalcMCRanges 遇到"单点位跨度超过 maxWords"会让**整台设备**的
	// 区间计算直接报错（range.go），也就是采不到任何值 —— 用户只是在扫描页填了个
	// 粒度，代价却是这台设备彻底停摆。宁可这里报一句清楚的错。
	if opt.width == widthDWord && maxWords < 2 {
		return nil, fmt.Errorf(
			"设备配置的单请求字数上限 maxReadWords=%d 小于双字点位所需的 2 字，请把它调大后再扫描"+
				"（双字点位建成后会让这台设备的采集整体失败）", maxWords)
	}

	read := func(head uint32, points uint16, bitMode bool) ([]byte, error) {
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
			data, err := s.client.Read(opt.dev, head, points, bitMode)
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

	return scanWith(read, opt, maxWords, deadline)
}

// mcReadFn 读 [head, head+points) 的原始字节。
//
// 签名与 mcTransport.Read 逐字相同（多出的是设备，由闭包带入），因此生产路径与
// 传输层之间没有中间的语义转换。这是 mcScanner 与"会话"之间唯一的缝：生产路径
// 每次调用都经 SessionAccess.Do 重新取一次驱动实例，测试路径直接指向一个假函数。
//
// 之所以需要这条缝：对半降级、请求数上限、连接中断中止、读到末尾这几条路径都要
// 靠"读到某个特定地址就报特定错误"来驱动，而假 PLC（testutil/fake 的 MC3E）只会
// 一律成功 —— 没有这条缝，这几条最容易写错的路径就只能靠读代码来确认。
// 与 s7drv / finsdrv 的 readFn 是同一条取舍。
type mcReadFn func(head uint32, points uint16, bitMode bool) ([]byte, error)

// scanWith 是 ScanPoints 的可测形态：读取原语以函数注入。
func scanWith(read mcReadFn, opt pointScanOptions, maxWords int, deadline time.Time) (*driver.PointScanResult, error) {
	if maxWords < 1 {
		maxWords = defaultMaxReadWords
	}
	sc := &mcScanner{
		opt:      opt,
		read:     read,
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
var errPointScanStop = errors.New("mitsubishi: point scan stop")

// mcScanner 一次扫描的可变状态。
type mcScanner struct {
	opt      pointScanOptions
	read     mcReadFn
	maxWords int // 单次请求最多读多少字/位（取自设备自己的 maxReadWords）
	deadline time.Time
	started  time.Time
	result   *driver.PointScanResult

	// requests 已发出的读取请求数（含降级途中那些）。
	requests int

	// incomplete 预算耗尽或请求数用尽。与 Truncated 是两回事：那个是"结果条数到
	// 上限"，这个是"范围还没扫完"，前端必须分开显示。
	incomplete bool
}

// run 逐块扫描，直到扫完、到上限或中止。
//
// 返回的 error 与结果**可以同时非 nil**（中止性错误发生时会带上已经扫到的部分）——
// 与 s7drv / finsdrv 的 ScanPoints 契约一致，网关侧两者都会透给用户。
func (sc *mcScanner) run() (*driver.PointScanResult, error) {
	for done := 0; done < sc.opt.count; {
		if sc.stopped() {
			break
		}
		n := sc.opt.count - done
		if per := sc.opt.pointsPerRequest(sc.maxWords); n > per {
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
			// 从 resumeAt 起这个软元件的这一段再也读不到了，而且它的地址空间是
			// **单片连续**的（否则 walk 不会回 hitEnd）。**后面的地址不再逐个去问**：
			// 连续区里"某个地址不存在"只可能是"已经越过这个区的末尾"，逐个探测
			// 只会把一次"扫到末尾"变成几百次必然失败的请求，而结论一模一样。
			//
			// 这批点按"读不到"计而不是"没扫过"：用户需要知道它们**在这个范围里
			// 但读不出来**，否则他会以为范围根本没覆盖到那里。resumeAt 是 walk
			// 已经交代过的下一个下标（它自己记到第一个读不到的那个为止），
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
func (sc *mcScanner) stopped() bool {
	if sc.result.Truncated || sc.incomplete {
		return true
	}
	if sc.requests >= mcPointScanMaxRequests {
		sc.incomplete = true
		return true
	}
	if time.Now().After(sc.deadline) {
		sc.incomplete = true
		return true
	}
	return false
}

// walk 扫 [start, start+n) 这段点位，读不通就对半降级。
//
// 返回 (hitEnd, resumeAt, err)：hitEnd 为 true 表示**这一段到此为止**，
// resumeAt 是"下一个还没被记账的点位下标"（walk 自己已经产出过 [start, resumeAt)
// 里的可读点、也记过第一个读不到的点，再往后的由 run() 一次性补记）。
//
// 降级的形状是「整段 → 对半 → … → 单点」：MC 对含"不存在地址"的整个请求会
// **整段拒绝**（结束码非 0），只有一路拆到单点才能把"哪里是末尾"摘出来。
// 驱动的 Read 那条路做不到这件事 —— 它整段失败就把该区间所有点标 Quality=0 了事。
//
// **hitEnd 只在单片连续的软元件上才返回 true**（见 contiguous）：对 X/Y/B/W，
// 单点读不到只说明"这个槽位/这段链接区没分配"，它后面完全可能是好的，
// 于是继续降级右子树、把清单扫全，代价是请求数。
//
// 这条推断成立的前提是 isAddressEndCode 已经把**可恢复的 PLC 状态错误**排除在外
// （它们不代表地址不存在）。两者缺一不可：少了那边的排除，一次"PLC 忙"就会把
// 清单从中间截断。
func (sc *mcScanner) walk(start, n int) (hitEnd bool, resumeAt int, err error) {
	if n <= 0 {
		return false, 0, nil
	}
	if sc.stopped() {
		return false, 0, errPointScanStop
	}

	head, points, bitMode := sc.opt.span(start, n)
	if points == 0 {
		// 不该发生（n >= 1 时 span 的 points 恒 >= 1），真发生了也不能把请求发出去。
		sc.result.Scanned += n
		sc.result.Unreadable += n
		return false, 0, nil
	}
	// 越界守卫：**必须挡在这里**，因为 buildMCBody 只把 head 发成 3 字节
	// （frame.go），超上限的地址会被静默截断成另一个地址 —— 读到的是**别处**
	// 的数据，而用户会把它当成这段地址的真实值。与 finsdrv 那条 16 位回绕守卫
	// 是同一条理由。
	if uint64(head)+uint64(points)-1 > uint64(maxDeviceNumber) {
		sc.result.Scanned += n
		sc.result.Unreadable += n
		return false, 0, nil
	}

	sc.requests++
	data, err := sc.read(head, points, bitMode)
	if err == nil {
		return false, 0, sc.emit(start, n, head, data)
	}
	if !isAddressEndCode(err) {
		// 链路类错误（超时、EOF、reset）或 PLC 自身状态错误（忙、密码锁）：
		// **中止而不是记成"地址不存在"**。把设备掉线记成"这里没有点位"，
		// 用户会从一份几乎全是空的清单里得出与事实完全相反的结论，
		// 而采集侧此时也已经在重连了 —— 两边的结论会互相矛盾。
		return false, 0, fmt.Errorf("扫描 %s 时读取失败: %w", sc.opt.describe(start, n), err)
	}

	if n == 1 {
		// 拆到单点了还是被拒：这个地址读不到。
		sc.result.Scanned++
		sc.result.Unreadable++
		if sc.opt.contiguous() {
			return true, start + 1, nil
		}
		return false, 0, nil
	}
	half := n / 2
	hit, resume, err := sc.walk(start, half)
	if err != nil || hit {
		// 左半边就已经越界：按连续地址空间的性质，右半边及其后全都读不到，
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
func (sc *mcScanner) emit(start, n int, head uint32, data []byte) error {
	for i := 0; i < n; i++ {
		if len(sc.result.Points) >= mcPointScanMaxPoints {
			sc.result.Truncated = true
			return errPointScanStop
		}
		idx := start + i
		sc.result.Scanned++
		sc.result.Readable++
		sc.result.Points = append(sc.result.Points, sc.buildPoint(idx, start, head, data))
	}
	return nil
}

// buildPoint 从读取缓冲区里取出第 idx 个点位的值。
//
// **两种读法的偏移完全不同，而错法的值都看着完全合理**，所以两条路径分成两个
// 函数级的分支、各自配一条断言位置的测试：
//
//	位单位读取（位软元件）  每点 1 字节，偏移 = 位地址之差
//	字单位读取（字软元件）  每点 2×unitWords 字节，偏移 = **字地址之差 × 2**
//
// 字单位那一侧最容易写错的是位粒度：偏移必须按**字**算（`idx/16 - start/16`），
// 写成 `(idx-start)/16` 在 start=5、idx=20 时会把 1 个字算成 0 个 —— 读到的是
// 含 bit0~bit15 的那个字，而值看着完全合理。FINS 的 wordSpan 踩过同一个坑。
func (sc *mcScanner) buildPoint(idx, start int, head uint32, data []byte) driver.DiscoveredPoint {
	o := sc.opt
	name := o.pointName(idx)

	if o.dev.isBit {
		// 位单位读取：每点 1 字节（0x00/0x01），偏移就是**点位下标之差**。
		//
		// 是 idx-start 而**不是** "绝对位地址 - head"：head 是绝对位地址
		//（base+start），而 idx 是点位下标，两者相差一个 base。用
		// `uint32(idx) - head` 在 base 非 0 时会得到一个负数或一个巨大的正数，
		// 表现是整块点位全部"读取数据不足" —— 起点为 0 的扫描则完全正常，
		// 所以这个错误只在非 0 起点上才露出来。
		off := idx - start
		if off < 0 || off+1 > len(data) {
			return driver.DiscoveredPoint{Name: name, Note: "值解析失败：读取数据不足"}
		}
		// 位软元件本身没有字内位号，Bit 必须留 -1：写 0 会让 ParseMCValue 走
		// IsBit() 分支、把这一字节当成"某个字里的第 0 位"去解，而它其实是
		// 该位自己的值 —— 两者恰好都是 0/1，所以解出来**看不出区别**，
		// 直到某天有人改了位序才发现。
		v, err := ParseMCValue(data[off:off+1],
			MCAddress{Device: o.dev, Number: o.base + uint32(idx), Bit: -1}, o.width.internalType())
		return sc.pointFrom(v, err, name, data[off:off+1])
	}

	// 字单位读取。点位所在的字相对块头的偏移，**四种模式都从字地址之差算**。
	unit := o.width.unitWords()
	var (
		wordIdx uint32
		bit     = -1
	)
	if o.width == widthBit {
		wordIdx = o.base + uint32(idx/16)
		bit = idx % 16
	} else {
		wordIdx = o.base + uint32(idx*unit)
	}
	off := int(wordIdx-head) * 2
	end := off + unit*2
	if off < 0 || end > len(data) {
		return driver.DiscoveredPoint{Name: name, Note: "值解析失败：读取数据不足"}
	}
	// **必须把该字自己的 2 字节切给 ParseMCValue**，不能把整段 data 传进去：
	// parser.go 的 IsBit 分支按 Bit%8 取字节、按 Bit%8 取位，全部相对于 raw[0] ——
	// 传整段会静默解出**块首那个字**的位。那条路径只检查 len(raw) >= 2，不会报错。
	raw := data[off:end]
	v, err := ParseMCValue(raw, MCAddress{Device: o.dev, Number: wordIdx, Bit: bit}, o.width.internalType())
	return sc.pointFrom(v, err, name, raw)
}

// pointFrom 把一次解码的结果渲染成 DiscoveredPoint，并处理零值那条分支。
//
// raw 是该点位的原始字节：零值的判据是**原始字节全 0**，不是解析后的值
// （假设类型是 DWord 时 0x00000001 会解成一个极小的非零数）。位粒度下 raw 是
// 那个字（2 字节）或那一字节，两种情况下"全 0"都等价于"这一位为 0"，
// 因为位取值路径只碰得到这一位 —— 而**位软元件那一路传的就是那一个字节**，
// 所以这里不需要第二套位索引（手搓 bit/8 + bit%8 只会多出一份会漂移的实现）。
func (sc *mcScanner) pointFrom(v any, err error, name string, raw []byte) driver.DiscoveredPoint {
	if err != nil {
		// 解析失败不改判"读到了"：地址确实是读到了，只是值渲染不出来。
		return driver.DiscoveredPoint{Name: name, Note: "值解析失败：" + err.Error()}
	}
	if zeroValue(v, raw) {
		// Value 留空、只给 Note：零值点位在界面上是"没有值"的那一类，
		// 前端据此实现"显示 0 值"这枚开关（插件侧刻意不做过滤）。
		return driver.DiscoveredPoint{Name: name, Note: mcPointScanZeroNote}
	}
	return driver.DiscoveredPoint{Name: name, Value: FormatMCValue(v, sc.opt.width.internalType())}
}

// zeroValue 这个点位的值是否为 0。
//
// 位点位看**解码后的布尔值**（它就是"这一位"，而它所在的字里别的位是 1 还是 0
// 与这个点位无关），字点位看**原始字节是否全 0**。
//
// 不做成"统一看原始字节"：位软元件那一路拿到的是 1 个字节，恰好也能用 allZeroBytes
// 判，但字软元件 + 位粒度那一路拿到的是**整个字**的 2 字节 —— 用 allZeroBytes 会
// 让同一个字里别的位为 1 时把本该显示 0 的点位漏掉。
func zeroValue(v any, raw []byte) bool {
	if b, ok := v.(bool); ok {
		return !b
	}
	for _, x := range raw {
		if x != 0 {
			return false
		}
	}
	return true
}

// finish 收尾：填耗时与标志，并把 nil 切片规整成空切片。
func (sc *mcScanner) finish() *driver.PointScanResult {
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

// ==================== 错误分类 ====================

// isAddressEndCode 这条错误是否**只关于地址**（而不是链路或 PLC 的状态）。
//
// MC 的结束码是单个 16 位值，**没有 FINS 那种「高字节是主码、可以按主码成组排除」
// 的结构**：同一个问题在不同家族上还是两个不同的码值（QnA 兼容那一套写 0xC0xx，
// iQ 那一套写 0x4xxx，见 frame.go 的 endCodeDeviceRange），所以这里只能逐条列
// **白名单**，做不到排除法。要判定的问题只有一个：
//
//	「这条错误能不能用来推断'这个地址（或这段范围）读不到'？」
//
// 收进来的四条，逐条给出处（常量定义在 frame.go，含义见三菱手册的结束码表）：
//
//	0xC051 设备指定错误   —— 这个 CPU 不支持该软元件（例如在只到 D 的型号上扫 ZR）。
//	                           它是对**整个软元件**的判定，因此每一段都会回它，
//	                           对半降级到单点后照样回 —— 结论"这一段都读不到"成立。
//	0xC056 地址范围外     —— 越过了这个区的末尾。这正是「读到末尾就停」要抓的信号。
//	0x4031 指定软元件编号超出范围 —— **上面那两条在 iQ 家族上的写法**，语义完全相同，
//	                           只是码值落进了 0x4xxx 那一段。真机实测（见 frame.go）：
//	                           D/W 越过已配置的范围、以及文件寄存器区为空时的任何
//	                           ZR/R 地址，回的都是它。**漏掉它就是这个 bug 的样子**：
//	                           在一台没配置文件寄存器的 PLC 上扫 ZR，每个地址都回
//	                           0x4031，而扫描把它当成"设备状态问题"中止 ⇒ 用户拿到的是
//	                           "扫描失败：扫描 ZR0.0 … 时读取失败: mc: end code 0x4031"，
//	                           而不是"这里没有点位"。
//	0xC05B 点数指定错误   —— 请求的点数超出了这个型号的单次上限。
//	                           它**看着像"我们的请求形状错了"**，但扫描期间子命令与
//	                           软元件是恒定的，会变的只有范围；而拆到单点后点数恒为 1、
//	                           必然能读。于是把它归进"地址问题"只会让请求数变多
//	                           （对数级），**不会截断清单** —— 收益是"老型号上单请求
//	                           上限比配置值小"这种情形能自愈，代价只是慢一点。
//
// **其余一律中止**，两类都要点名：
//
//	0xC0EF 通信错误（PLC 忙 / 程序运行中）—— 这是**可恢复的设备状态**，与地址无关。
//	           拿它做推断是危险的：一次偶然的忙就会把整份点位清单从中间截断，
//	           而用户看到的是一份短了一截、却看不出任何异常的清单。宁可中止并
//	           如实报错（已扫到的部分照常返回），也不要给一份静默变短的清单。
//	其余未知码（0xC059/0xC05C/0xC05E 等请求形状错误、0xC200 密码锁、以及一切
//	           本仓库没有依据的码）—— 同上，中止。
//
// 白名单与黑名单**各断言一遍**（pointscan_test.go 的 TestIsAddressEndCode）：
// 这个分类错在哪个方向都有明确的坏结果 —— 收宽了会静默截断清单，
// 收窄了会把一段本来扫得完的地址变成一条错误。
func isAddressEndCode(err error) bool {
	var e *mcEndCodeError
	if !errors.As(err, &e) {
		return false
	}
	switch e.code {
	case endCodeDeviceIllegal, endCodeAddrRange, endCodeDeviceRange, endCodePointsError:
		return true
	default:
		return false
	}
}

// ==================== 驱动内部资源的取用 ====================

// scanSession 一次点位扫描要用的驱动内部资源。
//
// 每次 Do 现取、用完即弃（见 ScanPoints 里 read 的注释）。
type scanSession struct {
	client mcTransport
	cfg    *MCConfig
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
// 而这里只有 MC 需要 —— 与 s7drv / finsdrv 的 scanSessionOf 同一条取舍。
func scanSessionOf(drv driver.Driver) (*scanSession, error) {
	d, ok := drv.(*mcDriver)
	if !ok {
		return nil, fmt.Errorf("mc: 不支持的驱动类型 %T", drv)
	}

	// 只读 d.client / d.config（Read 路径也只在 RLock 下取它们）。
	// **不碰 d.rangeCache**：那是采集侧的区间指纹缓存，扫描既不该读也不该写。
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.client == nil || d.config == nil || !d.client.IsConnected() {
		return nil, errors.New("mc: 设备未连接，无法扫描")
	}
	return &scanSession{client: d.client, cfg: d.config}, nil
}
