// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestDigestResponseRFC2617Vector 用 RFC 2617 §3.5 的官方向量钉住摘要算法。
//
// 摘要算错一位的表现是"密码明明是对的却一直 401"，现场完全无从下手 ——
// 所以必须靠文档里的已知答案来验，而不是靠"看起来能连上"。
func TestDigestResponseRFC2617Vector(t *testing.T) {
	c := &digestChallenge{
		Realm:     "testrealm@host.com",
		Nonce:     "dcd98b7102dd2f0e8b11d0f600bfb0c093",
		QOP:       "auth",
		Algorithm: "MD5",
	}
	got := digestResponse("Mufasa", "Circle Of Life", c, "GET", "/dir/index.html", "00000001", "0a4f113b")
	const want = "6629fae49393a05397450978507c4ef1"
	if got != want {
		t.Errorf("response = %s, RFC 2617 §3.5 的答案是 %s", got, want)
	}
}

// TestDigestResponseNoQOPBranch RFC 2069 分支（无 qop）的输入串必须是
// HA1:nonce:HA2 —— 少三项、也不多三项。
//
// 这里不复述 RFC 2069 的示例答案：那份文档的示例摘要本身有已知勘误
// （与其中间量对不上）。改用"独立拼出期望的哈希输入再算一遍"的方式钉住
// 公式形状；HA1/HA2 的正确性由上面那条 RFC 2617 向量保证。
func TestDigestResponseNoQOPBranch(t *testing.T) {
	c := &digestChallenge{
		Realm:     "testrealm@host.com",
		Nonce:     "dcd98b7102dd2f0e8b11d0f600bfb0c093",
		Algorithm: "MD5",
	}
	got := digestResponse("Mufasa", "Circle Of Life", c, "GET", "/dir/index.html", "00000001", "0a4f113b")

	ha1 := md5hex("Mufasa:testrealm@host.com:Circle Of Life")
	ha2 := md5hex("GET:/dir/index.html")
	want := md5hex(ha1 + ":" + c.Nonce + ":" + ha2)
	if got != want {
		t.Errorf("无 qop 分支 response = %s, 期望 %s（应为 MD5(HA1:nonce:HA2)）", got, want)
	}

	// 与带 qop 的结果必须不同 —— 否则说明分支根本没生效。
	withQOP := *c
	withQOP.QOP = "auth"
	if digestResponse("Mufasa", "Circle Of Life", &withQOP, "GET", "/dir/index.html", "00000001", "0a4f113b") == got {
		t.Error("带 qop 与不带 qop 算出了相同的摘要，qop 分支未生效")
	}
}

func TestParseDigestChallenge(t *testing.T) {
	ok := []struct {
		header string
		want   digestChallenge
	}{
		{
			// 标准形态。
			`Digest realm="ABB", qop="auth", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", opaque="op1"`,
			digestChallenge{Realm: "ABB", Nonce: "dcd98b7102dd2f0e8b11d0f600bfb0c093", Opaque: "op1", QOP: "auth", Algorithm: "MD5"},
		},
		{
			// 值不带引号：部分固件的 nonce/opaque 是裸的十六进制串。
			// 只按带引号解析会拿到一个带引号的 nonce，摘要永远对不上。
			`Digest realm=ABB, qop=auth, nonce=abcdef0123456789`,
			digestChallenge{Realm: "ABB", Nonce: "abcdef0123456789", QOP: "auth", Algorithm: "MD5"},
		},
		{
			// qop 是列表时取 auth（auth-int 我们不支持，但并列出现是合法的）。
			`Digest realm="ABB", nonce="n1", qop="auth-int,auth"`,
			digestChallenge{Realm: "ABB", Nonce: "n1", QOP: "auth", Algorithm: "MD5"},
		},
		{
			// RFC 2069：没有 qop。
			`Digest realm="ABB", nonce="n1"`,
			digestChallenge{Realm: "ABB", Nonce: "n1", Algorithm: "MD5"},
		},
		{
			// 显式 algorithm=MD5。
			`Digest realm="ABB", nonce="n1", algorithm=MD5, qop=auth`,
			digestChallenge{Realm: "ABB", Nonce: "n1", QOP: "auth", Algorithm: "MD5"},
		},
		{
			// 同一个头里并列多个挑战：必须只取 Digest 的参数。
			// 取错的表现是 realm 被 Basic 的覆盖掉，摘要全错。
			`Basic realm="SHOULD-NOT-WIN", Digest realm="ABB", nonce="n1", qop=auth`,
			digestChallenge{Realm: "ABB", Nonce: "n1", QOP: "auth", Algorithm: "MD5"},
		},
		{
			// Digest 在前、Basic 在后：Digest 的参数不能被后面的覆盖/污染。
			`Digest realm="ABB", nonce="n1", qop=auth, Basic realm="OTHER"`,
			digestChallenge{Realm: "ABB", Nonce: "n1", QOP: "auth", Algorithm: "MD5"},
		},
		{
			// 只有方案名、没有参数 —— 不以 nonce 缺失之外的理由失败。
			`Digest realm="ABB", nonce="n1", stale=true`,
			digestChallenge{Realm: "ABB", Nonce: "n1", Algorithm: "MD5", Stale: true},
		},
	}

	for _, c := range ok {
		got, err := parseDigestChallenge(c.header)
		if err != nil {
			t.Errorf("parseDigestChallenge(%q) 失败: %v", c.header, err)
			continue
		}
		if *got != c.want {
			t.Errorf("parseDigestChallenge(%q)\n got  %+v\n want %+v", c.header, *got, c.want)
		}
	}

	bad := []struct{ header, why string }{
		{"", "空头"},
		{`Basic realm="ABB"`, "不是 Digest 挑战"},
		{`Digest realm="ABB"`, "缺 nonce"},
		{`Digest realm="ABB", nonce="n1", qop="auth-int"`, "只支持 qop=auth"},
		{`Digest realm="ABB", nonce="n1", algorithm=MD5-sess`, "不支持 MD5-sess"},
		{`Digest realm="ABB", nonce="n1", algorithm=SHA-256`, "不支持 SHA-256"},
	}
	for _, c := range bad {
		if _, err := parseDigestChallenge(c.header); err == nil {
			t.Errorf("parseDigestChallenge(%q)（%s）应报错", c.header, c.why)
		}
	}
}

