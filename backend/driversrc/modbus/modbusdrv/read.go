// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	goburrowModbus "github.com/goburrow/modbus"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// 对半降级的预算上限，**单次 Read 共享**（见 readPlanState）。
//
// 两个上限都要，理由与点位扫描那边同一条：请求数上限兜住"设备应答很快但整段
// 拒绝"（否则一次 Read 会发出几百个必然失败的请求），时间上限兜住"设备应答很慢"
// （每个请求都吃满超时）。
//
// 上限必须远小于网关侧 Read 的 60s RPC 超时：连续 3 次超时会把整个插件进程判死，
// 该插件下所有协议、所有设备一起断。所以余量要留给同批次的其余区间。
const (
	degradeMaxRequests = 64
	degradeMaxDuration = 15 * time.Second
)

// isProtocolErr 判断错误是否为 Modbus 协议异常（设备响应但拒绝该请求，如 IllegalDataAddress）。
// 协议异常说明连接是通的，不应据此判定连接断开、也不应中断整台设备读取。
func isProtocolErr(err error) bool {
	var me *goburrowModbus.ModbusError
	return errors.As(err, &me)
}

// isAddressException 这条异常是否**只关于地址**（而不是从站/网关的状况）。
//
// 异常码里只有这两个是"我读不了这个地址 / 这个请求"：
//
//	0x02 IllegalDataAddress  地址不存在 / 越界 —— 一个点位写错就是它
//	0x03 IllegalDataValue    请求里的值（如数量）不被接受
//
// 其余一律**不是**地址问题，最要紧的是 0x06 ServerDeviceBusy（从站忙）与
// 0x05 Acknowledge：它们是**可恢复的设备状态**，拿它们去推断"这个地址不存在"
// 会把整段点位逐个误判成坏点、还会把这份误判缓存住 —— 比修复前的行为更糟
// （那时只是这一轮标 quality=0、下一轮照常重试）。0x04 / 0x08 / 0x0A / 0x0B
// 同理（设备故障与网关错误都与地址无关）。
func isAddressException(err error) bool {
	var me *goburrowModbus.ModbusError
	if !errors.As(err, &me) {
		return false
	}
	switch me.ExceptionCode {
	case goburrowModbus.ExceptionCodeIllegalDataAddress, goburrowModbus.ExceptionCodeIllegalDataValue:
		return true
	default:
		return false
	}
}

// groupAddrsByFC 将点位按功能码分组。
//
// 配置模式（StartAddress>0）：全部归入 cfg.FunctionCode，保持向后兼容。
// 自动推导模式：按 name 地址解析出的 FC 分组（线圈/保持寄存器等不同地址空间互不干扰），
// 纯数字/十六进制点位（解析不出 FC，FC=0）归入 cfg.FunctionCode。
// 这样同一台设备同时配置线圈与保持寄存器点位时可分组各自读取，而不是整组报错。
func groupAddrsByFC(addrs []po.DeviceAddress, cfg *ModbusTcpConfig) (map[byte][]po.DeviceAddress, error) {
	groups := make(map[byte][]po.DeviceAddress)

	if cfg.StartAddress > 0 {
		groups[cfg.FunctionCode] = append(groups[cfg.FunctionCode], addrs...)
		return groups, nil
	}

	for _, a := range addrs {
		nameFC, _, ok := ParsePLCAddress(a.Name)
		if !ok {
			return nil, fmt.Errorf("modbus: address %q (id=%s) invalid format", a.Name, a.ID)
		}
		fc := nameFC
		if fc == 0 {
			fc = cfg.FunctionCode
		}
		groups[fc] = append(groups[fc], a)
	}
	return groups, nil
}

// badAddr 计划期就被排除的点位：进不了区间规划的那几个。
//
// 这类点位不进任何功能码组，因此 readRanges 的预填骨架（按 plan.groups[fc] 建）
// 覆盖不到它，readBatch 末尾「addrs 里每个点位都恰好有一条记录」的重组也就无从
// 取值 —— 记录只能由计划带过来（见 readBatch 的播种循环与 modbusPlan.bad）。
//
// 与 markBadPoint 记下的坏点**成因不同**：那里是设备确认这个地址不存在（上过线、
// 被拒），这里是本地就没法把它放进计划（一个字节都没发出去）。两者都用
// driver.QualityBadAddress —— 都是「地址本身坏、设备是好的」且永不自我恢复。
type badAddr struct {
	id       string
	name     string
	dataType string
	// why 是给日志的一句话（**不含**协议前缀，logf 统一加 "modbus: "，见 log.go）。
	// 排除有两个来源（见 modbusExcluded），这句话是它们在日志里唯一的区别 ——
	// 质量码、缓存方式、自愈方式都相同。
	why string
}

