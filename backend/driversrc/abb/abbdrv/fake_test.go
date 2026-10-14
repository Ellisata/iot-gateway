// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeHandler 一个路由的响应：状态码 + 响应体。
type fakeHandler func(r *http.Request) (status int, body string)

// fakeRWS 可编程的假 ABB 控制器。
//
// 它实现的是**真实的 RWS 交互**：401 挑战 → 带凭据重试 → 返回 HAL JSON。
// 认证部分（尤其是 digest 的摘要校验）不是摆设 —— 假控制器按标准算法独立
// 重算一遍 response，对不上就回 401。这样"插件发出的摘要是否正确"
// 才算真的被验证过，而不是被一个照单全收的假服务器放过。
type fakeRWS struct {
	*httptest.Server

	requireAuth authScheme
	user, pass  string
	realm       string
	nonce       string

	// SetCookie 非零时随每个响应下发（测 cookie jar 的会话回发）。
	cookie string

	mu       sync.Mutex
	routes   map[string]fakeHandler
	fallback fakeHandler
	seen     []string // 收到的请求 RequestURI（含查询串）
	pathSeen []string // 只记路径（不含查询串），便于区分列表与单点
	authSeen []string
}

func newFakeRWS(t *testing.T, auth authScheme) *fakeRWS {
	t.Helper()
	f := &fakeRWS{
		requireAuth: auth,
		user:        "Default User",
		pass:        "robotics",
		realm:       "ABB",
		nonce:       "0dcd98b7102dd2f0e8b11d0f600bfb0c093",
		routes:      make(map[string]fakeHandler),
		fallback: func(*http.Request) (int, string) {
			return http.StatusNotFound, `{"error":"no route"}`
		},
	}
	// 探测与 verify 都要打这个端点，给它一个默认响应。
	f.route(systemPath, func(*http.Request) (int, string) {
		return http.StatusOK, `{"_embedded":{"_state":[{"rwversion":"7.12.0"}]}}`
	})
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeRWS) route(path string, h fakeHandler) {
	f.mu.Lock()
	f.routes[path] = h
	f.mu.Unlock()
}

// routeJSON 注册一个固定 JSON 响应的路由。
func (f *fakeRWS) routeJSON(path, body string) {
	f.route(path, func(*http.Request) (int, string) { return http.StatusOK, body })
}

// routeStatus 注册一个固定状态码的路由。
func (f *fakeRWS) routeStatus(path string, status int) {
	f.route(path, func(*http.Request) (int, string) {
		return status, fmt.Sprintf(`{"error":%d}`, status)
	})
}

// requests 返回收到过的全部 RequestURI（副本）。
func (f *fakeRWS) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

// hitCount 统计路径前缀命中次数（匹配 RequestURI 的路径部分）。
func (f *fakeRWS) hitCount(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, u := range f.seen {
		if strings.HasPrefix(u, prefix) {
			n++
		}
	}
	return n
}

// pathHits 统计**路径逐字相等**的请求次数。
//
// 用它区分 /rw/iosystem/signals（全量列表）与
// /rw/iosystem/signals/Local/DRV_1/DI1（单点）—— 只看前缀是分不开的，
// 而"批量优先到底省没省下请求"正是本项目最核心的承诺。
func (f *fakeRWS) pathHits(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.pathSeen {
		if p == path {
			n++
		}
	}
	return n
}

// hits 返回匹配前缀的请求，便于断言"到底发了哪些请求"。
func (f *fakeRWS) hits(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, u := range f.seen {
		if strings.HasPrefix(u, prefix) {
			out = append(out, u)
		}
	}
	return out
}

// authHeaderAt 返回第 i 个请求携带的 Authorization 头。
func (f *fakeRWS) authHeaderAt(t *testing.T, i int) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.authSeen) {
		t.Fatalf("只收到 %d 个请求，取不到第 %d 个", len(f.authSeen), i)
	}
	return f.authSeen[i]
}

func (f *fakeRWS) reset() {
	f.mu.Lock()
	f.seen = nil
	f.pathSeen = nil
	f.authSeen = nil
	f.mu.Unlock()
}