// TestParseDigestChallengeUnsupportedAlgorithmIsExplicit 不支持的算法必须是
// **明确报错**，而不是静默按 MD5 算 —— 后者只会换来一个语义不明的 401。
func TestParseDigestChallengeUnsupportedAlgorithmIsExplicit(t *testing.T) {
	_, err := parseDigestChallenge(`Digest realm="a", nonce="n", algorithm=SHA-256`)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Errorf("错误消息里应点名不支持的算法，实际: %v", err)
	}
}

func TestDetectAuthScheme(t *testing.T) {
	cases := []struct {
		header string
		want   authScheme
	}{
		{"", authNone},
		{`Digest realm="a", nonce="n"`, authDigest},
		{`Basic realm="a"`, authBasic},
		// 两者并列时 Digest 优先：它信息更全，且能同时发这两种的控制器都接受。
		{`Basic realm="a", Digest realm="b", nonce="n"`, authDigest},
		{`Negotiate`, authNone},
	}
	for _, c := range cases {
		if got := detectAuthScheme(c.header); got != c.want {
			t.Errorf("detectAuthScheme(%q) = %v, 期望 %v", c.header, got, c.want)
		}
	}
}

// TestSplitHeaderListSkipsQuotedCommas 引号内的逗号不能被当作分隔符。
//
// qop="auth,auth-int" 被切坏的话，qop 会变成半截，后面的 auth-int 会挤进
// 别的字段 —— 每次请求都 401，而响应里没有任何线索指向解析错误。
func TestSplitHeaderListSkipsQuotedCommas(t *testing.T) {
	got := splitHeaderList(`a="x,y", b=z, c="p,q"`)
	want := []string{`a="x,y"`, ` b=z`, ` c="p,q"`}
	if len(got) != len(want) {
		t.Fatalf("splitHeaderList = %q, 期望 %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("片段 %d = %q, 期望 %q", i, got[i], want[i])
		}
	}
}