// modbusExcluded 判定一个点位是否**计划期就该排除**，以及给日志的一句话。
//
// 两个来源，判据全部取自 CalcReadRanges 自己用的那一套表达式，不另立校验器：
//
//	① ParsePLCAddress 认不出的地址写法；
//	② 配置模式（startAddress>0）下寄存器地址 < startAddress —— 地址写法没问题，
//	   是该点位与 protocol_json 声明的地址区间对不上。
//
// 两者都是「不去改配置就永远不会好」，因此都归 driver.QualityBadAddress。
func modbusExcluded(a *po.DeviceAddress, startAddress uint16) (why string, excluded bool) {
	_, addr, ok := ParsePLCAddress(a.Name)
	if !ok {
		return fmt.Sprintf("address %q (id=%s) invalid format", a.Name, a.ID), true
	}
	if startAddress > 0 && addr < startAddress {
		return fmt.Sprintf("address %q (id=%s) reg=%d < startAddress=%d",
			a.Name, a.ID, addr, startAddress), true
	}
	return "", false
}

// partitionBadAddrs 用上面的判据把点位分成「进计划」与「计划期排除」两半。
//
// 为什么拆在 CalcReadRanges 之前、而不是让它自己容错：CalcReadRanges 的契约是
// 「全量解析、遇非法地址或地址低于 startAddress 报错」（它那两条错误被 parser_test.go
// 钉着）。拆在它上游，它见到的就永远是合法输入，那两条错误退化为防御性代码。
func partitionBadAddrs(addrs []po.DeviceAddress, startAddress uint16) (valid []po.DeviceAddress, bad []badAddr) {
	for i := range addrs {
		a := &addrs[i]
		why, excluded := modbusExcluded(a, startAddress)
		if excluded {
			bad = append(bad, badAddr{id: a.ID, name: a.Name, dataType: a.DataType, why: why})
			continue
		}
		valid = append(valid, *a)
	}
	return valid, bad
}

// ==================== 区间读取与失败降级 ====================

// readPlanState 一次 Read 的降级记账，**跨功能码组共享**。
//
// 降级只在失败的区间上发生，但一个批次可能有多个失败区间（还分布在不同的功能码
// 组里），所以预算与降级结果都挂在这一份状态上 —— 否则每组各花满一份，总耗时
// 随组数线性放大。
type readPlanState struct {
	budget   *isolateBudget
	warns    *warnSet
	next     map[byte][]AddressRange // 降级后各功能码组的区间（只有降过级的组才有）
	degraded bool
	// qualities 地址 → 质量码。**初始化时由 planCache.badFor 播种**（上一轮已确认的坏点），
	// 本轮的分类判定再往里写。它能同时服务三件事：本轮结束时回填预填骨架、
	// 回写缓存供下一轮播种，以及被 storeDegraded 一并持久化。
	qualities map[string]int
}

func newReadPlanState(w *warnSet) *readPlanState {
	return &readPlanState{
		budget:    newIsolateBudget(),
		warns:     w,
		next:      make(map[byte][]AddressRange),
		qualities: make(map[string]int),
	}
}

// setQuality 给一组点位记下同一个质量码（分类判定处调用，见 readRangeDegrading 等）。
func (st *readPlanState) setQuality(ids []string, quality int) {
	for _, id := range ids {
		st.qualities[id] = quality
	}
}

// idsOfRange 取区间内全部点位 id（质量码要按点位记，而 AddressMap 是 id→起始地址）。
func idsOfRange(r *AddressRange) []string {
	ids := make([]string, 0, len(r.AddressMap))
	for id := range r.AddressMap {
		ids = append(ids, id)
	}
	return ids
}

// idsOfPoints 取一段点位 id。
func idsOfPoints(pts []rangePoint) []string {
	ids := make([]string, 0, len(pts))
	for _, p := range pts {
		ids = append(ids, p.id)
	}
	return ids
}

