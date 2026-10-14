// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

// ==================== 点位扫描（设备地址页的「自动发现点位」） ====================
//
// 对**一台已存在的设备**把数据标识逐个问一遍，列出答得上来的那些。
//
// **本协议有一份标准化的数据标识字典**（dictionary.go），所以这里的形态与
// Modbus / S7 / FINS / MC 完全不同：那边是"扫一段地址范围"，这边是"枚举一份已知
// 的清单"——是 specs/point-discovery.md 里说的**原生枚举**（与 Rockwell.CIP、
// OPC.UA 同族），但它比那两个多一样东西：**有采样值**。读一遍既是判定"这块表
// 有没有这个数据标识"的手段，也顺带得到了值。
//
// 因此结果里**逐点**给 CommonDataType（每个数据标识的输出形态由它自己决定：
// 表号是 BCD 数字串、运行状态字是二进制位域、电量是 2 位小数的工程值…），
// 而结果级的 AssumedCommonDataType 也**非空**——它在这里是"本次结果确实带采样值"
// 这条事实的载体（前端据它决定是否显示"显示 0 值"开关），不是"为了保证能读而
// 假设的类型"（645 的类型从不影响读取：长度/小数位/符号/编码全在数据标识里，
// 见 decode.go）。这处用法与其它协议都不同，改动前请先读
// driver/pointDiscovery.go 里 AssumedCommonDataType / CommonDataType 的注释。
//
// **产出仍然是候选清单，不是点位表**：字典里有、但这块表没实现的数据标识很多
// （1997 与廉价单相表尤其明显），所以 Unreadable 是个重要的数字，不是异常信号。
// 插件侧**不做零值过滤**（过滤是前端的一枚开关）：在全都是 0 的表上过滤会返回一份
// 空清单，用户据此得出"这里没有点位"——与"没扫到"同型的错误。
//
// **不复用 drv.Read**：那是为采集设计的路径，会按 batch 建 readPlan 并写进驱动自己的
// planCache（RangeCacheMaxEntries=32，扫描的批次会把它挤掉、迫使采集重建规划），
// 还要为每个数据标识合成一行 po.DeviceAddress。这里直接用驱动自己的封帧/收发/解码
// 原语（buildReadFrame / exchange / fieldSlice / DecodeValue），因此扫描说出去的
// 每一帧与采集逐字相同：前导字节、校验和开关、收发间延时、超时全部取自设备配置。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"iot-gateway/driver"
)

// 点位扫描的规模上限。
const (
	// pointScanMaxPoints 单次扫描最多返回多少个点位。
	//
	// 字典本身只有 44（2007）/ 18（1997）条，256 是留给附加数据标识的余量，
	// 同时也是**事故半径**的上界：pluginwire 写帧持插件进程的写锁，一份大应答会
	// 头阻塞该进程上所有协议的应答。与 PointDiscovery().MaxPoints 必须一致 ——
	// 后者只是把它报给网关与前端。
	pointScanMaxPoints = 256

	// pointScanMaxExtraDIs 表单里「附加数据标识」最多收多少条。
	// 一份手滑贴进来的手册目录不该把一次扫描拖成几千个请求。
	pointScanMaxExtraDIs = 64

	// pointScanZeroNote 值为 0 的点位带的说明（与 modbusdrv 同一条约定）。
	//
	// 判据是**原始字节全 0**，不是解析后的值：按 Float 解析时 0x00000001 会解成
	// 一个极小的非零数，按解析值判"零"会漏掉它，"显示 0 值"那枚开关也就漏了点。
	pointScanZeroNote = "当前值为 0"
)

