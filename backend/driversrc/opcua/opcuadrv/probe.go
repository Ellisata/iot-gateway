// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
	"github.com/gopcua/opcua/uacp"

	"iot-gateway/driver"
)

// ==================== 局域网扫描发现 ====================
//
// 本文件实现 OPC UA 的「单目标探测」：给我一个 IP:端口，判断上面是否跑着
// OPC UA 服务器。
//
// **判据是「读回了一份自洽的端点表（GetEndpoints 应答）」**，不是「4840 端口
// 开着」。与 MC 的"读到一次软元件数据"、FINS 的"读到一次内存区数据"、
// Modbus 的"读到一次设备标识"是同一层意思：4840 上跑着 HTTP 管理页、串口服务器、
// 甚至另一个厂商的私有协议都很常见，而它们不会回一份 OPC UA 二进制编码的
// EndpointDescription 列表。
//
// **GetEndpoints 不需要会话**（它是 OPC UA 的发现服务，规范允许在只建立了
// SecureChannel、尚未 CreateSession 的情况下调用），所以一次探测是：
// TCP 连接 → OpenSecureChannel → GetEndpoints → 关闭。这也意味着探测
// **完全不校验认证**：用户名密码错、证书不受信，只要端点表读得回来就会命中 ——
// 这条已经写进 Discovery().Description，用户必须知道。
//
// **只做 GetEndpoints，不调 FindServers。** FindServers 能列出同一台主机上
// 在本地发现服务器（LDS）注册的其它 OPC UA 服务器（可能是别的端口），那是
// "一个目标产出多台设备"的广播型形态，会带来一串新的失败模式：discoveryUrls
// 里的主机名未必可解析、指向本机的 URL 未必指向被探测的那台、同一台服务器
// 会被列出多次。逐主机单播 + 一台设备，结果可预期、好解释，与 MC/S7/Modbus
// 的形态也一致。
//
// **没有候选参数。** 与 S7 的机架/槽位、FINS 的节点号不同，探测这一个目标
// 只有一种连法（SecurityPolicy=None），一次探测就是一次拨号加一次请求。

// 扫描默认值与上限。
const (
	// defaultScanPort 探测端口，与协议默认端口同为 4840（OPC UA 的注册端口，
	// 也是 LDS 的标准端口）。
	defaultScanPort = 4840

	// defaultScanTimeoutMs 单目标的超时基准。
	//
	// **一个旋钮管一件事**：这一值同时作为 TCP 拨号超时与请求超时，
	// 因此目标不可达或不是 OPC UA 时每台候选付出的代价就是它 ——
	// 扫描一个网段的总耗时基本由它决定。
	//
	// 比 MC 的 1500 大一倍：OPC UA 要多做一次 OpenSecureChannel 往返
	//（HEL/ACK + OPN/OPN 应答），且部分服务器在应用层应答前有明显延迟。
	// 3000 在局域网里仍然宽松，调小可显著加快扫描，代价是慢服务器可能被漏掉。
	defaultScanTimeoutMs = 3000

	// defaultScanBudget 未指定预算时的兜底值，与 Discovery().MaxBudgetMs 一致。
	defaultScanBudget = 8 * time.Second

	// probeMaxBatch 批量取 DataType 之外不涉及；这里只用于探测结果里
	// 「支持的认证方式」的展示上限。取值无实际约束，纯粹防止一个畸形服务器
	// 用几千条 UserTokenPolicy 撑爆一行 Extra。
	probeMaxAuthModes = 8
)

