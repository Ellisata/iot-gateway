// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func connectTestClient(t *testing.T, cfg *Config, withSession bool) (*rwsClient, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return newRealClient(ctx, cfg, withSession)
}

func TestConnectDetectsAuthScheme(t *testing.T) {
	cases := []struct {
		auth        authScheme
		wantVersion string
	}{
		{authNone, version20},   // http + 免认证 → 按 scheme 推；见 TestConnectVersionInference
		{authBasic, version20},  //
		{authDigest, version10}, //
	}
	for _, c := range cases {
		f := newFakeRWS(t, c.auth)
		cfg := configFor(t, f)
		cfg.RWSVersion = versionAuto // 这一条测的就是自动判定

		client, err := connectTestClient(t, cfg, false)
		if err != nil {
			t.Fatalf("auth=%v 时 Connect 失败: %v", c.auth, err)
		}
		defer client.Close()

		if got := client.authScheme(); got != c.auth {
			t.Errorf("auth=%v：探测出的认证方式 = %v", c.auth, got)
		}
		if c.auth != authNone && client.version != c.wantVersion {
			t.Errorf("auth=%v：推断的版本 = %q, 期望 %q", c.auth, client.version, c.wantVersion)
		}
	}
}

// TestConnectVersionInference RWS 版本首先看认证方式（1.0 用 Digest、
// 2.0 用 Basic），免认证时按 scheme 推（OmniCore 默认只开 HTTPS）。
func TestConnectVersionInference(t *testing.T) {
	f := newFakeRWS(t, authNone)
	cfg := configFor(t, f)
	cfg.RWSVersion = versionAuto

	client, err := connectTestClient(t, cfg, false)
	if err != nil {
		t.Fatalf("Connect 失败: %v", err)
	}
	defer client.Close()

	// 假服务器是明文 http → 推成 1.0。
	if client.version != version10 {
		t.Errorf("http 免认证应推成 1.0，实际 %q", client.version)
	}

	// 显式指定版本时，用户的设置优先于推断。
	cfg.RWSVersion = version20
	client2, err := connectTestClient(t, cfg, false)
	if err != nil {
		t.Fatalf("Connect 失败: %v", err)
	}
	defer client2.Close()
	if client2.version != version20 {
		t.Errorf("显式 rwsVersion=2.0 未被尊重，实际 %q", client2.version)
	}
}

// TestDigestHandshakeSucceeds Round 1：完整的 digest 握手必须走通。
//
// 假控制器会独立重算摘要（见 fakeRWS.checkDigest），所以这条测试真正验证的是
// "插件算出的 response 与 uri 是对的" —— 而不是"假服务器照单全收"。
func TestDigestHandshakeSucceeds(t *testing.T) {
	f := newFakeRWS(t, authDigest)
	cfg := configFor(t, f)

	client, err := connectTestClient(t, cfg, false)
	if err != nil {
		t.Fatalf("digest 握手失败: %v", err)
	}
	defer client.Close()

	// 第一次 401（无凭据）、第二次带摘要成功。
	sys := f.hits(systemPath)
	if len(sys) < 2 {
		t.Fatalf("期望至少两次 /rw/system 请求（401 + 重试），实际 %d 次", len(sys))
	}
	// RWS 1.0 靠查询参数强制 JSON，而摘要的 uri 必须与请求行一致。
	if !strings.Contains(sys[1], "json=1") {
		t.Errorf("重试请求应带 ?json=1，实际 %q", sys[1])
	}
}

// TestBasicSentPreemptively Basic 是预置的：验证请求首次发出就带凭据，
// 不必先吃一个 401 再重试。
//
// 注意 /rw/system 会命中两次：第一次是**探测**，它刻意不带凭据 ——
// 只有不带凭据，控制器才会回 401 挑战，而那个挑战正是判断认证方式的唯一手段。
// 所以"一次"指的是**验证**那一次，不是总共一次。
func TestBasicSentPreemptively(t *testing.T) {
	f := newFakeRWS(t, authBasic)
	cfg := configFor(t, f)

	client, err := connectTestClient(t, cfg, false)
	if err != nil {
		t.Fatalf("basic 握手失败: %v", err)
	}
	defer client.Close()

	hits := f.hits(systemPath)
	if len(hits) != 2 {
		t.Fatalf("期望 2 次 /rw/system（探测 + 验证），实际 %d 次：%q", len(hits), hits)
	}
	// 若 Basic 不是预置的，第二次会先吃一个 401 再来第三次。
	if auth := f.authHeaderAt(t, 0); auth != "" {
		t.Errorf("探测请求不该带凭据，实际 %q", auth)
	}
	if auth := f.authHeaderAt(t, 1); !strings.HasPrefix(auth, "Basic ") {
		t.Errorf("验证请求应预置 Basic 凭据，实际 %q", auth)
	}
}

