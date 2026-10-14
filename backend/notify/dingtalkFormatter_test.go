// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDingTalkMarkdownPayload(t *testing.T) {
	cfg := cfgOf(TypeDingTalk, "https://oapi.dingtalk.com/robot/send?access_token=tok")
	f, err := NewFormatter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.Format(testOfflineEvent())
	if err != nil {
		t.Fatal(err)
	}

	if req.Method != "POST" || req.Platform != TypeDingTalk {
		t.Fatalf("unexpected request meta: %+v", req)
	}
	if ct := req.Headers["Content-Type"]; !strings.Contains(ct, "application/json") {
		t.Fatalf("unexpected content type: %q", ct)
	}

	var payload struct {
		MsgType  string `json:"msgtype"`
		Markdown struct {
			Title string `json:"title"`
			Text  string `json:"text"`
		} `json:"markdown"`
		At struct {
			AtMobiles []string `json:"atMobiles"`
			AtUserIds []string `json:"atUserIds"`
			IsAtAll   bool     `json:"isAtAll"`
		} `json:"at"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v (body=%s)", err, req.Body)
	}

	if payload.MsgType != "markdown" {
		t.Fatalf("expected markdown, got %q", payload.MsgType)
	}
	if payload.Markdown.Title != "【断联报警】设备 dev1" {
		t.Fatalf("unexpected title: %q", payload.Markdown.Title)
	}
	for _, want := range []string{"### 【断联报警】设备 dev1", "- **目标名称**：dev1", "- **报警内容**：设备 dev1 断联"} {
		if !strings.Contains(payload.Markdown.Text, want) {
			t.Fatalf("markdown text missing %q, got:\n%s", want, payload.Markdown.Text)
		}
	}
	// 空的 @ 列表必须是数组而不是 null
	if payload.At.AtMobiles == nil {
		t.Fatal("atMobiles should serialize as [] not null")
	}
	if payload.At.IsAtAll {
		t.Fatal("isAtAll should be false by default")
	}
}

func TestDingTalkTextModeAndMentions(t *testing.T) {
	cfg := cfgOf(TypeDingTalk, "https://oapi.dingtalk.com/robot/send?access_token=tok")
	cfg.MsgType = MsgTypeText
	cfg.AtAll = 1
	cfg.AtList = `["13800000000"]`

	f, err := NewFormatter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.Format(testOfflineEvent())
	if err != nil {
		t.Fatal(err)
	}

	var payload struct {
		MsgType string `json:"msgtype"`
		Text    struct {
			Content string `json:"content"`
		} `json:"text"`
		Markdown map[string]any `json:"markdown"`
		At       struct {
			AtMobiles []string `json:"atMobiles"`
			IsAtAll   bool     `json:"isAtAll"`
		} `json:"at"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MsgType != "text" || len(payload.Markdown) != 0 {
		t.Fatalf("expected plain text payload, got %s", req.Body)
	}
	// 钉钉 @ 双写：正文里要有字面量，at 字段也要带上
	if !strings.Contains(payload.Text.Content, "@13800000000") || !strings.Contains(payload.Text.Content, "@所有人") {
		t.Fatalf("text content should carry literal mentions, got:\n%s", payload.Text.Content)
	}
	if len(payload.At.AtMobiles) != 1 || payload.At.AtMobiles[0] != "13800000000" {
		t.Fatalf("unexpected atMobiles: %v", payload.At.AtMobiles)
	}
	if !payload.At.IsAtAll {
		t.Fatal("isAtAll should be true")
	}
}

// 钉钉加签：timestamp 为毫秒、sign 为 HMAC-SHA256(secret, ts+"\n"+secret)，
// 且二者拼在查询串上（与飞书放在 body 里不同）。
func TestDingTalkSignInQuery(t *testing.T) {
	const secret = "dingtalk-sign-secret"
	cfg := cfgOf(TypeDingTalk, "https://oapi.dingtalk.com/robot/send?access_token=tok")
	cfg.Secret = secret

	f, err := NewFormatter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.Format(testOfflineEvent())
	if err != nil {
		t.Fatal(err)
	}

	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if got := q.Get("access_token"); got != "tok" {
		t.Fatalf("access_token should be preserved, got %q", got)
	}

	tsStr := q.Get("timestamp")
	if len(tsStr) != 13 {
		t.Fatalf("dingtalk timestamp should be milliseconds (13 digits), got %q", tsStr)
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if delta := time.Since(time.UnixMilli(ts)); delta > time.Minute || delta < -time.Minute {
		t.Fatalf("timestamp is not near now: delta=%s", delta)
	}

	// 独立重算期望签名，避免与实现共用同一份错误逻辑
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d\n%s", ts, secret)))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if got := q.Get("sign"); got != want {
		t.Fatalf("sign mismatch:\n got %q\nwant %q", got, want)
	}
}

// base64 签名里的 + 必须被转义成 %2B —— 这正是拼查询串时必须用
// url.Values.Encode 而不是字符串拼接的原因。
//
// 签名含 '+' 与否取决于时间戳，无法预先构造，故遍历若干密钥，
// 对真正出现 '+' 的那一次断言转义结果（出现概率随尝试次数指数上升）。
func TestDingTalkSignEscapesPlusInQuery(t *testing.T) {
	const baseURL = "https://oapi.dingtalk.com/robot/send?access_token=tok"

	exercised := false
	for i := 0; i < 32 && !exercised; i++ {
		cfg := cfgOf(TypeDingTalk, baseURL)
		cfg.Secret = fmt.Sprintf("escape-secret-%d", i)

		f, err := NewFormatter(cfg)
		if err != nil {
			t.Fatal(err)
		}
		req, err := f.Format(testOfflineEvent())
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(req.URL)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(u.Query().Get("sign"), "+") {
			continue // 本次签名没有 '+'，换一个密钥
		}
		exercised = true

		if strings.Contains(u.RawQuery, "+") {
			t.Fatalf("raw query contains an unescaped '+': %s", u.RawQuery)
		}
		if !strings.Contains(u.RawQuery, "%2B") {
			t.Fatalf("expected escaped '+' (%%2B) in raw query: %s", u.RawQuery)
		}
	}

	if !exercised {
		t.Fatal("32 次尝试都未构造出含 '+' 的签名，转义分支未被覆盖")
	}
}

// 不配 secret 时不应带任何加签参数。
func TestDingTalkNoSignWithoutSecret(t *testing.T) {
	cfg := cfgOf(TypeDingTalk, "https://oapi.dingtalk.com/robot/send?access_token=tok")
	f, err := NewFormatter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.Format(testOfflineEvent())
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(req.URL)
	if u.Query().Get("sign") != "" || u.Query().Get("timestamp") != "" {
		t.Fatalf("no sign expected without secret, got query: %s", u.RawQuery)
	}
}
