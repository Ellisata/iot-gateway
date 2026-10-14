// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// maxProbeCandidates 探测最多尝试的端点数（含重定向追加的）。
const maxProbeCandidates = 3

// errAuthFailed 鉴权失败（401 且重试后仍未通过）。
//
// 单独成一个哨兵而不是并进 *rwsError：两者的处理方式相反 ——
// 服务级错误（某个点位的 404）必须**保持连接**，而鉴权失败说明整台设备
// 都用不了，必须置为未连接并让 Read 报错，这样设备在界面上显示为异常，
// 而不是静默地一片 Quality=0。
var errAuthFailed = errors.New("abb: 鉴权失败")

// rwsError 服务级错误：请求到达了控制器，但结果不可用（4xx/5xx、非 JSON 响应）。
//
// 与传输级错误的区别在于**连接状态是否该动**：一个点位返回 404 说明不了
// 控制器失联，把它判成断连会让采集引擎白白重连一轮并重新探测。
type rwsError struct {
	status int
	msg    string
}

func (e *rwsError) Error() string {
	if e.status > 0 {
		return fmt.Sprintf("abb: HTTP %d：%s", e.status, e.msg)
	}
	return "abb: " + e.msg
}

// rwsClient RWS HTTP 客户端：探测出的端点与版本、凭据、cookie jar、
// 限流与并发闸门，以及跨轮次维护的连接状态。
//
// 线程安全：同一设备的多个频率分组会并行调用 Read（本驱动的 SerialExclusive
// 为 false），所以限流器、信号量、digest 的 nc 计数器都必须内部加锁。
type rwsClient struct {
	cfg *Config

	base    string // 探测出的 base URL，如 "https://192.168.1.10:443"
	version string // version10 | version20

	// auth 探测出的认证方式，**用原子存**：写侧不只有探测，request 的 401
	// 分支在发现"之前判成免认证、其实要 Basic/Digest"时也会改写它，而那一侧
	// 跑在采集线程上；读侧是每条请求的 applyAuth。两者可能落在不同 goroutine
	// （本驱动不声明 SerialExclusive）。一律经 authScheme() / setAuth 访问。
	auth atomic.Int32

	transport   *http.Transport
	httpc       *http.Client // 带 cookie jar：正式请求用
	probeClient *http.Client // 无 jar、不跟随重定向：仅探测用
	digest      *digestClient
	basicHdr    string

	limiter *rateLimiter
	sem     chan struct{}

	connected atomic.Bool
	closed    atomic.Bool
}

// newRawClient 按配置构造客户端（不做任何 I/O）。
func newRawClient(cfg *Config) *rwsClient {
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   cfg.Timeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig: &tls.Config{
			// 默认跳过校验：ABB 控制器出厂即自签名，且 CN 与现场分配的 IP
			// 常年不符。关掉校验（表单里的"跳过证书校验 → 否"）需要网关
			// 信任该证书，否则连不上 —— 这是用户显式的选择，不是默认行为。
			InsecureSkipVerify: cfg.InsecureSkipVerify,
			MinVersion:         tls.VersionTLS12,
		},
		// 控制器是 HTTP/1.1，不尝试 h2（协商失败会白多一次往返）。
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2, // 对齐 RWS 每会话 2 个 HTTP 连接的上限
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: cfg.Timeout,
	}

	c := &rwsClient{
		cfg:       cfg,
		transport: transport,
		httpc: &http.Client{
			Transport: transport,
			Jar:       jar,
			Timeout:   cfg.Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				// 不跟随重定向：不跟随才能把跳转目标当作新的探测候选
				// 自己决定要不要去（见 probe 的 https→http 拒绝规则）。
				return http.ErrUseLastResponse
			},
		},
		probeClient: &http.Client{
			Transport: transport,
			// 无 Jar：cookie jar 的键是 host、**不含端口**，从 https 收到的
			// 会话 cookie 会被发到 http 候选上。探测阶段各候选之间必须互不串味。
			Timeout: cfg.Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		digest:  newDigestClient(cfg.Username, cfg.Password),
		limiter: newRateLimiter(cfg.MaxRequestsPerSecond),
		sem:     make(chan struct{}, defaultMaxConcurrency),
	}
	c.basicHdr = basicAuthHeader(cfg.Username, cfg.Password)
	return c
}