// isolateBudget 对半降级的预算，**单次 Read 内共享**。
type isolateBudget struct {
	left     int
	deadline time.Time // 首个降级请求时锚定（零值 = 还没开始）
	skipped  int       // 因预算耗尽而本轮未取到值的点位数（收尾时汇总打一行）
}

func newIsolateBudget() *isolateBudget {
	return &isolateBudget{left: degradeMaxRequests}
}

// take 申请一次降级请求。false 表示预算已尽（请求数或时间，任一到了就算尽）。
//
// 时间窗口在**首个降级请求**时才开始计时，不是在 Read 开头：串口链路上一个批次
// 的常规读取本身就可能花掉十几秒（9600bps，几百个离散区间），从 Read 开头锚定的话
// 预算一出生就过期了 —— 恰好是最需要降级的那些设备上，降级一次都不会发生。
func (b *isolateBudget) take() bool {
	if b.left <= 0 {
		return false
	}
	now := time.Now()
	if b.deadline.IsZero() {
		b.deadline = now.Add(degradeMaxDuration)
	}
	if now.After(b.deadline) {
		return false
	}
	b.left--
	return true
}

func (b *isolateBudget) noteUnresolved(points int) { b.skipped += points }

// warnSet 日志去重（每个驱动实例一份，Connect 时清空）。
//
// 去重不是可选的：设备回"从站忙"这类瞬时状态时，那一行每个采集周期都成立，
// 而插件日志没有级别这道运行时闸门，不去重就是每秒把同一行刷一遍。
// 但也不能是一次性的：坏地址的日志只出现在它被摘出去的那一轮，此后它已不在
// 计划里、不再被读取，自然也不再打日志 —— 后来才翻日志的人会找不到线索。
// 按 warnTTL 重新说一次，既不刷屏，痕迹也留得下。
type warnSet struct {
	mu     sync.Mutex
	warned map[string]warnEntry
}

// warnTTL 同一条去重日志重新出现的间隔。
const warnTTL = 10 * time.Minute

type warnEntry struct {
	msg string
	at  time.Time
}

func (w *warnSet) once(key, format string, args ...any) {
	if w == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	now := time.Now()

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.warned == nil {
		w.warned = make(map[string]warnEntry)
	}
	if prev, ok := w.warned[key]; ok && prev.msg == msg && now.Sub(prev.at) < warnTTL {
		return
	}
	w.warned[key] = warnEntry{msg: msg, at: now}
	logf("%s", msg)
}

// clear 清空去重表（Connect 时调用：新连接下的失败是"新情况"，该重新说一次）。
func (w *warnSet) clear() {
	w.mu.Lock()
	w.warned = nil
	w.mu.Unlock()
}

// rangePoint 区间内一个点位：id + 起始地址 + 占用的单位数（寄存器数 / 位数）。
type rangePoint struct {
	id    string
	start uint16
	span  uint16
}

// rangePoints 取出区间内的点位并按其**单位**排序 —— 对半降级按这个顺序切，
// 两半覆盖的地址范围才大致相当。**不能直接用 r.AddressMap 的遍历顺序**：map 顺序
// 是随机的，按它切等于随机分割，降级会退化成线性扫描（也难以收敛）。
//
// 单位随区间语义而定：寄存器区间按寄存器（多寄存器类型按字节数折算），位区间每点一位。
// 两者不能混用 —— 拿位数当寄存器数会把读取头落到别的寄存器上，读到的是**别处**的
// 数据而值看着完全合理。
func rangePoints(addrs []po.DeviceAddress, r *AddressRange, cfg *ModbusTcpConfig, bitMode bool) []rangePoint {
	pts := make([]rangePoint, 0, len(r.AddressMap))
	for _, a := range addrs {
		start, ok := r.AddressMap[a.ID]
		if !ok {
			continue
		}
		span := uint16(1)
		if !bitMode {
			span = uint16((getDataTypeBytes(a.DataType, cfg.StringLen) + 1) / 2)
			if span < 1 {
				span = 1
			}
		}
		pts = append(pts, rangePoint{id: a.ID, start: start, span: span})
	}
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].start != pts[j].start {
			return pts[i].start < pts[j].start
		}
		// 同址重名时用 id 兜底：map 遍历顺序随机，排序不稳定的话分点是随机的
		return pts[i].id < pts[j].id
	})
	return pts
}

