// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gopcua/opcua/ua"
	"github.com/gopcua/opcua/uacp"

	"iot-gateway/driver"
	"iot-gateway/testutil/fake"
)

// ==================== 命中 ====================

// TestProbeFindsServer 真服务器 + 真探测：判据是端点表读得回来。
func TestProbeFindsServer(t *testing.T) {
	srv, _, err := fake.NewOpcUaTree()
	if err != nil {
		t.Fatalf("起假 OPC UA 服务器失败: %v", err)
	}
	defer srv.Close()

	target := driver.ScanTarget{Host: "127.0.0.1", Port: srv.Port()}
	devs, err := Probe(target, nil, 10*time.Second)
	if err != nil {
		t.Fatalf("Probe 返回错误: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("命中 %d 台设备, 期望 1 台", len(devs))
	}
	dev := devs[0]

	if dev.Host != "127.0.0.1" || dev.Port != srv.Port() {
		t.Errorf("设备地址 = %s:%d, 期望被探测的那个目标 127.0.0.1:%d",
			dev.Host, dev.Port, srv.Port())
	}
	if dev.Name == "" {
		t.Error("Name 为空 —— 它是列表里唯一能让人认出这台服务器的东西")
	}
	if dev.UnitID != 0 {
		t.Errorf("UnitID = %d，OPC UA 没有站号概念", dev.UnitID)
	}
	// Vendor/Model/Version 一律留空：GetEndpoints 不带会话，服务器不报这些，
	// 拿协议名冒充设备自述会让用户以为那是设备说的。
	if dev.Vendor != "" || dev.Model != "" || dev.Version != "" {
		t.Errorf("厂商/型号/版本应当留空，却给出了 %q/%q/%q", dev.Vendor, dev.Model, dev.Version)
	}

	// protocol_json 必须能被本驱动自己的配置解析器反解，且端口是**数字** ——
	// 写成字符串会被 json.Unmarshal 静默丢弃、回落成默认 4840，
	// 用户改过端口的设备一键建出来就连不上。
	cfg, err := ParseOpcUaConfig(dev.ProtocolJSON)
	if err != nil {
		t.Fatalf("protocol_json 解析失败: %v（%s）", err, dev.ProtocolJSON)
	}
	if cfg.Port != srv.Port() || cfg.Host != "127.0.0.1" {
		t.Errorf("protocol_json 回解出 %s:%d, 期望 127.0.0.1:%d（原文 %s）",
			cfg.Host, cfg.Port, srv.Port(), dev.ProtocolJSON)
	}
	if cfg.SecurityPolicy != "None" || cfg.SecurityMode != "None" {
		t.Errorf("安全策略/模式 = %s/%s, 期望 None/None（那是本次探测实际验证过可连的一种）",
			cfg.SecurityPolicy, cfg.SecurityMode)
	}
	if cfg.AuthMode != "Anonymous" {
		t.Errorf("认证方式 = %s, 期望 Anonymous（假服务器接受匿名）", cfg.AuthMode)
	}
	// 字段名写错了不会报错，只会让那一项落回默认值 —— 逐字钉住键名。
	for _, key := range []string{`"host"`, `"port"`, `"securityPolicy"`, `"securityMode"`, `"authMode"`} {
		if !contains(dev.ProtocolJSON, key) {
			t.Errorf("protocol_json 缺少 %s：%s", key, dev.ProtocolJSON)
		}
	}

	// Extra 是唯一的告警通道，也是用户判断"这台能不能连"的依据。
	if !extraHas(dev, "安全策略") {
		t.Errorf("Extra 里没有安全策略清单: %+v", dev.Extra)
	}
	if !extraHas(dev, "认证方式") {
		t.Errorf("Extra 里没有认证方式: %+v", dev.Extra)
	}
	if extraHas(dev, "提示") {
		t.Errorf("一台正常接受匿名 + None 的服务器不该带告警: %+v", dev.Extra)
	}
}

// TestProbeFindsServerOnCustomPort 端口覆盖：扫描表单里改过的端口必须写回配置。
//
// 与上面那条分开是因为它专门钉住"端口不是默认 4840 时仍然对得上" ——
// 一端写死 4840 的实现在默认端口上一路绿灯，到了现场（改了端口的服务器）
// 才会批量出问题。
func TestProbeFindsServerOnCustomPort(t *testing.T) {
	srv, _, err := fake.NewOpcUaTree()
	if err != nil {
		t.Fatalf("起假 OPC UA 服务器失败: %v", err)
	}
	defer srv.Close()

	devs, err := Probe(driver.ScanTarget{Host: "127.0.0.1", Port: srv.Port()}, nil, 10*time.Second)
	if err != nil || len(devs) != 1 {
		t.Fatalf("Probe = %v, %v", devs, err)
	}
	want := strconv.Itoa(srv.Port())
	cfg, err := ParseOpcUaConfig(devs[0].ProtocolJSON)
	if err != nil {
		t.Fatalf("protocol_json 解析失败: %v", err)
	}
	if strconv.Itoa(cfg.Port) != want {
		t.Errorf("回解端口 = %d, 期望 %s", cfg.Port, want)
	}
}

// ==================== 没扫到（不是错误） ====================

// TestProbeNotOpcUa 那些"端口那边有别的东西"的情形一律返回 (nil, nil)。
//
// **"没扫到"不是错误**：把这类情况报成 error 会让一次 /24 扫描产生两百多条
// 错误噪声，而用户从那些噪声里读不出任何可行动的信息。
func TestProbeNotOpcUa(t *testing.T) {
	t.Run("端口全闭", func(t *testing.T) {
		port := closedPort(t)
		assertNoDevice(t, port, "端口关闭")
	})
	t.Run("HTTP 服务", func(t *testing.T) {
		hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "hello") // 回一串不是 UA-TCP 的字节
		}))
		defer hs.Close()
		assertNoDevice(t, portOf(t, hs.URL), "4840 上跑着 HTTP 管理页")
	})
	t.Run("连上就关", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_ = c.Close()
			}
		}()
		assertNoDevice(t, ln.Addr().(*net.TCPAddr).Port, "连上就断")
	})
}