// connect 完成握手：探测端点 → 验证凭据 → （可选）创建会话。
//
// withSession 为 false 时不创建会话 —— Ping 走这条路（契约要求 Ping 不得
// 分配会话资源，见 driver.PingDevice 与 driversdk 的 ping 处理）。
func (c *rwsClient) connect(ctx context.Context, withSession bool) error {
	if err := c.probe(ctx); err != nil {
		return err
	}
	if err := c.verify(ctx); err != nil {
		return err
	}
	if withSession && c.shouldRequestSession() {
		c.requestSession(ctx)
	}
	c.connected.Store(true)
	return nil
}

// shouldRequestSession 判断本次连接要不要显式创建 RWS 会话。
func (c *rwsClient) shouldRequestSession() bool {
	switch c.cfg.SessionRequest {
	case triTrue:
		return true
	case triFalse:
		return false
	default: // auto
		return c.version == version10
	}
}

// probeResult 单个候选端点的探测结果。
type probeResult struct {
	auth     authScheme
	redirect string // 非空表示该端点返回了重定向
}

// probe 依次尝试候选端点，选出第一个可用的。
func (c *rwsClient) probe(ctx context.Context) error {
	queue := c.cfg.candidateBaseURLs()
	seen := make(map[string]bool, len(queue)+1)
	var lastErr error

	for len(queue) > 0 && len(seen) < maxProbeCandidates {
		base := queue[0]
		queue = queue[1:]
		if seen[base] {
			continue
		}
		seen[base] = true

		// 先做一次裸 TCP 可达性预检，**用端口是否可达**作为"能不能换下一个
		// 候选"的唯一判据。
		//
		// 不用错误类型来判断：TLS 握手在明文端口上失败时，客户端拿到的可能是
		// 一个读错误（对端回了 400 就关）而不是 tls.RecordHeaderError，按类型
		// 匹配会漏掉这一整类情况，于是悄悄退回明文 —— 那正是要防的事。
		// 显式拨一次，规则就变得可读、可测、不会随标准库的错误包装漂移。
		if err := portReachable(ctx, base, probeTimeout(c.cfg)); err != nil {
			lastErr = fmt.Errorf("%s 不可达: %w", base, err)
			logDebug("探测 %s 不可达（%v），尝试下一个候选", base, err)
			continue
		}

		res, err := c.probeOne(ctx, base)
		if err != nil {
			// 端口通，问题出在握手/表示层上 —— **绝不**退回明文。
			// 否则一个能干扰 TLS 的攻击者只要让握手失败，就能把 Basic 凭据
			// 诱导成 HTTP 明文发出去。
			if isTLSError(err) {
				return fmt.Errorf("abb: %s 端口可达，但不是可用的 TLS 服务（证书不受信，"+
					"或该端口在说明文 HTTP）；若控制器确实只提供 HTTP，请在表单里把「协议」设为「仅HTTP」：%w", base, err)
			}
			return fmt.Errorf("abb: 探测 %s 失败：%w（该端口可达但 RWS 握手未成功；"+
				"若控制器确实只提供 HTTP，请在表单里把「协议」设为「仅HTTP」）", base, err)
		}
		if res.redirect != "" {
			// https→http 的降级跳转同样拒绝：理由与上面一致。
			if strings.HasPrefix(base, "https://") && strings.HasPrefix(res.redirect, "http://") {
				return fmt.Errorf("abb: %s 重定向到明文地址 %s，已拒绝（请在表单里显式选择协议）", base, res.redirect)
			}
			lastErr = fmt.Errorf("%s 重定向到 %s", base, res.redirect)
			if !seen[res.redirect] {
				queue = append(queue, res.redirect)
			}
			continue
		}

		c.base = base
		c.setAuth(res.auth)
		c.version = c.pickVersion(res.auth, base)
		logDebug("探测成功：base=%s auth=%s version=%s", base, c.authScheme(), c.version)
		return nil
	}

	if lastErr == nil {
		lastErr = errors.New("没有可用的候选端点")
	}
	return fmt.Errorf("abb: 无法连接控制器：%w", lastErr)
}