func TestAuthFailureIsReported(t *testing.T) {
	f := newFakeRWS(t, authDigest)
	f.user = "Default User" // 服务器认这个用户……
	cfg := configFor(t, f)
	cfg.Password = "wrong-password" // ……但插件拿的是错密码

	_, err := connectTestClient(t, cfg, false)
	if err == nil {
		t.Fatal("错误密码应让 Connect 失败")
	}
	if !strings.Contains(err.Error(), "用户名或密码") {
		t.Errorf("错误消息应指向凭据问题，实际: %v", err)
	}
}

func TestProbeRejectsUnrecognizableChallenge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Negotiate")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.Endpoint = srv.URL
	_, err := connectTestClient(t, cfg, false)
	if err == nil {
		t.Fatal("无法识别的挑战应让 Connect 失败")
	}
}

// TestServiceErrorKeepsConnection 服务级错误（404 / 非 JSON）**不能**把连接
// 判成断开：一个点位不存在说明不了控制器失联。
func TestServiceErrorKeepsConnection(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsListPath, `{"_embedded":{"_state":[{"name":"DI1","lvalue":"1"}]}}`)
	f.routeStatus("/rw/panel/opmode", http.StatusNotFound)

	cfg := configFor(t, f)
	client, err := connectTestClient(t, cfg, false)
	if err != nil {
		t.Fatalf("Connect 失败: %v", err)
	}
	defer client.Close()

	if !client.IsConnected() {
		t.Fatal("Connect 之后应处于已连接状态")
	}

	ctx := context.Background()
	_, err = client.getStates(ctx, "/rw/panel/opmode")
	var se *rwsError
	if !errors.As(err, &se) {
		t.Fatalf("404 应返回 *rwsError，实际 %T: %v", err, err)
	}
	if !isServiceError(err) {
		t.Error("404 是服务级错误")
	}
	if isFatal(err) {
		t.Error("404 不该被判为致命（会触发无谓的重连）")
	}
	if !client.IsConnected() {
		t.Error("服务级错误不该改变连接状态")
	}
}

func TestNonJSONResponseIsServiceError(t *testing.T) {
	f := newFakeRWS(t, authNone)
	// RWS 1.0 在没带 ?json=1 时回 XML —— 最常见的配置错误。
	f.routeJSON("/rw/test", `<?xml version="1.0"?><html></html>`)

	cfg := configFor(t, f)
	client, err := connectTestClient(t, cfg, false)
	if err != nil {
		t.Fatalf("Connect 失败: %v", err)
	}
	defer client.Close()

	_, err = client.getStates(context.Background(), "/rw/test")
	if !errors.Is(err, errNotJSON) {
		t.Fatalf("应返回 errNotJSON，实际 %T: %v", err, err)
	}
	if isFatal(err) {
		t.Error("非 JSON 响应是服务级错误，不该触发重连")
	}
	if !client.IsConnected() {
		t.Error("服务级错误不该改变连接状态")
	}
}

// TestTransportErrorMarksDisconnected 传输级错误要把连接标记为断开，
// 让采集引擎在下一轮重连。
func TestTransportErrorMarksDisconnected(t *testing.T) {
	f := newFakeRWS(t, authNone)
	cfg := configFor(t, f)

	client, err := connectTestClient(t, cfg, false)
	if err != nil {
		t.Fatalf("Connect 失败: %v", err)
	}
	defer client.Close()

	f.Close() // 服务器没了

	_, err = client.getStates(context.Background(), signalsListPath)
	if err == nil {
		t.Fatal("服务器已关闭，请求应失败")
	}
	if isServiceError(err) {
		t.Errorf("连接被拒应算传输级错误，实际被判为服务级: %v", err)
	}
	if !isFatal(err) {
		t.Errorf("传输级错误应致命: %v", err)
	}
	client.MarkDisconnected()
	if client.IsConnected() {
		t.Error("传输级错误后连接状态应为断开")
	}
}

// TestCookieJarCarriesSessionCookie CookieJar 会自动收下并回发 Set-Cookie，
// 这正是"不必显式创建会话也能带 cookie"的依据。
func TestCookieJarCarriesSessionCookie(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.cookie = "sess-abc-123"
	f.routeJSON(signalsListPath, `{"_embedded":{"_state":[{"name":"DI1","lvalue":"1"}]}}`)

	cfg := configFor(t, f)
	client, err := connectTestClient(t, cfg, false)
	if err != nil {
		t.Fatalf("Connect 失败: %v", err)
	}
	defer client.Close()

	cookies := client.httpc.Jar.Cookies(mustParseURL(t, f.URL))
	if len(cookies) == 0 {
		t.Fatal("CookieJar 应已收下 Set-Cookie")
	}
	found := false
	for _, c := range cookies {
		if c.Name == "-http-session-" && c.Value == "sess-abc-123" {
			found = true
		}
	}
	if !found {
		t.Errorf("CookieJar 里没有 -http-session-，实际 %+v", cookies)
	}
}