// TestProbeUnreachableHost 不可达的地址不能报错，也必须在预算内返回。
//
// 一个 /24 里绝大多数地址都走这条分支，它决定了扫描一个网段要多久。
func TestProbeUnreachableHost(t *testing.T) {
	port := closedPort(t)
	target := driver.ScanTarget{Host: "127.0.0.1", Port: port}

	start := time.Now()
	devs, err := Probe(target, nil, 2*time.Second)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("不可达不该报错: %v", err)
	}
	if len(devs) != 0 {
		t.Fatalf("不可达却报出了 %d 台设备", len(devs))
	}
	// 上限给得很松：这条断言防的是"忘了设超时"（库的默认请求超时是 10s），
	// 不是精确计时 —— 在本机端口全闭时它应当是毫秒级。
	if elapsed > 5*time.Second {
		t.Errorf("不可达目标的探测用了 %s，远超预算 2s（是不是漏传了 DialTimeout/RequestTimeout？）",
			elapsed)
	}
}

// TestProbeEmptyHost 空目标直接跳过。
func TestProbeEmptyHost(t *testing.T) {
	devs, err := Probe(driver.ScanTarget{Host: "   "}, nil, time.Second)
	if err != nil || len(devs) != 0 {
		t.Errorf("空目标应当被跳过: %v, %v", devs, err)
	}
}