// pickVersion 决定用哪一套协议方言。
// 用户的显式设置优先（探测失效时的逃生口）。
func (c *rwsClient) pickVersion(a authScheme, base string) string {
	if c.cfg.RWSVersion != versionAuto {
		return c.cfg.RWSVersion
	}
	// 认证方式是版本最强的信号：RWS 1.0（IRC5/RW6）发 Digest，
	// RWS 2.0（OmniCore/RW7）发 Basic。
	switch a {
	case authDigest:
		return version10
	case authBasic:
		return version20
	}
	// 免认证的控制器只能按 scheme 推：OmniCore 默认只开 HTTPS。
	if strings.HasPrefix(base, "https://") {
		return version20
	}
	return version10
}

// probeOne 探测单个候选端点。
//
// 用的是**无凭据**的客户端：只有不带凭据，控制器才会回 401 挑战，
// 而那个挑战正是我们判断认证方式的唯一手段。
func (c *rwsClient) probeOne(ctx context.Context, base string) (probeResult, error) {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout(c.cfg))
	defer cancel()

	req, err := http.NewRequestWithContext(pctx, http.MethodGet, base+systemPath, nil)
	if err != nil {
		return probeResult{}, err
	}
	resp, err := c.probeClient.Do(req)
	if err != nil {
		return probeResult{}, err
	}
	defer drain(resp)

	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		loc := resp.Header.Get("Location")
		if loc == "" {
			return probeResult{}, fmt.Errorf("abb: HTTP %d 重定向但没有 Location 头", resp.StatusCode)
		}
		u, err := resp.Request.URL.Parse(loc)
		if err != nil {
			return probeResult{}, fmt.Errorf("abb: 重定向地址 %q 无法解析: %w", loc, err)
		}
		return probeResult{redirect: normalizeBaseURL(u.Scheme + "://" + u.Host)}, nil

	case resp.StatusCode == http.StatusUnauthorized:
		hdr := resp.Header.Get("WWW-Authenticate")
		scheme := detectAuthScheme(hdr)
		if scheme == authNone {
			return probeResult{}, fmt.Errorf("abb: 控制器要求认证，但 WWW-Authenticate 头无法识别：%q", hdr)
		}
		return probeResult{auth: scheme}, nil

	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		// 免认证的控制器（或已按 IP 白名单放开）。
		return probeResult{auth: authNone}, nil

	default:
		return probeResult{}, &rwsError{status: resp.StatusCode, msg: "探测请求未返回 2xx"}
	}
}

// verify 用凭据做一次真实请求，确认能读到数据。
//
// 这一步是"测试连接"按钮真正验证的东西：探测只能证明端口通、能拿到挑战，
// 证明不了用户名密码对不对。错密码必须在这里被抓住，而不是等到设备投入
// 采集之后表现为一堆空值。
func (c *rwsClient) verify(ctx context.Context) error {
	body, err := c.request(ctx, http.MethodGet, systemPath, nil)
	if err != nil {
		switch {
		case errors.Is(err, errAuthFailed):
			return fmt.Errorf("abb: 连接 %s 失败：用户名或密码不正确（用户 %q）", c.base, c.cfg.Username)
		case isServiceError(err):
			return fmt.Errorf("abb: 连接 %s 失败：%w", c.base, err)
		default:
			return fmt.Errorf("abb: 连接 %s 失败：%w", c.base, err)
		}
	}
	if _, err := decodeJSON(body); err != nil {
		return fmt.Errorf("abb: 连接 %s 失败：%w（可尝试在表单里指定 RWS 版本）", c.base, err)
	}
	// 探测结果与真实验证一致，标记为已连接。
	return nil
}

