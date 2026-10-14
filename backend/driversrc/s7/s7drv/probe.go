// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package s7

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/robinson/gos7"

	"iot-gateway/driver"
)

// ==================== 局域网扫描发现 ====================
//
// 本文件实现 S7 的「单目标探测」：给我一个 IP:端口，判断上面是否跑着西门子 PLC。
//
// **为什么本地没有像 Modbus 那样裸发协议帧**：S7 跑在 ISO-on-TCP（RFC1006）上，
// 一次会话要依次完成 TPKT/COTP 连接、S7 PDU 长度协商两轮握手，之后才谈得上读数据。
// 这三步在 gos7 里就是 handler.Connect() 一个调用，而它正是本驱动采集时用的那条路径 ——
// 探测与采集走完全相同的握手代码，探测能连上的设备采集必定也能连上（反之亦然）。
// Modbus 那边另起炉灶是因为 goburrow 没有 FC43；S7 这边 gos7 有我们要的全部原语，
// 再手写一遍 TPKT/COTP 只会多出一份可能与采集路径漂移的实现。
//
// **判据是「S7 会话协商成功」**，不是「102 端口开着」。HMI 触摸屏、Comfort Panel、
// 以及各类 S7 网关都监听 102，但只有 PLC 会完成 S7 PDU 协商。这条判据与
// device-scan.md 里 Modbus「收到自洽的 MBAP 应答」是同一层意思。

// S7 扫描默认值与上限。
const (
	// defaultScanPort ISO-on-TCP 端口。102 是 IANA 注册的 ISO-TSAP 端口，
	// RFC1006 与 S7 都跑在它上面（gos7 内部的 isoTCP 也是这个值）。
	defaultScanPort = 102

	// defaultScanConnectTimeoutMs 单个候选的握手超时基准。
	//
	// **一个旋钮管三件事**：gos7 的 handler.Timeout 同时用于 net.Dialer 的拨号上限
	// 与 Send 里 socket 的读写 deadline，拆不开也不必拆。
	defaultScanConnectTimeoutMs = 1500

	// defaultScanBudget 未指定预算时的兜底值，与 Discovery().MaxBudgetMs 一致。
	defaultScanBudget = 8 * time.Second

	// maxScanRackSlots 单次探测最多试几组机架/槽位。
	//
	// 上限存在的意义不是性能而是**事故半径**：一份手滑的选项（比如照着某份资料
	// 把十几个组合全列上）会把单目标探测拖过预算，而超预算会撞网关侧的 RPC 超时
	// 并累积插件卡死计数 —— 连续几次会把整个插件进程连同它下面所有协议、所有设备
	// 一起重启。
	maxScanRackSlots = 4

	// maxScanRack / maxScanSlot 机架与槽位的取值上限。
	//
	// gos7 算远程 TSAP 的公式是 `connectType<<8 + rack*0x20 + slot`
	// （见其 tcpclient.go 的 setConnectionParameters）：机架占 3 位、槽位占 5 位。
	// 越界会把进位串到连接类型那个字节上，连出去的是一条**含义完全不同**的 TSAP ——
	// 不报任何错，只是永远连不上，现场表现为"这台 PLC 扫不到"。
	maxScanRack = 7
	maxScanSlot = 31

	// s7HandshakeStages 一次成功握手最多包含几次"等一个 Timeout 的 socket 操作"。
	//
	// 依次是：TCP 拨号(1) → ISO 连接 Send(2) → PDU 协商 Send(3) → 读 CPU 标识时
	// SZL 的首帧(4)与后续分片帧(5)。这个数字用于把**整次探测**的耗时反推着钳在
	// 预算之内（见 ProbeTCP 里对 wait 的计算），取自 gos7 的实现而非估计：
	// 它在每一处都会用 mb.Timeout 重设 socket deadline。
	s7HandshakeStages = 5
)

