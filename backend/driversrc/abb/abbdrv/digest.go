// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// 认证方式。RWS 1.0（IRC5 / RobotWare 6）发 Digest 挑战，
// RWS 2.0（OmniCore / RobotWare 7）发 Basic 挑战 —— 探测结果同时决定了
// 用哪种认证，以及后面用哪一套 Accept 头（见 session.go）。
type authScheme int

const (
	authNone authScheme = iota
	authBasic
	authDigest
)

func (s authScheme) String() string {
	switch s {
	case authBasic:
		return "Basic"
	case authDigest:
		return "Digest"
	}
	return "None"
}

// detectAuthScheme 从 WWW-Authenticate 头里判断认证方式。
//
// 一个头里可能并列多个挑战（"Digest realm=..., Basic realm=..."），
// **Digest 优先**：它信息更全，且能同时发这两种的控制器两者都接受。
func detectAuthScheme(header string) authScheme {
	schemes := challengeSchemes(header)
	for _, s := range schemes {
		if s == "digest" {
			return authDigest
		}
	}
	for _, s := range schemes {
		if s == "basic" {
			return authBasic
		}
	}
	return authNone
}

// challengeSchemes 返回头里出现的挑战方案名（小写）。
func challengeSchemes(header string) []string {
	var out []string
	for _, tok := range splitHeaderList(header) {
		if s := leadingScheme(tok); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// splitChallenge 把一个挑战片段切成方案名与剩余参数串。
//
//	"Digest realm=\"x\"" → ("digest", "realm=\"x\"")
//	"Digest"             → ("digest", "")
//	"nonce=\"abc\""      → ("", "nonce=\"abc\"")   纯参数片段
//
// 必须把方案名从串里摘掉再交给 cutParam：否则 `Digest realm="x"` 会被切成
// 键 `digest realm`、值 `x`，而那些真正的参数（nonce 等）反而落到了后续
// 片段上 —— 结果是 realm 取到脏值、nonce 取不到，报一句莫名其妙的"缺 nonce"。
func splitChallenge(tok string) (scheme, rest string) {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return "", ""
	}
	i := strings.IndexAny(tok, "= ")
	switch {
	case i < 0:
		// 没有 '=' 也没有空格：整个片段就是方案名（"Digest"）
		return strings.ToLower(tok), ""
	case tok[i] == ' ':
		return strings.ToLower(tok[:i]), strings.TrimSpace(tok[i+1:])
	default:
		return "", tok // 形如 "realm=..." 的参数片段
	}
}

// leadingScheme 取一个挑战片段开头的方案名，没有则返回 ""。
func leadingScheme(tok string) string {
	scheme, _ := splitChallenge(tok)
	return scheme
}

// splitHeaderList 按逗号切分 HTTP 头的列表值，**跳过引号内的逗号**。
//
// 必须区分引号内外：qop="auth,auth-int" 里的逗号一旦被当作分隔符，
// qop 就变成了 "auth 半截，后面的 auth-int" 会挤进 nc 或算法字段，
// 结果是每次请求都 401，而响应里没有任何线索指向解析错误。
func splitHeaderList(s string) []string {
	var (
		out     []string
		cur     strings.Builder
		inQuote bool
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && inQuote && i+1 < len(s):
			// 引号内的转义：连同下一个字符一起原样保留（RWS 的 nonce 是
			// 十六进制不会用到，但别的固件可能有）。
			cur.WriteByte(c)
			i++
			cur.WriteByte(s[i])
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == ',' && !inQuote:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(out, cur.String())
}

// digestChallenge 解析后的 Digest 挑战。
type digestChallenge struct {
	Realm     string
	Nonce     string
	Opaque    string
	QOP       string // "auth"；空串表示 RFC 2069（无 qop）
	Algorithm string // 归一化为 "MD5"（缺省与显式都是它）
	Stale     bool
}

// parseDigestChallenge 解析 WWW-Authenticate 头里的 Digest 挑战。
//
// 值**可带引号也可不带**：部分固件的 nonce/opaque 是裸的十六进制串。
// 只按带引号解析会拿到一个带着引号的 nonce，于是算出的 response 永远不匹配，
// 表现为"密码明明是对的却一直 401"。
//
// 不支持的算法（MD5-sess / SHA-256）一律**明确报错**，不静默按 MD5 算 ——
// 那样只会得到一个语义不明的 401，比现在这条错误难查得多。
func parseDigestChallenge(header string) (*digestChallenge, error) {
	params := map[string]string{}
	found := false
	cur := ""
	for _, tok := range splitHeaderList(header) {
		scheme, rest := splitChallenge(tok)
		if scheme != "" {
			cur = scheme
			if scheme == "digest" {
				found = true
			}
		} else {
			rest = tok // 纯参数片段，沿用当前方案
		}
		// 只收 Digest 挑战下的参数：同一个头里并列 Basic 时（
		// "Digest realm=a, nonce=n, Basic realm=b"），Basic 的 realm 会把
		// Digest 的覆盖掉，算出的摘要就对不上了。
		if cur != "digest" {
			continue
		}
		if k, v, ok := cutParam(rest); ok {
			params[k] = v
		}
	}
	if !found {
		return nil, fmt.Errorf("abb: WWW-Authenticate %q 中没有 Digest 挑战", header)
	}

	c := &digestChallenge{
		Realm:     params["realm"],
		Nonce:     params["nonce"],
		Opaque:    params["opaque"],
		Algorithm: params["algorithm"],
	}
	if c.Nonce == "" {
		return nil, fmt.Errorf("abb: Digest 挑战缺少 nonce")
	}

	// 算法归一化：缺省即 MD5。
	if c.Algorithm == "" {
		c.Algorithm = "MD5"
	}
	if !strings.EqualFold(c.Algorithm, "MD5") {
		return nil, fmt.Errorf("abb: 不支持的 Digest algorithm %q（只支持 MD5）", c.Algorithm)
	}
	c.Algorithm = "MD5"

	// qop 归一化：取 "auth"；只有 auth-int 时明确拒绝。
	switch qop := strings.ToLower(strings.TrimSpace(params["qop"])); {
	case qop == "":
		// RFC 2069：无 qop 分支，response 少三项哈希输入。
	case hasQOPToken(qop, "auth"):
		c.QOP = "auth"
	default:
		return nil, fmt.Errorf("abb: 不支持的 Digest qop %q（只支持 auth）", params["qop"])
	}

	// stale=true 表示"凭据没问题、只是 nonce 过期了"，要用新挑战重试一次。
	// 把它当成密码错误会让现场看到一句指错方向的"鉴权失败"。
	c.Stale = strings.EqualFold(strings.TrimSpace(params["stale"]), "true")
	return c, nil
}

// hasQOPToken 在逗号分隔的 qop 列表里精确匹配一个令牌。
// 用精确匹配而非 Contains：qop="auth-int" 里含有子串 "auth"，
// 用 Contains 会把不支持的 auth-int 当成支持的 auth 放过去。
func hasQOPToken(qop, want string) bool {
	for _, t := range strings.Split(qop, ",") {
		if strings.TrimSpace(t) == want {
			return true
		}
	}
	return false
}

// cutParam 切分 "key=value"，去掉值两侧的引号。
func cutParam(s string) (key, val string, ok bool) {
	i := strings.IndexByte(s, '=')
	if i <= 0 {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(s[:i]))
	val = strings.Trim(strings.TrimSpace(s[i+1:]), `"`)
	if key == "" {
		return "", "", false
	}
	return key, val, true
}

// digestResponse 按 RFC 2617 / 7616 计算 response 摘要（MD5）。
//
// 抽成纯函数是为了能用 RFC 2617 §3.5 的官方向量直接钉住 —— 摘要算法错一位
// 的表现是"一直 401"，现场完全无从下手，必须靠测试而不是靠现场发现。
func digestResponse(user, pass string, c *digestChallenge, method, uri, nc, cnonce string) string {
	ha1 := md5hex(user + ":" + c.Realm + ":" + pass)
	ha2 := md5hex(method + ":" + uri)
	if c.QOP == "" {
		return md5hex(ha1 + ":" + c.Nonce + ":" + ha2)
	}
	return md5hex(ha1 + ":" + c.Nonce + ":" + nc + ":" + cnonce + ":" + c.QOP + ":" + ha2)
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// digestClient 维护 Digest 挑战状态并生成 Authorization 头。
//
// 并发安全：同一设备的多个频率分组会并行调用 Read
// （插件侧 SerialExclusive 为 false，见 driver.go），而 nc 计数器是共享状态。
type digestClient struct {
	user string
	pass string

	mu     sync.Mutex
	cha    *digestChallenge
	nc     uint64
	cnonce string
}

func newDigestClient(user, pass string) *digestClient {
	return &digestClient{user: user, pass: pass}
}

// setChallenge 记录服务端发来的挑战。
//
// nonce 变化时重置 nc 与 cnonce —— 这两者都是**绑定在 nonce 上的**：
// nc 从 00000001 重新开始，cnonce 换新。nonce 没变时（例如 stale=true 但
// nonce 恰好没换）保持原状态继续递增 nc，这是对的：对端认为这个 nonce
// 还有效，重放计数就不该倒回去。
func (d *digestClient) setChallenge(c *digestChallenge) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cha != nil && d.cha.Nonce == c.Nonce && d.cha.Realm == c.Realm {
		d.cha = c
		return
	}
	d.cha = c
	d.nc = 0
	d.cnonce = newCnonce()
}

// challenge 返回当前挑战（未握手时为 nil）。
func (d *digestClient) challenge() *digestChallenge {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cha
}

// authorize 为请求装配 Authorization: Digest 头。
//
// uri 取 req.URL.RequestURI() 而**不是** req.URL.Path：摘要里的 uri 必须与
// 实际发出的请求行逐字节相同，而本插件的每条请求都带 ?json=1 查询串 ——
// 用 Path 会让服务端算出的 HA2 与客户端不一致，结果是永久 401 死循环。
// 这是 digest 实现里最常见、也最难自查的一个坑。
func (d *digestClient) authorize(req *http.Request) {
	d.mu.Lock()
	c := d.cha
	if c == nil {
		d.mu.Unlock()
		return
	}
	d.nc++
	nc := fmt.Sprintf("%08x", d.nc) // nc 必须是 8 位十六进制，不足补前导零
	cnonce := d.cnonce
	d.mu.Unlock()

	uri := req.URL.RequestURI()
	resp := digestResponse(d.user, d.pass, c, req.Method, uri, nc, cnonce)

	var b strings.Builder
	b.WriteString(`Digest username="`)
	b.WriteString(quote(d.user))
	b.WriteString(`", realm="`)
	b.WriteString(quote(c.Realm))
	b.WriteString(`", nonce="`)
	b.WriteString(quote(c.Nonce))
	b.WriteString(`", uri="`)
	b.WriteString(quote(uri))
	b.WriteString(`", response="`)
	b.WriteString(resp)
	b.WriteByte('"')
	if c.Opaque != "" {
		b.WriteString(`, opaque="`)
		b.WriteString(quote(c.Opaque))
		b.WriteByte('"')
	}
	b.WriteString(", algorithm=MD5")
	if c.QOP != "" {
		b.WriteString(", qop=")
		b.WriteString(c.QOP)
		b.WriteString(", nc=")
		b.WriteString(nc)
		b.WriteString(`, cnonce="`)
		b.WriteString(cnonce)
		b.WriteByte('"')
	}
	req.Header.Set("Authorization", b.String())
}

// quote 转义摘要头里的双引号与反斜杠，避免拼出一个畸形的头。
func quote(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// newCnonce 生成一个客户端随机数。
// 每个 nonce 固定一个（见 setChallenge），而不是每请求一个 —— curl 就是这么
// 做的，且有固件要求同一 nonce 期间 cnonce 恒定，每请求换新会一直 401。
func newCnonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 在本进程里不可用属于极端情况；退化为固定串仍能工作
		// （摘要不需要密码学强度的随机数，只需要与其它客户端不同）。
		return "abb-fallback"
	}
	return hex.EncodeToString(b[:])
}