// pointsBounds 返回一段点位覆盖的地址范围 [start, start+quantity)。
//
// 子区间恒是父区间的子集（只按点位切、不向外延伸），所以不会溢出 uint16 ——
// 父区间的末端本来就在 uint16 内。
func pointsBounds(pts []rangePoint) (uint16, uint16) {
	lo := pts[0].start
	hi := lo + pts[0].span
	for _, p := range pts[1:] {
		if p.start < lo {
			lo = p.start
		}
		if e := p.start + p.span; e > hi {
			hi = e
		}
	}
	return lo, hi - lo
}

// pointsMap 由点位列表重建 AddressMap，供解析函数按区间偏移取值。
func pointsMap(pts []rangePoint) map[string]uint16 {
	m := make(map[string]uint16, len(pts))
	for _, p := range pts {
		m[p.id] = p.start
	}
	return m
}

// parseRange 解析一段读取结果并回填为记录。
func parseRange(data []byte, addrs []po.DeviceAddress, r *AddressRange, cfg *ModbusTcpConfig, bitMode bool) []driver.ReadResult {
	sub := filterAddrsByMap(addrs, r.AddressMap)
	if bitMode {
		return parseBitResults(data, sub, r)
	}
	return parseRegisterResults(data, sub, cfg, r)
}

// readRangeDegrading 读一个区间并解析；整段被以**地址类异常**拒绝时，按点位
// 对半降级把真正读不到的点摘出来。
//
// 返回该区间在**下一轮计划**里的形态：
//
//	整段读通            → 该区间原样
//	地址类异常 → 对半降级 → 读得通的子区间 + 没摘完的子区间（见 degradeSpan）
//	非地址类异常         → 该区间原样（下轮重试，一个点位都不确认）
//
// 网络/超时错误原样上抛。
func readRangeDegrading(read spanReader, addrs []po.DeviceAddress, r *AddressRange,
	cfg *ModbusTcpConfig, bitMode bool, st *readPlanState, out *[]driver.ReadResult) ([]AddressRange, error) {

	data, err := read(r.StartAddress, r.Quantity)
	if err == nil {
		*out = append(*out, parseRange(data, addrs, r, cfg, bitMode)...)
		return []AddressRange{*r}, nil
	}
	if !isProtocolErr(err) {
		return nil, fmt.Errorf("modbus: read %s [%d, %d) failed: %w",
			rangeUnitName(bitMode), r.StartAddress, r.StartAddress+r.Quantity, err)
	}

	// 设备拒绝了整段，但异常**不是地址问题**（从站忙 / 设备故障 / 网关错误）：
	// 这条错误说明不了任何一个地址是否存在，所以既不降级也不确认任何点位。
	// 整段保持 quality=0 并原样留在计划里，下轮重试。
	if !isAddressException(err) {
		st.setQuality(idsOfRange(r), driver.QualityBadComms)
		st.warns.once(fmt.Sprintf("transient:%d:%v:%d", r.StartAddress, bitMode, r.Quantity),
			"modbus: %s [%d, %d) not readable now (%v), keep %d points for retry",
			rangeUnitName(bitMode), r.StartAddress, r.StartAddress+r.Quantity, err, len(r.AddressMap))
		return []AddressRange{*r}, nil
	}

	// 地址类异常：多半是区间里混进了一个设备上不存在的地址（越界的寄存器、
	// 声明长度超出的 string）。直接整段标 quality=0 会把同区间里本来读得到的
	// 点位一起丢掉，所以这里按点位对半降级，把真正读不到的那几个摘出来。
	//
	// 已经知道整段读不通，所以这里直接拆，不重读这一整段
	//（degradeSpan 是"试读一段"，它会先读一次；再走它等于白付一次必然失败的往返）。
	pts := rangePoints(addrs, r, cfg, bitMode)
	logf("modbus: %s [%d, %d) rejected (%v), degrade per point over %d points",
		rangeUnitName(bitMode), r.StartAddress, r.StartAddress+r.Quantity, err, len(pts))
	st.degraded = true
	return degradeBadSpan(read, addrs, pts, cfg, bitMode, st, out, err)
}

// spanReader 读一段连续地址（已绑定分块逻辑），返回原始字节。
type spanReader func(start, quantity uint16) ([]byte, error)