// requestSession 尽力创建一个 RWS 会话，失败不致命。
//
// 为什么失败不致命：CookieJar 已经从**任何**响应里收过 Set-Cookie，
// 后续请求本就带着会话 cookie。这个调用的价值只在于"某些固件在没有会话
// cookie 时对部分资源回 503"这一种情况，而它的代价是每建立一个驱动实例就
// 占一个会话槽 —— RWS 的会话数是硬上限（全局 70、每 IP 5），而**每次重连
// 都会新建驱动实例**。所以它必须是尽力而为，绝不能把 Connect 拖垮。
func (c *rwsClient) requestSession(ctx context.Context) {
	if _, err := c.request(ctx, http.MethodPost, sessionRequestPath,
		url.Values{"action": {"request"}}); err != nil {
		logDebug("创建 RWS 会话失败（不影响使用）：%v", err)
		return
	}
	logDebug("已请求 RWS 会话")
}

// request 执行一次请求，必要时重试一次（401 → 解析挑战 → 带凭据重试）。
//
// 返回的 error 分三类，调用方据 errors.Is / errors.As 分派：
//   - errAuthFailed（哨兵）：凭据问题 → 置未连接 + 中止整轮
//   - *rwsError（含 errNotJSON 包装）：服务级 → 该点位/该类列表本轮作废，连接不动
//   - 其余：传输级 → 置未连接 + 中止整轮
func (c *rwsClient) request(ctx context.Context, method, path string, form url.Values) ([]byte, error) {
	if c.closed.Load() {
		return nil, errors.New("abb: 客户端已关闭")
	}

	// URL 只构造一次：digest 摘要里的 uri 必须与**实际发出的请求行**逐字节
	// 相同，重试时换一个 query 串就会算出对不上的 response。
	rawURL := c.base + path
	if c.version == version10 {
		// RWS 1.0 用查询参数强制 JSON 表示（2.0 改用 Accept 头）。
		sep := "?"
		if strings.ContainsRune(path, '?') {
			sep = "&"
		}
		rawURL += sep + "json=1"
	}

	for attempt := 0; attempt < 2; attempt++ {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
		if err != nil {
			return nil, err
		}
		c.applyHeaders(req, form != nil)

		if err := c.limiter.wait(ctx); err != nil {
			return nil, err
		}
		if err := c.acquire(ctx); err != nil {
			return nil, err
		}
		// applyAuth 必须在 acquire **之后**：Digest 分支会在这里递增 nc
		// （重放计数器），而服务端要求 nc 与请求到达的顺序一致。放在闸门之前，
		// 并发 >1 时两次请求可能拿到 nc=1/2 却按 2、1 的顺序发出去，
		// 撞上重放保护就是一次 401 —— 而凭据本身毫无问题。
		c.applyAuth(req)
		resp, err := c.httpc.Do(req)
		c.release()
		if err != nil {
			return nil, err // 传输级：拨号/TLS/超时/连接重置
		}

		payload, readErr := readBody(resp)

		if resp.StatusCode == http.StatusUnauthorized {
			// 只在第一次带挑战重试；第二次再 401 就说明凭据/摘要确实不对，
			// 再试一次只会把控制器那点配额烧掉。
			if attempt == 0 {
				hdr := resp.Header.Get("WWW-Authenticate")
				switch detectAuthScheme(hdr) {
				case authDigest:
					ch, cerr := parseDigestChallenge(hdr)
					if cerr != nil {
						return nil, fmt.Errorf("%w：%v", errAuthFailed, cerr)
					}
					// stale=true（凭据有效、nonce 过期）走的是同一条路：
					// 换 nonce 重算一次即可，绝不能当成密码错。
					c.digest.setChallenge(ch)
					c.setAuth(authDigest)
					continue
				case authBasic:
					// 之前判成免认证、这里才发现要 Basic：补上凭据重试。
					c.setAuth(authBasic)
					continue
				}
			}
			return nil, errAuthFailed
		}

		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &rwsError{status: resp.StatusCode, msg: snippet(payload)}
		}
		return payload, nil
	}
	// 循环只有两次迭代，而第二次的 401 在上面就返回了 —— 走到这里只能是
	// "两次都是 401 且都走了重试分支"，实际不可达。留着它是为了满足函数的
	// 终止语句要求，而不是表达某种可达状态。
	return nil, errAuthFailed
}

