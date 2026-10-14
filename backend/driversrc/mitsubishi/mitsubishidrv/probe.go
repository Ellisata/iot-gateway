// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

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
// 本文件实现 MC 的「单目标探测」：给我一个 IP:端口，判断上面是否跑着三菱 PLC。
//
// **判据是「读到一次软元件数据」**，不是「5007 端口开着」。与 FINS / S7 那两侧
// 同一层意思：5007 上跑着串口服务器、SCADA 网关、甚至另一个厂商的私有协议都
// 很常见，而它们不会回一条子头（0xD0）与结束码都自洽的 MC 3E 应答。
//
// **复用驱动自己的传输层**（newTransport + client.Read），而不是另写一套裸帧：
// 探测与采集因此走完全相同的封帧、监视定时器与路由字段（frame.go 的
// mcNetNo/mcPCNo/mcIONo/mcStation），探测得通的设备采集必定也连得上（反之亦然）。
// Modbus 那边另起炉灶是因为它的探测需要驱动读路径上没有的 FC43；MC 这边驱动
// 路径上什么都有，再手写一遍只是多出一份会与采集漂移的实现。
//
// **没有候选参数。** 与 S7 的机架/槽位、FINS 的节点号不同，MC 3E 帧里的
// 网络号/PC 号/I-O 号/站号在本驱动里是**写死的常量**（见 frame.go），既不是
// protocol_json 的字段，也不随目标变化 —— 于是这里没有"逐候选尝试"这一层，
// 一次探测就是一次拨号加一次读取。

// 扫描默认值与上限。
const (
	// defaultScanPort 探测端口，与协议默认端口同为 5007（Q/L 系列内置以太网模块）。
	//
	// 注意它**不是** 502：三菱的 SLMP 用的是自己注册的 5007，而现场也确实有把它
	// 改到别的端口的情况 —— 因此扫描表单里的端口是可覆盖的。
	defaultScanPort = defaultPort

	// defaultScanTimeoutMs 单目标的命令-应答超时基准。
	//
	// **一个旋钮管一件事**：MC 一次探测只有一次"发一帧、等一帧"，这个值就是目标
	// 不可达时每台候选付出的代价，因此扫描一个网段的总耗时基本由它决定。
	// 1500 在局域网里已经非常宽松（往返是毫秒级），调小它可以显著加快扫描。
	defaultScanTimeoutMs = 1500

	// defaultScanBudget 未指定预算时的兜底值，与 DiscoveryTCP().MaxBudgetMs 一致。
	defaultScanBudget = 8 * time.Second
)