// scanForm OPC UA 的扫描选项表单（form-create schema，风格与 main.go 里的协议配置表单一致）。
//
// 只有一项，且 info 必须说清它是**扫描速度唯一的调节点**：扫描页最常被抱怨的
// 就是慢，而用户不会想到那个数字就是原因。
const scanForm = `{
  "rule": [
    {"type":"inputNumber","field":"timeoutMs","title":"单目标超时(ms)","$required":false,"value":3000,
     "info":"默认 3000。一次探测要完成 TCP 连接与 SecureChannel 握手，再读一次端点表；目标不可达或不是 OPC UA 时会等满这个时间，因此扫描一个网段的总耗时基本由它决定：调小可显著加快扫描，代价是慢服务器可能被漏掉"}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// Discovery 返回 OPC.UA 的扫描能力声明，供 main.go 填进 Meta.Protocols。
//
// **逐主机单播，不用广播**：OPC UA 的局域网发现（LDS-ME 的 mDNS、UDP 组播）
// 是**另一条传输形态**，而本插件的驱动实现只有 opc.tcp 长连接一种。
// 声明一个驱动本身都采不了的形态，一键建出来的设备连不上 —— 那比扫不到更糟。
// （这也是 specs/device-scan.md 里"广播型协议"那一段只点了 EtherNet/IP 与
// FINS/UDP 的原因。）
func Discovery() *driver.DiscoverySpec {
	return &driver.DiscoverySpec{
		Title: "OPC UA 扫描",
		Description: "逐主机连接 4840/TCP，建立 SecureChannel 后读一次端点表（GetEndpoints），" +
			"拿到自洽的应答即认定是 OPC UA 服务器。" +
			"判据是「读回一份端点表」而不是「端口开着」——端口上跑着别的服务不会回出 OPC UA 形状的应答。" +
			"**两点必须知道**：① 本探测**不校验认证**，用户名密码错误、证书不受信的服务器照样会被列出来，" +
			"那要到设备采集时才会暴露；② 服务器只提供加密端点（无 None 安全策略）时仍会列出，" +
			"但网关当前未实现客户端证书，建了也连不上 —— 结果里会明确标注",
		FormJSON:    json.RawMessage(scanForm),
		DefaultPort: defaultScanPort,
		TargetMode:  driver.TargetModeUnicast,
		// 8s：够一次拨号、一次 SecureChannel 握手加一次 GetEndpoints 在最坏
		// 情况下跑满默认超时。网关侧的 probe RPC 超时 = 本值 + 固定余量
		//（pluginhost 的 probeRPCGrace）。
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
// 不是那一个字段失效，而是整次扫描全失败。与 s7drv / finsdrv / mitsubishidrv 的
// rawInt 是同一条理由的四个独立实现：本仓库规定**插件之间不得互相 import**
// （共用一段源码会把两个插件的构建绑在一起），需要就复制那几个纯函数。
//
// 本包内 probe.go 与 pointscan.go 共用这一个（同属一个包，不必各持一份）：
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
// 与点位扫描的选项解析取舍相反：那边缺项一律报错（"从哪儿开始、扫多深"没有
// 合理的默认值），这里缺项静默回默认值 ——"没填超时"就是"用 3000ms"。
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

// 探测结论。三分而不是两分，是本次探测最需要小心的一处。
type probeVerdict int

const (
	// verdictNotOurs 对面不是 OPC UA 服务器（端口不通、是别的服务、或者干脆
	// 不应答）。调用方返回 (nil, nil) ——"没扫到"不是错误。
	verdictNotOurs probeVerdict = iota
	// verdictIsServer 读回了端点表，确定无疑的 OPC UA 服务器。
	verdictIsServer
	// verdictRejectedNone 对面**是** OPC UA（它按协议回了错误），但拒绝了
	// 我们的 None 安全策略。用户选的是"照样列出 + 明确告警"：一台只提供
	// 加密端点的服务器是真实存在的设备，静默不报会让用户从"扫不到"得出
	// "那里没有 OPC UA 服务器"的错误结论。
	verdictRejectedNone
)

// policyRejectStatus 表明"对面是 OPC UA，但它不接受我们的安全参数"的状态码。
//
// 这一组的意义在于**把"设备存在但连不上"与"没有设备"分开**。它们全都是
// 服务端（或对端的 SecureChannel 层）发出来的合法 OPC UA 状态码 ——
// 一个不是 OPC UA 的东西发不出这些值。
var policyRejectStatus = map[ua.StatusCode]bool{
	ua.StatusBadSecurityPolicyRejected:     true, // 0x80550000
	ua.StatusBadSecurityModeRejected:       true, // 0x80540000
	ua.StatusBadSecurityModeInsufficient:   true, // 0x80E60000
	ua.StatusBadNoValidCertificates:        true, // 0x80590000
	ua.StatusBadSecurityChecksFailed:       true, // 0x80130000
	ua.StatusBadCertificateInvalid:         true, // 0x80120000
	ua.StatusBadCertificateUntrusted:       true, // 0x801A0000
	ua.StatusBadCertificateChainIncomplete: true, // 0x810D0000
	ua.StatusBadCertificateUseNotAllowed:   true, // 0x80180000
	ua.StatusBadCertificateTimeInvalid:     true, // 0x80140000
	ua.StatusBadCertificateHostNameInvalid: true, // 0x80160000
	ua.StatusBadCertificateRevoked:         true, // 0x801D0000
	ua.StatusBadNonceInvalid:               true, // 0x80240000
}

// classifyProbeErr 判断一次 GetEndpoints 的失败意味着什么。
//
// errors.As 对 *uacp.Error 与 ua.StatusCode **都能命中**：gopcua 的 errors 包
// 只是标准库的转发（Is/As/Unwrap 逐个转调），而 *uacp.Error 自带 Unwrap()
// 返回它内部的 ua.StatusCode。所以下面两条分支的顺序**不能调换** ——
// 先问 *uacp.Error，否则一个 TCP 层 ERRF 会被当成服务级状态码来解读。
func classifyProbeErr(err error) probeVerdict {
	if err == nil {
		return verdictIsServer
	}

	// UA-TCP 层回了 ERRF：对面按 OPC UA 成帧并给出了拒绝理由，一定是 OPC UA。
	// （这里的错误码是 StatusBadTCP* 一族，与安全策略无关。）
	var ae *uacp.Error
	if errors.As(err, &ae) {
		logDebug("probe: 对端回了 UA-TCP ERRF: %v", err)
		return verdictIsServer
	}

	var sc ua.StatusCode
	if errors.As(err, &sc) {
		// StatusBadTimeout 是 gopcua **本地合成**的（等不到应答时由计时器发出），
		// 不是对端说的。它有两种成因且**无从区分**：对端完全不应答，或对端在
		// OPN 之后用一个裸 ERRF 拒绝而我们没有序号可以把它对上（uasc 会把这样
		// 的错误交给一个没人读的通道，见其 secure_channel.go 的 readChunk）。
		// 既然分不清，就不该把"可能不是 OPC UA"的东西报成设备 —— 误报一台
		// 连不上的设备比漏报更费用户的时间。
		if sc == ua.StatusBadTimeout || sc == ua.StatusBadServerNotConnected {
			logDebug("probe: 应答超时或连接已断（无法区分「不是 OPC UA」与「静默拒绝」）: %v", err)
			return verdictNotOurs
		}
		if policyRejectStatus[sc] {
			logDebug("probe: 对面是 OPC UA，但拒绝了 None 安全策略: %v", err)
			return verdictRejectedNone
		}
		// 其余服务级状态码（BadServiceUnsupported、BadServerHalted、
		// BadUserAccessDenied……）只有 OPC UA 对端发得出来。
		logDebug("probe: 对端回了服务级状态码，仍视为 OPC UA: %v", err)
		return verdictIsServer
	}

	// 拨号失败（*net.OpError）、DNS 失败、连上就断（io.EOF）、UA-TCP 帧头解不出来
	// （HTTP 服务在 4840 上就会走这条）、ctx 超时 —— 都不是 OPC UA。
	logDebug("probe: 不是 OPC UA（%T）: %v", err, err)
	return verdictNotOurs
}

// Probe 探测单个 OPC UA 目标。
//
// 返回 (nil, nil) 表示"这个目标上没有 OPC UA 服务器" —— **"没扫到"不是错误**：
// 端口不通、是别的服务、或者设备根本不在，都是扫描的正常结果。把这类情况报成
// error 会让一次 /24 扫描产生两百多条错误噪声。
//
// error 只用于**选项本身有问题**（选项 JSON 解析不出来），那是用户的输入错误，
// 值得原样报出来让前端显示。
//
// 一定在 budget 内返回：本次请求的超时从剩余预算里反推（见下面 wait 的计算），
// 预算耗尽就带上已有结论（此处只有"没找到"）正常返回，绝不返回 error ——
// 超预算会撞网关侧 RPC 超时并累积插件卡死计数，连续几次会把整个插件进程连同
// 它下面所有协议、所有设备一起重启。
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
	if target.Broadcast {
		// 防御性分支：本协议的 Discovery 声明的是 unicast，引擎不会给我们广播目标。
		// 真收到（有人改了 TargetMode、或引擎那边出了岔子）就什么都不做 ——
		// 朝一个子网广播地址连 TCP，等于同时对整个网段发起 SecureChannel 握手。
		logDebug("probe: 跳过广播目标 %s（OPC UA 的扫描是逐主机单播）", host)
		return nil, nil
	}
	port := target.Port
	if port <= 0 {
		port = defaultScanPort
	}

	// 本次探测分到的预算 = min(用户填的超时, 剩余预算)。不这么钳，一个填大了的
	// 超时值就能把"prober 一定在 Budget 内返回"这条承诺变成一个摆设。
	wait := opt.timeout
	if remaining := time.Until(deadline); remaining < wait {
		wait = remaining
	}
	if wait <= 0 {
		return nil, nil
	}

	started := time.Now()
	endpoint := "opc.tcp://" + net.JoinHostPort(host, strconv.Itoa(port))

	// ctx 与两个 Option 都必须设：库的默认请求超时是 10s（见其 config.go），
	// 只交 ctx 不够 —— 端点的 OPN 计时器独立于 ctx，会直接击穿插件自报的
	// MaxBudgetMs 承诺并累积卡死计数。
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()

	endpoints, err := opcua.GetEndpoints(ctx, endpoint,
		opcua.SecurityPolicy("None"),
		opcua.SecurityModeString("None"),
		opcua.AutoReconnect(false),
		opcua.DialTimeout(wait),
		opcua.RequestTimeout(wait),
	)

	verdict := classifyProbeErr(err)
	if verdict == verdictNotOurs {
		return nil, nil
	}

	dev := buildDevice(host, port, endpoints, verdict == verdictRejectedNone)
	dev.ElapsedMs = int(time.Since(started).Milliseconds())
	logDebug("probe: %s:%d 命中（端点 %d 个，拒绝 None=%v）", host, port, len(endpoints), verdict == verdictRejectedNone)
	return []driver.DiscoveredDevice{dev}, nil
}

// securityModeName 把安全模式枚举还原成配置表单里用的短名。
//
// 必须与 config.go 的 validSecurityModes 逐字一致（"None"/"Sign"/"SignAndEncrypt"）：
// 这几个值会被写进 protocol_json 再经 ParseOpcUaConfig 读回，写错的表现是
// "扫出来的设备一键建好却连不上，且报的是含糊的握手失败"。
var securityModeName = map[ua.MessageSecurityMode]string{
	ua.MessageSecurityModeNone:           "None",
	ua.MessageSecurityModeSign:           "Sign",
	ua.MessageSecurityModeSignAndEncrypt: "SignAndEncrypt",
}

// securityPolicyName 从安全策略 URI 里取短名。
//
// 端点表里给的是完整 URI（"http://opcfoundation.org/UA/SecurityPolicy#Basic256Sha256"），
// 而配置表单与 validSecurityPolicies 用的是井号后面的短名。取不到井号就原样返回 ——
// 那是一个我们不认识的策略，如实显示比猜一个近似的更有用。
func securityPolicyName(uri string) string {
	if i := strings.LastIndex(uri, "#"); i >= 0 && i+1 < len(uri) {
		return uri[i+1:]
	}
	return uri
}

// buildDevice 把一次命中的探测组装成 DiscoveredDevice。
//
// 拆成独立函数（而不是写在 Probe 里）是为了让"识别成功"那条分支能被直接单测：
// 真应答是服务器回的字节流，用假服务器造一份形状正确的记录既费劲又容易造假 ——
// 假数据一旦与真设备不符，这条分支反而永远测不到。与 s7drv / finsdrv /
// mitsubishidrv 同一条取舍。
//
// rejected 为 true 表示对面是 OPC UA 但拒绝了 None 策略：此时 endpoints 必定为空
// （我们连端点表都没读回来），所以名称与 Extra 只能给通用值加一句告警。
func buildDevice(host string, port int, endpoints []*ua.EndpointDescription, rejected bool) driver.DiscoveredDevice {
	dev := driver.DiscoveredDevice{
		Host: host,
		Port: port,
		// UnitID 留 0：OPC UA 没有站号概念。
		Name: "OPC UA 服务器",
	}

	var warnings []string
	if rejected {
		warnings = append(warnings,
			"服务端拒绝了无安全策略（None）的连接：它只提供加密端点，"+
				"而网关当前未实现客户端证书 —— 建成设备后连不上，需要先配置证书")
	}

	// 服务端自述的名字。**只取这一项**：GetEndpoints 不带会话，服务器不会报
	// 厂商/型号/固件（那些要走 ServerStatus，代价是建一次会话）。
	// Vendor/Model/Version 因此一律留空 —— 拿协议名冒充设备自述，用户会以为
	// 那是设备报的。与 mitsubishidrv / s7drv / finsdrv 同一条取舍。
	if name := serverName(endpoints); name != "" {
		dev.Name = name
	}

	if app := serverApplication(endpoints); app != nil {
		if app.ApplicationURI != "" {
			dev.Extra = append(dev.Extra, driver.Attribute{Name: "应用URI", Value: app.ApplicationURI})
		}
		if app.ProductURI != "" {
			dev.Extra = append(dev.Extra, driver.Attribute{Name: "产品URI", Value: app.ProductURI})
		}
	}

	if policies := distinctPolicies(endpoints); len(policies) > 0 {
		dev.Extra = append(dev.Extra, driver.Attribute{
			Name: "安全策略", Value: strings.Join(policies, "、"),
		})
	}
	if modes := distinctAuthModes(endpoints); len(modes) > 0 {
		dev.Extra = append(dev.Extra, driver.Attribute{
			Name: "认证方式", Value: strings.Join(modes, "、"),
		})
	}
	// 服务器不接受匿名登录时，写回一个 authMode=Anonymous 只会把失败推迟到采集时。
	// 不给 authMode（驱动默认就是匿名，结果一样），但把原因说在 Extra 里。
	if len(endpoints) > 0 && !offersAnonymous(endpoints) {
		warnings = append(warnings, "服务端不接受匿名登录，请在设备配置里填用户名与密码")
	}
	if len(warnings) > 0 {
		dev.Extra = append(dev.Extra, driver.Attribute{Name: "提示", Value: strings.Join(warnings, "；")})
	}

	dev.ProtocolJSON = protocolJSONOf(host, port, endpoints)
	return dev
}

// serverApplication 取端点表里第一条带服务器描述的记录。
//
// 同一台服务器的所有端点描述的是同一个 ApplicationDescription，取第一条即可；
// 防御 nil 是因为这是个纯透传结构，畸形应答里出现空指针不会让任何人提前发现。
func serverApplication(endpoints []*ua.EndpointDescription) *ua.ApplicationDescription {
	for _, ep := range endpoints {
		if ep != nil && ep.Server != nil {
			return ep.Server
		}
	}
	return nil
}

// serverName 服务端自述的 ApplicationName（如 "KEPServerEX/6"、"Siemens OPC UA"）。
//
// 空值不补"OPC UA"之类的占位：调用方会回落到通用名，两处都不猜。
func serverName(endpoints []*ua.EndpointDescription) string {
	app := serverApplication(endpoints)
	if app == nil || app.ApplicationName == nil {
		return ""
	}
	return strings.TrimSpace(app.ApplicationName.Text)
}

// distinctPolicies 端点表里出现过的安全策略短名，**保序去重**。
//
// 保序（而不是排序）是为了让同一台服务器每次扫描给出相同的结果行 ——
// 端点表的顺序由服务器决定且稳定，排序反而会让"现场截图"与"再扫一次"对不上。
func distinctPolicies(endpoints []*ua.EndpointDescription) []string {
	seen := make(map[string]bool, len(endpoints))
	out := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		if ep == nil {
			continue
		}
		name := securityPolicyName(ep.SecurityPolicyURI)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// distinctAuthModes 端点表里出现过的认证方式短名（中文，因为这一格直接给用户看）。
func distinctAuthModes(endpoints []*ua.EndpointDescription) []string {
	seen := make(map[ua.UserTokenType]bool)
	out := make([]string, 0, 3)
	for _, ep := range endpoints {
		if ep == nil {
			continue
		}
		for _, tok := range ep.UserIdentityTokens {
			if tok == nil || seen[tok.TokenType] || len(out) >= probeMaxAuthModes {
				continue
			}
			seen[tok.TokenType] = true
			if name := authModeLabel(tok.TokenType); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// authModeLabel 用户令牌类型的中文名。未知类型返回 ""（不猜、不显示）。
func authModeLabel(t ua.UserTokenType) string {
	switch t {
	case ua.UserTokenTypeAnonymous:
		return "匿名"
	case ua.UserTokenTypeUserName:
		return "用户名/密码"
	case ua.UserTokenTypeCertificate:
		return "证书"
	case ua.UserTokenTypeIssuedToken:
		return "令牌服务"
	default:
		return ""
	}
}

// offersAnonymous 端点表里有没有任何一条接受匿名登录。
//
// 空端点表（服务器不支持发现服务、或拒绝 None 策略）时返回 false 由调用方
// 用 len(endpoints) > 0 把关 —— 这里不替调用方判断"没有信息"与"没有匿名"。
func offersAnonymous(endpoints []*ua.EndpointDescription) bool {
	for _, ep := range endpoints {
		if ep == nil {
			continue
		}
		for _, tok := range ep.UserIdentityTokens {
			if tok != nil && tok.TokenType == ua.UserTokenTypeAnonymous {
				return true
			}
		}
	}
	return false
}

// protocolJSONOf 生成可直接建 device 行的连接配置。
//
// **键名必须与 opcUaConfigRaw（config.go）逐一对应**，且 **port 必须是数字**：
// 那个字段在 opcUaConfigRaw 里声明的是 int，写成字符串会被 json.Unmarshal
// 静默丢弃、ParseOpcUaConfig 回落到默认端口 4840 —— 用户改过端口的设备，
// 扫描结果看着正常，一键建出来的设备却连不上。
//
// 安全策略取"服务端提供的 None 端点"，这正是本次探测证明可连的那一种；
// 一个 None 端点都没有时取 SecurityLevel 最低的（**写回真相**，连不上这件事
// 由 Extra 的告警说明）—— 写回一个我们没验证过的 None 只会让错误更难查。
//
// authMode 只在服务端确实接受匿名时才写：不写时驱动默认就是匿名，结果一样，
// 但少一个"配置里写着匿名、实际连不上"的假象。
func protocolJSONOf(host string, port int, endpoints []*ua.EndpointDescription) string {
	policy, mode := "None", "None"
	if ep := pickEndpoint(endpoints); ep != nil {
		policy = securityPolicyName(ep.SecurityPolicyURI)
		if name, ok := securityModeName[ep.SecurityMode]; ok {
			mode = name
		}
	}

	cfg := map[string]any{
		"host":           host,
		"port":           port,
		"securityPolicy": policy,
		"securityMode":   mode,
	}
	if offersAnonymous(endpoints) {
		cfg["authMode"] = "Anonymous"
	}
	// map 的键序在 json.Marshal 里是排过序的，因此这段输出稳定可断言。
	b, err := json.Marshal(cfg)
	if err != nil {
		// 走不到：map[string]any 里全是字符串与数字。写成回退而不是 panic，
		// 是因为探测路径上的 panic 只会被 handle 的 recover 咽成一次 RPC 错误。
		logf("组装 protocol_json 失败（%v），回落到最保守的默认配置", err)
		return fmt.Sprintf(`{"host":%q,"port":%d,"securityPolicy":"None","securityMode":"None"}`, host, port)
	}
	return string(b)
}

// pickEndpoint 选一条写回配置端点。
//
// 优先 None+None（本次探测实际用的那一种，是**唯一被验证过可连**的），
// 否则取 SecurityLevel 最低的一条 —— 它的含义就是"这个服务器上最不安全的
// 那一种安全组合"，也是离 None 最近的一种。SecurityLevel 相同则按原顺序，
// 保证同一台服务器每次扫描给出同一份配置。
func pickEndpoint(endpoints []*ua.EndpointDescription) *ua.EndpointDescription {
	var best *ua.EndpointDescription
	for _, ep := range endpoints {
		if ep == nil {
			continue
		}
		if securityPolicyName(ep.SecurityPolicyURI) == "None" && ep.SecurityMode == ua.MessageSecurityModeNone {
			return ep
		}
		if best == nil || ep.SecurityLevel < best.SecurityLevel {
			best = ep
		}
	}
	return best
}