// TestDigestClientNCAndCnonce 钉住三个计数器约定：
//   - nc 是 8 位十六进制，同 nonce 递增；
//   - nonce 变化时 nc 归 1、cnonce 换新；
//   - cnonce 在同一个 nonce 期间**保持不变**（curl 的行为，有固件依赖它）。
func TestDigestClientNCAndCnonce(t *testing.T) {
	d := newDigestClient("user", "pass")
	ch := &digestChallenge{Realm: "r", Nonce: "n1", QOP: "auth", Algorithm: "MD5"}
	d.setChallenge(ch)

	authOf := func() string {
		req, _ := http.NewRequest(http.MethodGet, "http://host/rw/system?json=1", nil)
		d.authorize(req)
		return req.Header.Get("Authorization")
	}

	a1, a2 := authOf(), authOf()
	if !strings.Contains(a1, "nc=00000001") {
		t.Errorf("首次请求的 nc 应为 00000001，实际头: %s", a1)
	}
	if !strings.Contains(a2, "nc=00000002") {
		t.Errorf("第二次请求的 nc 应为 00000002，实际头: %s", a2)
	}
	if nc := extractParam(a1, "cnonce"); nc == "" || nc != extractParam(a2, "cnonce") {
		t.Errorf("同一 nonce 期间 cnonce 必须恒定，实际 %q vs %q", extractParam(a1, "cnonce"), extractParam(a2, "cnonce"))
	}

	// nonce 变化：nc 归 1、cnonce 换新。
	d.setChallenge(&digestChallenge{Realm: "r", Nonce: "n2", QOP: "auth", Algorithm: "MD5"})
	a3 := authOf()
	if !strings.Contains(a3, "nc=00000001") {
		t.Errorf("换 nonce 后 nc 应归 1，实际头: %s", a3)
	}
	if extractParam(a3, "cnonce") == extractParam(a1, "cnonce") {
		t.Error("换 nonce 后 cnonce 应换新")
	}
}

// TestAuthorizeUsesRequestURIWithQuery 摘要里的 uri 必须带上查询串。
//
// 本插件的每条请求都带 ?json=1，而摘要的 uri 必须与**实际发出的请求行**
// 逐字节相同。用 req.URL.Path（丢掉查询串）会让服务端算出的 HA2 与客户端
// 不一致，结果是永久 401 死循环 —— 这是 digest 实现里最经典的坑。
func TestAuthorizeUsesRequestURIWithQuery(t *testing.T) {
	d := newDigestClient("user", "pass")
	d.setChallenge(&digestChallenge{Realm: "r", Nonce: "n", QOP: "auth", Algorithm: "MD5"})

	req, _ := http.NewRequest(http.MethodGet, "http://host/rw/system?json=1", nil)
	d.authorize(req)

	uri := extractParam(req.Header.Get("Authorization"), "uri")
	if uri != "/rw/system?json=1" {
		t.Errorf("摘要 uri = %q, 期望 /rw/system?json=1（必须含查询串）", uri)
	}

	// 与手工按同 uri 算出的摘要一致，确认 uri 真的参与了计算。
	want := digestResponse("user", "pass",
		&digestChallenge{Realm: "r", Nonce: "n", QOP: "auth", Algorithm: "MD5"},
		http.MethodGet, "/rw/system?json=1", "00000001", extractParam(req.Header.Get("Authorization"), "cnonce"))
	if got := extractParam(req.Header.Get("Authorization"), "response"); got != want {
		t.Errorf("response = %s, 期望 %s", got, want)
	}
}

// TestAuthorizeWithoutChallengeIsNoop 未拿到挑战时不该凭空造一个头。
func TestAuthorizeWithoutChallengeIsNoop(t *testing.T) {
	d := newDigestClient("user", "pass")
	req, _ := http.NewRequest(http.MethodGet, "http://host/rw/system", nil)
	d.authorize(req)
	if h := req.Header.Get("Authorization"); h != "" {
		t.Errorf("未握手时不该有 Authorization 头，实际 %q", h)
	}
}

// TestAuthorizeCarriesOpaque opaque 若存在必须逐字节回传。
func TestAuthorizeCarriesOpaque(t *testing.T) {
	d := newDigestClient("user", "pass")
	d.setChallenge(&digestChallenge{Realm: "r", Nonce: "n", Opaque: "op-123", QOP: "auth", Algorithm: "MD5"})
	req, _ := http.NewRequest(http.MethodGet, "http://host/rw/system", nil)
	d.authorize(req)
	if got := extractParam(req.Header.Get("Authorization"), "opaque"); got != "op-123" {
		t.Errorf("opaque = %q, 期望 op-123", got)
	}
}

// extractParam 从 Authorization: Digest 头里取一个参数。
//
// 先在引号外把 ", " 换成 "&"，再交给 url.ParseQuery（它自己不认引号，
// 所以最后要把值的引号剥掉）。
func extractParam(header, key string) string {
	q, err := url.ParseQuery(strings.ReplaceAll(strings.TrimPrefix(header, "Digest "), ", ", "&"))
	if err != nil {
		return ""
	}
	return strings.Trim(q.Get(key), `"`)
}