// degradeSpan 试读一段点位。
//
//	读得通              → 解析回填，并把该子区间收进计划（下轮直接照它读）
//	读不通（地址类）      → 交给 degradeBadSpan 拆
//	预算耗尽 / 非地址异常 → 该子区间**原样收进计划**：本轮保持 quality=0，
//	                      下轮接着摘
func degradeSpan(read spanReader, addrs []po.DeviceAddress, pts []rangePoint,
	cfg *ModbusTcpConfig, bitMode bool, st *readPlanState, out *[]driver.ReadResult) ([]AddressRange, error) {

	if len(pts) == 0 {
		return nil, nil
	}
	start, quantity := pointsBounds(pts)
	sub := AddressRange{StartAddress: start, Quantity: quantity, AddressMap: pointsMap(pts)}

	if !st.budget.take() {
		// 预算耗尽：这一段本轮取不到值。**留在计划里** —— 丢掉它等于把这些点位
		// 排除在计划之外，它们会在 quality=0 上一直挂到 TTL 到期，"下轮接着摘"
		// 从来没有发生。
		st.budget.noteUnresolved(len(pts))
		return []AddressRange{sub}, nil
	}

	data, err := read(start, quantity)
	if err == nil {
		*out = append(*out, parseRange(data, addrs, &sub, cfg, bitMode)...)
		return []AddressRange{sub}, nil
	}
	if !isProtocolErr(err) {
		return nil, fmt.Errorf("modbus: degraded read %s [%d, %d) failed: %w",
			rangeUnitName(bitMode), start, start+quantity, err)
	}
	if !isAddressException(err) {
		// 同上：非地址类异常说明不了这个地址是否存在。原样留着下轮再试，
		// 不确认任何点位 —— 也不能把它当成"拆到单点仍读不到"。
		st.setQuality(idsOfPoints(pts), driver.QualityBadComms)
		return []AddressRange{sub}, nil
	}
	return degradeBadSpan(read, addrs, pts, cfg, bitMode, st, out, err)
}

// degradeBadSpan 处理一段**已知读不通**的点位（cause 为那次拒绝的异常，必为地址类）。
//
//	只剩单点 → 确认这个地址不存在：保持 quality=0，并从计划里摘掉
//	还有多点 → 一分为二，两半各自重试
//
// 递归必然终止：len(pts) >= 2 时 mid >= 1 且 mid < len(pts)，两半都严格变小。
func degradeBadSpan(read spanReader, addrs []po.DeviceAddress, pts []rangePoint,
	cfg *ModbusTcpConfig, bitMode bool, st *readPlanState, out *[]driver.ReadResult,
	cause error) ([]AddressRange, error) {

	if len(pts) == 1 {
		// 拆到单点仍被拒。保持 quality=0，从计划里摘掉 —— 留在计划里就是每轮
		// 为它重付一次必然失败的往返。
		markBadPoint(st, pts[0].id, addrs, cause)
		return nil, nil
	}
	mid := len(pts) / 2
	left, err := degradeSpan(read, addrs, pts[:mid], cfg, bitMode, st, out)
	if err != nil {
		return nil, err
	}
	right, err := degradeSpan(read, addrs, pts[mid:], cfg, bitMode, st, out)
	if err != nil {
		return nil, err
	}
	return append(left, right...), nil
}

// markBadPoint 记下一个已确认读不到的地址（日志按地址去重），并标成因。
//
// 走到这里意味着"整段被以地址类异常拒绝 + 对半降到单点仍被拒"—— 这是**地址本身**
// 的问题（越界的寄存器、声明长度超出的 string），设备是好的，故成因是 address
// 而非 comms：它下一轮还会被摘在计划外，不会自己恢复。
func markBadPoint(st *readPlanState, id string, addrs []po.DeviceAddress, cause error) {
	name := id
	for i := range addrs {
		if addrs[i].ID == id {
			name = addrs[i].Name
			break
		}
	}
	st.qualities[id] = driver.QualityBadAddress
	st.warns.once("bad:"+id, "modbus: address %q (id=%s) unreadable, mark quality=bad-address: %v", name, id, cause)
}

// rangeUnitName 区间单位名（仅用于日志）。
func rangeUnitName(bitMode bool) string {
	if bitMode {
		return "bit range"
	}
	return "register range"
}