// TestProbeSkipsBroadcastTarget 本协议声明的是 unicast，收到广播目标什么都不做。
//
// 朝一个子网广播地址连 TCP 等于同时对整个网段发起 SecureChannel 握手，
// 而我们对裸 TCP 的应答方毫无校验手段。
func TestProbeSkipsBroadcastTarget(t *testing.T) {
	devs, err := Probe(driver.ScanTarget{Host: "192.168.1.255", Port: 4840, Broadcast: true},
		nil, time.Second)
	if err != nil || len(devs) != 0 {
		t.Errorf("广播目标应当被跳过: %v, %v", devs, err)
	}
}

// TestProbeBadOptions 选项本身坏了要报错（那是用户的输入错误）。
func TestProbeBadOptions(t *testing.T) {
	_, err := Probe(driver.ScanTarget{Host: "127.0.0.1"}, json.RawMessage(`{不是 JSON}`), time.Second)
	if err == nil {
		t.Fatal("坏掉的选项应当报错")
	}
}

// ==================== 错误分类（三分法） ====================

// TestClassifyProbeErr 三类错误的判据逐条钉住。
//
// 这一层是"设备存在但连不上"与"没有设备"之间的分界线，判错的两种表现都很糟：
// 把别的服务当成设备（用户对着一台连不上的幽灵排障），或者把
// "只提供加密端点的服务器"当成没有设备（用户从"扫不到"得出错误的结论）。
func TestClassifyProbeErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want probeVerdict
	}{
		{"成功", nil, verdictIsServer},
		{"UA-TCP 层 ERRF", &uacp.Error{ErrorCode: uint32(ua.StatusBadTCPMessageTypeInvalid)}, verdictIsServer},
		{"策略被拒", ua.StatusBadSecurityPolicyRejected, verdictRejectedNone},
		{"安全模式被拒", ua.StatusBadSecurityModeRejected, verdictRejectedNone},
		{"证书不受信", ua.StatusBadCertificateUntrusted, verdictRejectedNone},
		{"无可用证书", ua.StatusBadNoValidCertificates, verdictRejectedNone},
		{"服务不支持", ua.StatusBadServiceUnsupported, verdictIsServer},
		// StatusBadTimeout 是 gopcua **本地合成**的（等不到应答时由计时器发出），
		// 不是对端说的：它既可能是"对面根本不应答"，也可能是"对面静默拒绝了"，
		// 两者无从区分 ⇒ 按"不是我们的设备"处理（误报一台连不上的设备比漏报更费时间）。
		{"本地合成的超时", ua.StatusBadTimeout, verdictNotOurs},
		{"连接未建立", ua.StatusBadServerNotConnected, verdictNotOurs},
		{"拨号失败", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, verdictNotOurs},
		{"连上就断", io.EOF, verdictNotOurs},
		{"帧头解不出来", errors.New("uacp: message too large"), verdictNotOurs},
	}
	for _, c := range cases {
		if got := classifyProbeErr(c.err); got != c.want {
			t.Errorf("%s: classifyProbeErr = %v, 期望 %v", c.name, got, c.want)
		}
	}

	// 包一层之后仍要认得出来：真实错误会经库的多个函数返回上来。
	wrapped := fmt.Errorf("opcua: connect failed: %w", ua.StatusBadSecurityPolicyRejected)
	if got := classifyProbeErr(wrapped); got != verdictRejectedNone {
		t.Errorf("被包装过的策略拒绝没被认出来: %v", got)
	}
}

