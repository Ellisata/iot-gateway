// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"iot-gateway/driver"
)

// ==================== 局域网扫描发现 ====================
//
// 本文件实现 FINS 的「单目标探测」：给我一个 IP:端口，判断上面是否跑着欧姆龙 PLC。
//
// **判据是「读到一次内存区数据」**，不是「9600 端口开着」。与 S7 那条
// （完成 S7 会话协商）同一层意思：9600 上跑着串口服务器、SCADA 网关、
// 甚至另一个厂商的私有协议都很常见，而它们不会回一条命令码自洽的 FINS 应答。
//
// **复用驱动自己的传输层**（newTransport + client.Read），而不是另写一套裸帧：
// 探测与采集因此走完全相同的地址解析、节点号推导与封帧代码，探测得通的设备
// 采集必定也连得上（反之亦然）。Modbus 那边另起炉灶是因为它的探测需要
// 驱动读路径上没有的 FC43；FINS 这边驱动路径上什么都有，再手写一遍只是
// 多出一份会与采集漂移的实现。
//
// **关于节点号（DA1/SA1）**：两者的默认值都与采集时逐字相同 —— DA1 由
// resolveDstNode(host) 取 IP 末段，SA1 由 resolveSrcNode 取本机到目标的源 IP 末段
// （真机核实 SA1=0 会被设备拒绝，见 transport.go）。这条一致性是有意的：
// 扫描页只负责回答"这里有没有 PLC"，而"采不采得到"由同一套推导决定。
// 若某台 PLC 的节点号不是 IP 末段，它需要用户在设备里手工配 dstNode 才能采集，
// 因此扫描表单里的「节点号候选」是可选项而不是必填项。

// 扫描默认值与上限。
const (
	// defaultScanPort 探测端口，与协议默认端口同为 9600。
	defaultScanPort = defaultPort

	// defaultScanTimeoutMs 单个候选的命令-应答超时基准。
	//
	// **一个旋钮管一件事**：FINS 一次探测只有一次"发一帧、等一帧"，
	// 这个值就是目标不可达时每台候选付出的代价，因此扫描一个网段的总耗时
	// 基本由它决定。1500 在局域网里已经非常宽松（UDP 往返是毫秒级），
	// 调小它可以显著加快扫描。
	defaultScanTimeoutMs = 1500

	// defaultScanBudget 未指定预算时的兜底值，与 DiscoveryXxx().MaxBudgetMs 一致。
	defaultScanBudget = 8 * time.Second

	// maxScanNodes 单次探测最多试几个节点号候选。
	//
	// 上限存在的意义不是性能而是**事故半径**：每个候选就是一次完整的超时等待，
	// 一份手滑的选项（比如把 1..254 全列上）会把单目标探测拖过预算，而超预算
	// 会撞网关侧的 RPC 超时并累积插件卡死计数 —— 连续几次会把整个插件进程
	// 连同它下面所有协议、所有设备一起重启。
	maxScanNodes = 4
)