// pointScanForm DLT645 点位扫描的参数表单（form-create schema）。
//
// **只有一个可选项**：字典之外的附加数据标识。协议版本、单帧数据标识数、超时、
// 校验和开关、前导字节一律取自**这台设备自己的配置** —— 扫描与采集因此走同一份
// 参数，用户不必在弹窗里再填一遍，也就不可能填出与采集不一致的值。
const pointScanForm = `{
  "rule": [
    {"type":"input","field":"extraDIs","title":"附加数据标识","$required":false,
     "info":"可留空。字典未收录的厂商私有数据标识，用逗号或换行分隔；每条按地址语法写 DI[:字节数[:小数位[:标志]]]，如 12345678:4:2:s；与字典里的数据标识重复时以你写的格式为准。留空即只扫内置字典",
     "props":{"type":"textarea","rows":3,"placeholder":"12345678:4:2:s"}}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// PointDiscovery 返回 DL/T 645 的点位扫描能力声明，供 main.go 填进 Meta.Protocols。
//
// **串口与 TCP 共用同一份**（与 Modbus 把 PointDiscovery 同时挂 TCP/RTU 同一条取舍）：
// 点位扫描的目标是一台**已存在的设备**，串口设备同样有"它有哪些点位"这个问题，
// 而且串口恰恰是会话复用最重要的场景（回落临时实例会抢占串口句柄，把采集打断）。
// 这与"串口不得声明 Discovery（扫网段）"是两条相反的规矩，理由见
// specs/point-discovery.md §3。
func PointDiscovery() *driver.PointDiscoverySpec {
	return &driver.PointDiscoverySpec{
		Title: "扫描 DL/T 645 数据标识",
		Description: "把 DL/T 645 内置数据标识字典里的项目逐个读一遍，列出**读得到**的那些与当前值，" +
			"并可按地址语法补上一批字典未收录的厂商私有数据标识。" +
			"**字典里有、但这块表没实现的很多**（1997 版与廉价单相表尤其明显），" +
			"「读不到」的条数因此是个正常数字——它只说明这块表没有那个数据标识，不代表链路有问题。" +
			"少数老表对不支持的数据标识**干脆不答**（而不是回异常应答），这种表每问一个都会等到超时，扫描会很快撞上预算上限；" +
			"串口设备还受网关侧 7000ms 的预算钳制（插件声明的 20s 会被钳到这个值），大字典扫描会经常标着「未扫完」返回。请把结果当作候选清单，而不是点位表",
		FormJSON: json.RawMessage(pointScanForm),
		// "Float" 是 TypeMap 的键（通用类型名）—— 写成内部裸名 "float" 会被
		// Meta.Validate 拒掉。字典里绝大多数条目是带小数位的数值量，故取它作
		// 下拉初值；逐点类型在结果里逐个给出，它只是兜底。
		DefaultCommonDataType: driver.TypeFloat,
		// 20000ms 是留给"串口 + 大字典"的空间，但注意**网关侧还会把它钳到 7000ms**
		// （pluginhost 的 pointScanGatewayBudgetCapMs，因为这条链路是同步 HTTP，
		// 超时会拖住网关停机）。实际生效的总是被钳过的那一个。
		MaxBudgetMs: 20000,
		MaxPoints:   pointScanMaxPoints,
	}
}

// SessionAccess 与 driversdk.SessionAccess 同形，但**刻意本地声明而不 import
// driversdk**：与 probe.go / modbusdrv 的同名接口是同一条取舍 —— 协议实现只依赖
// driver 这一份契约，不被整套插件开发套件绑住，本包因此也能脱离插件进程单独测试。
type SessionAccess interface {
	Do(fn func(drv driver.Driver) error) error
}

// scanTarget 一个待扫的数据标识。
type scanTarget struct {
	name   string // 写进 device_address.name 的地址写法（必须能被 ParseAddress 还原）
	label  string // 建议标签（字典中文名 + 单位）
	spec   DISpec
	common string // 建议的通用类型名（TypeMap 的键）
	intern string // 解码用的内部类型裸名
}

// scanItem 一帧里单个点位的读取结果。
type scanItem struct {
	value string
	// zero 取到了值，但原始字节全 0（前端据此实现"显示 0 值"开关）。
	zero bool
	// ok 取到了值（**含值为 0 的**）。false 表示这一点位读不到：不产出点位行，
	// 只计入 Unreadable —— 契约要求 len(points) <= readable <= scanned，
	// 读不到的数据标识不该出现在清单里（与 Modbus/FINS/MC 一致）。
	ok bool
}

// dltPointReadFn 读一帧（一批点位）的原始读取原语。
//
// 抽这条缝是为了能测：整块被拒后的降级、数据域错位、静默超时后的存活复探、
// 预算耗尽——这几条最容易写错的路径都要靠"读到某一步就报特定错误"来驱动，
// 而真表只会一律成功。与 s7drv / finsdrv / mitsubishidrv 的 readFn 同一条取舍。
//
// 生产路径每次调用都经 SessionAccess.Do 现取一次驱动实例；测试路径直接指向一个假函数。
type dltPointReadFn func(targets []scanTarget) ([]scanItem, error)

var (
	// errPointScanStop 内部哨兵：停止扫描，但**不是错误**（预算耗尽 / 达到 MaxPoints）。
	errPointScanStop = errors.New("dlt645: point scan stop")

	// errFollowUp 应答带后续帧标志（0xB1 / 0xB2）。驱动刻意不支持拼接后续帧
	// （见 codec.go 的 hasFollowUp），首帧数据只是该数据项的一部分 —— 拿它解码会
	// 得到一个**看似合理实则被截断**的值，比取不到值危险得多。
	errFollowUp = errors.New("dlt645: 应答带后续帧，本版本不支持拼接")

	// errFieldLength 成帧打包的应答，数据域短于请求的总长：有数据项没带数据。
	// 判据与理由见 readOneFrame。
	errFieldLength = errors.New("dlt645: 应答数据域长度与请求不符")
)

// ScanPoints 扫描一台已存在的 DL/T 645 电能表上的数据标识。
//
// 选项只有一项可选内容（附加数据标识），协议版本等一律取自这台设备的配置 ——
// 而配置**不在选项里**：网关不把 protocol_json 透给 finder（见 driversdk.FindPointsRequest），
// 所以先做一次预检 Do 从驱动实例上取它。预检顺带把"根本没有可用连接"判在这里。
//
// 每一帧一次 sess.Do（与 finsdrv / mitsubishidrv / s7drv 的 readFn 同形）：会话锁
// 在一帧与下一帧之间是放开的，因此一次扫描不会把采集卡死。**不要**把整次扫描包进
// 一次 Do —— 那是 specs/point-discovery.md §6 明令禁止的形态。
//
// 返回的 error 与结果**可以同时非 nil**：中止性错误（链路中断、设备被采集抢走）
// 发生时，已经扫到的部分一并返回 —— 丢掉它们对用户没有任何好处（网关侧的
// warning 文案正是为这条路径准备的）。
func ScanPoints(sess SessionAccess, options json.RawMessage, deadline time.Time) (*driver.PointScanResult, error) {
	extras, err := parsePointScanOptions(options)
	if err != nil {
		return nil, err
	}

	// 预检：协议版本决定枚举哪一本字典，而它是**无法从选项里得到**的 —— 只能问
	// 驱动实例。结论走 preflightErr 而不是 Do 的返回值：那是**设备配置/连接状态**
	// 的问题，不是会话坏了，混在一起会被当成"插件调用失败"报上去，指向完全错误的方向。
	var (
		preflightErr error
		cfg          *DLT645Config
	)
	if err := sess.Do(func(drv driver.Driver) error {
		s, err := scanSessionOf(drv)
		if err != nil {
			preflightErr = err
			return nil
		}
		cfg = s.cfg
		return nil
	}); err != nil {
		return nil, err
	}
	if preflightErr != nil {
		return nil, preflightErr
	}

	targets, err := scanTargets(cfg, extras)
	if err != nil {
		return nil, err
	}

	read := func(batch []scanTarget) ([]scanItem, error) {
		// 每次现取驱动实例与它的 client/cfg/表地址，**块与块之间不缓存**：热刷新会
		// 重建实例、重连会换掉 client，缓存下来的读的是一个已经 Close 的句柄。
		var (
			items []scanItem
			rerr  error
		)
		if err := sess.Do(func(drv driver.Driver) error {
			s, err := scanSessionOf(drv)
			if err != nil {
				rerr = err
				return nil
			}
			items, rerr = readOneFrame(s, batch)
			return nil
		}); err != nil {
			return nil, err
		}
		return items, rerr
	}

	return scanWith(read, cfg, targets, deadline)
}

// scanWith 是 ScanPoints 的可测形态：读取原语以函数注入。
func scanWith(read dltPointReadFn, cfg *DLT645Config, targets []scanTarget, deadline time.Time) (*driver.PointScanResult, error) {
	s := &pointScanner{
		cfg:      cfg,
		targets:  targets,
		read:     read,
		deadline: deadline,
		started:  time.Now(),
		perRead:  perRead(cfg),
		result: &driver.PointScanResult{
			// 非空是**有意的**：它同时是前端"本次结果带采样值"的判据（据它显示
			// "显示 0 值"开关与"读得到/读不到"的措辞），而这次结果确实带采样值。
			// 逐点类型仍然逐行给出，本字段只是兜底与那枚开关的开关 —— 见文件头。
			AssumedCommonDataType: driver.TypeFloat,
		},
	}
	return s.run()
}

// perRead 单帧打包几个数据标识。
//
// **1997 版强制 1**：1997 的读命令数据域固定为一个 2 字节数据标识（plan.go 的
// buildPlan 是同一条规则，两处必须一致）。照抄 maxDIsPerRead 会让单帧数据域超过
// maxDataLen1997，而 parseAt 对超长的 L 直接判"不是帧头"——设备的**合法应答永远
// 解析不出来**，表现是每一帧都超时（看起来像"设备不在"，而不是"参数配错了"）。
func perRead(cfg *DLT645Config) int {
	if !cfg.Is2007() {
		return 1
	}
	if cfg.MaxDIsPerRead < 1 {
		return 1
	}
	return cfg.MaxDIsPerRead
}

// pointScanner 一次扫描的可变状态。
type pointScanner struct {
	cfg      *DLT645Config
	targets  []scanTarget
	read     dltPointReadFn
	deadline time.Time
	started  time.Time
	result   *driver.PointScanResult

	// perRead 当前每帧打包的数据标识数。首次撞上"整块被拒"（或数据域错位、带后续帧）
	// 就降到 1：此后每块都先发一帧注定被拒的批量请求纯属白等，而老表与实现了部分
	// 数据标识的表恰恰最常见。这是本实现比采集侧（readGroup 每轮都重试批量）更强的
	// 一点取舍，只影响扫描（采集的失败恢复语义不能动）。
	perRead int

	// linkChecked 本块内是否已经复探确认过"链路仍然是通的"。每块开头重置。
	//
	// 一个不答的数据标识只需在**本块第一次**静默时花一次复探：同块内后续的静默沿用
	// 同一个结论，否则一张有 n 个静默条目的表要花 2n 帧才问得完。每块重置则是为了
	// 兜住"复探之后链路才断"：那种情况下本块最多多记十几个"读不到"，下一块一进来
	// 就会重新复探并如实中止。
	linkChecked bool

	// incomplete 预算耗尽（块间判断）。与 Truncated 是两回事：那个是"结果条数到上限"，
	// 这个是"清单还没问完"，前端必须分开显示。
	incomplete bool
}

// run 逐块扫描，直到问完、到上限或中止。
func (s *pointScanner) run() (*driver.PointScanResult, error) {
	for done := 0; done < len(s.targets); {
		if s.expired() {
			s.incomplete = true
			break
		}
		n := len(s.targets) - done
		if n > s.perRead {
			n = s.perRead
		}
		if err := s.readChunk(done, n); err != nil {
			if errors.Is(err, errPointScanStop) {
				break
			}
			// 中止性错误：把已经扫到的部分一并返回 —— 丢掉它们对用户没有任何好处。
			return s.finish(), err
		}
		done += n
	}
	return s.finish(), nil
}

// readChunk 读一块，并按失败的性质决定降级还是中止。
//
// 返回 errPointScanStop 表示"停下了，但不是错误"（预算耗尽 / 达到上限）。
func (s *pointScanner) readChunk(start, count int) error {
	// 每块重新确认链路（见 linkChecked）。
	s.linkChecked = false

	batch := s.targets[start : start+count]
	items, err := s.read(batch)

	switch {
	case err == nil:
		return s.emit(start, batch, items)

	case errors.Is(err, errPointScanStop):
		return errPointScanStop

	case errors.Is(err, context.DeadlineExceeded):
		// 会话层在等待锁时就已经没预算了（sessionAccess.Do 的 wait<=0 分支）。
		// 这不是链路问题，如实报"未扫完"。
		s.incomplete = true
		return errPointScanStop
	}

	// 这一帧失败了，且**定位不到是哪一个数据标识**——异常应答不回显失败项、
	// 数据域错位、带后续帧标志，三种都是这个性质；而"设备干脆没答"（超时/EOF）
	// 同样定位不到（645 的静默有歧义，见 linkAlive 的注释）。**四条路都只能逐个
	// 重问**才能把"哪几个真的读不到"摘出来。
	//
	// 唯一的区别是"设备没答"那一类要先证明链路还是通的：不通就是设备掉线，必须
	// 中止——把"掉线"记成"这批数据标识都不存在"，用户会从一份几乎全是空的清单里
	// 得出与事实完全相反的结论，而采集侧此时已经在重连了。
	if !degradable(err) && !s.linkAlive() {
		return err
	}

	if count == 1 {
		// 单点：已经问到这一步了，结论就是"这一点位读不到"。
		if s.expired() {
			s.incomplete = true
			return errPointScanStop
		}
		s.markUnreadable()
		return nil
	}

	s.perRead = 1
	return s.eachOne(start, count)
}

// degradable 这个帧级错误是否"降级成逐个重读就能问清楚"。
//
// 判据是**设备应答了**：异常应答、数据域长度不符、带后续帧标志，都说明链路是通的，
// 只是这一帧的答复不能整体采信。而超时 / EOF 没有这个性质 —— 拿它降级就会把
// "设备掉线"记成"这批数据标识都不存在"，正是 specs/point-discovery.md §6 明令禁止
// 的那种误判（用户从一份几乎全是空的清单里得出与事实完全相反的结论，而采集侧
// 此时已经在重连了）。
func degradable(err error) bool {
	return isAbnormalErr(err) ||
		errors.Is(err, errFieldLength) ||
		errors.Is(err, errFollowUp)
}

// eachOne 把一块拆成单点逐个重读（降级路径）。
//
// 每一点一次独立请求、一次独立取锁：块内任何一点失败都不影响其余点位，
// 也不会把会话锁持太久（与 readGroup → readGroupPerDI 的取舍同形）。
func (s *pointScanner) eachOne(start, count int) error {
	for i := 0; i < count; i++ {
		if s.expired() {
			s.incomplete = true
			return errPointScanStop
		}
		one := s.targets[start+i : start+i+1]
		items, err := s.read(one)
		switch {
		case err == nil:
			if err := s.emit(start+i, one, items); err != nil {
				return err
			}
		case errors.Is(err, errPointScanStop):
			return errPointScanStop
		case errors.Is(err, context.DeadlineExceeded):
			s.incomplete = true
			return errPointScanStop
		case degradable(err):
			// 设备应答了，只是拒绝了这一次读取：这一点位就是读不到。
			s.markUnreadable()
		default:
			// 单点读也没人答。链路由本块开头那次复探（linkChecked）背书：
			// 通了就记"读不到"，不通才中止。
			if !s.linkAlive() {
				return err
			}
			if s.expired() {
				s.incomplete = true
				return errPointScanStop
			}
			s.markUnreadable()
		}
	}
	return nil
}

// linkAlive 复探一次链路，确认它是否仍然通（同一块内只真的探一次，见 linkChecked）。
//
// DL/T 645 的「静默」有歧义：规范规定设备对不支持的数据标识回异常应答（0xD1，
// 由 degradable 处理），但老表与廉价表常常**干脆不答**。那时"这块表没有这个数据标识"
// 与"设备掉线"在报文层面完全无法区分，只能**主动去问一次**：复探一个各版本最普遍
// 实现的数据标识（与 Ping 用的是同一个），答了就说明链路是活的，这一点位按"读不到"计；
// 复探也不通才中止。
//
// 这是**有意偏离** specs/point-discovery.md §6「连接类错误必须中止」的一处，与 FINS
// 「结束码错误也算设备」是同一类取舍（**拒绝应答也证明对面在**）。它成立的前提
// ——"静默 == 这块表没有这个数据标识"——无法从仓库验证，需要真机确认；若真机上
// 不支持的数据标识其实都会回异常应答，这条分支就是死代码（无害）。
//
// 判据照 driver.go 的 pingRead：**异常应答也算通**（它证明设备应答了，只是拒绝了
// 这次读取）。复探本身发不出去（会话被采集抢走、会话已关闭）同样按"不通"处理 ——
// 那说明此刻读不了这台设备，中止比继续硬撑更诚实（原错误会一路带上去）。
func (s *pointScanner) linkAlive() bool {
	if s.linkChecked {
		return true
	}
	probe := scanTarget{spec: DISpec{DI: livenessDI(s.cfg), Bytes: 4, Decimals: 2}}
	_, err := s.read([]scanTarget{probe})
	if err == nil || isAbnormalErr(err) || errors.Is(err, errFollowUp) {
		s.linkChecked = true
		return true
	}
	return false
}

// livenessDI 各版本最普遍实现的数据标识（与 driver.go 的 pingRead 用的是同一个）。
//
// 复用它而不是另挑一个：让"测试连接"（Ping）与这条复探共用同一个判据 —— 两者分叉
// 只会让"测试连接是通的、扫描却说设备掉线"这种自相矛盾的现象出现在现场。
func livenessDI(cfg *DLT645Config) uint32 {
	if !cfg.Is2007() {
		return 0x9010 // 1997：正向有功总电能
	}
	return 0x00000000 // 2007：组合有功总电能
}

// emit 把一帧的逐项结果写进结果集。
func (s *pointScanner) emit(start int, batch []scanTarget, items []scanItem) error {
	for i := range batch {
		if len(s.result.Points) >= pointScanMaxPoints {
			// 达到上限：**在尝试之前**停手 —— 超出的数据标识既不计入
			// Scanned/Readable，也不进列表，这样"读得到 M 个"与列表长度始终自洽
			// （len(points) <= readable <= scanned 这条不变量）。
			s.result.Truncated = true
			return errPointScanStop
		}
		s.result.Scanned++
		if i >= len(items) || !items[i].ok {
			// 应答在项数上就对不齐（设备少给了一项）：按"这一点位读不到"计。
			s.result.Unreadable++
			continue
		}
		s.result.Readable++
		s.result.Points = append(s.result.Points, buildPoint(batch[i], items[i]))
	}
	return nil
}

// markUnreadable 记一个"读不到"的数据标识。
//
// 不计入 Truncated：读不到 ≠ 结果条数到上限，两者前端必须分开显示。
func (s *pointScanner) markUnreadable() {
	s.result.Scanned++
	s.result.Unreadable++
}

// buildPoint 组装一个点位行。
func buildPoint(t scanTarget, it scanItem) driver.DiscoveredPoint {
	p := driver.DiscoveredPoint{
		Name:           t.name,
		Label:          t.label,
		CommonDataType: t.common,
	}
	if it.zero {
		// 零值点位**照样列出来**，只是不给 Value、只给 Note：插件侧刻意不做零值
		// 过滤（那是前端的一枚开关），判据与理由见 pointScanZeroNote。
		p.Note = pointScanZeroNote
		return p
	}
	p.Value = it.value
	return p
}

// finish 收尾：填耗时、Incomplete 与统计的收口。
func (s *pointScanner) finish() *driver.PointScanResult {
	if s.incomplete {
		s.result.Incomplete = true
	}
	s.result.ElapsedMs = int(time.Since(s.started).Milliseconds())
	if s.result.Points == nil {
		// nil 切片过线是 JSON null，前端得为它多写一条分支。
		s.result.Points = []driver.DiscoveredPoint{}
	}
	return s.result
}

// expired 是否已过截止时刻。
func (s *pointScanner) expired() bool { return time.Now().After(s.deadline) }

// ==================== 扫描清单的组装 ====================

// scanTargets 组装本次要扫的清单：内置字典 + 用户附加的数据标识。
//
// 顺序是**字典在前（按数据标识升序）、附加在后**，且稳定 —— 两次扫描的清单顺序
// 一致，用户才能逐行比对两次结果（直接遍历 map 会让每次的行序都不同）。
//
// 与字典重复的附加数据标识**覆盖**字典条目（位置不动）。用户显式写出
// `12345678:4:2` 就是想改字典给的格式；静默丢弃会让字典悄悄赢，而他以为自己的
// 写法生效了 —— 这正是"配置改了但没生效"那类最难查的问题。
func scanTargets(cfg *DLT645Config, extras []string) ([]scanTarget, error) {
	dictDIs := DictionaryDIs(cfg.Version)
	targets := make([]scanTarget, 0, len(dictDIs)+len(extras))
	at := make(map[uint32]int, len(dictDIs))

	for _, di := range dictDIs {
		name := formatDI(di, cfg.Version)
		t, err := newScanTarget(cfg, name, true)
		if err != nil {
			// VerifyDictionary 已保证条目结构合法；走到这里说明字典被改坏了。
			return nil, fmt.Errorf("内置字典条目 %s 无法解析: %w", name, err)
		}
		at[t.spec.DI] = len(targets)
		targets = append(targets, t)
	}

	for _, tok := range extras {
		t, err := newScanTarget(cfg, tok, false)
		if err != nil {
			return nil, err
		}
		if i, ok := at[t.spec.DI]; ok {
			targets[i] = t
			continue
		}
		at[t.spec.DI] = len(targets)
		targets = append(targets, t)
	}

	if len(targets) == 0 {
		return nil, errors.New("没有可扫描的数据标识：内置字典为空，也没有填附加数据标识")
	}
	return targets, nil
}

// newScanTarget 把一个地址写法解析成待扫条目。
// inDict 为真表示它来自内置字典（地址写法就是裸数据标识）。
func newScanTarget(cfg *DLT645Config, name string, inDict bool) (scanTarget, error) {
	spec, err := ParseAddress(name, cfg.Version)
	if err != nil {
		return scanTarget{}, err
	}
	intern, common := pointTypes(spec)
	return scanTarget{
		name:   pointName(cfg, spec, inDict),
		label:  pointLabel(spec),
		spec:   spec,
		common: common,
		intern: intern,
	}, nil
}

// pointName 返回该规格**能被 ParseAddress 还原**的地址写法。
//
// 这条不变量比它看起来重要得多：一个解析不出来的地址写进 device_address 之后，
// 这个点位永远采不到值（本驱动的 buildPlan 早就是逐点记 err、不让整批失败，
// 标 QualityBadAddress；六个区间规划器 modbus/s7/fins/mitsubishi/Omron.CIP/
// Rockwell.CIP 现在也是同一个形态）。
// 两种来源两种写法：
//
//	磁盘字典条目 → 裸数据标识（02010100 / 9010），靠字典还原全部规格；
//	附加数据标识 → 数据标识 + 显式字节数与小数位（+ 标志），不依赖字典
//	              （ParseAddress 对字典外的数据标识宁可报错也不猜长度）。
//
// 由 pointscan_test.go 的 TestPointNameRoundTrip 对两个版本的全部字典条目
// 与一批刁钻的附加规格逐条钉住。
func pointName(cfg *DLT645Config, spec DISpec, inDict bool) string {
	di := formatDI(spec.DI, cfg.Version)
	if inDict {
		return di
	}
	out := fmt.Sprintf("%s:%d:%d", di, spec.Bytes, spec.Decimals)
	var flags strings.Builder
	if spec.Signed {
		flags.WriteByte('s')
	}
	if spec.Binary {
		flags.WriteByte('b')
	}
	if flags.Len() > 0 {
		out += ":" + flags.String()
	}
	return out
}

// pointTypes 返回该数据标识的解码用内部裸名与建议的通用类型名。
//
// 与其它协议"整段共用一个假设类型"不同，645 的输出形态由**数据标识本身**决定
// （长度/小数位/符号/编码全在它是多少位的哪一个值里，见 decode.go），所以逐点给出
// 类型是唯一说得通的做法 —— 否则表号（6 字节 BCD 数字串）与运行状态字（二进制位域）
// 会被建成数值点位，用户看着"类型已经填好了"就提交，采出来的是错的。
//
// 这条判断不依赖任何读到的值（按值推断必然出错：恒为 0/1 的模拟量会被推成 Boolean，
// 而错误只在时序库里表现为"字段类型变了"），只依赖字典条目本身的可判定特征。
func pointTypes(spec DISpec) (intern, common string) {
	switch {
	case spec.Layout == layoutDate || spec.Layout == layoutTime:
		return typeNameDateTime, driver.TypeDate
	case spec.Layout == layoutAddr:
		return typeNameString, driver.TypeString
	case spec.Binary:
		return typeNameUint, driver.TypeWord
	default:
		return typeNameFloat, driver.TypeFloat
	}
}

// pointLabel 建议的标签：字典名 + 单位。
//
// **本功能里少见的"确实有标签来源"的情形**：别的协议得让用户逐行自己填（设备不会
// 告诉你"这个寄存器是温度"），而 645 的数据标识字典自带中文名与单位。单位没有单独的
// 列可放，附在名字后的括号里 —— 它是要显示出来的：电压的 V 与电流的 A 差一个字母，
// 值差三个数量级。
func pointLabel(spec DISpec) string {
	if spec.Name == "" {
		return ""
	}
	if spec.Unit == "" {
		return spec.Name
	}
	return fmt.Sprintf("%s（%s）", spec.Name, spec.Unit)
}

// ==================== 扫描选项 ====================

// pointScanOptionsRaw 扫描选项的原始形态。键名与 pointScanForm 的 field 一一对应。
type pointScanOptionsRaw struct {
	ExtraDIs scanText `json:"extraDIs"`
}

// scanText 容错文本：同时接受 JSON 字符串与数字（数字取其字面量）。
//
// 表单里它是 textarea（产出字符串），但声明成 string 时一份带数字的选项会让
// json.Unmarshal **整体**报错 —— 不是那一栏失效，而是整次扫描全失败
// （与 config.go 里 rawInt / rawBool 是同一条取舍）。
//
// **必须走 json.Unmarshal 解字符串，不能只把首尾引号 trim 掉**：这一栏是 textarea，
// 用户按手册一行一个数据标识粘进来时，换行在 JSON 里正是以 `\n` 的形式过来的；
// 只 trim 引号会让它原样留在 token 里，于是每一条附加数据标识都解析失败，而报错
// 说的是"标志无法识别"——指着完全无关的地方。
type scanText struct {
	val string
}

func (r *scanText) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		// 数字等非字符串形态：取字面量（trim 引号对数字是恒等变换）。
		s = strings.Trim(strings.TrimSpace(string(b)), `"`)
	}
	if s == "" || s == "null" {
		return nil
	}
	r.val = s
	return nil
}