// scanForm MC 的扫描选项表单（form-create schema，风格与 main.go 里的协议配置表单一致）。
//
// 只有一项，而且 info 必须说清它是**扫描速度唯一的调节点**：扫描页最常被抱怨的
// 就是慢，而用户不会想到那个数字就是原因。
const scanForm = `{
  "rule": [
    {"type":"inputNumber","field":"timeoutMs","title":"单目标超时(ms)","$required":false,"value":1500,
     "info":"默认 1500。一次探测只有一次「发帧-等应答」，目标不可达时每台就会等满这个时间，因此扫描一个网段的总耗时基本由它决定：调小可显著加快扫描，代价是慢设备可能被漏掉"}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// DiscoveryTCP 返回 Mitsubishi.MC.TCP 的扫描能力声明，供 main.go 填进 Meta.Protocols。
//
// **只有以太网协议声明它。** 串口的 Mitsubishi.MC.Serial 由
// driversdk.Meta.Validate **明文禁止**声明 Discovery（目标模型是 host/port，
// 表达不了 comPort；串口上"总线上有哪些站号"是另一个功能），声明了插件启动即失败。
//
// **逐主机单播**，不用广播：MC 的 UDP 广播发现确实存在（3E 帧的 UDP 变体），
// 但它是**另一条传输形态**（无连接、需要另一套重传与报文组装），而本插件的
// 驱动实现只有 TCP 与串口两种（见 newTransport）。声明一个驱动本身都采不了的
// 传输形态，一键建出来的设备连不上 —— 那比扫不到更糟。
func DiscoveryTCP() *driver.DiscoverySpec {
	return &driver.DiscoverySpec{
		Title: "Mitsubishi MC 扫描",
		Description: "逐主机连接 5007/TCP，发一条 MC 批量读命令（3E 帧，读 D0 一个字），" +
			"收到子头与结束码自洽的应答即认定是三菱 PLC。" +
			"判据是「读到一次软元件数据」而不是「端口开着」——端口上跑着别的服务不会回出 MC 形状的应答。" +
			"设备回结束码错误（设备指定错误、地址范围外等）同样算命中：它**应答了**，只是拒绝了这次读取",
		FormJSON:    json.RawMessage(scanForm),
		DefaultPort: defaultScanPort,
		TargetMode:  driver.TargetModeUnicast,
		// 8s：够一次拨号加一次读取在最坏情况下跑满默认超时。
		// 网关侧的 probe RPC 超时 = 本值 + 固定余量（pluginhost 的 probeRPCGrace）。
		MaxBudgetMs: 8000,
	}
}

// scanOptions 解析后的扫描选项。
type scanOptions struct {
	timeout time.Duration
}

// scanOptionsRaw 扫描选项的原始形态。键名与 scanForm 的 field 一一对应。
type scanOptionsRaw struct {
	TimeoutMs rawInt `json:"timeoutMs"`
}

// rawInt 容错整数：同时接受 JSON 数字与数字字符串。
//
// form-create 的不同控件产出的 JSON 形态不一致（inputNumber 给数字，某些控件
// 给字符串），而声明成 int 时一份带字符串的选项会让 json.Unmarshal **整体**报错 ——
// 不是那一个字段失效，而是整次扫描全失败。与 s7drv / finsdrv 的 rawInt 是同一条
// 理由的三个独立实现：本仓库规定**插件之间不得互相 import**（共用一段源码会把
// 两个插件的构建绑在一起），需要就复制那几个纯函数。
//
// 本包内 probe.go 与 pointscan.go **共用这一个**（同属一个包，不必各持一份）：
// 两处的选项都来自同一批 form-create 控件，形态完全一样。
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
		// 认不出就当作没配，走默认值 / 由调用方按缺失处理。这个值错了的表现本来
		// 就只是"扫得慢"或"扫得少"，不该再多一层报错。
		return nil
	}
	r.set = true
	r.val = v
	return nil
}

// parseScanOptions 解析扫描选项。options 为空（长度 0）时全部走默认值。
//
// 与点位扫描的 parsePointScanOptions 取舍相反：那边缺项一律报错（"扫多少"没有
// 合理的默认值），这里缺项静默回默认值 —— "没填超时"就是"用 1500ms"。
func parseScanOptions(options json.RawMessage) (scanOptions, error) {
	opt := scanOptions{timeout: defaultScanTimeoutMs * time.Millisecond}
	if len(options) == 0 {
		return opt, nil
	}

	var raw scanOptionsRaw
	if err := json.Unmarshal(options, &raw); err != nil {
		return opt, fmt.Errorf("解析扫描选项失败: %w", err)
	}
	if raw.TimeoutMs.set && raw.TimeoutMs.val > 0 {
		opt.timeout = time.Duration(raw.TimeoutMs.val) * time.Millisecond
	}
	return opt, nil
}

// ProbeTCP 探测单个 MC/TCP 目标。
//
// 返回 (nil, nil) 表示"这个目标上没有 MC 设备" —— **"没扫到"不是错误**：
// 端口不通、是别的服务、或者设备根本不在，都是扫描的正常结果。把这类情况报成
// error 会让一次 /24 扫描产生两百多条错误噪声。
//
// error 只用于**选项本身有问题**（选项 JSON 解析不出来），那是用户的输入错误，
// 值得原样报出来让前端显示。
//
// 一定在 budget 内返回：本次读取的超时从剩余预算里反推（见下面 wait 的计算），
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
	if target.Broadcast {
		// 防御性分支：本协议的 Discovery 声明的是 unicast，引擎不会给我们广播目标。
		// 真收到（有人改了 TargetMode、或引擎那边出了岔子）就什么都不做 ——
		// 朝一个子网广播地址连 TCP，等于同时对整个网段发起连接，而我们对
		// 裸 TCP 应答方毫无校验手段。
		logDebug("probe: 跳过广播目标 %s（MC 的扫描是逐主机单播）", host)
		return nil, nil
	}
	port := target.Port
	if port <= 0 {
		port = defaultScanPort
	}

	// 本次读取分到的预算 = min(用户填的超时, 剩余预算)。不这么钳，一个填大了的
	// 超时值就能把"prober 一定在 Budget 内返回"这条承诺变成一个摆设。
	wait := opt.timeout
	if remaining := time.Until(deadline); remaining < wait {
		wait = remaining
	}
	if wait <= 0 {
		return nil, nil
	}

	client, err := newTransport(scanConfig(host, port, wait))
	if err != nil {
		if isDialErr(err) {
			// 链路层就没通。与 S7 / FINS 同一条取舍：协议层的候选参数（这里是
			// "没有"）与可达性无关，而一个 /24 里绝大多数地址都走这条分支。
			logDebug("probe: %s:%d 不可达: %v", host, port, err)
			return nil, nil
		}
		logDebug("probe: %s:%d 连接失败: %v", host, port, err)
		return nil, nil
	}

	readable, alive := probeMCDevice(client)
	client.Close()

	if !alive {
		logDebug("probe: %s:%d 无有效应答", host, port)
		return nil, nil
	}

	logDebug("probe: %s:%d 命中（读到数据=%v）", host, port, readable)
	return []driver.DiscoveredDevice{buildMCDevice(host, port, readable)}, nil
}

// scanConfig 组装一次探测用的配置。
//
// Timeout 是**本次探测分到的那一份预算**，不是用户的 timeoutMs —— 预算分摊的
// 结果才是这个探测真正允许花的时间。TimeoutMS 一并写上是为了让 newMCTCPClient
// 的兜底分支（`timeout <= 0` 时取 5s）不生效：两个字段指向同一个值，读哪个都对。
func scanConfig(host string, port int, wait time.Duration) *MCConfig {
	cfg := DefaultMCConfig()
	cfg.Transport = TransportTCP
	cfg.Host = host
	cfg.Port = port
	cfg.TimeoutMS = int(wait.Milliseconds())
	cfg.Timeout = wait
	return cfg
}

// probeMCDevice 判断这个连接后面有没有 MC 设备，返回 (读到了数据, 是设备)。
//
// 三种结果分得很清楚：
//
//	( true,  true) 读到了一次软元件数据   —— 确定无疑的 PLC
//	(false,  true) 设备回了结束码错误     —— 也是设备：它**应答了**，
//	                                         只是拒绝了这次读取（设备指定错误、
//	                                         地址范围外、PLC 忙…）
//	(false, false) 超时 / 连接被拒 / 帧非法 —— 不是设备
//
// 中间那一行容易被漏掉：把"设备明确拒绝"当成"没扫到"，会让一台真实存在的 PLC
// 因为一句不认识的结束码而消失，而用户在扫描页上完全看不出发生过什么。
// 这与 finsdrv "结束码错误照样报出来"、modbusdrv "设备不支持读设备标识照样报出来"
// 是同一条取舍。
//
// **读 D0 一个字**而不是别的地址：D 区是每个型号都有的数据寄存器，且 0 号必然
// 在范围内 —— 读一个可能不存在的地址会把"命中"变成"取决于型号"。
func probeMCDevice(client mcTransport) (readable, alive bool) {
	dev, ok := lookupDevice("D")
	if !ok {
		// 设备表是包内常量，走不到这里；写成守卫而不是 panic，是因为
		// 探测路径上的 panic 只会被 handle 的 recover 咽成一次 RPC 错误。
		return false, false
	}
	_, err := client.Read(dev, 0, 1, false)
	switch {
	case err == nil:
		return true, true
	case IsEndCodeError(err):
		logDebug("probe: 设备回了结束码错误，仍视为 MC 设备: %v", err)
		return false, true
	default:
		return false, false
	}
}

// buildMCDevice 把一次成功的探测组装成 DiscoveredDevice。
//
// 拆成独立函数（而不是写在 ProbeTCP 里）是为了让"识别成功"那条分支能被直接单测：
// 真应答是设备回的字节流，用假 PLC 造一份形状正确的记录既费劲又容易造假 ——
// 假数据一旦与真设备不符，这条分支反而永远测不到。与 s7drv / finsdrv 同一条取舍。
func buildMCDevice(host string, port int, readable bool) driver.DiscoveredDevice {
	// ProtocolJSON 的键名必须与 mcConfigRaw（config.go）逐一对应，且 **port 必须是
	// 数字**：那个字段在 mcConfigRaw 里声明的是 int，写成字符串会被 json.Unmarshal
	// 静默丢弃、ParseMCConfig 回落到默认端口 5007 —— 用户改过端口的设备，
	// 扫描结果看着正常，一键建出来的设备却连不上。
	protocolJSON := fmt.Sprintf(`{"host":%q,"port":%d}`, host, port)

	dev := driver.DiscoveredDevice{
		Host:         host,
		Port:         port,
		ProtocolJSON: protocolJSON,
		// UnitID 留 0：MC 3E 帧里的站号在本驱动里是写死的常量 0x00（frame.go），
		// 既不是 protocol_json 的字段，也不参与连接判定。填一个驱动并不使用的
		// "站号"会让扫描结果与设备表单上的字段对不上。
	}
	// **Vendor/Model/Version 一律留空。**
	//
	// MC 没有任何设备标识读取手段：本插件的驱动只实现了批量读（0x0401），
	// 仓库里也没有第二条可依据的 SLMP 命令实现（型号读取那类命令的码与应答
	// 布局无处可对）。填 "Mitsubishi" 是拿协议名冒充设备自述 —— 而且 SLMP 并非
	// 只有三菱在用（部分国产 PLC 与网关同样实现它）。与 s7drv / finsdrv 同一条取舍。
	//
	// 这里**刻意不猜着补一条读型号的命令**：写错的表现是返回一串乱码或一份空
	// 信息，而"设备不支持"这个理由看起来完全说得通，最难查。
	dev.Name = "MC 设备"
	if readable {
		dev.Extra = append(dev.Extra, driver.Attribute{
			Name: "识别方式", Value: "读到 D 区数据(批量读 0x0401)",
		})
	} else {
		// 有 MC 应答但读不出数据（结束码非 0）。如实标注，用户才知道为什么行里
		// 只有地址。这一格同样证明对面是设备，不是"没扫到"。
		dev.Extra = append(dev.Extra, driver.Attribute{
			Name: "识别方式", Value: "设备回了结束码错误(0x0000 之外的应答码)——应答即证明是 MC 设备",
		})
	}
	dev.Extra = append(dev.Extra,
		driver.Attribute{Name: "传输", Value: "MC 3E 帧 (TCP)"},
	)
	return dev
}

// isDialErr 这次失败发生在 **TCP 拨号**这一层（地址不可达或端口无人监听），
// 而不是 MC 应用层。
//
// 与 s7drv / finsdrv 的 isDialErr 同形（各插件包自持一份，理由同 rawInt）：
// 拨号失败意味着"这个地址上没有可用的 5007 服务"，重试只会把同一个超时再花一遍。
// 本包目前没有候选参数，这条判断因此只用于把日志分成两类 —— 但它仍值得留着，
// 因为将来若给 MC 加上"站号候选"之类的参数，这条分支就是那个必须存在的出口。
func isDialErr(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}
