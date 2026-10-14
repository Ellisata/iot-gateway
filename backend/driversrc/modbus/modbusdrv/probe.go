// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"iot-gateway/driver"
)

// ==================== 局域网扫描发现 ====================
//
// 本文件实现 Modbus TCP 的「单目标探测」：给我一个 IP:端口，判断上面是否
// 跑着 Modbus 从站、能不能读出设备标识、有哪些站号在应答。
//
// **为什么自己发 MBAP 帧、不复用 clientTcp.go 的客户端**：
// 依赖的 goburrow/modbus v0.1.0 的 Client 接口只覆盖 FC1/2/3/4/5/6/15/16/22/23/24
// 与 FIFO，**没有 FC43（Read Device Identification）**，且它内部的 send 未导出。
// 而 FC43 恰恰是"这台设备叫什么"的唯一标准途径。裸发 MBAP 只有几十行，
// 比引一个新依赖或改第三方库划算。

// Modbus TCP 扫描默认值。
const (
	// defaultScanPort 默认探测端口。502 是 IANA 注册的 Modbus 端口。
	defaultScanPort = 502

	// defaultScanDialTimeoutMs 单目标 TCP 拨号超时。
	// 目标不可达时靠它止损，而一个 /24 网段的总耗时主要由它决定
	// （不可达的目标占绝大多数，每个都要等这么久）。
	defaultScanDialTimeoutMs = 1500

	// defaultScanRequestTimeoutMs 单帧 Modbus 请求的超时。
	defaultScanRequestTimeoutMs = 1000

	// defaultScanBudget 未指定预算时的兜底值，与 Discovery().MaxBudgetMs 一致。
	defaultScanBudget = 6 * time.Second

	// maxScanUnitIDs 单次探测最多扫多少个站号。
	// 上限存在的意义是防止一份手滑的选项（如 "1-255"）把单目标探测拖过预算，
	// 而超预算会撞网关侧的 RPC 超时并累积插件卡死计数。
	maxScanUnitIDs = 32

	// maxScanUnitID Modbus 从站地址上限（0 是广播地址，不作为探测目标）。
	maxScanUnitID = 247
)

// MBAP / PDU 常量。
const (
	// mbapHeaderLen MBAP 头长度：事务号(2) + 协议号(2) + 长度(2) + 单元标识(1)。
	mbapHeaderLen = 7

	// mbapMaxPDU Modbus PDU 上限（协议规定 253 字节）。
	mbapMaxPDU = 253

	// meiReadDeviceID / meiReadDeviceIDCode FC43 的 MEI 类型与"读基本标识"子码。
	meiReadDeviceID     = 0x2B
	meiReadDeviceIDCode = 0x0E

	// exceptionFlag 异常应答的功能码高位。pdu[0]&exceptionFlag != 0 表示异常应答。
	exceptionFlag = 0x80
)

// 设备标识对象 ID（FC43 基本类别，ReadDeviceIdCode=1）。
const (
	objVendorName      = 0x00
	objProductCode     = 0x01
	objMajorMinorRev   = 0x02
	objVendorURL       = 0x03
	objProductName     = 0x04
	objModelName       = 0x05
	objApplicationName = 0x06
)

