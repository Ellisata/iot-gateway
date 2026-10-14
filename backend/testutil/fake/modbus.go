// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"encoding/binary"
	"io"
	"net"
	"sync/atomic"
	"time"
)

// modbusServerOpts 假从站的行为开关。
//
// 各字段的零值 = 保持 NewModbusTCP 的原有行为，因此既有构造函数与既有用例
// （黄金比对、容量压测）的语义完全不受影响。新增的四个字段同样遵守这条承诺：
// 不配它们时，读写行为与从前逐字节一致（含 parity 黄金基线）。
type modbusServerOpts struct {
	// latency 每次事务的固定延迟。
	latency time.Duration
	// deviceID 是否应答 FC43/14（读设备标识）。false 时走 IllegalFunction 异常 ——
	// 这正是多数真实 PLC 的行为，因此默认关闭本身就是一条有价值的测试夹具。
	deviceID bool
	// units 非空时只应答这些站号，其余**静默丢弃**（不回异常、不回数据），
	// 模拟 Modbus TCP→RTU 网关上"该从站不存在"的情形。
	units map[byte]bool

	// regs 有值的保持寄存器/输入寄存器；不在表里的回 0。
	regs map[uint16]uint16
	// bits 为 true 的线圈/离散输入；不在表里的回 0。
	bits map[uint16]bool
	// spans 非空时，**整个读请求**必须落在其中**某一个**闭区间之内，否则回
	// IllegalDataAddress(2)。模拟"设备只实现了这几段地址空间"。
	//
	// 用"必须是某一个区间的子集"而不是"所有地址都在并集里"：真实设备拒绝的是
	// **跨越空洞的那一帧**，而不是"逐地址检查后只回存在的那些"。少了这条，
	// 点位扫描的对半降级就不会发生（设备会痛快地回一帧带洞的数据）。
	spans [][2]uint16
	// reqs 非 nil 时每收到一个请求自增，供用例断言分块帧数。
	reqs *atomic.Int64
}

// ModbusOption 调整假从站的行为。需要同时用多个（如"有空洞 + 统计帧数"）时用
// NewModbusTCPWithOption，单个需求用下面那三个便捷构造函数即可。
type ModbusOption func(*modbusServerOpts)

// NewModbusTCPWithRegisters 同 NewModbusTCP，但在指定寄存器上回配的值。
//
// 点位扫描的用例需要它：现有的 NewModbusTCP 一律回 0，而"扫到非零值"与
// "零值也要列出来"恰恰是那个功能最需要验证的两条。
func NewModbusTCPWithRegisters(regs map[uint16]uint16) (*Server, error) {
	return NewModbusTCPWithOption(func(o *modbusServerOpts) { o.regs = regs })
}

// NewModbusTCPWithUnreadableGap 同 NewModbusTCP，但只有落在这个闭区间**之内**的
// 读请求才被受理，其余回 IllegalDataAddress(2)。
//
// 模拟"设备只实现了这一段地址空间"—— 点位扫描的**对半降级**只有靠它才验得到：
// 扫描器对整段读失败时会一路拆到单点，把空洞一个个摘出来。
func NewModbusTCPWithUnreadableGap(lo, hi uint16) (*Server, error) {
	return NewModbusTCPWithOption(WithModbusSpan(lo, hi))
}

// NewModbusTCPWithRequestCounter 同 NewModbusTCP，但把收到的请求数累计到 reqs。
//
// 供"分块不超单帧上限"这类用例断言：扫描 300 个寄存器必须拆成至少 3 帧，
// 而一次性发 300 会被真实设备拒掉（协议上限 125）。用帧数当判据比断言
// "没有报错"强得多 —— 后者在实现退化成"整段一帧"时依然会通过。
func NewModbusTCPWithRequestCounter(reqs *atomic.Int64) (*Server, error) {
	return NewModbusTCPWithOption(func(o *modbusServerOpts) { o.reqs = reqs })
}

// withinSpan 报告这次读是否落在一个被实现的地址区间**之内**。
//
// spans 为空（默认）= 整片地址空间都存在，与从前的行为一致。
//
// 判据是"整个请求是某一段的子集"，不是"每个地址都落在并集里"：真实设备拒绝的
// 是跨越空洞的那一帧。用 int 算边界是因为 start+qty 会越过 uint16 上限，
// 在 uint16 里溢出会把"读 65000 起的 2000 个寄存器"错判成落在区间内。
func (o modbusServerOpts) withinSpan(start, qty uint16) bool {
	if len(o.spans) == 0 || qty == 0 {
		return true
	}
	from := int(start)
	to := from + int(qty) - 1
	for _, sp := range o.spans {
		if from >= int(sp[0]) && to <= int(sp[1]) {
			return true
		}
	}
	return false
}

// NewModbusTCPWithOption 用一组选项启动假从站。
func NewModbusTCPWithOption(opts ...ModbusOption) (*Server, error) {
	var o modbusServerOpts
	for _, fn := range opts {
		fn(&o)
	}
	return newServer(func(c net.Conn) { handleModbusConn(c, o) })
}

