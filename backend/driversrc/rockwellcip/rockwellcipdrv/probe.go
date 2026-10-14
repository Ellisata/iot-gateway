// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"iot-gateway/driver"
	cipcore "iot-gateway/driversrc/rockwellcip/cipcore"
)

// ==================== 局域网扫描发现 ====================
//
// 本文件实现 Rockwell.CIP 的「设备发现」：用 EtherNet/IP 标准的 ListIdentity
// （封装层命令 0x0063）向 44818/UDP 发一条空载荷请求，设备回一条自述身份 ——
// 厂商、设备类型、产品名、版本、序列号、通信地址。
//
// **帧的构造与解析在本插件自己的 cipcore 子包里，本文件不碰一个字节**：
//
//	import cipcore "iot-gateway/driversrc/rockwellcip/cipcore"
//
// 那份代码与 `driversrc/omroncip/cipcore` **是两份互不依赖的副本**，而不是共享库。
// 这件事值得说明白，因为共用看着更省事：帧格式确实厂商无关，欧姆龙那边当初也是按
// "Rockwell 将来直接复用"写的。但**共用会让两个插件的构建互相牵连**，而插件在本
// 仓库里的定位是能独立改名、独立删除、独立拆去别的仓库的产物。
//
// 因此各持一份，随之而来的"两份可能漂移"由**同一套独立实现的假应答**兜底：
// 两边的解析器测试都由 testutil/fake/cipidentity.go 驱动（那份是照规范另写一遍的），
// 哪一边写错，哪一边自己的测试就会红，不会被"另一份是对的"掩盖。
// 完整论证见 cipcore/listidentity.go 的文件头。
//
// **与 FINS/S7/Modbus 的形态完全不同**：那三个是逐主机拨号，一个目标一台设备；
// 这里一条广播数据报就能拿回**整个网段**的答案。这正是 TargetMode 存在的理由，
// 也是本协议声明 TargetModeBoth 的原因（见 Discovery 的注释）。
//
// **不建立会话**：发现阶段连 Register Session 都不做 —— ListIdentity 是无状态的
// 封装层命令，不需要会话句柄。这与 probe 契约"不分配会话、不持有跨调用资源"
// 完全一致（见 specs/device-scan.md §3）。

// 扫描默认值与上限。
const (
	// defaultScanTimeoutMs 单播目标等待应答、以及广播目标**等第一个应答**的上限。
	defaultScanTimeoutMs = 1500

	// defaultReplyWindowMs 广播探测的"安静窗口"：收到第一个应答后，再等这么久
	// 没有新的应答就认为收齐了。
	//
	// 为什么需要它：广播之后我们无法预知有几台设备、也无从知道最后一台何时到达，
	// 只能靠"一段时间内没有新的"来判定结束。取值权衡 —— 太小会漏掉应答慢的设备
	// （表现是"扫到的比实际少"，而用户无从察觉），太大则每次扫描白等。
	// 500ms 相对局域网 UDP 往返（毫秒级）已经很宽松。
	defaultReplyWindowMs = 500

	// defaultScanBudget 未指定预算时的兜底值，与 Discovery().MaxBudgetMs 一致。
	defaultScanBudget = 5 * time.Second

	// maxScanDevices 单次探测最多上报几台设备。
	//
	// 上限存在的意义不是性能而是**事故半径**：一条广播在大型网络上可能招来
	// 几百条应答，而 pluginwire 写帧持插件进程的写锁 —— 一份巨大的应答会把
	// 该进程上所有协议的应答一起头阻塞。64 台对单个 OT 网段绰绰有余。
	// 触顶时停止接收并记一条日志，不静默截断（见 collectIdentities）。
	maxScanDevices = 64

	// listIdentityBufSize 单条应答的读取缓冲。
	//
	// 一条 Identity 项约 50~90 字节，一帧里即使有几十项也远小于 2KB。
	listIdentityBufSize = 2048
)