// parsePointScanOptions 解析扫描选项，返回附加数据标识的原始 token 列表。
//
// 与设备扫描那份的取舍不同：那边缺项静默回默认值；这里唯一的一项是**可选的**
// （留空即只扫字典，是合法且默认的行为），但**填错一律报错** —— 一条解析不出来的
// 数据标识猜不出长度，只能请用户按电表手册改对（ParseAddress 宁可报错也不猜一个
// 长度，理由见 address.go）。
func parsePointScanOptions(options json.RawMessage) ([]string, error) {
	if len(options) == 0 {
		return nil, nil
	}
	var raw pointScanOptionsRaw
	if err := json.Unmarshal(options, &raw); err != nil {
		return nil, fmt.Errorf("解析扫描选项失败: %w", err)
	}
	return splitExtraDIs(raw.ExtraDIs.val)
}

// splitExtraDIs 把「附加数据标识」那一栏拆成一条条 token。
//
// 分隔符收逗号、分号、顿号与所有空白（含换行）：用户是从电表手册上抄的，
// 写法五花八门，而多收一个分隔符不会让任何合法输入变得歧义。
func splitExtraDIs(s string) ([]string, error) {
	out := make([]string, 0, 4)
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ',', ';', '，', '；', '、', ' ', '\t', '\n', '\r':
			return true
		}
		return false
	}) {
		if tok == "" {
			continue
		}
		if len(out) >= pointScanMaxExtraDIs {
			return nil, fmt.Errorf("附加数据标识最多 %d 条", pointScanMaxExtraDIs)
		}
		out = append(out, tok)
	}
	return out, nil
}