// WithModbusRegisters/WithModbusBits/WithModbusSpan/WithModbusRequests 是
// NewModbusTCPWithOption 的选项形态，便于组合出复杂夹具。
func WithModbusRegisters(regs map[uint16]uint16) ModbusOption {
	return func(o *modbusServerOpts) { o.regs = regs }
}

func WithModbusBits(bits map[uint16]bool) ModbusOption {
	return func(o *modbusServerOpts) { o.bits = bits }
}

// WithModbusSpan 追加一段设备实现的地址区间。可以多次调用拼出**带空洞**的设备
// （如 [100,103] 与 [110,111] 两段，中间 104-109 不存在）—— 点位扫描的对半降级
// 正是为这种设备写的。
func WithModbusSpan(lo, hi uint16) ModbusOption {
	return func(o *modbusServerOpts) { o.spans = append(o.spans, [2]uint16{lo, hi}) }
}

func WithModbusRequests(reqs *atomic.Int64) ModbusOption {
	return func(o *modbusServerOpts) { o.reqs = reqs }
}

// NewModbusTCP 启动一个 Modbus TCP 从站模拟器（MBAP + PDU）。
//
// 支持读功能码 1/2/3/4（返回定长全零数据）与写功能码 5/6/15/16（回显成功），
// 其余功能码返回 IllegalFunction 异常。不对读取数量做协议上限校验（数量即来自请求，
// 响应长度完全按请求回填），用于把网关侧容量从设备侧限制中隔离出来。
func NewModbusTCP() (*Server, error) {
	return newServer(func(c net.Conn) { handleModbusConn(c, modbusServerOpts{}) })
}

// NewModbusTCPWithLatency 同 NewModbusTCP，但每次事务注入固定延迟，模拟真实网络 RTT。
// 驱动按 mergeWindow 串行发帧（5000 点 ≈ 40 帧），RTT 直接叠加进轮询周期。
func NewModbusTCPWithLatency(d time.Duration) (*Server, error) {
	return newServer(func(c net.Conn) { handleModbusConn(c, modbusServerOpts{latency: d}) })
}

// NewModbusTCPWithDeviceID 同 NewModbusTCP，但额外应答 FC43/14 读设备标识
// （基本三件套：厂商 / 产品代码 / 版本号）。
//
// 供局域网扫描发现的用例使用：探测器的"读设备标识"分支只有这里能覆盖到。
// 与之相对，**不加这个开关的 NewModbusTCP 就是"不支持 FC43 的 PLC"**夹具 ——
// 那条降级分支（改用一次寄存器读取判定是否 Modbus）由它覆盖。
//
// 应答的标识内容：厂商 "iot-gateway"、产品代码 "FX3U-64M"、版本 "1.0.0"。
func NewModbusTCPWithDeviceID() (*Server, error) {
	return newServer(func(c net.Conn) { handleModbusConn(c, modbusServerOpts{deviceID: true}) })
}

// NewModbusTCPWithUnitIDs 同 NewModbusTCP，但只应答这些站号的请求，其余静默丢弃。
//
// 用于验证探测器的站号扫描：既验证"列出来的站号都应答"，也验证
// "没列出来的站号不会被误报成设备"。
func NewModbusTCPWithUnitIDs(units ...byte) (*Server, error) {
	set := make(map[byte]bool, len(units))
	for _, u := range units {
		set[u] = true
	}
	return newServer(func(c net.Conn) { handleModbusConn(c, modbusServerOpts{units: set}) })
}

func handleModbusConn(conn net.Conn, opts modbusServerOpts) {
	hdr := make([]byte, 7)
	for {
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		// MBAP 长度字段 = 1(unitID) + PDU 长度
		length := binary.BigEndian.Uint16(hdr[4:6])
		if length < 2 { // 至少 unitID + FC
			return
		}
		pdu := make([]byte, length-1)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			return
		}

		// 计数放在站号过滤**之前**：被静默丢弃的请求也真实到过设备，
		// 用例要断言的是"网关发了几帧"，不是"设备回了几帧"。
		if opts.reqs != nil {
			opts.reqs.Add(1)
		}

		// 站号过滤放在这里而不是靠 buildModbusResponse 返回 nil：
		// 返回 nil 的既有语义是"关闭连接"，用它来表示"静默丢弃"会让探测器
		// 看到连接被关（进而报错），而真实网关的表现是**什么都不回**、由
		// 请求方超时。两者的区别正是这条用例要覆盖的。
		if len(opts.units) > 0 && !opts.units[hdr[6]] {
			continue
		}

		resp := buildModbusResponse(hdr, pdu, opts)
		if resp == nil {
			return
		}
		if opts.latency > 0 {
			time.Sleep(opts.latency)
		}
		if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}