func (f *fakeRWS) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, r.URL.RequestURI())
	f.pathSeen = append(f.pathSeen, r.URL.Path)
	f.authSeen = append(f.authSeen, r.Header.Get("Authorization"))
	auth := f.requireAuth
	cookie := f.cookie
	f.mu.Unlock()

	if cookie != "" {
		http.SetCookie(w, &http.Cookie{Name: "-http-session-", Value: cookie, Path: "/"})
	}

	if auth != authNone && !f.checkAuth(r) {
		f.challenge(w, auth)
		return
	}

	// 会话创建请求：由假服务器自己应答，测试的路由表不必认识它。
	if strings.HasPrefix(r.URL.Path, "/rw/elog/") {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("session created"))
		return
	}

	h := f.lookup(r.URL.Path)
	status, body := h(r)
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// lookup 先精确匹配，再取最长前缀匹配（路径带参数时用得上）。
func (f *fakeRWS) lookup(path string) fakeHandler {
	f.mu.Lock()
	defer f.mu.Unlock()
	if h, ok := f.routes[path]; ok {
		return h
	}
	keys := make([]string, 0, len(f.routes))
	for k := range f.routes {
		if strings.HasPrefix(path, k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return f.fallback
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	return f.routes[keys[0]]
}

// checkAuth 校验请求携带的凭据。
//
// Digest 分支**独立重算一遍摘要**：这样"插件算出的 response 对不对"是被
// 真验证过的。一个照单全收的假服务器会让摘要算错也一路绿灯，
// 而那正是现场最难查的一类故障（密码明明对，却一直 401）。
func (f *fakeRWS) checkAuth(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	switch f.requireAuth {
	case authBasic:
		return h == basicAuthHeader(f.user, f.pass)
	case authDigest:
		return f.checkDigest(r, h)
	}
	return true
}

func (f *fakeRWS) checkDigest(r *http.Request, header string) bool {
	if !strings.HasPrefix(header, "Digest ") {
		return false
	}
	get := func(k string) string { return extractParam(header, k) }

	if get("username") != f.user {
		return false
	}
	uri := get("uri")
	// 摘要里的 uri 必须与实际请求行**逐字节**相同。本插件每条请求都带
	// ?json=1，所以这里校验的很实在地覆盖了那个最经典的坑。
	if uri != r.URL.RequestURI() {
		return false
	}
	ch := &digestChallenge{
		Realm: f.realm, Nonce: get("nonce"),
		QOP: get("qop"), Algorithm: "MD5",
	}
	if ch.Nonce != f.nonce {
		return false
	}
	want := digestResponse(f.user, f.pass, ch, r.Method, uri, get("nc"), get("cnonce"))
	return want == get("response")
}

func (f *fakeRWS) challenge(w http.ResponseWriter, scheme authScheme) {
	switch scheme {
	case authBasic:
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm=%q`, f.realm))
	case authDigest:
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(
			`Digest realm=%q, nonce=%q, qop="auth", algorithm=MD5`, f.realm, f.nonce))
	}
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte("unauthorized"))
}

// configFor 造一个指向假控制器的配置。
func configFor(t *testing.T, f *fakeRWS, opts ...func(*Config)) *Config {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Endpoint = f.URL
	cfg.Username = "Default User"
	cfg.Password = "robotics"
	cfg.RWSVersion = version10 // 探测之外默认按 1.0，测试里显式钉住
	cfg.MaxRequestsPerSecond = 100
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}

// signalListBody 造一个 IO 信号列表响应。
func signalListBody(entries ...map[string]string) string {
	var b strings.Builder
	b.WriteString(`{"_embedded":{"_state":[`)
	for i, e := range entries {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('{')
		first := true
		for _, k := range sortedStringKeys(e) {
			if !first {
				b.WriteByte(',')
			}
			first = false
			fmt.Fprintf(&b, "%q:%q", k, e[k])
		}
		b.WriteByte('}')
	}
	b.WriteString(`]}}`)
	return b.String()
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
