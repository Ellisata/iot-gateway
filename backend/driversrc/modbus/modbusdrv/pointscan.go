// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	goburrowModbus "github.com/goburrow/modbus"

	"iot-gateway/driver"
)

// ==================== 点位范围扫描 ====================
//
// 对一台**已存在的设备**扫一段地址范围，把读得到的地址列出来。
//
// **这个功能产出的是一份「候选清单」，不是这台设备的点位表。** Modbus 没有任何
// 标准手段能枚举设备上有哪些点位（除了一些厂商私有的 FC 之外），所以这里做的是
// 「在这个范围里，哪些地址读得到」——而"读得到"与"有意义"是两回事：
//
//   - 非零的地址不一定有意义（可能是标定表、计数器、别人的数据）；
//   - 读得到 0 的地址可能恰恰是有效值（还没开机的状态、本来就应该为 0 的量）。
//
// 因此插件**不做零值过滤**（那是前端的一枚开关）：在所有寄存器都是 0 的设备上，
// 插件侧过滤会返回一份空列表，用户据此得出"这里没有点位"——与"没扫到"同型的错误。
//
// **不复用 driver.Read / readRegisterRanges。** 那两条路都是为**采集**设计的：
// 它们按设备的 cfg 做区间规划（配置模式下会给出错位的值，见 ScanPoints 的注释），
// 且连续区间失败时直接markRangeZero 把整段标 0 —— "整块→子段→单地址"的降级
// 根本不会发生，而我们恰恰要靠它把范围里的空洞一个个摘出来。

// SessionAccess 与 driversdk.SessionAccess 同形，但**刻意本地声明而不 import
// driversdk**：与 probe.go 的 ProbeTCP 收 target/options/budget 三个原始值
// （而不是 driversdk.ProbeRequest）是同一条取舍 —— 协议实现只依赖 driver 这一份
// 契约，不被整套插件开发套件绑住，本包因此也能脱离插件进程单独测试。
type SessionAccess interface {
	Do(fn func(drv driver.Driver) error) error
}