// ==================== 驱动内部资源的取用 ====================

// scanSession 一次扫描要用的驱动内部资源。
//
// 每次 Do 现取、用完即弃：热刷新会重建驱动实例、重连会换掉 client，
// 缓存下来的读的是一个已经 Close 的句柄（拿回上一轮的缓存值，或不巧触发
// nil 解引用把整个插件进程带走）。
type scanSession struct {
	client dlt645Transport
	cfg    *DLT645Config
	addr   [6]byte
}

// scanSessionOf 从驱动实例取出扫描要用的三个值。
//
// 用类型断言而不是给 driver.Driver 加方法：那会要求全部协议都实现它，
// 而这里只有 DLT645 的两种传输层需要（与 modbusdrv 的 scanSessionOf 同一取舍）。
// **快照后立刻放锁**：会话锁（driversdk 的 sess.mu）已经排除了所有会话级调用者，
// 持 d.mu.RLock 跨整个 I/O 只会无谓地挡住一次并发的 Connect / Close。
func scanSessionOf(drv driver.Driver) (*scanSession, error) {
	d, ok := drv.(*dlt645Driver)
	if !ok {
		return nil, fmt.Errorf("dlt645: 不支持的驱动类型 %T", drv)
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.client == nil || d.config == nil || !d.client.IsConnected() {
		return nil, errors.New("dlt645: 设备未连接，无法扫描点位")
	}
	return &scanSession{client: d.client, cfg: d.config, addr: d.meterAddr}, nil
}

// readOneFrame 读一帧（一批点位）。
//
// 帧级错误分三类，调用方按类型处置（见 degradable / failOne）：
//   - *abnormalError：设备应答了但拒绝本次请求
//   - errFollowUp / errFieldLength：这一帧的答复不能整体采信，需要降级重问
//   - 其它（超时 / EOF）：链路问题
func readOneFrame(s *scanSession, batch []scanTarget) ([]scanItem, error) {
	specs := make([]DISpec, len(batch))
	for i := range batch {
		specs[i] = batch[i].spec
	}

	// 加锁的形状与采集侧的 readGroup 逐字相同（driver.go）：传输层互斥锁只护住
	// "一次请求-应答"，收发间延时与超时都取自设备配置。
	s.client.Lock()
	req := buildReadFrame(s.cfg, s.addr, specs)
	resp, err := exchange(s.client, s.cfg, s.addr, req)
	s.client.Unlock()

	if err != nil {
		return nil, err
	}
	if hasFollowUp(s.cfg, resp) {
		// 首帧数据只是该数据项的一部分，拿它解码会得到一个**看似合理实则被截断**
		// 的值。刻意不支持拼接（各厂商手册对后续帧是否回显序号、回显在哪儿描述
		// 不一，臆测布局会静默取到错位的数据）——见 codec.go 的 hasFollowUp。
		return nil, errFollowUp
	}
	if len(specs) > 1 {
		// 成帧打包时先核对数据域总长。
		//
		// **回显校验（fieldSlice 里的 gotDI != spec.DI）只保证"这一项的标识对"，
		// 不保证"这一项的数据落在正确的位置"**：设备少回一项的数据时，后面每一项
		// 的窗口都会整体前移，于是解出的是一个**看似合理实为别处**的值，而它会被
		// 算成"读到了"。总长不足是这一情形的必要条件（少回 k 项数据就少 k 项字节，
		// floor 也补不回来），因此拿它当判据降级为逐个重读 —— 单点读时窗口无从错位。
		//
		// 只判"短于"不判"长于"：多出来的尾巴不影响前面的位置，多判一条只会把
		// 一块本来扫得完的表降级成几十次单读。
		want := 0
		for i := range specs {
			want += s.cfg.DIBytes() + specs[i].Bytes
		}
		if len(resp.Data) < want {
			return nil, errFieldLength
		}
	}

	items := make([]scanItem, len(specs))
	pos := 0
	for i := range specs {
		raw, next, ferr := fieldSlice(s.cfg, &specs[i], resp.Data, pos)
		pos = next
		if ferr != nil {
			// 与采集侧刻意不同：那边要把成因归到点位级并打出原始报文（供现场比对），
			// 这边只需要"这一点位读不到"这个结论 —— 读不到的数据标识不进清单。
			continue
		}
		val, err := DecodeValue(raw, &specs[i], batch[i].intern)
		if err != nil {
			continue
		}
		items[i] = newScanItem(raw, FormatValue(val, batch[i].intern))
	}
	return items, nil
}

// newScanItem 组装一个点位读取结果。
func newScanItem(raw []byte, value string) scanItem {
	if allZeroBytes(raw) {
		return scanItem{ok: true, zero: true}
	}
	return scanItem{ok: true, value: value}
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