// readBatch 一轮采集：按计划逐功能码组读取、必要时对半降级、并把降级后的区间
// 回写缓存。prefix 为错误文案前缀（"modbus" / "modbus rtu"）。
//
// 与两个驱动的 Read 分开，是为了让**这一段只有一份实现**：TCP 与 RTU 的 Read 原先是
// 逐句重复的两份，而客户端是具体类型（*ModbusClient / *ModbusRTUClient），造不出来，
// 于是这段最要紧的"降级有没有回写""下轮有没有接着摘"在测试里够不着 —— 传入一台
// 假 PLC（modbusPointReader）就能量到生产代码本身。
func readBatch(prefix string, reader modbusPointReader, plan *modbusPlan, cache *planCache,
	sig uint64, addrs []po.DeviceAddress, cfg *ModbusTcpConfig, warns *warnSet) ([]driver.ReadResult, error) {

	fcs := make([]byte, 0, len(plan.groups))
	for fc := range plan.groups {
		fcs = append(fcs, fc)
	}
	sort.Slice(fcs, func(i, j int) bool { return fcs[i] < fcs[j] })

	// 降级预算与降级后的区间记在一份跨组的状态里：一个批次可能有多个失败区间
	// 分布在不同功能码组，预算必须统一下发，否则每组各花满一份。
	st := newReadPlanState(warns)
	// 播种上一轮已确认的坏点质量码：那些地址已被摘出计划、本轮不会再被解析，
	// 只有在这里带进来，它们的质量码才不会在后续每一轮都退化成“未归类”。
	for id, q := range cache.badFor(sig) {
		st.qualities[id] = q
	}
	byID := make(map[string]driver.ReadResult, len(addrs))
	// 播种计划期被排除的点位（见 partitionBadAddrs）。它们不在任何功能码组里，
	// readRanges 的预填骨架覆盖不到；不补的话末尾按 addrs 重组会产出一条零值记录 ——
	// 下游 MQTT 负载与时序库表结构正靠 DataType/Kind 决定怎么写（见 readRanges 的
	// 契约说明）。功能码循环只写组内的 id，不可能覆盖这里补的记录。
	for i := range plan.bad {
		b := &plan.bad[i]
		byID[b.id] = driver.ReadResult{
			DeviceAddressID: b.id,
			Value:           "",
			DataType:        b.dataType,
			Kind:            typeKind(b.dataType),
			Quality:         driver.QualityBadAddress,
		}
		// 不自己写协议前缀：logf 统一加 "modbus: "（见 log.go）。
		warns.once("badaddr:"+b.id, "%s, mark quality=bad-address", b.why)
	}
	for _, fc := range fcs {
		gAddrs := plan.groups[fc]
		ranges := cache.rangesFor(sig, fc, plan.ranges[fc])

		var groupResults []driver.ReadResult
		var err error
		switch fc {
		case FuncCodeReadHoldingRegisters, FuncCodeReadInputRegisters:
			readFn := reader.ReadHoldingRegisters
			if fc == FuncCodeReadInputRegisters {
				readFn = reader.ReadInputRegisters
			}
			// 共享读取助手：逐区间读取；整段被以地址类异常拒绝时按点位对半降级，
			// 只把真正读不到的点位标 quality=0（而不是把整段一起标）。
			groupResults, err = readRegisterRanges(readFn, gAddrs, ranges, cfg, fc, st)
			if err != nil {
				return nil, fmt.Errorf("%s: read registers (fc=%d) failed: %w", prefix, fc, err)
			}

		case FuncCodeReadCoils, FuncCodeReadDiscreteInputs:
			readFn := reader.ReadCoils
			if fc == FuncCodeReadDiscreteInputs {
				readFn = reader.ReadDiscreteInputs
			}
			groupResults, err = readBitRanges(readFn, gAddrs, ranges, fc, st)
			if err != nil {
				return nil, fmt.Errorf("%s: read bits (fc=%d) failed: %w", prefix, fc, err)
			}

		default:
			return nil, fmt.Errorf("%s: unsupported function code: %d", prefix, fc)
		}
		for _, r := range groupResults {
			byID[r.DeviceAddressID] = r
		}
	}
	if st.degraded {
		cache.storeDegraded(sig, st.next, st.qualities)
	}
	if st.budget.skipped > 0 {
		warns.once("budget", "%s: degrade budget exhausted, %d points left unread this round (continues next round)",
			prefix, st.budget.skipped)
	}

	// 按原始 addrs 顺序重组，保证返回结果与 addrs 一一对应
	results := make([]driver.ReadResult, 0, len(addrs))
	for _, a := range addrs {
		results = append(results, byID[a.ID])
	}
	return results, nil
}