// 点位扫描的规模上限。
const (
	// pointScanRegChunk FC3/FC4 单帧能读的寄存器上限（Modbus 协议规定 125）。
	pointScanRegChunk = 125

	// pointScanBitChunk FC1/FC2 单帧能读的位上限（Modbus 协议规定 2000）。
	pointScanBitChunk = 2000

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

// 扫描结果里"假设的通用类型名"。
//
// **刻意不让插件猜类型**：网关侧拿不到解码函数，而按值推断必然出错 ——
// 模拟量恒为 0/1 时会被推断成 Boolean，且这种错误只在时序库里表现为
// "字段类型变了"，比 panic 更难发现（同 abbdrv/types.go 的 resolveKind 那段论证）。
// 因此寄存器区一律按 Word、位区一律按 Boolean 读，把类型的选择交给用户。
const (
	pointScanRegCommonType = driver.TypeWord
	pointScanBitCommonType = driver.TypeBoolean
)

// pointScanZeroNote 值为 0 的点位带的说明。
//
// 判据是**原始字节全 0**，不是解析后的值：假设类型是 Float 时 0x00000001 会解成
// 一个极小的非零数，按解析值判"零"会漏掉它，"显示 0 值"这枚开关也就漏了点。
const pointScanZeroNote = "当前值为 0"

// pointScanForm Modbus 点位扫描的参数表单（form-create schema）。
//
// 文案要把**这个功能做不到什么**说在前面：用户最容易的误解是"扫出来 = 这台设备
// 的点位表"，而实际上没被读到的地址既可能不存在、也可能存在但这一段被拒绝了。
const pointScanForm = `{
  "rule": [
    {"type":"select","field":"functionCode","title":"功能区","$required":false,"value":"3",
     "info":"决定要扫哪一片地址空间，也决定生成的点位地址前缀（保持寄存器 %MW、输入寄存器 %IW、线圈 %MX、离散输入 %IX）",
     "options":[{"label":"保持寄存器 (FC3)","value":"3"},{"label":"输入寄存器 (FC4)","value":"4"},
                {"label":"线圈 (FC1)","value":"1"},{"label":"离散输入 (FC2)","value":"2"}]},
    {"type":"inputNumber","field":"startAddress","title":"起始地址","$required":true,"value":0,
     "info":"0-based 偏移量，与点位地址写法一致（如起始 100 对应 %MW100）。不是 4xxxx 那种 1-based 编号"},
    {"type":"inputNumber","field":"count","title":"扫描数量","$required":true,"value":64,
     "info":"寄存器区单位为寄存器（单帧上限 125，超出会自动分块）；位区单位为位（单帧上限 2000）。数量越大耗时可观：整段扫描有预算上限，超时会返回已完成的部分并把「未扫完」标出来"}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// PointDiscovery 返回 Modbus 的点位扫描能力声明，供 main.go 填进 Meta.Protocols。
//
// **TCP 与 RTU 都挂**（与 Discovery 只挂 TCP 正相反）：点位扫描的目标是一台
// 已存在的设备，串口设备同样有"它有哪些点位"这个问题，而且串口恰是会话复用
// 最重要的场景 —— 回落临时实例会抢占串口句柄，把采集打断。
func PointDiscovery() *driver.PointDiscoverySpec {
	return &driver.PointDiscoverySpec{
		Title: "扫描 Modbus 地址范围",
		Description: "按功能区扫一段地址，列出**读得到**的地址与当前值。" +
			"设备不会告诉你它有哪些点位：非零的地址不一定有意义，读得到 0 的也可能是有效值，" +
			"没列出来的地址既可能不存在、也可能只是这一段被设备拒绝了。请把结果当作候选清单，而不是点位表",
		FormJSON: json.RawMessage(pointScanForm),
		// "Word" 是 TypeMap 的键（通用类型名）—— 写成内部裸名 "word" 会被
		// Meta.Validate 拒掉。寄存器区按 Word 读，这也正是前端的类型下拉初值。
		DefaultCommonDataType: pointScanRegCommonType,
		// 20000ms 是留给"串口 + 大范围"的空间，但注意**网关侧还会把它钳到
		// 7000ms**（pluginhost 的 pointScanGatewayBudgetCapMs，因为这条链路是
		// 同步 HTTP，超时会拖住网关停机）。实际生效的总是被钳过的那一个。
		MaxBudgetMs: 20000,
		MaxPoints:   pointScanMaxPoints,
	}
}

// pointScanOptions 解析后的扫描参数。
type pointScanOptions struct {
	functionCode byte
	start        uint16
	count        uint16
}

// pointScanOptionsRaw 扫描选项的原始形态。键名与 pointScanForm 的 field 一一对应。
//
// 用 rawInt 而不是 int：form-create 的 select 控件产出的是**字符串**
// （"3"），声明成 int 时一份带字符串的选项会让 json.Unmarshal 整体报错 ——
// 不是那一个字段失效，而是整次扫描全失败。这与 config.go 里同名取舍同一条理由。
type pointScanOptionsRaw struct {
	FunctionCode rawInt `json:"functionCode"`
	StartAddress rawInt `json:"startAddress"`
	Count        rawInt `json:"count"`
}

// parsePointScanOptions 解析扫描选项。
//
// 与 scanOptions 的取舍不同：那边缺项时静默回默认值（站号"没填就是要探 1 号"），
// 这里**缺失或不合法一律报错**。"扫多少"没有合理的默认值 —— 猜一个 64 会让用户
// 拿到一份他并没有要求的清单，而多扫的那部分在串口上要花掉几秒。
func parsePointScanOptions(options json.RawMessage) (pointScanOptions, error) {
	opt := pointScanOptions{functionCode: FuncCodeReadHoldingRegisters}
	if len(options) == 0 {
		return opt, errors.New("扫描选项为空：需要指定功能区、起始地址与数量")
	}

	var raw pointScanOptionsRaw
	if err := json.Unmarshal(options, &raw); err != nil {
		return opt, fmt.Errorf("解析扫描选项失败: %w", err)
	}

	if raw.FunctionCode.set {
		switch byte(raw.FunctionCode.val) {
		case FuncCodeReadCoils, FuncCodeReadDiscreteInputs,
			FuncCodeReadHoldingRegisters, FuncCodeReadInputRegisters:
			opt.functionCode = byte(raw.FunctionCode.val)
		default:
			return opt, fmt.Errorf("功能码 %d 不是可读功能区（只支持 1/2/3/4）", raw.FunctionCode.val)
		}
	}
	if raw.StartAddress.set {
		if raw.StartAddress.val < 0 || raw.StartAddress.val > 65535 {
			return opt, fmt.Errorf("起始地址 %d 超出 [0,65535]", raw.StartAddress.val)
		}
		opt.start = uint16(raw.StartAddress.val)
	}
	if !raw.Count.set || raw.Count.val <= 0 {
		return opt, errors.New("扫描数量必须大于 0")
	}
	// 地址空间是 uint16，越过 65535 的部分不存在 —— 截断而不是报错：
	// 用户填 "从 60000 起扫 10000 个" 的意图很清楚，就是要扫到末尾。
	end := int(opt.start) + raw.Count.val
	if end > 1<<16 {
		end = 1 << 16
	}
	opt.count = uint16(end - int(opt.start))
	return opt, nil
}

// ScanPoints 扫描一段 Modbus 地址范围。
//
// sess 是读驱动实例的唯一入口（见 SessionAccess）：**每块读一次 Do**，
// 块与块之间锁是放开的，因此一次扫描不会把采集卡死几十秒。
//
// **配置模式（protocol_json 里 startAddress > 0）一律拒绝。** 理由见
// scanSessionOf：那种设备上"地址"的语义由配置决定，扫描按功能区生成的地址会被
// 配置里的 FunctionCode 读走，而解析是越界安全的 —— 不报错、不 panic，
// 只是静默给出一份错位的值。更糟的是这些地址一旦建成 device_address，
// 那个点位会**永远取到别处的数据**（在时序库里表现为一条完全合理的曲线）。
//
// 返回的 error 与结果**可以同时非 nil**：中止性错误（设备掉线）发生时，
// 已经扫到的部分一并返回 —— 丢掉它们对用户没有任何好处。
func ScanPoints(sess SessionAccess, options json.RawMessage, deadline time.Time) (*driver.PointScanResult, error) {
	opt, err := parsePointScanOptions(options)
	if err != nil {
		return nil, err
	}

	sc := &pointScanner{
		sess:     sess,
		opt:      opt,
		deadline: deadline,
		result: &driver.PointScanResult{
			AssumedCommonDataType: opt.commonType(),
		},
		started: time.Now(),
	}

	// 预检：在**开始扫描之前**把"这台设备根本不能扫"的两种情形判掉 ——
	// 配置模式（会给出错位的值）与根本没有可用连接。
	// 一次额外的 Do 换一条明确的报错，比让用户拿到一份错位的清单便宜得多。
	//
	// 预检的结论走 preflightErr 而不是 Do 的返回值：那是**设备配置**的问题，
	// 不是会话坏了，混在一起会被当成"插件调用失败"报上去，指向完全错误的方向。
	var preflightErr error
	if err := sess.Do(func(drv driver.Driver) error {
		s, err := scanSessionOf(drv)
		if err != nil {
			preflightErr = err
			return nil
		}
		if s.cfg.StartAddress > 0 {
			preflightErr = errConfigModeNotScannable
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if preflightErr != nil {
		return nil, preflightErr
	}

	// 逐块扫描。每块一次 Do：中途任何一块失败都不影响已经拿到的部分。
	for done := 0; done < int(opt.count); {
		if sc.result.Truncated {
			break
		}
		if time.Now().After(deadline) {
			sc.result.Incomplete = true
			break
		}

		qty := int(opt.count) - done
		if qty > sc.chunkSize() {
			qty = sc.chunkSize()
		}
		start := uint16(int(opt.start) + done)

		chunkErr := sc.runChunk(start, uint16(qty))
		done += qty
		if chunkErr != nil {
			if errors.Is(chunkErr, errPointScanStop) {
				// 达到 MaxPoints 或预算耗尽：不是错误，收工。
				break
			}
			return sc.finish(), chunkErr
		}
	}

	return sc.finish(), nil
}

// errPointScanStop 内部哨兵：停止扫描，但**不是错误**（达到 MaxPoints 或预算耗尽）。
var errPointScanStop = errors.New("modbus: point scan stop")

// pointScanner 一次扫描的可变状态。
type pointScanner struct {
	sess     SessionAccess
	opt      pointScanOptions
	deadline time.Time
	started  time.Time
	result   *driver.PointScanResult

	// incomplete 预算耗尽（块间或降级途中）。与 Truncated 是两回事：
	// 那个是"结果条数到上限"，这个是"范围还没扫完"，前端必须分开显示。
	incomplete bool

	// lastCfg 最后一次 Do 看到的设备配置，只用来定字节序/字序的**展示**。
	// 详见 cfgByteOrder 的说明。
	lastCfg *ModbusTcpConfig
}

// chunkSize 本功能区单帧能读多少个单位（寄存器或位）。
func (sc *pointScanner) chunkSize() int {
	if sc.opt.isBit() {
		return pointScanBitChunk
	}
	return pointScanRegChunk
}

// runChunk 在一个会话（持锁）内扫完一个块。
func (sc *pointScanner) runChunk(start, count uint16) error {
	return sc.sess.Do(func(drv driver.Driver) error {
		s, err := scanSessionOf(drv)
		if err != nil {
			return err
		}
		// 配置模式在这里再判一次：预检与这次 Do 之间设备可能被改了配置
		//（热刷新会重建驱动实例，Do 拿到的可能是新的那个）。
		if s.cfg.StartAddress > 0 {
			return errConfigModeNotScannable
		}
		sc.lastCfg = s.cfg
		return sc.walk(s, start, count)
	})
}

// errConfigModeNotScannable 设备处于"配置模式"，点位扫描拒绝执行。
var errConfigModeNotScannable = errors.New(
	"该设备的连接配置里指定了起始地址与功能码（配置模式），此时地址的含义由配置决定，" +
		"扫描会给出错位的值。请改用自动模式（清空 protocol_json 的 startAddress）后再扫描")

// walk 扫一个区间，失败时对半降级。
//
// 降级的形状是「整段 → 对半 → … → 单点」：设备对含空洞的区间会整段拒绝
// （IllegalDataAddress），只有一路拆到单点才能把"哪几个地址真的不存在"摘出来。
// readRegisterRanges 那条路做不到这件事 —— 它整段失败就直接 markRangeZero，
// 把一段里所有点都标成 Quality=0 了事。
func (sc *pointScanner) walk(s *scanSession, start, count uint16) error {
	if count == 0 {
		return nil
	}
	if time.Now().After(sc.deadline) {
		sc.incomplete = true
		return errPointScanStop
	}

	raw, err := sc.read(s, start, count)
	if err == nil {
		return sc.emit(start, count, raw)
	}
	if !isUnreadableAddrErr(err) {
		// 连接类错误（超时、EOF、从站无应答）：**中止而不是记成"地址不存在"**。
		// 把设备掉线记成"这里没有点位"，用户会从一份几乎全是"读不到"的清单里
		// 得出与事实完全相反的结论，而且采集侧此时也已经在重连了。
		return fmt.Errorf("扫描 [%d, %d) 时连接中断: %w", start, start+count, err)
	}

	if count == 1 {
		// 拆到单点了还是被拒：这个地址就是读不到。
		sc.result.Scanned++
		sc.result.Unreadable++
		return nil
	}
	half := count / 2
	if err := sc.walk(s, start, half); err != nil {
		return err
	}
	return sc.walk(s, start+half, count-half)
}

// read 按功能区读一段。
func (sc *pointScanner) read(s *scanSession, start, count uint16) ([]byte, error) {
	switch sc.opt.functionCode {
	case FuncCodeReadHoldingRegisters:
		return s.reader.ReadHoldingRegisters(start, count)
	case FuncCodeReadInputRegisters:
		return s.reader.ReadInputRegisters(start, count)
	case FuncCodeReadCoils:
		return s.reader.ReadCoils(start, count)
	case FuncCodeReadDiscreteInputs:
		return s.reader.ReadDiscreteInputs(start, count)
	default:
		return nil, fmt.Errorf("modbus: 不支持的功能码 %d", sc.opt.functionCode)
	}
}

// emit 把一次成功读取的结果按地址逐个产出。
//
// 逐个产出而不是整段写进 Points，是为了让 MaxPoints 截断**精确**：
// 达到上限时立刻停手，超出的地址既不计入 Scanned/Readable，也不进列表 ——
// 这样"可读到 M 个"与列表长度始终自洽（否则位区一个 2000 位的块能把
// 上限顶到两倍）。
func (sc *pointScanner) emit(start, count uint16, raw []byte) error {
	for i := uint16(0); i < count; i++ {
		if len(sc.result.Points) >= pointScanMaxPoints {
			sc.result.Truncated = true
			return errPointScanStop
		}
		value, ok := sc.slice(raw, i)
		if !ok {
			// 应答长度与请求数量不符：设备给了个残缺的帧。
			// 不猜，按"这个地址读不到"处理并停在这一块上。
			sc.result.Scanned++
			sc.result.Unreadable++
			continue
		}
		sc.result.Scanned++
		sc.result.Readable++
		sc.result.Points = append(sc.result.Points,
			sc.buildPoint(start+i, value))
	}
	return nil
}

// slice 取出第 i 个地址的**原始字节**。
//
// 寄存器区每个地址 2 字节，位区每个地址 1 位（展开成一个字节 0/1，
// 好让"零值"的判据在两种区里是同一条）。
func (sc *pointScanner) slice(raw []byte, i uint16) ([]byte, bool) {
	if sc.opt.isBit() {
		byteIdx := int(i) / 8
		if byteIdx >= len(raw) {
			return nil, false
		}
		return []byte{(raw[byteIdx] >> (i % 8)) & 0x01}, true
	}
	off := int(i) * 2
	if off+2 > len(raw) {
		return nil, false
	}
	return raw[off : off+2], true
}

// buildPoint 组装一个点位。
func (sc *pointScanner) buildPoint(addr uint16, raw []byte) driver.DiscoveredPoint {
	p := driver.DiscoveredPoint{Name: formatPointName(sc.opt.functionCode, addr)}

	if allZeroBytes(raw) {
		// Value 留空、只给 Note：零值点位在界面上是"没有值"的那一类，
		// 前端据此实现"显示 0 值"这枚开关（插件侧刻意不做过滤）。
		p.Note = pointScanZeroNote
		return p
	}

	v, err := ParseReadResult(raw, sc.opt.valueType(), sc.cfgByteOrder(), sc.cfgWordOrder())
	if err != nil {
		// 解析失败不改判"读到了"：地址确实是读到了，只是值渲染不出来。
		p.Note = "值解析失败：" + err.Error()
		return p
	}
	p.Value = FormatValue(v, sc.opt.valueType())
	return p
}

// cfgByteOrder/cfgWordOrder 用**最后一次 Do 看到的**设备配置。
//
// 热刷新会重建驱动实例，字节序可能已经变了；而这两个值只影响"值的展示"，
// 不影响"地址存在与否"这个主结论，所以取最新一次即可，不必为它多取一次会话。
func (sc *pointScanner) cfgByteOrder() string {
	if sc.lastCfg == nil {
		return ByteOrderBigEndian
	}
	return sc.lastCfg.ByteOrder
}

func (sc *pointScanner) cfgWordOrder() string {
	if sc.lastCfg == nil {
		return WordOrderBigEndian
	}
	return sc.lastCfg.WordOrder
}

// finish 收尾：填耗时、Incomplete 与统计的收口。
func (sc *pointScanner) finish() *driver.PointScanResult {
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

// isBit 本位区（线圈/离散输入）。
func (o pointScanOptions) isBit() bool {
	return o.functionCode == FuncCodeReadCoils || o.functionCode == FuncCodeReadDiscreteInputs
}

// valueType 本位区/寄存器区读取时假设的**内部类型裸名**。
func (o pointScanOptions) valueType() string {
	if o.isBit() {
		return typeNameBool
	}
	return typeNameWord
}

// commonType 本位区/寄存器区假设的**通用类型名**（结果里回给前端的那一个）。
func (o pointScanOptions) commonType() string {
	if o.isBit() {
		return pointScanBitCommonType
	}
	return pointScanRegCommonType
}

// formatPointName 把 (功能区, 0-based 偏移) 渲染成点位地址写法。
//
// **必须用 IEC 61131-3 的 %MW/%IW/%MX/%IX 形式**，因为只有它经 ParsePLCAddress
// 往返之后还能还原回同一个 (功能区, 偏移)。传统写法在下面这三处都会坏
// （都是 ParsePLCAddress 的实测行为，由 TestTraditionalFormatWouldBreakThis 钉住）：
//
//	满量程地址  (FC3, 65535) 的传统写法是 "465536"（4 + 6 位 1-based 65536），
//	          ParseUint(s[1:], 10, 16) 解不出 65536 ⇒ **整个串解析不出来** ⇒
//	          该点位被摘出区间规划、永远采不到值（修复前是区间规划直接报错 ⇒
//	          **整台设备的采集全部失败**）；
//	差一       传统写法是 1-based，"465535" 指的是偏移 65534 而不是 65535；
//	功能区丢失 线圈区的偏移 10000 写成 5 位是 "10000"，它落进**纯数字分支**
//	          （functionCode = 0，未知）而不是线圈区，读它的 FC 由设备的
//	          cfg.FunctionCode 决定 —— 也就是被当成保持寄存器读。
//
// IEC 写法没有这些问题：tryIECFormat 纯 0-based，且 ParseUint(numStr, 10, 16)
// 恰好覆盖 uint16 满量程（%MW65535 可用）。
//
// 这条不只是"好看"：一个解析不出来的地址写进 device_address 之后，这个点位永远
// 采不到值（只报「地址无效」，要改配置才恢复）。往返由
// pointscan_test.go 的 TestPointNameRoundTrip 钉住。
func formatPointName(functionCode byte, addr uint16) string {
	var prefix string
	switch functionCode {
	case FuncCodeReadHoldingRegisters:
		prefix = "%MW"
	case FuncCodeReadInputRegisters:
		prefix = "%IW"
	case FuncCodeReadCoils:
		prefix = "%MX"
	case FuncCodeReadDiscreteInputs:
		prefix = "%IX"
	default:
		// 解析期已经拒绝了其余功能码，走到这里说明代码被改坏了；
		// 给一个明显不对的前缀比给一个"看起来正常但会错位"的好。
		prefix = "%?"
	}
	return fmt.Sprintf("%s%d", prefix, addr)
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

// isUnreadableAddrErr 这条错误是否等于设备在说"这个地址我读不了"。
//
// 只有 IllegalFunction(1) 与 IllegalDataAddress(2) 算 —— 它们是设备**明确拒绝
// 这个请求**的答复，据此才能把"地址不存在"与"设备掉线"分开。**其余异常一律中止扫描**：
// 宁可少扫一段并如实报告，也不要把"从站忙/网关路径不通"记成"这些地址不存在"，
// 后者会往用户的候选清单里塞进一批假的"读不到"。
func isUnreadableAddrErr(err error) bool {
	var me *goburrowModbus.ModbusError
	if !errors.As(err, &me) {
		return false
	}
	return me.ExceptionCode == goburrowModbus.ExceptionCodeIllegalFunction ||
		me.ExceptionCode == goburrowModbus.ExceptionCodeIllegalDataAddress
}

// ==================== 驱动内部资源的取用 ====================

// scanSession 一次点位扫描要用的驱动内部资源。
//
// 每次 Do 现取、用完即弃：热刷新会重建驱动实例、重连会换掉 client，
// 缓存下来的指针读的是一个已经 Close 的句柄（拿回上一轮的缓存值，
// 或不巧触发 nil 解引用把整个插件进程带走）。
type scanSession struct {
	reader modbusPointReader
	cfg    *ModbusTcpConfig
}

// modbusPointReader TCP 与 RTU 两种客户端共有的读方法集（签名逐一一致），
// 让扫描逻辑只写一份。
type modbusPointReader interface {
	ReadHoldingRegisters(address, quantity uint16) ([]byte, error)
	ReadInputRegisters(address, quantity uint16) ([]byte, error)
	ReadCoils(address, quantity uint16) ([]byte, error)
	ReadDiscreteInputs(address, quantity uint16) ([]byte, error)
}

// scanSessionOf 从驱动实例取出扫描要用的客户端与配置视图。
//
// 用类型开关而不是给 driver.Driver 加方法：那会要求全部协议都实现它，
// 而这里只有 Modbus 的两种传输层需要。
func scanSessionOf(drv driver.Driver) (*scanSession, error) {
	switch d := drv.(type) {
	case *modbusTCPDriver:
		d.mu.RLock()
		defer d.mu.RUnlock()
		if d.client == nil || d.config == nil || !d.client.IsConnected() {
			return nil, errors.New("modbus: 设备未连接，无法扫描")
		}
		// 扫描用的是设备自己的从站号：多从站网关上不设这一下，
		// 读的是上一次请求留下的那个从站的地址空间。
		d.client.SetUnitID(d.config.UnitID)
		return &scanSession{reader: d.client, cfg: d.config}, nil

	case *modbusRTUDriver:
		d.mu.RLock()
		defer d.mu.RUnlock()
		if d.client == nil || d.config == nil || !d.client.IsConnected() {
			return nil, errors.New("modbus rtu: 设备未连接，无法扫描")
		}
		d.client.SetUnitID(d.config.UnitID)
		return &scanSession{reader: d.client, cfg: sharedTCPConfig(d.config)}, nil
	}
	return nil, fmt.Errorf("modbus: 不支持的驱动类型 %T", drv)
}