// applyHeaders 按 RWS 版本装配表示层协商头。
//
// RWS 1.0 用查询参数 ?json=1 指定 JSON，且**不设 Accept** ——
// 显式设置会让部分固件直接回 "Server cannot generate response for given
// accept header"。RWS 2.0 反过来：JSON 只能通过 Accept 头要到，
// 设成 application/json 会被回 406，必须带 ;v=2.0 版本参数。
func (c *rwsClient) applyHeaders(req *http.Request, hasForm bool) {
	if c.version != version20 {
		return
	}
	req.Header.Set("Accept", acceptV2)
	if hasForm {
		req.Header.Set("Content-Type", contentTypeV2)
	}
}

// applyAuth 装配鉴权头。Basic 是预置的（首个请求就带上）；
// Digest 必须先拿到挑战，所以第一次请求不带 Authorization。
func (c *rwsClient) applyAuth(req *http.Request) {
	switch c.authScheme() {
	case authBasic:
		req.Header.Set("Authorization", c.basicHdr)
	case authDigest:
		c.digest.authorize(req)
	}
}

// getStates 发一次 GET 并解析出状态条目。
func (c *rwsClient) getStates(ctx context.Context, path string) ([]map[string]any, error) {
	return c.states(ctx, http.MethodGet, path, nil)
}

// postStates 发一次表单 POST 并解析出状态条目。
func (c *rwsClient) postStates(ctx context.Context, path string, form url.Values) ([]map[string]any, error) {
	return c.states(ctx, http.MethodPost, path, form)
}

func (c *rwsClient) states(ctx context.Context, method, path string, form url.Values) ([]map[string]any, error) {
	body, err := c.request(ctx, method, path, form)
	if err != nil {
		return nil, err
	}
	root, err := decodeJSON(body)
	if err != nil {
		return nil, err // errNotJSON：响应到达了、但内容不可用 → 服务级
	}
	states := statesOf(root)
	if len(states) == 0 {
		return nil, fmt.Errorf("%w：响应里没有 _state 条目（前 120 字节：%s）", errNotJSON, snippet(body))
	}
	return states, nil
}

// isServiceError 判断错误是否属于服务级（连接状态不该动）。
func isServiceError(err error) bool {
	var se *rwsError
	if errors.As(err, &se) {
		return true
	}
	return errors.Is(err, errNotJSON)
}

func (c *rwsClient) IsConnected() bool { return c != nil && c.connected.Load() }

func (c *rwsClient) IsClosed() bool { return c == nil || c.closed.Load() }

// MarkDisconnected 标记连接断开（由 Read 遇到传输级错误时调用）。
func (c *rwsClient) MarkDisconnected() {
	if c != nil {
		c.connected.Store(false)
	}
}

func (c *rwsClient) setAuth(s authScheme) {
	c.auth.Store(int32(s))
}

// authScheme 返回当前认证方式（原子读，见字段注释）。
func (c *rwsClient) authScheme() authScheme {
	return authScheme(c.auth.Load())
}

// Close 释放连接资源，**不阻塞**。
//
// 刻意不尝试"登出会话"：RWS 没有通用的、有文档的 HTTP 注销端点，猜一个
// 只会把一次停机变成一次可能 4xx 的请求。会话由控制器侧超时回收。
//
// 同样刻意不等待在途请求：采集任务的 Stop() 会逐个驱动 Close，
// 任何等待都会直接变成停机延迟。
func (c *rwsClient) Close() {
	if c == nil || c.closed.Swap(true) {
		return
	}
	c.connected.Store(false)
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

// acquire / release 是并发闸门。RWS 每个会话只给 2 个 HTTP 连接，
// 超了会被回 503；默认串行（1）就够用，因为一轮只有 2~3 个列表请求。
func (c *rwsClient) acquire(ctx context.Context) error {
	select {
	case c.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *rwsClient) release() {
	select {
	case <-c.sem:
	default:
	}
}

// readBody 读完响应体再关闭 —— 必须读完，否则连接不会被放回空闲池，
// 每次请求都要重新握手，还会多占控制器的会话连接。
func readBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, responseBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("abb: 读取响应失败: %w", err)
	}
	return body, nil
}