// TestBuildDeviceRejectsNoneEndpoints 只提供加密端点的服务器照样列出，但必须告警。
//
// 用户选的是"照样列出 + 明确告警"：一台只提供加密端点的服务器是真实存在的
// 设备，静默不报会让用户从"扫不到"得出"那里没有 OPC UA 服务器"的错误结论。
func TestBuildDeviceRejectsNoneEndpoints(t *testing.T) {
	dev := buildDevice("10.0.0.5", 4840, nil, true)

	if dev.Host != "10.0.0.5" || dev.Port != 4840 {
		t.Errorf("设备地址 = %s:%d", dev.Host, dev.Port)
	}
	if dev.Name == "" {
		t.Error("拒绝 None 时也该给一个名字（列表里那一行不能是空的）")
	}
	if !extraHas(dev, "提示") {
		t.Errorf("必须有一条告警说明为什么建了也连不上: %+v", dev.Extra)
	}
	// 端点表都没读回来 ⇒ 安全策略只能写最保守的 None；写一个我们**没验证过**
	// 的值只会让错误更难查（那台机器上根本没有证书）。
	cfg, err := ParseOpcUaConfig(dev.ProtocolJSON)
	if err != nil {
		t.Fatalf("protocol_json 解析失败: %v", err)
	}
	if cfg.SecurityPolicy != "None" || cfg.SecurityMode != "None" {
		t.Errorf("没有端点信息时应当退到 None/None，得到 %s/%s", cfg.SecurityPolicy, cfg.SecurityMode)
	}
}

// TestPickEndpointPrefersNone 端点选择：优先被验证过可连的 None，否则取最不安全的那个。
func TestPickEndpointPrefersNone(t *testing.T) {
	encrypted := &ua.EndpointDescription{
		EndpointURL:       "opc.tcp://h:4840",
		SecurityPolicyURI: "http://opcfoundation.org/UA/SecurityPolicy#Basic256Sha256",
		SecurityMode:      ua.MessageSecurityModeSignAndEncrypt,
		SecurityLevel:     5,
	}
	none := &ua.EndpointDescription{
		EndpointURL:       "opc.tcp://h:4840",
		SecurityPolicyURI: "http://opcfoundation.org/UA/SecurityPolicy#None",
		SecurityMode:      ua.MessageSecurityModeNone,
		SecurityLevel:     0,
	}
	weak := &ua.EndpointDescription{
		EndpointURL:       "opc.tcp://h:4840",
		SecurityPolicyURI: "http://opcfoundation.org/UA/SecurityPolicy#Basic128Rsa15",
		SecurityMode:      ua.MessageSecurityModeSign,
		SecurityLevel:     1,
	}

	if got := pickEndpoint([]*ua.EndpointDescription{encrypted, none}); got != none {
		t.Error("有 None 端点时应当选它（那是本次探测实际验证过可连的一种）")
	}
	if got := pickEndpoint([]*ua.EndpointDescription{encrypted, weak}); got != weak {
		t.Error("没有 None 时应当取 SecurityLevel 最低的那一条")
	}
	if got := pickEndpoint(nil); got != nil {
		t.Error("空端点表应当返回 nil")
	}
}

// TestDistinctPoliciesAndAuthModes 端点表的信息提取：保序去重、空值不编造。
func TestDistinctPoliciesAndAuthModes(t *testing.T) {
	eps := []*ua.EndpointDescription{
		nil, // 畸形应答里的空指针不该让整次探测 panic
		{
			SecurityPolicyURI: "http://opcfoundation.org/UA/SecurityPolicy#Basic256Sha256",
			UserIdentityTokens: []*ua.UserTokenPolicy{
				{TokenType: ua.UserTokenTypeAnonymous},
				{TokenType: ua.UserTokenTypeUserName},
			},
		},
		{
			SecurityPolicyURI: "http://opcfoundation.org/UA/SecurityPolicy#None",
			UserIdentityTokens: []*ua.UserTokenPolicy{
				{TokenType: ua.UserTokenTypeAnonymous}, // 重复项不该出现两次
			},
		},
	}
	policies := distinctPolicies(eps)
	if len(policies) != 2 || policies[0] != "Basic256Sha256" || policies[1] != "None" {
		t.Errorf("安全策略清单 = %v, 期望保序去重的 [Basic256Sha256 None]", policies)
	}
	modes := distinctAuthModes(eps)
	if len(modes) != 2 || modes[0] != "匿名" || modes[1] != "用户名/密码" {
		t.Errorf("认证方式清单 = %v, 期望 [匿名 用户名/密码]", modes)
	}
	if !offersAnonymous(eps) {
		t.Error("端点表里有匿名令牌，offersAnonymous 却回了 false")
	}
	if offersAnonymous([]*ua.EndpointDescription{{
		UserIdentityTokens: []*ua.UserTokenPolicy{{TokenType: ua.UserTokenTypeUserName}},
	}}) {
		t.Error("只提供用户名/密码时 offersAnonymous 不该回 true")
	}
	// 空端点表不是"没有匿名"，是"没有信息"——由调用方按 len(eps)>0 把关。
	if offersAnonymous(nil) {
		t.Error("空端点表应当回 false")
	}
}