// scanForm S7 的扫描选项表单（form-create schema，风格与 main.go 里的协议配置表单一致）。
//
// 文案要把「机架/槽位」这个 S7 独有的坑说在前面：它不是设备的可选参数，而是
// PLC 上连接资源的**地址**，填错表现为"端口开着却连不上"，从错误信息里完全看不出来。
const scanForm = `{
  "rule": [
    {"type":"input","field":"rackSlots","title":"机架/槽位","$required":false,"value":"0/1,0/2",
     "info":"逗号分隔的 rack/slot 候选，最多 4 组，按顺序尝试，第一个连上的写入设备配置。S7-1200/1500 多为 0/1，S7-300/400 多为 0/2；确认某 IP 上确实有 PLC 却没扫到，先放宽这里"},
    {"type":"inputNumber","field":"connectTimeoutMs","title":"连接超时(ms)","$required":false,"value":1500,
     "info":"默认 1500。兼作 TCP 拨号与 ISO-on-TCP 协商的超时。目标不可达时靠它止损，扫一个网段的总耗时主要由它决定，调小可显著加快扫描，代价是慢设备可能被漏掉"}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// Discovery 返回 S7 的扫描能力声明，供 main.go 填进 Meta.Protocols。
//
// 本插件只有一个协议（Siemens.S7），且它只有 ISO-on-TCP 一种传输、不独占串行总线，
// 因此 driversdk.Meta.Validate 里那条「SerialExclusive 不得声明 Discovery」的禁令
// 在这里不适用 —— 与 Modbus 的 RTU 正相反。
func Discovery() *driver.DiscoverySpec {
	return &driver.DiscoverySpec{
		Title: "Siemens S7 扫描",
		Description: "逐主机连接 102 端口（ISO-on-TCP），完成 S7 会话协商即认定是 S7 设备，" +
			"并尝试读 CPU 标识（SZL 0x001C）补上型号与序列号。" +
			"机架/槽位按候选逐个尝试；102 端口有服务但协商不出 S7 会话的主机不上报" +
			"（避免把同样监听 102 的 HMI 面板报成 PLC）",
		FormJSON:    json.RawMessage(scanForm),
		DefaultPort: defaultScanPort,
		TargetMode:  driver.TargetModeUnicast,
		// 8s：够「一个候选跑满 5 个握手阶段（5×1500ms）」或「若干候选先后失败」。
		// 网关侧的 probe RPC 超时 = 本值 + 固定余量（pluginhost 的 probeRPCGrace）。
		MaxBudgetMs: 8000,
	}
}

// rackSlot 一组机架/槽位候选。
type rackSlot struct {
	rack int
	slot int
}

// String 渲染成 "0/1"，用于日志与扫描结果的展示。
func (r rackSlot) String() string { return strconv.Itoa(r.rack) + "/" + strconv.Itoa(r.slot) }

// defaultRackSlots 未填 rackSlots 时的候选。
//
// 为什么是这两组：S7-1200/1500（TIA 时代，现场存量最大）的 CPU 固定在机架 0 槽位 1，
// S7-300/400（STEP7 时代）通常是机架 0 槽位 2。其余组合（0/0、0/3、1/x）只在
// 带扩展机架或通信处理器时出现，让用户自己往表单里加而不是默认替他多花两个超时。
var defaultRackSlots = []rackSlot{{0, 1}, {0, 2}}

// scanOptions 解析后的扫描选项。
type scanOptions struct {
	rackSlots   []rackSlot
	connectWait time.Duration
}

// scanOptionsRaw 扫描选项的原始形态。键名与 scanForm 的 field 一一对应。
//
// 超时用 rawInt（而非 int）：form-create 的不同控件产出的 JSON 形态不一致
// （inputNumber 给数字，某些控件给字符串），而声明成 int 时一份带字符串的
// 选项会让 json.Unmarshal 整体报错 —— 不是那一个字段失效，而是整次扫描全失败。
// 与 config.go 里 s7ConfigRaw 把 TimeoutMS 声明成 string 是同一条理由的两面。
type scanOptionsRaw struct {
	RackSlots        string `json:"rackSlots"`
	ConnectTimeoutMs rawInt `json:"connectTimeoutMs"`
}

// rawInt 容错整数：同时接受 JSON 数字与数字字符串。
//
// set 区分「字段缺席」与「显式配了 0」。与 modbusdrv/dlt645drv 的同名类型是
// 三处独立实现（各驱动包自持一份，不跨包共享，避免为一个小工具类型引入包依赖）。
type rawInt struct {
	set bool
	val int
}

func (r *rawInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		// 认不出就当作没配，走默认值。这个值错了的表现本来就只是"扫得慢/扫不到"，
		// 不该再多一层报错。
		return nil
	}
	r.set = true
	r.val = v
	return nil
}

// parseScanOptions 解析扫描选项。options 为空（长度 0）时全部走默认值。
//
// 与点位扫描的 parsePointScanOptions 取舍不同：那边缺项一律报错（"扫多少"没有
// 合理的默认值），这里缺项静默回默认值 —— "没填机架/槽位"就是"按最常见的两组试"。
func parseScanOptions(options json.RawMessage) (scanOptions, error) {
	opt := scanOptions{
		rackSlots:   defaultRackSlots,
		connectWait: defaultScanConnectTimeoutMs * time.Millisecond,
	}
	if len(options) == 0 {
		return opt, nil
	}

	var raw scanOptionsRaw
	if err := json.Unmarshal(options, &raw); err != nil {
		return opt, fmt.Errorf("解析扫描选项失败: %w", err)
	}

	slots, err := parseRackSlots(raw.RackSlots)
	if err != nil {
		return opt, err
	}
	if len(slots) > 0 {
		opt.rackSlots = slots
	}
	if raw.ConnectTimeoutMs.set && raw.ConnectTimeoutMs.val > 0 {
		opt.connectWait = time.Duration(raw.ConnectTimeoutMs.val) * time.Millisecond
	}
	return opt, nil
}

// parseRackSlots 解析机架/槽位候选："0/1,0/2"。
//
// 空串返回 nil（调用方回落到默认候选）。非法片段一律**报错而不是静默丢弃**：
// 一个写错的候选若被悄悄忽略，用户看到的是"某台 PLC 扫不到"，
// 而原因指向的是网络，排查方向完全错 —— 与 modbusdrv.parseUnitIDs 同一条取舍。
func parseRackSlots(expr string) ([]rackSlot, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, nil
	}

	seen := make(map[rackSlot]bool)
	out := make([]rackSlot, 0, maxScanRackSlots)
	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		rackStr, slotStr, ok := strings.Cut(part, "/")
		if !ok {
			return nil, fmt.Errorf("机架/槽位 %q 不是 rack/slot 形式（如 0/1）", part)
		}
		rack, err := strconv.Atoi(strings.TrimSpace(rackStr))
		if err != nil {
			return nil, fmt.Errorf("机架/槽位 %q 的机架号不是数字", part)
		}
		slot, err := strconv.Atoi(strings.TrimSpace(slotStr))
		if err != nil {
			return nil, fmt.Errorf("机架/槽位 %q 的槽位号不是数字", part)
		}
		// 越界报错而不是钳制：钳过之后发出去的是一条**别的** TSAP，
		// 用户拿到的结果是"这一组试过了但没通"，与事实不符。
		if rack < 0 || rack > maxScanRack {
			return nil, fmt.Errorf("机架号 %d 超出 [0,%d]（%q）", rack, maxScanRack, part)
		}
		if slot < 0 || slot > maxScanSlot {
			return nil, fmt.Errorf("槽位号 %d 超出 [0,%d]（%q）", slot, maxScanSlot, part)
		}

		rs := rackSlot{rack: rack, slot: slot}
		if seen[rs] {
			continue // 重复的候选只试一次，别白花一个超时
		}
		seen[rs] = true
		out = append(out, rs)
	}

	if len(out) > maxScanRackSlots {
		return nil, fmt.Errorf("机架/槽位候选有 %d 组，超过单次最多 %d 组", len(out), maxScanRackSlots)
	}
	return out, nil
}

// ProbeTCP 探测单个 S7 目标。
//
// 返回 (nil, nil) 表示"这个目标上没有 S7 设备" —— **"没扫到"不是错误**：
// 端口不通、是 HMI 面板、或者是别的服务，都是扫描的正常结果。把这类情况报成
// error 会让一次 /24 扫描产生两百多条错误噪声。
//
// error 只用于**选项本身有问题**（机架/槽位表达式非法、超时值不是数字），
// 那是用户的输入错误，值得原样报出来让前端显示。
//
// 一定在 budget 内返回：每个候选的超时都从剩余预算里反推（见下面的 wait），
// 预算耗尽就带上已有结论（此处只有"没找到"）正常返回，绝不返回 error ——
// 超预算会撞网关侧 RPC 超时并累积插件卡死计数，连续几次会把整个插件进程连同
// 它下面所有协议、所有设备一起重启。
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
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	for _, rs := range opt.rackSlots {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break // 预算耗尽：带上"没找到"正常返回
		}
		// 每个候选最多只花剩余预算的 1/s7HandshakeStages —— 一次握手要顺序做
		// 5 次"等一个 Timeout"的 socket 操作，不这么分，最后一个候选就能带着
		// 整份剩余预算跑满 5 倍超时，把"prober 一定在 Budget 内返回"这条承诺
		// 变成一个用户填大超时值就能打破的摆设。
		wait := opt.connectWait
		if share := remaining / s7HandshakeStages; share < wait {
			wait = share
		}
		if wait <= 0 {
			break
		}

		// 每个候选一个**全新的 handler**：gos7 的 ConnectContext 在 ISO 阶段
		// 失败时会关掉底层 conn 却不把它置回 nil，同一个 handler 再 Connect 会
		// 跳过拨号直接复用那条已关闭的连接。换个 handler 就绕开了这个状态残留。
		handler := newTCPHandler(addr, rs.rack, rs.slot, ConnectionTypePG, wait)
		connectErr := handler.Connect()
		if connectErr != nil {
			_ = handler.Close()
			if isDialErr(connectErr) {
				// TCP 层就没通：可达性与机架/槽位无关，换一组候选只会把同一个
				// 超时再花一遍 —— 而一个 /24 里绝大多数地址都走这条分支，
				// 不跳出就会让整次扫描的耗时乘以候选数。
				return nil, nil
			}
			logDebug("probe: %s 以 rack=%s 协商失败: %v", addr, rs, connectErr)
			continue // 协议层失败：可能是机架/槽位不对，试下一个候选
		}

		info, identified := probeCPUInfo(handler)
		_ = handler.Close()
		dev := buildDevice(host, port, rs, info, identified)
		logDebug("probe: %s 命中（rack/slot=%s，已识别=%v）", addr, rs, identified)
		return []driver.DiscoveredDevice{dev}, nil
	}

	return nil, nil
}

// isDialErr 这次握手失败在 **TCP 拨号**这一层（地址不可达），而不是 ISO/S7 协议层。
//
// 只看 opErr.Op == "dial" 是不够宽松就够精确的：握手途中的读写失败同样是
// *net.OpError，但它说明"这台主机上确实有个东西接了线又断开了"——
// 那更可能是机架/槽位不对（部分 PLC 对未知 TSAP 直接 RST），必须继续试下一个候选。
// 只有拨号本身失败才意味着"这个地址上没有任何东西"，换候选是纯粹的浪费。
func isDialErr(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// probeCPUInfo 尝试读取 CPU 标识（SZL 0x001C），失败时 ok=false。
//
// 读不出来**不是失败**：S7-1200/1500 对 SZL 的支持随固件而异，老设备也可能拒绝。
// "能完成 S7 会话协商"这件事本身已经证明了它是 S7 设备，标识只是锦上添花 ——
// 这一点与 Modbus 探测器里"不支持 FC43 的设备照样报出来"完全一致。
//
// **绝不能改用 gos7 的 GetOrderCode() 来补固件版本。** 它内部 readSzl 的命名返回值
// `size` 从未被赋值（恒为 0），而它做的是 `szl.Data[size-3]`，也就是 `Data[-3]` ——
// 只要 readSzl 成功，它**必然 panic**。这不是"可能越界"而是"必然越界"，
// 写在这里是为了挡住后人"顺手把型号补全"的那一次改动。
func probeCPUInfo(handler *gos7.TCPClientHandler) (info gos7.S7CpuInfo, ok bool) {
	// 必须 recover：GetCPUInfo 直接切片 szl.Data[172:204]、[138:162]、[2:26]、
	// [104:130]、[36:60]，设备回的 SZL 记录短于 204 字节就是一次越界 panic。
	// panic 会被 driversdk 的 handle recover 兜成一次 RPC 错误 —— 于是
	// "这台设备的标识读不出来"会升级成"整次探测失败"，把一台好设备报成扫不到。
	defer func() {
		if r := recover(); r != nil {
			info, ok = gos7.S7CpuInfo{}, false
			logf("probe: %s 的 CPU 标识解析越界（设备回的 SZL 记录偏短），按未识别处理: %v",
				handler.Address, r)
		}
	}()

	// 注意不能写 `info, err := ...`：那会遮蔽命名返回值 info，函数返回的仍是零值。
	var (
		got gos7.S7CpuInfo
		err error
	)
	got, err = gos7.NewClient(handler).GetCPUInfo()
	if err != nil {
		logDebug("probe: %s 读 CPU 标识失败（设备可能不支持 SZL 0x001C）: %v", handler.Address, err)
		return gos7.S7CpuInfo{}, false
	}
	if strings.TrimSpace(got.ModuleTypeName) == "" && strings.TrimSpace(got.ModuleName) == "" &&
		strings.TrimSpace(got.SerialNumber) == "" {
		// 读通了但三个字段全是空白：与"读失败"对用户是同一件事。
		return gos7.S7CpuInfo{}, false
	}
	return got, true
}

// buildDevice 把一次成功的探测组装成 DiscoveredDevice。
//
// 拆成独立函数（而不是写在 ProbeTCP 里）是为了让"识别成功"那条分支能被直接单测：
// SZL 是设备回的字节流，用假 PLC 造一份形状正确的 0x001C 记录既费劲又容易造假 ——
// 假数据一旦与真设备不符，这条分支反而永远测不到。
func buildDevice(host string, port int, rs rackSlot, info gos7.S7CpuInfo, identified bool) driver.DiscoveredDevice {
	dev := driver.DiscoveredDevice{
		Host: host,
		Port: port,
		// ProtocolJSON 的键名必须与 s7ConfigRaw（config.go）逐一对应，且
		// **rack/slot/connectionType 都必须是字符串**：那三个字段在 s7ConfigRaw 里
		// 声明的是 string，写成数字会被 json.Unmarshal 静默丢弃，
		// ParseS7Config 于是回落到默认的 rack=0/slot=1 —— 设备建出来即连不上，
		// 而表单上显示的机架/槽位看着完全正确，现场极难定位。
		ProtocolJSON: fmt.Sprintf(
			`{"host":%q,"port":%d,"rack":%q,"slot":%q,"connectionType":%q}`,
			host, port, strconv.Itoa(rs.rack), strconv.Itoa(rs.slot),
			strconv.Itoa(ConnectionTypePG)),
	}

	if identified {
		model := strings.TrimSpace(info.ModuleTypeName)
		name := model
		if name == "" {
			name = strings.TrimSpace(info.ModuleName)
		}
		if name == "" {
			name = "S7 设备"
		}
		dev.Name = name
		dev.Model = model
		dev.Extra = append(dev.Extra, driver.Attribute{
			Name: "识别方式", Value: "读 CPU 标识(SZL 0x001C)",
		})
		if mn := strings.TrimSpace(info.ModuleName); mn != "" && mn != name {
			dev.Extra = append(dev.Extra, driver.Attribute{Name: "模块名称", Value: mn})
		}
		if sn := strings.TrimSpace(info.SerialNumber); sn != "" {
			dev.Extra = append(dev.Extra, driver.Attribute{Name: "序列号", Value: sn})
		}
	} else {
		// 有 S7 会话但读不出标识。如实标注，用户才知道为什么型号是空的。
		dev.Name = "S7 设备"
		dev.Extra = append(dev.Extra, driver.Attribute{
			Name:  "识别方式",
			Value: "完成 S7 会话协商；设备未回 CPU 标识（多为此固件不支持 SZL 0x001C）",
		})
	}

	// **Vendor 一律留空**，包括识别成功的情形。
	//
	// DiscoveredDevice.Vendor 的语义是"设备**自述**的厂商"，而 S7 协议与 SZL 0x001C
	// 都不携带厂商名 —— 填 "Siemens" 是拿协议名冒充设备自述。说 S7 的设备也不都是
	// 西门子（VIPA、部分国产 PLC 同样实现 S7 通信），写上就是编造。
	dev.Extra = append(dev.Extra, driver.Attribute{Name: "机架/槽位", Value: rs.String()})
	return dev
}