// scanForm FINS 的扫描选项表单（form-create schema，风格与 main.go 里的协议配置表单一致）。
//
// 两条 info 都是现场真会踩的：
//   - 节点号那一栏必须说清"留空就够了"，否则用户会以为必填，从而把节点号猜错、
//     扫出一堆空结果；
//   - 超时那一栏要说明它同时决定"扫描有多慢"与"慢设备会不会被漏掉"——
//     扫描页最常被抱怨的就是慢，而这个值是唯一的调节点。
const scanForm = `{
  "rule": [
    {"type":"input","field":"nodes","title":"节点号候选","$required":false,
     "info":"逗号分隔的 FINS 节点号（DA1，1-254），最多 4 个，按顺序尝试。留空即按「设备IP 末段」推导——与采集时的默认行为一致，绝大多数现场留空即可。只有 PLC 的节点号被改成了非 IP 末段时才需要填，命中的那一个会写进新建设备的配置里"},
    {"type":"inputNumber","field":"timeoutMs","title":"单目标超时(ms)","$required":false,"value":1500,
     "info":"默认 1500。一次探测只有一次「发帧-等应答」，目标不可达时每台就会等满这个时间，因此扫描一个网段的总耗时基本由它决定：调小可显著加快扫描，代价是慢设备可能被漏掉"}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// DiscoveryUDP 返回 Omron.FINS.UDP 的扫描能力声明，供 main.go 填进 Meta.Protocols。
//
// **逐主机单播**，不用广播。FINS 确实有一条广播式的节点查询，但它是**另一条
// 命令**（不是这里用的内存区读取），其命令码与应答布局在本仓库里没有第二处
// 依据可对 —— 而一条写错的广播探测的表现是"扫不到设备"，正是最难查的一类。
// 单播则复用采集路径上已经真机核实过的 0x0101，代价是 /24 要发 254 个数据报。
func DiscoveryUDP() *driver.DiscoverySpec {
	return &driver.DiscoverySpec{
		Title: "Omron FINS/UDP 扫描",
		Description: "逐主机向 9600/UDP 发送一条内存区读取命令（DM 字 0），" +
			"收到命令码自洽的 FINS 应答即认定是欧姆龙 PLC。" +
			"UDP 无连接，探测与采集一样不做握手；端口上跑着别的服务不会应答，因此不会误报。" +
			"节点号默认按「设备IP 末段」推导（与采集一致），可改成候选列表逐个尝试",
		FormJSON:    json.RawMessage(scanForm),
		DefaultPort: defaultScanPort,
		TargetMode:  driver.TargetModeUnicast,
		// 8s：够「一个候选跑满默认 1.5s 超时」或「若干个候选先后失败」。
		// 网关侧的 probe RPC 超时 = 本值 + 固定余量（pluginhost 的 probeRPCGrace）。
		MaxBudgetMs: 8000,
	}
}

// DiscoveryTCP 返回 Omron.FINS.TCP 的扫描能力声明。
//
// 与 UDP 的差别只在传输层：FINS/TCP 建连时先做一次连接握手（0x00000000 →
// 0x00000001），握手成功本身已经是很强的证据，但判据仍取"读到一次数据"——
// 握手只证明对面实现了 FINS/TCP 的封帧，不证明它后面挂着一台 PLC。
func DiscoveryTCP() *driver.DiscoverySpec {
	return &driver.DiscoverySpec{
		Title: "Omron FINS/TCP 扫描",
		Description: "逐主机连接 9600/TCP，完成 FINS/TCP 连接握手后再读一次 DM 字 0，" +
			"两者都成功才认定是欧姆龙 PLC（只握手不读会把同样实现了 FINS/TCP 封帧的服务端报成 PLC）。" +
			"节点号默认按「设备IP 末段」推导（与采集一致），可改成候选列表逐个尝试",
		FormJSON:    json.RawMessage(scanForm),
		DefaultPort: defaultScanPort,
		TargetMode:  driver.TargetModeUnicast,
		MaxBudgetMs: 8000,
	}
}

// scanOptions 解析后的扫描选项。
type scanOptions struct {
	// nodes 显式的节点号候选。为空表示"按 IP 末段推导"，即采集时的默认行为。
	nodes []byte
	// explicit 节点号是用户填的（而不是推导出来的）。
	//
	// 它决定命中的那个节点号要不要**写回 ProtocolJSON**：用户给的是候选列表，
	// 命中哪一个是探测才回答得出来的事实，不写回去，一键建出来的设备就会退回
	// 推导值、连不上。反之推导值本来就与采集时一致，写进配置只会把一个能跟随
	// IP 变化自动更新的默认值冻死。
	explicit bool
	timeout  time.Duration
}

// scanOptionsRaw 扫描选项的原始形态。键名与 scanForm 的 field 一一对应。
//
// 超时用 rawInt（而非 int）：form-create 的不同控件产出的 JSON 形态不一致
// （inputNumber 给数字，某些控件给字符串），而声明成 int 时一份带字符串的选项
// 会让 json.Unmarshal 整体报错 —— 不是那一个字段失效，而是整次扫描全失败。
// 与 s7drv/probe.go 的 rawInt 是同一条理由的两个独立实现（各驱动包自持一份，
// 不为一个小工具类型引入包依赖）。
type scanOptionsRaw struct {
	Nodes     string `json:"nodes"`
	TimeoutMs rawInt `json:"timeoutMs"`
}

// rawInt 容错整数：同时接受 JSON 数字与数字字符串。
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
// 合理的默认值），这里缺项静默回默认值 —— "没填节点号"就是"按 IP 末段推导"。
func parseScanOptions(options json.RawMessage) (scanOptions, error) {
	opt := scanOptions{timeout: defaultScanTimeoutMs * time.Millisecond}
	if len(options) == 0 {
		return opt, nil
	}

	var raw scanOptionsRaw
	if err := json.Unmarshal(options, &raw); err != nil {
		return opt, fmt.Errorf("解析扫描选项失败: %w", err)
	}

	nodes, err := parseScanNodes(raw.Nodes)
	if err != nil {
		return opt, err
	}
	if len(nodes) > 0 {
		opt.nodes = nodes
		opt.explicit = true
	}
	if raw.TimeoutMs.set && raw.TimeoutMs.val > 0 {
		opt.timeout = time.Duration(raw.TimeoutMs.val) * time.Millisecond
	}
	return opt, nil
}

// parseScanNodes 解析节点号候选："10,20,30"。
//
// 空串返回 nil（调用方回落到"按 IP 末段推导"）。非法片段一律**报错而不是静默
// 丢弃**：一个写错的候选若被悄悄忽略，用户看到的是"某台 PLC 扫不到"，
// 而原因指向的是网络，排查方向完全错 —— 与 s7drv.parseRackSlots 同一条取舍。
func parseScanNodes(expr string) ([]byte, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, nil
	}

	seen := make(map[byte]bool)
	out := make([]byte, 0, maxScanNodes)
	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("节点号 %q 不是数字", part)
		}
		// 1..254 之外不是合法 FINS 节点号（0 与 255 是保留值）。
		// **报错而不是钳制**：钳过之后发出去的是另一个节点号，
		// 用户拿到的结果是"这一组试过了但没通"，与事实不符。
		if v < 1 || v > 254 {
			return nil, fmt.Errorf("节点号 %d 超出 [1,254]（%q）", v, part)
		}
		if seen[byte(v)] {
			continue // 重复的候选只试一次，别白花一个超时
		}
		seen[byte(v)] = true
		out = append(out, byte(v))
	}

	if len(out) > maxScanNodes {
		return nil, fmt.Errorf("节点号候选有 %d 个，超过单次最多 %d 个", len(out), maxScanNodes)
	}
	return out, nil
}

// candidates 本次探测要依次尝试的节点号。
//
// 用户没填时只有**一个**候选：resolveDstNode(host)，即采集时用的那个推导值。
// 返回的 explicit 透传 scanOptions.explicit，决定命中后要不要把节点号写回配置。
func (o scanOptions) candidates(host string) ([]byte, bool) {
	if len(o.nodes) > 0 {
		return o.nodes, true
	}
	return []byte{resolveDstNode(host)}, false
}

// ProbeUDP 探测单个 FINS/UDP 目标。
//
// 返回 (nil, nil) 表示"这个目标上没有 FINS 设备" —— **"没扫到"不是错误**：
// 端口不通、是别的服务、或者设备根本不在，都是扫描的正常结果。把这类情况报成
// error 会让一次 /24 扫描产生两百多条错误噪声。
//
// error 只用于**选项本身有问题**（节点号候选非法），那是用户的输入错误，
// 值得原样报出来让前端显示。
//
// 一定在 budget 内返回：每个候选的超时都从剩余预算里反推（见 probeFINS），
// 预算耗尽就带上已有结论（此处只有"没找到"）正常返回，绝不返回 error ——
// 超预算会撞网关侧 RPC 超时并累积插件卡死计数，连续几次会把整个插件进程连同
// 它下面所有协议、所有设备一起重启。
func ProbeUDP(target driver.ScanTarget, options json.RawMessage, budget time.Duration) ([]driver.DiscoveredDevice, error) {
	return probeFINS(target, options, budget, TransportUDP)
}

// ProbeTCP 探测单个 FINS/TCP 目标。
func ProbeTCP(target driver.ScanTarget, options json.RawMessage, budget time.Duration) ([]driver.DiscoveredDevice, error) {
	return probeFINS(target, options, budget, TransportTCP)
}

// probeFINS 两种以太网传输共用的探测实现。
func probeFINS(target driver.ScanTarget, options json.RawMessage, budget time.Duration, transport string) ([]driver.DiscoveredDevice, error) {
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
	if target.Broadcast {
		// 防御性分支：本协议的 Discovery 声明的是 unicast，引擎不会给我们广播目标。
		// 真收到（有人改了 TargetMode、或引擎那边出了岔子）就什么都不做 ——
		// 朝一个子网广播地址发一帧 DA1=0 的读取命令，等于让整个网段的设备
		// 同时回一条应答，而我们对裸数据报的应答方毫无校验手段。
		logDebug("probe: 跳过广播目标 %s（FINS 的扫描是逐主机单播）", host)
		return nil, nil
	}
	port := target.Port
	if port <= 0 {
		port = defaultScanPort
	}

	nodes, explicit := opt.candidates(host)
	for _, node := range nodes {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break // 预算耗尽：带上"没找到"正常返回
		}
		// 每个候选最多只花剩余预算的 1/候选数：一次探测每个候选要完整地
		// "发一帧、等一帧"，不这么分，最后一个候选就能带着整份剩余预算跑满
		// 一次超时，把"prober 一定在 Budget 内返回"这条承诺变成一个
		// 用户填大超时值就能打破的摆设。
		wait := opt.timeout
		if share := remaining / time.Duration(len(nodes)); share < wait {
			wait = share
		}
		if wait <= 0 {
			break
		}

		cfg := scanConfig(host, port, node, transport, wait)
		client, err := newTransport(cfg)
		if err != nil {
			if isDialErr(err) {
				// 链路层就没通：可达性与节点号无关，换一个候选只会把同一个
				// 超时再花一遍 —— 而一个 /24 里绝大多数地址都走这条分支，
				// 不跳出就会让整次扫描的耗时乘以候选数。
				logDebug("probe: %s:%d 不可达: %v", host, port, err)
				return nil, nil
			}
			logDebug("probe: %s:%d 连接失败: %v", host, port, err)
			continue
		}

		readable, alive := probeFINSDevice(client)

		// 型号只有"读到过数据"时才去问：结束码错误的那条分支里，设备已经
		// 明确拒绝了这次读取，紧接着再发一条更冷门的命令只会多花一个超时。
		//
		// **必须在 Close 之前读**：客户端一关就 connected=false，
		// 之后的每条命令都直接返回"未连接" —— 症状是型号永远为空，
		// 而"设备不支持 0x0501"这个理由看起来完全说得通，最难查。
		model := ""
		if readable {
			model, _ = readControllerModel(client)
		}
		client.Close()

		if !alive {
			// 这一轮没应答。节点号不对是最可能的原因（尤其用户给了候选列表时），
			// 因此继续试下一个候选而不是就此放弃。
			logDebug("probe: %s:%d node=%d 无有效应答", host, port, node)
			continue
		}

		logDebug("probe: %s:%d 命中（node=%d, 型号=%q）", host, port, node, model)
		return []driver.DiscoveredDevice{
			buildFINSDevice(host, port, node, explicit, transport, model),
		}, nil
	}

	return nil, nil
}

// scanConfig 组装一次探测用的配置。
//
// Timeout 是**本次候选分到的那一份预算**，不是用户的 timeoutMs ——
// 预算分摊的结果才是这个探测真正允许花的时间。
func scanConfig(host string, port int, node byte, transport string, wait time.Duration) *FINSConfig {
	cfg := DefaultFINSConfig()
	cfg.Transport = transport
	cfg.Host = host
	cfg.Port = port
	cfg.DstNode = node
	cfg.TimeoutMS = int(wait.Milliseconds())
	cfg.Timeout = wait
	// SrcNode 留 0：交给 resolveSrcNode 按"本机到目标的源 IP 末段"推导，
	// 与采集时逐字相同。填死一个值会让多网卡网关上探测与采集的 SA1 分叉。
	cfg.SrcNode = 0
	return cfg
}

// probeFINSDevice 判断这个连接后面有没有 FINS 设备，返回 (读到了数据, 是设备)。
//
// 三种结果分得很清楚：
//
//	( true,  true) 读到了一次 DM 区数据       —— 确定无疑的 PLC
//	(false,  true) 设备回了结束码错误         —— 也是设备：它**应答了**，
//	                                            只是拒绝了这次读取（区域不存在、
//	                                            地址越界、单元号不对…）
//	(false, false) 超时 / 连接被拒 / 帧非法   —— 不是设备
//
// 中间那一行容易被漏掉：把"设备明确拒绝"当成"没扫到"，会让一台真实存在的 PLC
// 因为一句不认识的结束码而消失，而用户在扫描页上完全看不出发生过什么。
// 这与 modbusdrv 里"设备不支持读设备标识照样报出来"是同一条取舍。
func probeFINSDevice(client finsTransport) (readable, alive bool) {
	_, err := client.Read(AreaDM, 0, 1)
	switch {
	case err == nil:
		return true, true
	case IsEndCodeError(err):
		logDebug("probe: 设备回了结束码错误，仍视为 FINS 设备: %v", err)
		return false, true
	default:
		return false, false
	}
}

// readControllerModel 尽力读取控制器型号（0x0501 控制器数据读取）。
//
// 读不出来**不是失败**：老型号、单元号不对、或设备不支持这条命令都会失败。
// 而"能读到一次内存区数据"这件事本身已经证明了它是 FINS 设备，型号只是锦上添花 ——
// 与 s7drv 里"读不出 CPU 标识照样报出来"完全一致。
//
// **布局依据**：CONTROLLER DATA READ 的响应载荷前 20 字节是控制器型号（ASCII，
// 空格或 NUL 补齐），例如 "CJ2M-CPU31"。这里**只解析这 20 字节**，版本等后续字段
// 一律不取 —— 那些字段在各系列（CS/CJ 与 NJ/NX）上的偏移并不一致，而解析错一个
// 偏移不会报任何错，只会给用户一个看着像型号的乱码。
//
// 型号必须是**可打印 ASCII** 才被采纳：这一条同时兜住了"命令码或布局与设备实际
// 不符"的情形 —— 那时拿到的是一串二进制，宁可留空也不要把乱码当型号展示。
func readControllerModel(client finsTransport) (string, bool) {
	cmd, ok := client.(finsCommander)
	if !ok {
		// 串口与 Host Link over TCP 没有实现这条命令（见 finsCommander 的说明）。
		// 探测不走它们，这里只是把"没有"如实表达出来。
		return "", false
	}

	payload, err := cmd.Command(cmdControllerDataRead, nil)
	if err != nil {
		logDebug("probe: 读控制器数据失败（设备可能不支持 0x0501）: %v", err)
		return "", false
	}
	if len(payload) < controllerModelLen {
		logDebug("probe: 控制器数据过短（%d 字节，型号字段需 %d 字节）", len(payload), controllerModelLen)
		return "", false
	}

	model := strings.TrimRight(string(payload[:controllerModelLen]), "\x00 ")
	if model == "" {
		// 读通了但型号字段是空的：与"读失败"对用户是同一件事。
		return "", false
	}
	for _, r := range model {
		if r < 0x20 || r > 0x7E {
			logDebug("probe: 控制器数据里的型号不是可打印 ASCII，按未识别处理")
			return "", false
		}
	}
	return model, true
}

// buildFINSDevice 把一次成功的探测组装成 DiscoveredDevice。
//
// 拆成独立函数（而不是写在 probeFINS 里）是为了让"识别成功"那条分支能被直接单测：
// 0x0501 的响应是设备回的字节流，用假 PLC 造一份形状正确的记录既费劲又容易造假 ——
// 假数据一旦与真设备不符，这条分支反而永远测不到。与 s7drv.buildDevice 同一条取舍。
func buildFINSDevice(host string, port int, node byte, explicit bool, transport, model string) driver.DiscoveredDevice {
	// ProtocolJSON 的键名必须与 finsConfigRaw（config.go）逐一对应，且
	// **dstNode 必须是字符串**：那个字段在 finsConfigRaw 里声明的是 string，
	// 写成数字会被 json.Unmarshal 静默丢弃，ParseFINSConfig 于是回落到
	// "按 IP 末段推导" —— 用户填的候选白填了，而表单上看着完全正常。
	//
	// 只有在用户**显式给了候选列表**时才写回 dstNode：那是探测才回答得出来的
	// 事实（"这一个 PLC 的节点号不是 IP 末段"）。推导值不写回 —— 写进去等于把
	// 一个本来能跟着 IP 变化自动更新的默认值冻死，将来给设备改个 IP 就静默失联。
	protocolJSON := fmt.Sprintf(`{"host":%q,"port":%d}`, host, port)
	if explicit {
		protocolJSON = fmt.Sprintf(`{"host":%q,"port":%d,"dstNode":%q}`,
			host, port, strconv.Itoa(int(node)))
	}

	dev := driver.DiscoveredDevice{
		Host: host,
		Port: port,
		// UnitID 用 FINS 节点号：它就是这个协议里的"站地址"，也是用户在设备
		// 配置里要填的 dstNode。填进去让扫描结果与设备表单上的字段对得上。
		UnitID:       int(node),
		ProtocolJSON: protocolJSON,
	}
	// **Vendor 一律留空。**
	//
	// DiscoveredDevice.Vendor 的语义是"设备**自述**的厂商"，而 FINS 的应答里
	// 没有任何厂商字段，型号串（"CJ2M-CPU31"）也只是控制器型号而不是厂商名。
	// 填 "Omron" 是拿协议名冒充设备自述 —— 而且 FINS 并非只有欧姆龙在用
	// （部分网关与国产 PLC 同样实现了它）。与 s7drv.buildDevice 同一条取舍。
	label := strings.TrimSpace(model)
	if label == "" {
		label = "FINS 设备"
	}
	dev.Name = label
	dev.Model = strings.TrimSpace(model)

	if dev.Model != "" {
		dev.Extra = append(dev.Extra, driver.Attribute{
			Name: "识别方式", Value: "读控制器数据(0x0501)",
		})
	} else {
		// 有 FINS 会话但读不出型号。如实标注，用户才知道为什么型号是空的。
		dev.Extra = append(dev.Extra, driver.Attribute{
			Name:  "识别方式",
			Value: "读到 DM 区数据(0x0101)；设备未回控制器数据（多为该型号不支持 0x0501）",
		})
	}
	dev.Extra = append(dev.Extra,
		driver.Attribute{Name: "传输", Value: transportLabel(transport)},
		driver.Attribute{Name: "节点号", Value: strconv.Itoa(int(node))},
	)
	return dev
}

// transportLabel 传输层的展示名。
func transportLabel(transport string) string {
	switch transport {
	case TransportUDP:
		return "FINS/UDP"
	case TransportTCP:
		return "FINS/TCP"
	case TransportSerial:
		return "Host Link 串口"
	case TransportHostLinkTCP:
		return "Host Link over TCP"
	default:
		return transport
	}
}

// isDialErr 这次失败发生在 **TCP 拨号**这一层（地址不可达或端口无人监听），
// 而不是 FINS 应用层。
//
// 与 s7drv.isDialErr 同形，理由也一样：拨号失败意味着"这个地址上没有可用的
// 9600 服务"，换一个节点号候选只会把同一个超时再花一遍 —— 而一个 /24 里
// 绝大多数地址都走这条分支。协议层失败（连上了但没答对）才值得换候选。
//
// UDP 没有拨号阶段，因此这条判断对它天然为 false，无需额外分支。
func isDialErr(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// finsCommander 能发送任意 FINS 命令的传输层。
//
// **刻意做成可选的第二接口，而不是塞进 finsTransport**：只有 UDP 与 TCP 两种
// 以太网传输实现了它，而它们正是扫描用得上的两种。串口的 Host Link 是另一套
// 封帧（`@<单元号>…` 的 ASCII 帧），把 Command 加进主接口，就等于逼着去实现
// 一份没有调用方、也无法在本仓库验证的 Host Link 命令封帧 —— 那正是本仓库
// 一直在挡的"猜着写"。探到不支持的传输时型号留空，如实降级。
type finsCommander interface {
	Command(cmd uint16, body []byte) ([]byte, error)
}