// TestSessionRequestOnlyWhenConfigured 会话创建是**尽力而为**的，且只在
// 明确需要时发（auto 只在 RWS 1.0 上发）。
func TestSessionRequestOnlyWhenConfigured(t *testing.T) {
	cases := []struct {
		name        string
		auth        authScheme
		version     string
		session     string
		wantSession bool
	}{
		{"auto + 1.0 → 发", authDigest, version10, triAuto, true},
		{"auto + 2.0 → 不发", authBasic, version20, triAuto, false},
		{"显式 true → 发", authBasic, version20, triTrue, true},
		{"显式 false → 不发", authDigest, version10, triFalse, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeRWS(t, c.auth)
			cfg := configFor(t, f, func(cfg *Config) {
				cfg.RWSVersion = c.version
				cfg.SessionRequest = c.session
			})
			client, err := connectTestClient(t, cfg, true)
			if err != nil {
				t.Fatalf("Connect 失败: %v", err)
			}
			defer client.Close()

			n := f.hitCount(sessionRequestPath)
			if c.wantSession && n == 0 {
				t.Error("期望发出会话创建请求，实际没有")
			}
			if !c.wantSession && n != 0 {
				t.Errorf("不该发会话创建请求，实际发了 %d 次", n)
			}
		})
	}
}

// TestSessionRequestFailureDoesNotBreakConnect 会话创建失败**不影响** Connect。
//
// 它的价值只在于"某些固件在没有会话 cookie 时对部分资源回 503"，
// 而 CookieJar 本就带着 cookie；把它做成硬失败会让一台本来能用的控制器
// 因为一个可选动作失败而完全不可用。
func TestSessionRequestFailureDoesNotBreakConnect(t *testing.T) {
	f := newFakeRWS(t, authDigest)
	f.routeStatus(sessionRequestPath, http.StatusInternalServerError)

	cfg := configFor(t, f, func(cfg *Config) { cfg.SessionRequest = triTrue })
	client, err := connectTestClient(t, cfg, true)
	if err != nil {
		t.Fatalf("会话创建失败不该让 Connect 失败: %v", err)
	}
	defer client.Close()
	if !client.IsConnected() {
		t.Error("会话创建失败后仍应处于已连接状态")
	}
}

// TestProbeRefusesDowngradeToPlaintext 是本文件里最重要的安全边界。
//
// 场景：控制器在那个端口上说的是**明文 HTTP**。若允许"HTTPS 失败就回落 HTTP"，
// 一个能干扰 TLS 的攻击者只要让握手失败，就能把 Basic 凭据诱导成明文发出。
// 所以回落只允许发生在端口根本不可达时；握手层面的失败必须是硬错误，
// 并在消息里给出可操作的出路。
func TestProbeRefusesDowngradeToPlaintext(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer plain.Close()

	u := mustParseURL(t, plain.URL)
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("解析端口失败: %v", err)
	}
	port := 0
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("解析端口失败: %v", err)
	}

	cfg := DefaultConfig()
	cfg.Host = host
	cfg.Port = port
	cfg.UseHTTPS = triAuto // 候选：https://host:port 与 http://host:port
	cfg.TimeoutMS = 2000
	cfg.Timeout = 2 * time.Second

	client, err := connectTestClient(t, cfg, false)
	if err == nil {
		client.Close()
		t.Fatal("HTTPS 端口上不是 TLS 服务时必须报错，**不能**悄悄降级到明文 HTTP")
	}
	// 错误要指向可操作的出路。
	if !strings.Contains(err.Error(), "仅HTTP") {
		t.Errorf("错误消息应提示可以显式选择仅 HTTP，实际: %v", err)
	}
}

// TestPortReachableIsTheFallbackCriterion portReachable 是上面那条安全边界的
// 具体判据：**端口可达 ⟹ 不换候选**。
func TestPortReachableIsTheFallbackCriterion(t *testing.T) {
	ctx := context.Background()

	// 活着的服务器 → 可达。
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer live.Close()
	if err := portReachable(ctx, live.URL, time.Second); err != nil {
		t.Errorf("活着的服务器应判为可达: %v", err)
	}

	// 关掉的服务器 → 不可达（连接被拒）。这就是唯一允许换候选的情形。
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	if err := portReachable(ctx, deadURL, time.Second); err == nil {
		t.Error("已关闭的端口应判为不可达")
	}

	// DNS 解析不了的域名 → 不可达。
	if err := portReachable(ctx, "https://no-such-host.invalid:443", time.Second); err == nil {
		t.Error("无法解析的主机应判为不可达")
	}
}