// TestProtocolJSONOmitsAuthModeWhenNoAnonymous 服务器不接受匿名时不写 authMode。
//
// 写一个 authMode=Anonymous 只是把失败推迟到采集时；不写时驱动的默认值也是匿名，
// 结果一样但少一个"配置里写着匿名、实际连不上"的假象。
func TestProtocolJSONOmitsAuthModeWhenNoAnonymous(t *testing.T) {
	eps := []*ua.EndpointDescription{{
		SecurityPolicyURI: "http://opcfoundation.org/UA/SecurityPolicy#None",
		SecurityMode:      ua.MessageSecurityModeNone,
		UserIdentityTokens: []*ua.UserTokenPolicy{
			{TokenType: ua.UserTokenTypeUserName},
		},
	}}
	got := protocolJSONOf("10.0.0.9", 4840, eps)
	if contains(got, "authMode") {
		t.Errorf("不接受匿名时不该写 authMode: %s", got)
	}
	dev := buildDevice("10.0.0.9", 4840, eps, false)
	if !extraHas(dev, "提示") {
		t.Errorf("不接受匿名时必须告警：用户要能知道得去填用户名密码: %+v", dev.Extra)
	}
}

// ==================== 选项 ====================

// TestParseScanOptions 缺项走默认、坏 JSON 报错、数字字符串能认。
func TestParseScanOptions(t *testing.T) {
	opt, err := parseScanOptions(nil)
	if err != nil || opt.timeout != defaultScanTimeoutMs*time.Millisecond {
		t.Errorf("空选项应当走默认: %v, %v", opt.timeout, err)
	}

	opt, err = parseScanOptions(json.RawMessage(`{"timeoutMs":"1500"}`))
	if err != nil || opt.timeout != 1500*time.Millisecond {
		t.Errorf("数字字符串形式的超时没被接受: %v, %v", opt.timeout, err)
	}

	opt, err = parseScanOptions(json.RawMessage(`{"timeoutMs":0}`))
	if err != nil || opt.timeout != defaultScanTimeoutMs*time.Millisecond {
		t.Errorf("0 应当视为未配置: %v, %v", opt.timeout, err)
	}

	if _, err := parseScanOptions(json.RawMessage(`{不是 JSON}`)); err == nil {
		t.Error("坏掉的选项应当报错")
	}
}

// ==================== 测试辅助 ====================

// extraHas 设备属性里有没有这一项（Extra 是唯一的告警通道）。
func extraHas(dev driver.DiscoveredDevice, name string) bool {
	for _, a := range dev.Extra {
		if a.Name == name && a.Value != "" {
			return true
		}
	}
	return false
}

// assertNoDevice 断言这个端口上扫不到 OPC UA 设备，且**不报错**。
func assertNoDevice(t *testing.T, port int, what string) {
	t.Helper()
	devs, err := Probe(driver.ScanTarget{Host: "127.0.0.1", Port: port}, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("%s: 不该报错（「没扫到」不是错误）: %v", what, err)
	}
	if len(devs) != 0 {
		t.Fatalf("%s: 却报出了 %d 台设备", what, len(devs))
	}
}

// closedPort 返回一个刚探测出来、随后立刻关掉的端口（用它的"没人监听"）。
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// portOf 从 httptest 的 URL 里取出端口。
func portOf(t *testing.T, url string) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(url[len("http://"):])
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return port
}