// readRegisterRanges 对多个 AddressRange 执行寄存器读取（FC3/FC4）。
// 与 readBitRanges 供 Modbus TCP / RTU 驱动共享，fc 用于把降级后的区间记回计划。
func readRegisterRanges(readFn func(uint16, uint16) ([]byte, error),
	addrs []po.DeviceAddress, ranges []AddressRange, cfg *ModbusTcpConfig,
	fc byte, st *readPlanState) ([]driver.ReadResult, error) {

	read := func(start, quantity uint16) ([]byte, error) {
		return readRegistersChunked(readFn, start, quantity)
	}
	return readRanges(read, addrs, ranges, cfg, false, fc, st)
}

// readBitRanges 对多个 AddressRange 执行位读取（FC1/FC2）。
// 语义与 readRegisterRanges 相同，仅针对线圈/离散输入。
func readBitRanges(readFn func(uint16, uint16) ([]byte, error),
	addrs []po.DeviceAddress, ranges []AddressRange,
	fc byte, st *readPlanState) ([]driver.ReadResult, error) {

	read := func(start, quantity uint16) ([]byte, error) {
		return readBitsChunked(readFn, start, quantity)
	}
	return readRanges(read, addrs, ranges, nil, true, fc, st)
}

// readRanges 逐区间读取并汇总结果，同时把每个区间在下一轮计划里的形态记进 st。
//
// **契约：addrs 里每个点位都恰好有一条记录**（读得到的带值、读不到的 Quality=0），
// 一条都不少、也不重复。骨架在这里预填而不是留给驱动，有两个理由：
//
//  1. 降级会把确认读不到的点位从计划里摘掉，摘掉之后它们不再出现在任何一次解析里，
//     只有按 addrs 预填才能保证下游仍收到一条带 DataType/Kind 的记录（空着就是往下游
//     发一条没有类型的记录，而 MQTT 负载与时序库表结构正靠它决定怎么写）；
//  2. 摘掉之后**也不能按区间去预填** —— 那些点位已经不在任何区间里了。
func readRanges(read spanReader, addrs []po.DeviceAddress, ranges []AddressRange,
	cfg *ModbusTcpConfig, bitMode bool, fc byte, st *readPlanState) ([]driver.ReadResult, error) {

	// 预填骨架：addrs 里每个点位先摆一条空结果。
	// 质量码取 st.qualities —— 此时它装的是**上一轮**已确认的坏点；本轮新确认的要等
	// 循环走完（分类判定发生在循环里）才回填，见函数末。
	byID := make(map[string]driver.ReadResult, len(addrs))
	for i := range addrs {
		a := &addrs[i]
		byID[a.ID] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           "",
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         st.qualities[a.ID],
		}
	}

	var parsed []driver.ReadResult
	next := make([]AddressRange, 0, len(ranges))
	for i := range ranges {
		subs, err := readRangeDegrading(read, addrs, &ranges[i], cfg, bitMode, st, &parsed)
		if err != nil {
			return nil, err
		}
		next = append(next, subs...)
	}
	st.next[fc] = next

	// 按原始 addrs 顺序输出（驱动按 id 取用，顺序只为可读）
	for _, r := range parsed {
		byID[r.DeviceAddressID] = r
	}
	results := make([]driver.ReadResult, 0, len(addrs))
	for i := range addrs {
		r := byID[addrs[i].ID]
		// 回填本轮新确认坏点的质量码。两处「只在仍是未归类时才回填」的讲究：
		//  1. 解析失败的点自带上 QualityBadParse（比区间级的 comms/address 更具体，别覆盖）；
		//  2. Quality==QualityGood 的点不动 —— 正常点位不该被标坏。
		if r.Quality == driver.QualityBad {
			r.Quality = st.qualities[addrs[i].ID]
		}
		results = append(results, r)
	}
	return results, nil
}