// scanForm Rockwell.CIP 的扫描选项表单（form-create schema）。
//
// 只有超时一个旋钮，而且它在这两个目标模式下作用不同 —— info 必须说清楚，
// 否则用户调小它之后会得到"广播扫不到慢设备"这种看不出原因的结果。
const scanForm = `{
  "rule": [
    {"type":"inputNumber","field":"timeoutMs","title":"应答等待(ms)","$required":false,"value":1500,
     "info":"默认 1500。广播探测时是「等第一个应答」与「收齐后的安静窗口」的上限；逐主机探测时就是单台设备的等待上限。调小可显著加快逐主机扫描，代价是慢设备可能被漏掉"}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// Discovery 返回 Rockwell.CIP 的扫描能力声明，供 main.go 填进 Meta.Protocols。
//
// **TargetModeBoth**：既扫各网段的子网广播地址，也扫逐主机地址。这个组合是
// 有意选的，两个方向各自解决一个真实问题：
//
//   - 广播是 ListIdentity 的**标准用法**，一条数据报拿回整个网段，比逐主机
//     单播快两个数量级 —— 少了它，/24 网段要发 254 个数据报、每个都等一次超时；
//   - 单播是**兜底**：受管交换机、ACL、以及部分只应答单播 ListIdentity 的设备，
//     会让广播一台都扫不到。只声明 broadcast 的话，这种网络上的症状是
//     "扫描正常结束但一台设备都没有"，而用户完全无从判断是网络还是配置。
//
// 两条路都开时，同一台设备会被广播与它自己的单播目标**各报一次**，
// 由 devicescan 引擎按 host:port:unitId 去重（见 job.acceptDevice）。
func Discovery() *driver.DiscoverySpec {
	return &driver.DiscoverySpec{
		Title: "Rockwell EtherNet/IP 扫描",
		Description: "用 EtherNet/IP 标准的 ListIdentity（44818/UDP）列出网段里的罗克韦尔设备，" +
			"型号与版本取自设备**自述**的身份信息（不是猜的）。" +
			"广播与逐主机两种目标都扫：广播快（一条数据报覆盖整个网段），逐主机兜底" +
			"（禁广播的受管网络上仍能扫到），重复的设备会自动合并。" +
			"只上报罗克韦尔（厂商号 1）的 EtherNet/IP 设备 —— 别的厂商的设备本插件采不了",
		FormJSON:    json.RawMessage(scanForm),
		DefaultPort: defaultPort,
		TargetMode:  driver.TargetModeBoth,
		// 5s：广播那一轮最多花「等第一个应答 1.5s + 安静窗口 0.5s」，
		// 逐主机那一轮每个目标最多 1.5s（由网关侧的并发度决定总时长）。
		// 网关侧的 probe RPC 超时 = 本值 + 固定余量（pluginhost 的 probeRPCGrace）。
		MaxBudgetMs: 5000,
	}
}

// scanOptions 解析后的扫描选项。
type scanOptions struct {
	timeout     time.Duration
	replyWindow time.Duration
}

// scanOptionsRaw 扫描选项的原始形态。键名与 scanForm 的 field 一一对应。
//
// 超时用 rawInt（而非 int）：form-create 的不同控件产出的 JSON 形态不一致
// （inputNumber 给数字，某些控件给字符串），而声明成 int 时一份带字符串的选项
// 会让 json.Unmarshal 整体报错 —— 不是那一个字段失效，而是整次扫描全失败。
type scanOptionsRaw struct {
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
		// 认不出就当作没配，走默认值。这个值错了的表现本来就只是"扫得慢/扫不到"。
		return nil
	}
	r.set = true
	r.val = v
	return nil
}

// parseScanOptions 解析扫描选项。options 为空（长度 0）时全部走默认值。
func parseScanOptions(options json.RawMessage) (scanOptions, error) {
	opt := scanOptions{
		timeout:     defaultScanTimeoutMs * time.Millisecond,
		replyWindow: defaultReplyWindowMs * time.Millisecond,
	}
	if len(options) == 0 {
		return opt, nil
	}

	var raw scanOptionsRaw
	if err := json.Unmarshal(options, &raw); err != nil {
		return opt, fmt.Errorf("解析扫描选项失败: %w", err)
	}
	if raw.TimeoutMs.set && raw.TimeoutMs.val > 0 {
		opt.timeout = time.Duration(raw.TimeoutMs.val) * time.Millisecond
		// 安静窗口跟着超时一起缩放，但**上限仍是默认值**：用户把超时从 1500
		// 调到 3000 时，安静窗口没有理由跟着变成 1s（应答是同时到达的）；
		// 反过来调小超时（想扫快一点）时，安静窗口必须一起小下去，
		// 否则"调小了却一点没变快"。
		win := time.Duration(raw.TimeoutMs.val) * time.Millisecond / 3
		if win > defaultReplyWindowMs*time.Millisecond {
			win = defaultReplyWindowMs * time.Millisecond
		}
		opt.replyWindow = win
	}
	return opt, nil
}

// Probe 探测一个 EtherNet/IP 目标。
//
// 返回 (nil, nil) 表示"这个目标上没有罗克韦尔设备" —— **"没扫到"不是错误**：
// 目标不可达、是别的厂商的 EtherNet/IP 设备、或者压根没设备，都是扫描的正常
// 结果。把这类情况报成 error 会让一次 /24 扫描产生两百多条错误噪声。
//
// error 只用于**探测本身发起不了**（源地址绑不上、选项非法），那是环境或输入
// 的问题，值得原样报出来让前端显示。
//
// 一定在 budget 内返回：两种模式下的等待时长都从剩余预算里反推，
// 预算耗尽就带上已收到的设备正常返回，绝不返回 error —— 超预算会撞网关侧
// RPC 超时并累积插件卡死计数，连续几次会把整个插件进程连同它下面所有协议、
// 所有设备一起重启。
func Probe(target driver.ScanTarget, options json.RawMessage, budget time.Duration) ([]driver.DiscoveredDevice, error) {
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
	// 目标永远是字面 IP（由网关从网段展开而来）。域名在这里没有意义，
	// 而且解析域名会引入一次可能阻塞的 DNS 查询 —— 那会突破预算。
	dstIP := net.ParseIP(host)
	if dstIP == nil || dstIP.To4() == nil {
		logDebug("probe: 目标 %q 不是 IPv4 字面地址，跳过", host)
		return nil, nil
	}
	port := target.Port
	if port <= 0 {
		port = defaultPort
	}

	// 源地址绑定：**广播目标必须绑**。
	//
	// 多网卡机器（办公网 + OT 网 + docker0 + VPN 是工业现场的常态）上，
	// 不绑源地址的 UDP 只会从默认路由那一张网卡出去 —— 于是"扫 OT 网段"实际
	// 发到了办公网上，一台设备也发现不了。这条取舍与 discovery/announcer.go
	// 的"逐网卡定向广播"、以及 driver.ScanTarget.LocalIP 的注释完全一致。
	//
	// 用**未连接**的套接字（ListenUDP + WriteToUDP）而不是 DialUDP：
	// 广播探测的应答来自各台设备自己的地址，连接型套接字只收对端（广播地址）
	// 发来的数据报，会把全部应答丢掉。两种模式因此共用这一条路径。
	laddr := localAddrFor(target.LocalIP, dstIP)
	conn, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		return nil, fmt.Errorf("rockwell: 绑定源地址 %v 失败: %w", laddr, err)
	}
	defer conn.Close()

	if _, err := conn.WriteToUDP(cipcore.BuildListIdentity(), &net.UDPAddr{IP: dstIP, Port: port}); err != nil {
		// 写失败 = 目标不可达/网络不可达。**不是错误**：与"这里没有设备"同义，
		// 报成 error 只会让一次 /24 扫描产出两百多条噪声。
		logDebug("probe: 向 %s:%d 发送 ListIdentity 失败: %v", host, port, err)
		return nil, nil
	}

	return collectIdentities(conn, target, opt, deadline), nil
}

// localAddrFor 决定本次探测绑定哪个源地址。
//
// 优先用网关为该目标算出的 LocalIP（多网卡上它就是"这个网段本机的那张网卡"）。
// 拿不到时用 0.0.0.0 交给内核按路由选 —— 单播目标上这是对的（去程走哪张网卡，
// 回程就从哪张回来），广播目标上则可能选错，但那种情况下 LocalIP 一定存在
// （由 discovery 的网段枚举填的），所以只是兜底。
//
// dstIP 参数目前只用于日志：将来的多网段路由判断会用到它，先把签名留出来，
// 免得调用点到时候一起改。
func localAddrFor(localIP string, dstIP net.IP) *net.UDPAddr {
	if localIP != "" {
		if ip := net.ParseIP(strings.TrimSpace(localIP)); ip != nil && ip.To4() != nil {
			return &net.UDPAddr{IP: ip.To4()}
		}
	}
	logDebug("probe: 没有可用的源地址（目标 %v），交给内核按路由选择", dstIP)
	return &net.UDPAddr{}
}

// collectIdentities 收应答并组装设备清单。
//
// 收应答的形状由目标模式决定：
//
//	广播目标  一直收到"安静窗口内没有新应答"为止 —— 一个目标可能产出多台设备
//	单播目标  收到第一条就停 —— 一个单播地址上只可能有一台设备，
//	          再等一个安静窗口只是白花时间（而 /24 里绝大多数目标都不应答，
//	          每个都多等 500ms 会让整次扫描慢一倍）
func collectIdentities(conn *net.UDPConn, target driver.ScanTarget, opt scanOptions, deadline time.Time) []driver.DiscoveredDevice {
	var (
		devices    []driver.DiscoveredDevice
		seen       = make(map[string]bool)
		filtered   int
		buf        = make([]byte, listIdentityBufSize)
		firstRound = true
	)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		wait := opt.timeout
		if !firstRound {
			wait = opt.replyWindow
		}
		if wait > remaining {
			wait = remaining
		}
		if err := conn.SetReadDeadline(time.Now().Add(wait)); err != nil {
			break
		}

		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			// 超时 = 收齐了。其它读取错误（套接字被关等）同样按收齐处理：
			// 已经有结果了，把它丢掉对用户没有任何好处。
			break
		}
		firstRound = false

		ids, err := cipcore.ParseListIdentity(buf[:n])
		if err != nil {
			// 不是 ListIdentity 应答（端口上跑着别的服务、或帧被截断）。
			// **不是错误**：这个目标上没有我们的设备。
			logDebug("probe: %s 的应答不是合法的 ListIdentity: %v", from, err)
			continue
		}

		for _, id := range ids {
			if id.VendorID != cipcore.VendorAllenBradley {
				filtered++
				continue
			}
			dev, ok := buildDevice(id, from)
			if !ok {
				continue
			}
			key := dev.Host + ":" + strconv.Itoa(dev.Port)
			if seen[key] {
				continue
			}
			if len(devices) >= maxScanDevices {
				// 不静默截断：运维需要知道"扫到的比实际少"这件事
				//（与 device-scan.md §9 那条"这条提示不能省"同一条要求）。
				logf("probe: %s 一个目标上报的设备数达到上限 %d，其余设备已忽略",
					target.Host, maxScanDevices)
				return devices
			}
			seen[key] = true
			devices = append(devices, dev)
		}

		if !target.Broadcast {
			break // 单播：一台设备，收到即止
		}
	}

	if filtered > 0 {
		// 只记日志不上报：这些设备对**本插件**不可用，但对用户不是"错误"，
		// 报进 errors 明细只会让人以为扫描出了问题。
		logDebug("probe: %s 上有 %d 台非罗克韦尔（厂商号非 %d）的 EtherNet/IP 设备，已跳过",
			target.Host, filtered, cipcore.VendorAllenBradley)
	}
	return devices
}

// buildDevice 把一条设备自述的 Identity 组装成 DiscoveredDevice。
//
// 返回 false 表示这条 Identity 里没有可用的地址（设备自述 0.0.0.0，而数据报
// 源地址也不是 IPv4）—— 没有地址就建不出设备。
//
// 拆成独立函数（而不是写在 collectIdentities 里）是为了让"身份 → 设备"这条
// 映射能被直接单测：它的输入是设备的字节流，用假 PLC 造一份形状正确的
// ListIdentity 应答既费劲又容易造假，假数据一旦与真设备不符就永远测不到。
func buildDevice(id cipcore.Identity, from *net.UDPAddr) (driver.DiscoveredDevice, bool) {
	// 地址优先取设备**自述**的通信地址，它为空时回落到数据报的源地址。
	//
	// 为什么不是反过来：一次广播里所有应答都来自各台设备自己的 IP，两者理应
	// 一致；不一致时自述值才是设备认为自己在的地址（多网口的 PLC 会自述它
	// 面向本网段的那个口）。但部分设备会填 0.0.0.0，那时只能靠源地址。
	host := id.IP
	if host == "" && from != nil && from.IP != nil {
		if ip4 := from.IP.To4(); ip4 != nil && !ip4.Equal(net.IPv4zero) {
			host = ip4.String()
		}
	}
	if host == "" {
		return driver.DiscoveredDevice{}, false
	}
	port := id.Port
	if port <= 0 {
		port = defaultPort
	}

	dev := driver.DiscoveredDevice{
		Host: host,
		Port: port,
		// ProtocolJSON 的键名必须与 rockwellConfigRaw（config.go）逐一对应：
		// host 是 string、port 是 number。写反了这个设备连不上，而两边都不报错。
		ProtocolJSON: fmt.Sprintf(`{"host":%q,"port":%d}`, host, port),
	}

	name := strings.TrimSpace(id.ProductName)
	if name == "" {
		// 设备没自报产品名（少数精简实现）。展示名给个中性值，
		// 不能编造型号，也不能因此不报这台设备。
		name = "EtherNet/IP 设备"
	}
	dev.Name = name
	dev.Model = strings.TrimSpace(id.ProductName)
	dev.Version = fmt.Sprintf("%d.%d", id.RevisionMajor, id.RevisionMinor)

	// **Vendor 在这里可以填**，而且必须填：与 FINS/S7 不同，ListIdentity 的
	// 厂商字段是**设备自报的**（ODVA 分配的厂商号），"Allen-Bradley" 不是拿协议名
	// 冒充设备自述 —— 我们本来就是按"厂商号 = 1"筛出它的。
	dev.Vendor = "Allen-Bradley"

	dev.Extra = append(dev.Extra,
		driver.Attribute{Name: "识别方式", Value: "EtherNet/IP ListIdentity(0x0063)"},
		driver.Attribute{Name: "厂商号", Value: strconv.Itoa(int(id.VendorID))},
		// 设备类型的**数字**照原样给出，不翻译成名字：ODVA 的设备类型表很长，
		// 而且同一个数字在不同代次的产品上叫法不一，译错比不译更容易误导。
		driver.Attribute{Name: "设备类型", Value: fmt.Sprintf("0x%04X", id.DeviceType)},
		driver.Attribute{Name: "产品代码", Value: strconv.Itoa(int(id.ProductCode))},
	)
	if id.SerialNumber != 0 {
		dev.Extra = append(dev.Extra,
			driver.Attribute{Name: "序列号", Value: fmt.Sprintf("0x%08X", id.SerialNumber)})
	}
	if id.Status != 0 {
		// 设备自报非零状态（多为处于故障/需维护）。如实标出来，用户才知道
		// 这台还没投用的设备为什么连着却没数据。
		dev.Extra = append(dev.Extra,
			driver.Attribute{Name: "状态字", Value: fmt.Sprintf("0x%04X", id.Status)})
	}
	return dev, true
}