// TestIsTLSErrorClassifiesHandshakeFailures isTLSError 只影响**错误消息的措辞**
// （据此给出"请在表单里选仅HTTP"的提示），不再承担任何放行决定 ——
// 放行与否由 portReachable 单独决定。这条测试钉住它别退化成放行判据。
func TestIsTLSErrorClassifiesHandshakeFailures(t *testing.T) {
	yes := []error{
		&url.Error{Op: "Get", URL: "https://h/", Err: &tls.CertificateVerificationError{}},
		&url.Error{Err: x509.UnknownAuthorityError{}},
		&url.Error{Err: x509.HostnameError{Certificate: &x509.Certificate{}}},
		&url.Error{Err: tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}},
	}
	for _, err := range yes {
		if !isTLSError(err) {
			t.Errorf("%T 应被识别为 TLS 层错误", err)
		}
	}

	no := []error{
		nil,
		errors.New("随便一个错误"),
		&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
	}
	for _, err := range no {
		if isTLSError(err) {
			t.Errorf("%v 不该被识别为 TLS 层错误", err)
		}
	}
}

// TestTLSServerWithInsecureSkipVerify 自签名证书 + 默认跳过校验应当连得上；
// 关掉跳过校验则必须失败（而不是悄悄连上）。
func TestTLSServerWithInsecureSkipVerify(t *testing.T) {
	f := newFakeRWS(t, authBasic)
	tlsSrv := httptest.NewTLSServer(f.Server.Config.Handler)
	defer tlsSrv.Close()

	cfg := DefaultConfig()
	cfg.Endpoint = tlsSrv.URL
	cfg.UseHTTPS = triTrue
	cfg.RWSVersion = version20

	// 默认跳过校验：应当成功（ABB 控制器出厂即自签名）。
	client, err := connectTestClient(t, cfg, false)
	if err != nil {
		t.Fatalf("跳过证书校验时自签名服务器应连得上: %v", err)
	}
	client.Close()

	// 显式要求校验：必须失败（这条同时证明前一条不是"碰巧"）。
	cfg.InsecureSkipVerify = false
	if _, err := connectTestClient(t, cfg, false); err == nil {
		t.Fatal("要求校验证书时自签名服务器必须连不上")
	}
}

// TestRateLimiterRespectsBurst 令牌桶：桶内突发不等待，超出后按速率放行。
func TestRateLimiterRespectsBurst(t *testing.T) {
	ctx := context.Background()
	l := newRateLimiter(20) // burst=20，速率 20/s

	start := time.Now()
	for i := 0; i < 20; i++ {
		if err := l.wait(ctx); err != nil {
			t.Fatalf("第 %d 次取令牌失败: %v", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("桶内突发不该等待，实际耗时 %v", elapsed)
	}

	// 第 21 次必须等大约 50ms（1/20 秒）。
	start = time.Now()
	if err := l.wait(ctx); err != nil {
		t.Fatalf("取令牌失败: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("超出桶容量后应等待约 50ms，实际 %v（限流未生效）", elapsed)
	}
}

func TestRateLimiterHonoursContextCancellation(t *testing.T) {
	l := newRateLimiter(1)
	ctx := context.Background()
	if err := l.wait(ctx); err != nil { // 用掉桶里唯一的令牌
		t.Fatalf("取令牌失败: %v", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := l.wait(cctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("应返回 ctx 的错误，实际 %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("ctx 取消后应立即返回，实际等了 %v", elapsed)
	}
}

func TestCloseIsIdempotentAndNonBlocking(t *testing.T) {
	f := newFakeRWS(t, authNone)
	client, err := connectTestClient(t, configFor(t, f), false)
	if err != nil {
		t.Fatalf("Connect 失败: %v", err)
	}

	client.Close()
	client.Close() // 幂等
	if client.IsConnected() {
		t.Error("Close 之后不该是已连接")
	}
	if !client.IsClosed() {
		t.Error("Close 之后应标记为已关闭")
	}

	// 已关闭的客户端不再发请求。
	start := time.Now()
	if _, err := client.getStates(context.Background(), signalsListPath); err == nil {
		t.Error("已关闭的客户端发请求应报错")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("已关闭的客户端应立即失败，实际等了 %v", elapsed)
	}
}

func mustParseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("解析 URL %q 失败: %v", s, err)
	}
	return u
}
