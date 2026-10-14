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

// 飞书加签与钉钉的关键差异：HMAC 的 key 是 stringToSign 本身、data 为空；
// 时间戳是秒；且 timestamp/sign 在 body 顶层，不在 URL 查询串上。
func TestFeishuSignInBodyNotQuery(t *testing.T) {
	const secret = "feishu-secret"
	cfg := cfgOf(TypeFeishu, "https://open.feishu.cn/open-apis/bot/v2/hook/tok")
	cfg.Secret = secret

	f, err := NewFormatter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.Format(testOfflineEvent())
	if err != nil {
		t.Fatal(err)
	}

	// URL 必须保持原样：加签参数若拼在查询串上验签会失败
	u, err := url.Parse(req.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.RawQuery != "" {
		t.Fatalf("feishu signature must not go into the query string, got: %s", u.RawQuery)
	}

	var payload struct {
		Timestamp string `json:"timestamp"`
		Sign      string `json:"sign"`
		MsgType   string `json:"msg_type"`
		Content   struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}

	if len(payload.Timestamp) != 10 {
		t.Fatalf("feishu timestamp should be seconds (10 digits), got %q", payload.Timestamp)
	}
	ts, err := strconv.ParseInt(payload.Timestamp, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if delta := time.Since(time.Unix(ts, 0)); delta > time.Minute || delta < -time.Minute {
		t.Fatalf("timestamp is not near now: delta=%s", delta)
	}

	// 独立重算：key = "<ts>\n<secret>"，data 为空
	mac := hmac.New(sha256.New, []byte(fmt.Sprintf("%d\n%s", ts, secret)))
	mac.Write(nil)
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if payload.Sign != want {
		t.Fatalf("sign mismatch:\n got %q\nwant %q", payload.Sign, want)
	}

	// 反向证明：用钉钉算法算出的值不应等于飞书签名，避免两者被混淆实现
	dingStyle := dingTalkSign(secret, ts*1000)
	if dingStyle == payload.Sign {
		t.Fatal("feishu signature unexpectedly equals the dingtalk-style signature")
	}

	if payload.MsgType != "text" {
		t.Fatalf("expected text msg_type, got %q", payload.MsgType)
	}
	if !strings.HasPrefix(payload.Content.Text, "【断联报警】设备 dev1") {
		t.Fatalf("unexpected text content:\n%s", payload.Content.Text)
	}
}

func TestFeishuNoSignWithoutSecret(t *testing.T) {
	cfg := cfgOf(TypeFeishu, "https://open.feishu.cn/open-apis/bot/v2/hook/tok")
	f, _ := NewFormatter(cfg)
	req, err := f.Format(testOfflineEvent())
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["timestamp"]; ok {
		t.Fatal("timestamp should be omitted when no secret is configured")
	}
	if _, ok := payload["sign"]; ok {
		t.Fatal("sign should be omitted when no secret is configured")
	}
}

func TestFeishuCardPayload(t *testing.T) {
	cfg := cfgOf(TypeFeishu, "https://open.feishu.cn/open-apis/bot/v2/hook/tok")
	cfg.MsgType = MsgTypeCard
	cfg.AtAll = 1

	f, err := NewFormatter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.Format(testOfflineEvent())
	if err != nil {
		t.Fatal(err)
	}

	var payload struct {
		MsgType string `json:"msg_type"`
		Card    struct {
			Header struct {
				Title struct {
					Content string `json:"content"`
				} `json:"title"`
				Template string `json:"template"`
			} `json:"header"`
			Elements []struct {
				Tag  string `json:"tag"`
				Text struct {
					Content string `json:"content"`
				} `json:"text"`
			} `json:"elements"`
		} `json:"card"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MsgType != "interactive" {
		t.Fatalf("expected interactive, got %q", payload.MsgType)
	}
	if payload.Card.Header.Template != "red" {
		t.Fatalf("offline card should be red, got %q", payload.Card.Header.Template)
	}
	if payload.Card.Header.Title.Content != "【断联报警】设备 dev1" {
		t.Fatalf("unexpected card title: %q", payload.Card.Header.Title.Content)
	}
	body := payload.Card.Elements[0].Text.Content
	if !strings.Contains(body, "**目标名称**：dev1") {
		t.Fatalf("card body missing rows:\n%s", body)
	}
	if !strings.Contains(body, `<at user_id="all"></at>`) {
		t.Fatalf("card body should carry the at-all tag:\n%s", body)
	}
}

// 恢复通知的卡片用绿色，与断联的红色区分。
func TestFeishuCardRecoverIsGreen(t *testing.T) {
	cfg := cfgOf(TypeFeishu, "https://open.feishu.cn/open-apis/bot/v2/hook/tok")
	cfg.MsgType = MsgTypeCard
	f, _ := NewFormatter(cfg)
	req, err := f.Format(testRecoverEvent())
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Card struct {
			Header struct {
				Template string `json:"template"`
			} `json:"header"`
		} `json:"card"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Card.Header.Template != "green" {
		t.Fatalf("recover card should be green, got %q", payload.Card.Header.Template)
	}
}

// 飞书 @ 用 open_id，与钉钉的手机号语法不同。
func TestFeishuMentionUsesOpenID(t *testing.T) {
	cfg := cfgOf(TypeFeishu, "https://open.feishu.cn/open-apis/bot/v2/hook/tok")
	cfg.AtList = `["ou_abc"]`

	f, err := NewFormatter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.Format(testOfflineEvent())
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload.Content.Text, `<at user_id="ou_abc"></at>`) {
		t.Fatalf("text should carry the open_id mention:\n%s", payload.Content.Text)
	}
}