// scanForm Modbus TCP 的扫描选项表单（form-create schema，风格与 main.go 里的
// 协议配置表单一致）。
//
// 门户上要交代清楚"站号"这一项的陷阱：原生 TCP PLC 多数**忽略** MBAP 里的
// 单元标识，对任何站号都答，于是"逐站号探测"会把同一台物理设备报成 8 台。
// 所以默认只探 1，要扫范围必须显式写。
const scanForm = `{
  "rule": [
    {"type":"input","field":"unitIds","title":"站号","$required":false,"value":"1",
     "info":"要探测的从站地址，逗号分隔（如 1,2,5）或区间（如 1-8），最多 32 个。默认只探 1。注意：原生 TCP PLC 多数忽略站号，对任何站号都应答，扫区间会把同一台设备报成多台；只有经 Modbus TCP→RTU 网关接入时站号才有区分意义"},
    {"type":"inputNumber","field":"dialTimeoutMs","title":"拨号超时(ms)","$required":false,"value":1500,
     "info":"默认 1500。目标不可达时靠它止损；扫一个网段的总耗时主要由它决定，调小可显著加快扫描，代价是慢设备可能被漏掉"},
    {"type":"inputNumber","field":"requestTimeoutMs","title":"单帧超时(ms)","$required":false,"value":1000,
     "info":"默认 1000。设备应答慢时调大；过小会把慢设备的应答判成\"没有设备\""}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// Discovery 返回 Modbus TCP 的扫描能力声明，供 main.go 填进 Meta.Protocols。
//
// 只挂在 TCP 上：RTU 是串口协议，目标模型（host/port）表达不了 comPort，
// driversdk.Meta.Validate 也会拒绝 SerialExclusive + Discovery 的组合。
func Discovery() *driver.DiscoverySpec {
	return &driver.DiscoverySpec{
		Title:       "Modbus TCP 扫描",
		Description: "逐主机拨号 502 端口，先读设备标识（FC43/14），不支持则用一次寄存器读取判定是否为 Modbus 从站",
		FormJSON:    json.RawMessage(scanForm),
		DefaultPort: defaultScanPort,
		TargetMode:  driver.TargetModeUnicast,
		// 6s：够一次拨号(1.5s) + 设备标识(1s) + 兜底寄存器读(1s) + 少量站号，
		// 同时保证一个 /24 网段即便全不可达也只在可接受的时间内跑完。
		MaxBudgetMs: 6000,
	}
}

// scanOptions 解析后的扫描选项。
type scanOptions struct {
	unitIDs        []int
	dialTimeout    time.Duration
	requestTimeout time.Duration
}

// scanOptionsRaw 扫描选项的原始形态。键名与 scanForm 的 field 一一对应。
//
// 超时用 rawInt（而非 int）：form-create 的不同控件产出的 JSON 形态不一致
// （inputNumber 给数字，某些控件给字符串），而声明成 int 时一份带字符串的
// 选项会让 json.Unmarshal 整体报错 —— 不是那一个字段失效，而是整次扫描全失败。
// 这与 config.go 里同名取舍是同一条理由。
type scanOptionsRaw struct {
	UnitIDs          string `json:"unitIds"`
	DialTimeoutMs    rawInt `json:"dialTimeoutMs"`
	RequestTimeoutMs rawInt `json:"requestTimeoutMs"`
}

// parseScanOptions 解析扫描选项。options 为空（长度 0）时全部走默认值。
func parseScanOptions(options json.RawMessage) (scanOptions, error) {
	opt := scanOptions{
		unitIDs:        []int{1},
		dialTimeout:    defaultScanDialTimeoutMs * time.Millisecond,
		requestTimeout: defaultScanRequestTimeoutMs * time.Millisecond,
	}
	if len(options) == 0 {
		return opt, nil
	}

	var raw scanOptionsRaw
	if err := json.Unmarshal(options, &raw); err != nil {
		return opt, fmt.Errorf("解析扫描选项失败: %w", err)
	}

	if units, err := parseUnitIDs(raw.UnitIDs); err != nil {
		return opt, err
	} else if len(units) > 0 {
		opt.unitIDs = units
	}
	if raw.DialTimeoutMs.set && raw.DialTimeoutMs.val > 0 {
		opt.dialTimeout = time.Duration(raw.DialTimeoutMs.val) * time.Millisecond
	}
	if raw.RequestTimeoutMs.set && raw.RequestTimeoutMs.val > 0 {
		opt.requestTimeout = time.Duration(raw.RequestTimeoutMs.val) * time.Millisecond
	}
	return opt, nil
}

// parseUnitIDs 解析站号表达式：逗号分隔的单值（"1,2,5"）与闭区间（"1-8"）可混用。
//
// 空串返回 nil（调用方回落到默认的 [1]）。越界与非法片段一律**报错而不是静默丢弃**：
// 一个写错的站号表达式若被悄悄忽略，用户看到的是"少扫到几台设备"，
// 而原因指向的是网络，排查方向完全错。
func parseUnitIDs(expr string) ([]int, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, nil
	}

	seen := make(map[int]bool)
	out := make([]int, 0, 4)
	add := func(v int) error {
		if v < 1 || v > maxScanUnitID {
			return fmt.Errorf("站号 %d 超出 [1,%d]", v, maxScanUnitID)
		}
		if seen[v] {
			return nil
		}
		seen[v] = true
		out = append(out, v)
		return nil
	}

	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		if !isRange {
			v, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("站号 %q 不是数字", part)
			}
			if err := add(v); err != nil {
				return nil, err
			}
		} else {
			from, err := strconv.Atoi(strings.TrimSpace(lo))
			if err != nil {
				return nil, fmt.Errorf("站号区间 %q 的起点不是数字", part)
			}
			to, err := strconv.Atoi(strings.TrimSpace(hi))
			if err != nil {
				return nil, fmt.Errorf("站号区间 %q 的终点不是数字", part)
			}
			if from > to {
				return nil, fmt.Errorf("站号区间 %q 的起点大于终点", part)
			}
			// 区间先单独卡上限再展开：一份 "1-65535" 的选项不该先构造一个
			// 六万元素的切片再被截断。
			if to-from+1 > maxScanUnitIDs {
				return nil, fmt.Errorf("站号区间 %q 超过单次最多 %d 个站号", part, maxScanUnitIDs)
			}
			for v := from; v <= to; v++ {
				if err := add(v); err != nil {
					return nil, err
				}
			}
		}
		if len(out) > maxScanUnitIDs {
			return nil, fmt.Errorf("站号超过单次最多 %d 个", maxScanUnitIDs)
		}
	}
	return out, nil
}

// deviceIdentity FC43 读回的设备标识（基本类别）。
type deviceIdentity struct {
	vendor      string
	productCode string
	revision    string
	productName string
	modelName   string
}

// empty 报告是否什么都没读到。
func (id deviceIdentity) empty() bool {
	return id.vendor == "" && id.productCode == "" && id.revision == "" &&
		id.productName == "" && id.modelName == ""
}

// model 返回最贴近"型号"的那一项。
//
// FC43 的这几个对象在不同厂商的设备上叫法不一：有的只填 ProductCode，
// 有的只填 ModelName / ProductName。按"信息量"从具体到笼统挑一个，
// 比固定读某一个字段的空手率高得多。
func (id deviceIdentity) model() string {
	for _, s := range []string{id.modelName, id.productName, id.productCode} {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// ProbeTCP 探测单个 Modbus TCP 目标。
//
// 返回 (nil, nil) 表示"这个目标上没有 Modbus 从站" —— **"没扫到"不是错误**，
// 目标不可达、跑着别的服务、或者压根没设备，都是扫描的正常结果。把这类情况
// 报成 error 会让一次 /24 扫描产生两百多条错误噪声。
//
// error 只用于**选项本身有问题**（站号表达式非法、超时值不是数字）——
// 那是用户的输入错误，值得原样报出来让前端显示。
//
// 一定在 budget 内返回：站号扫描途中预算耗尽就带上已有结果正常返回，
// 绝不返回 error —— 超预算会撞网关侧 RPC 超时并累积插件卡死计数，
// 连续几次会把整个插件进程连同它下面所有协议、所有设备一起重启。
func ProbeTCP(target driver.ScanTarget, options json.RawMessage, budget time.Duration) ([]driver.DiscoveredDevice, error) {
	opt, err := parseScanOptions(options)
	if err != nil {
		return nil, err
	}
	if budget <= 0 {
		budget = defaultScanBudget
	}
	deadline := time.Now().Add(budget)

	host := strings.TrimSpace(target.Host)
	if host == "" {
		return nil, nil
	}
	port := target.Port
	if port <= 0 {
		port = defaultScanPort
	}

	// 拨号超时夹到剩余预算内：预算 500ms 而拨号超时 1500ms 时，若不等它，
	// 这一次请求必然撞网关侧超时。
	dialTimeout := opt.dialTimeout
	if remaining := time.Until(deadline); remaining < dialTimeout {
		dialTimeout = remaining
	}
	if dialTimeout <= 0 {
		return nil, nil // 预算已耗尽，直接当作没扫到
	}

	conn, err := (&net.Dialer{Timeout: dialTimeout}).Dial("tcp",
		net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		// 连不上 = 这里没有设备。这是最常见的分支（一个 /24 里绝大多数地址没人）。
		return nil, nil
	}
	defer func() { _ = conn.Close() }()

	m := &mbapConn{conn: conn, deadline: deadline, timeout: opt.requestTimeout}

	first := opt.unitIDs[0]
	id, identified := m.readDeviceID(first)
	if !identified && !m.probeUnit(first) {
		// 两次都没拿到自洽的 Modbus 应答：502 端口上跑着别的服务（HTTP、
		// 或者某厂商的私有协议），不是 Modbus 设备。
		return nil, nil
	}

	devices := []driver.DiscoveredDevice{buildDevice(host, port, first, id, identified)}

	// 其余站号。默认只有 [1]，因此这段通常不执行 —— 要扫范围必须用户在
	// 扫描表单里显式写（见 scanForm 里对"原生 TCP PLC 忽略站号"的说明）。
	for _, unit := range opt.unitIDs[1:] {
		if time.Now().After(deadline) {
			break // 预算用尽：带上已完成的部分正常返回
		}
		if m.probeUnit(unit) {
			devices = append(devices, buildDevice(host, port, unit, deviceIdentity{}, false))
		}
	}
	return devices, nil
}

// buildDevice 把一次成功的探测组装成 DiscoveredDevice。
func buildDevice(host string, port, unit int, id deviceIdentity, identified bool) driver.DiscoveredDevice {
	dev := driver.DiscoveredDevice{
		Host:   host,
		Port:   port,
		UnitID: unit,
		// ProtocolJSON 的键名必须与 modbusConfigRaw（config.go）逐一对应 ——
		// 那份结构**没有** timeoutMs 字段，所以这里也不写它：写了会被静默忽略，
		// 用户看到表单里配的超时"不生效"却又找不到原因。
		ProtocolJSON: fmt.Sprintf(`{"host":%q,"port":%d,"unitId":%d}`, host, port, unit),
	}

	if identified {
		name := strings.TrimSpace(strings.Join(
			[]string{id.vendor, id.model()}, " "))
		if name == "" {
			name = "Modbus 设备"
		}
		dev.Name = name
		dev.Vendor = strings.TrimSpace(id.vendor)
		dev.Model = id.model()
		dev.Version = strings.TrimSpace(id.revision)
		dev.Extra = append(dev.Extra, driver.Attribute{Name: "识别方式", Value: "读设备标识(FC43/14)"})
		if p := strings.TrimSpace(id.productName); p != "" {
			dev.Extra = append(dev.Extra, driver.Attribute{Name: "产品名称", Value: p})
		}
	} else {
		// 有应答但读不出标识：设备是 Modbus 从站，只是不支持 FC43
		//（多数 PLC 如此）。如实标注，用户才知道为什么厂商/型号是空的。
		dev.Name = "Modbus 设备"
		dev.Extra = append(dev.Extra, driver.Attribute{
			Name: "识别方式", Value: "读寄存器(FC3)——设备不支持读设备标识",
		})
	}
	return dev
}

// mbapConn 一条 Modbus TCP 连接上的 MBAP 收发。
//
// 不复用 goburrow 的客户端：它没有 FC43（见文件头说明），也不需要它的
// 功能码编解码 —— 这里发的是三种固定帧。
type mbapConn struct {
	conn net.Conn
	// txID 事务号，每次交换自增并校验应答回显。
	// 校验回显不是形式主义：对端若不是 Modbus（比如 502 上开着 HTTP），
	// 我们读回的"头"会是协议文本，长度与协议号校验能立刻判定这不是 Modbus，
	// 而不是把一段垃圾当成 PDU 解析下去。
	txID uint16
	// deadline 本次探测的绝对截止时刻，每个操作都要夹在它之内。
	deadline time.Time
	// timeout 单次交换的超时。
	timeout time.Duration
}

// exchange 发一帧请求，收一帧应答，返回应答 PDU。
//
// 返回的 error 一律表示"对端没有按 Modbus 说话"（超时、连接断开、协议号不符、
// 长度自洽性不成立）。**异常应答（FC|0x80）不是 error** —— 它同样是自洽的
// Modbus 应答，证明对端是 Modbus 设备，只是不支持该功能码。这条区分正是
// "是 Modbus 设备"与"支持 FC43"两件事能分开判定的关键。
func (m *mbapConn) exchange(unit byte, pdu []byte) ([]byte, error) {
	if len(pdu) == 0 || len(pdu) > mbapMaxPDU {
		return nil, fmt.Errorf("modbus: PDU 长度 %d 非法", len(pdu))
	}

	txID := m.txID
	m.txID++

	req := make([]byte, mbapHeaderLen+len(pdu))
	binary.BigEndian.PutUint16(req[0:2], txID)
	binary.BigEndian.PutUint16(req[2:4], 0) // 协议号恒为 0
	binary.BigEndian.PutUint16(req[4:6], uint16(len(pdu)+1))
	req[6] = unit
	copy(req[mbapHeaderLen:], pdu)

	if !m.setDeadline() {
		return nil, fmt.Errorf("modbus: 探测预算已耗尽")
	}
	if _, err := m.conn.Write(req); err != nil {
		return nil, err
	}

	if !m.setDeadline() {
		return nil, fmt.Errorf("modbus: 探测预算已耗尽")
	}
	var hdr [mbapHeaderLen]byte
	if _, err := io.ReadFull(m.conn, hdr[:]); err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint16(hdr[2:4]) != 0 {
		return nil, fmt.Errorf("modbus: 协议号非 0（对端不是 Modbus）")
	}
	if echo := binary.BigEndian.Uint16(hdr[0:2]); echo != txID {
		return nil, fmt.Errorf("modbus: 事务号回显不符（发出 %d，收到 %d）", txID, echo)
	}
	length := int(binary.BigEndian.Uint16(hdr[4:6]))
	// 长度字段含单元标识，故至少为 2（unit + 功能码），上限为 1 + PDU 上限。
	if length < 2 || length > mbapMaxPDU+1 {
		return nil, fmt.Errorf("modbus: MBAP 长度字段 %d 非法", length)
	}

	resp := make([]byte, length-1)
	if _, err := io.ReadFull(m.conn, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// setDeadline 在「单次交换超时」与「探测预算截止」之间取先到者。
// 返回 false 表示预算已耗尽，调用方应立即放弃。
func (m *mbapConn) setDeadline() bool {
	limit := time.Now().Add(m.timeout)
	if m.deadline.Before(limit) {
		limit = m.deadline
	}
	if !time.Now().Before(limit) {
		return false
	}
	// SetDeadline 失败（连接已关闭）不必单独处理：紧接着的读写会返回错误。
	_ = m.conn.SetDeadline(limit)
	return true
}

// readDeviceID 用 FC43/14 读基本设备标识。identified 为 false 表示设备
// 不支持该功能码（或应答不成形），**不表示失败**。
func (m *mbapConn) readDeviceID(unit int) (deviceIdentity, bool) {
	pdu, err := m.exchange(byte(unit), []byte{meiReadDeviceID, meiReadDeviceIDCode, 0x01, 0x00})
	if err != nil {
		return deviceIdentity{}, false
	}
	if len(pdu) == 0 || pdu[0] == meiReadDeviceID|exceptionFlag {
		// 异常应答：设备是 Modbus 从站但不支持读设备标识。极常见。
		return deviceIdentity{}, false
	}
	// 应答 PDU：2B 0E <code> <conformity> <moreFollows> <nextObjId> <numObjects> 然后逐对象。
	// 头 7 字节 + 每个对象至少 2 字节（objId + objLen）。
	if len(pdu) < 7 || pdu[0] != meiReadDeviceID || pdu[1] != meiReadDeviceIDCode {
		return deviceIdentity{}, false
	}

	var id deviceIdentity
	count := int(pdu[6])
	for i, off := 0, 7; i < count && off+2 <= len(pdu); i++ {
		objID := pdu[off]
		objLen := int(pdu[off+1])
		off += 2
		if off+objLen > len(pdu) {
			break // 对象被截断：保留已解析出来的部分
		}
		val := string(pdu[off : off+objLen])
		off += objLen

		switch objID {
		case objVendorName:
			id.vendor = val
		case objProductCode:
			id.productCode = val
		case objMajorMinorRev:
			id.revision = val
		case objProductName:
			id.productName = val
		case objModelName:
			id.modelName = val
		case objVendorURL, objApplicationName:
			// 展示价值低，忽略（前端有更值得占位置的信息）。
		}
	}
	return id, !id.empty()
}

// probeUnit 判断某个站号是否应答，即该站号上是否有从站。
//
// 判据是「收到自洽的 MBAP 应答」而不是「读取成功」：异常应答（如非法数据地址
// 0x02）同样是自洽应答，它证明**这个站号上有一个 Modbus 从站**，只是不支持
// 我们问的那个寄存器。这正是探测想要的结论。
func (m *mbapConn) probeUnit(unit int) bool {
	// FC3 读 1 个保持寄存器（地址 0）。任何自洽应答都算命中。
	pdu, err := m.exchange(byte(unit), []byte{FuncCodeReadHoldingRegisters, 0x00, 0x00, 0x00, 0x01})
	if err != nil || len(pdu) == 0 {
		return false
	}
	if pdu[0] != FuncCodeReadHoldingRegisters && pdu[0] != FuncCodeReadHoldingRegisters|exceptionFlag {
		return false
	}
	return true
}