// buildModbusResponse 构造 Modbus TCP 响应帧。
// hdr 为请求的 7 字节 MBAP 头（回显事务号与 unitID），pdu 为请求 PDU。
func buildModbusResponse(hdr, pdu []byte, opts modbusServerOpts) []byte {
	fc := pdu[0]
	var body []byte
	switch fc {
	case 1, 2: // 读线圈 / 读离散输入：响应数据 = ceil(数量/8) 字节位图
		if len(pdu) != 5 {
			return modbusException(hdr, fc, 3)
		}
		start := binary.BigEndian.Uint16(pdu[1:3])
		qty := binary.BigEndian.Uint16(pdu[3:5])
		if !opts.withinSpan(start, qty) {
			return modbusException(hdr, fc, 2) // IllegalDataAddress
		}
		bits := make([]byte, (int(qty)+7)/8)
		for i := 0; i < int(qty); i++ {
			if opts.bits[start+uint16(i)] {
				bits[i/8] |= 1 << (uint(i) % 8)
			}
		}
		body = append([]byte{fc, byte(len(bits))}, bits...)
	case 3, 4: // 读保持寄存器 / 读输入寄存器：响应数据 = 数量 × 2 字节
		if len(pdu) != 5 {
			return modbusException(hdr, fc, 3)
		}
		start := binary.BigEndian.Uint16(pdu[1:3])
		qty := binary.BigEndian.Uint16(pdu[3:5])
		if !opts.withinSpan(start, qty) {
			return modbusException(hdr, fc, 2) // IllegalDataAddress
		}
		data := make([]byte, int(qty)*2)
		for i := 0; i < int(qty); i++ {
			binary.BigEndian.PutUint16(data[i*2:], opts.regs[start+uint16(i)])
		}
		body = append([]byte{fc, byte(len(data))}, data...)
	case 5, 6: // 写单线圈 / 写单寄存器：回显请求
		if len(pdu) != 5 {
			return modbusException(hdr, fc, 3)
		}
		body = pdu
	case 15, 16: // 写多线圈 / 写多寄存器：返回起始地址 + 数量
		if len(pdu) < 6 {
			return modbusException(hdr, fc, 3)
		}
		body = append([]byte{fc}, pdu[1:5]...)
	case 43: // 读设备标识（MEI 0x2B / 子码 0x0E）：仅在显式开启时应答
		if !opts.deviceID {
			// 未开启 = 多数真实 PLC 的行为：不支持该功能码。
			// 这条分支刻意与 default 同款，好让"不支持 FC43"这条路径
			// 在假设备上也走得与真设备一致。
			return modbusException(hdr, fc, 1)
		}
		// 请求 PDU: 2B 0E <ReadDeviceIdCode> <ObjectId>
		if len(pdu) != 4 || pdu[1] != 0x0E {
			return modbusException(hdr, fc, 3)
		}
		body = modbusDeviceIDResponse()
	default:
		return modbusException(hdr, fc, 1) // IllegalFunction
	}

	resp := make([]byte, 0, 7+len(body))
	resp = append(resp, hdr[0], hdr[1])                            // 事务号回显
	resp = append(resp, 0, 0)                                      // 协议号 = 0
	resp = append(resp, byte((len(body)+1)>>8), byte(len(body)+1)) // 长度 = unitID + body
	resp = append(resp, hdr[6])                                    // unitID 回显
	resp = append(resp, body...)
	return resp
}

// modbusDeviceIDResponse 构造 FC43/14 读设备标识的应答 PDU（基本类别三件套）。
//
// 布局：2B 0E <ReadDeviceIdCode> <ConformityLevel> <MoreFollows> <NextObjectId>
// <NumberOfObjects> 然后逐对象 <ObjId> <ObjLen> <ObjValue>。
//
// 只回基本三件套（0x00 厂商 / 0x01 产品代码 / 0x02 版本），不回模型名与产品名 ——
// 这恰好覆盖探测器 deviceIdentity.model() 的"退到 ProductCode"那条分支：
// 真实设备里只填 ProductCode 的同样常见。
//
// 内容固定为厂商 "iot-gateway"、产品代码 "FX3U-64M"、版本 "1.0.0"，供用例断言。
func modbusDeviceIDResponse() []byte {
	objs := []struct {
		id  byte
		val string
	}{
		{0x00, "iot-gateway"},
		{0x01, "FX3U-64M"},
		{0x02, "1.0.0"},
	}
	body := []byte{0x2B, 0x0E, 0x01, 0x01, 0x00, 0x00, byte(len(objs))}
	for _, o := range objs {
		body = append(body, o.id, byte(len(o.val)))
		body = append(body, o.val...)
	}
	return body
}

// modbusException 构造 Modbus 异常响应（FC|0x80 + 异常码）。
func modbusException(hdr []byte, fc, code byte) []byte {
	body := []byte{fc | 0x80, code}
	resp := make([]byte, 0, 9)
	resp = append(resp, hdr[0], hdr[1], 0, 0, 0, byte(len(body)+1), hdr[6])
	resp = append(resp, body...)
	return resp
}