// drain 读掉并关闭响应体（探测等不关心内容的场景）。
func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

// probeTimeout 返回单个候选端点的探测超时。
func probeTimeout(cfg *Config) time.Duration {
	if cfg.Timeout > probeTimeoutCap {
		return probeTimeoutCap
	}
	return cfg.Timeout
}

// portReachable 判断 base URL 的 TCP 端口能否建立连接。
//
// 它是"能不能换下一个候选"的唯一判据，也是本插件的安全边界所在：
// 只有**端口根本不可达**才允许从 HTTPS 回落到 HTTP。端口可达而握手失败
// （证书不受信、对端在说明文 HTTP、超时）一律硬失败，交由用户在表单里
// 显式选择「仅HTTP」—— 一个能干扰 TLS 的攻击者因此无法把 Basic 凭据
// 诱导成明文发出。
func portReachable(ctx context.Context, base string, timeout time.Duration) error {
	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	hostport := u.Host
	if u.Port() == "" {
		hostport = net.JoinHostPort(u.Hostname(), defaultPortFor(u.Scheme))
	}

	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dctx, "tcp", hostport)
	if err != nil {
		return err
	}
	return conn.Close()
}

func defaultPortFor(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// basicAuthHeader 生成 Basic 鉴权头的值。
// 预置而非等挑战：RWS 2.0 的 Basic 是标准用法，先发一次能省掉一个往返。
func basicAuthHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// isTLSError 判断错误是否来自 TLS 握手/证书校验。
func isTLSError(err error) bool {
	var cve *tls.CertificateVerificationError
	if errors.As(err, &cve) {
		return true
	}
	var rec tls.RecordHeaderError
	if errors.As(err, &rec) {
		// 该端口在说明文 HTTP —— 端口是通的，按"不可达"处理会误伤安全边界。
		return true
	}
	var ua x509.UnknownAuthorityError
	if errors.As(err, &ua) {
		return true
	}
	var hn x509.HostnameError
	if errors.As(err, &hn) {
		return true
	}
	var ci x509.CertificateInvalidError
	return errors.As(err, &ci)
}

// rateLimiter 令牌桶限流器。
//
// 自己实现而不是引入 golang.org/x/time/rate：后者不在 go.mod 里，
// 而插件应当自包含（见 docs/driver-plugins.md 的"插件自带全部实现"）。
// 逻辑只有二十来行，不值得为它加一个依赖。
type rateLimiter struct {
	mu     sync.Mutex
	rate   float64 // 每秒补充的令牌数
	burst  float64 // 桶容量
	tokens float64
	last   time.Time
}

func newRateLimiter(perSecond int) *rateLimiter {
	rate := float64(perSecond)
	if rate <= 0 {
		rate = 1
	}
	return &rateLimiter{rate: rate, burst: rate, tokens: rate, last: time.Now()}
}

// wait 取一个令牌，不够就等到够。ctx 取消则返回 ctx.Err()。
func (l *rateLimiter) wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := time.Now()
		if elapsed := now.Sub(l.last).Seconds(); elapsed > 0 {
			l.tokens += elapsed * l.rate
			if l.tokens > l.burst {
				l.tokens = l.burst
			}
			l.last = now
		}
		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}
		need := time.Duration((1 - l.tokens) / l.rate * float64(time.Second))
		l.mu.Unlock()

		if need < time.Millisecond {
			need = time.Millisecond
		}
		timer := time.NewTimer(need)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
