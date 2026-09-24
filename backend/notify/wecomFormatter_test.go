// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWeComMarkdownPayload(t *testing.T) {
	cfg := cfgOf(TypeWeCom, "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=k")
	f, err := NewFormatter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.Format(testOfflineEvent())
	if err != nil {
		t.Fatal(err)
	}
	if req.URL != cfg.URL {
		t.Fatalf("wecom url should be untouched (no sign support), got %s", req.URL)
	}

	var payload struct {
		MsgType  string `json:"msgtype"`
		Markdown struct {
			Content string `json:"content"`
		} `json:"markdown"`
		At map[string]any `json:"at"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MsgType != "markdown" {
		t.Fatalf("expected markdown, got %q", payload.MsgType)
	}
	if !strings.HasPrefix(payload.Markdown.Content, "## 【断联报警】设备 dev1") {
		t.Fatalf("unexpected content head:\n%s", payload.Markdown.Content)
	}
	if !strings.Contains(payload.Markdown.Content, "> **目标名称**：dev1") {
		t.Fatalf("unexpected content:\n%s", payload.Markdown.Content)
	}
	// 企微不支持 @，报文里不该出现 at 字段
	if payload.At != nil {
		t.Fatalf("wecom payload should not carry at, got %v", payload.At)
	}
}

func TestWeComTextModeRespectsSmallerLimit(t *testing.T) {
	cfg := cfgOf(TypeWeCom, "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=k")
	cfg.MsgType = MsgTypeText

	ev := testOfflineEvent()
	ev.Content = strings.Repeat("超长内容", 500) // 远超 2048 字节

	f, err := NewFormatter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.Format(ev)
	if err != nil {
		t.Fatal(err)
	}

	var payload struct {
		MsgType string `json:"msgtype"`
		Text    struct {
			Content string `json:"content"`
		} `json:"text"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.MsgType != "text" {
		t.Fatalf("expected text, got %q", payload.MsgType)
	}
	if got := len(payload.Text.Content); got > wecomTextMaxBytes {
		t.Fatalf("text content %d bytes exceeds wecom limit %d", got, wecomTextMaxBytes)
	}
	if !utf8.ValidString(payload.Text.Content) {
		t.Fatal("truncated text is not valid UTF-8")
	}
	if !strings.HasSuffix(payload.Text.Content, truncSuffix) {
		t.Fatal("truncated text should carry the truncation marker")
	}
}

// 企微 markdown 上限 4096 字节，超限必须截断且不切断汉字。
func TestWeComMarkdownTruncation(t *testing.T) {
	cfg := cfgOf(TypeWeCom, "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=k")
	f, err := NewFormatter(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ev := testOfflineEvent()
	ev.Content = strings.Repeat("超长内容", 1000)

	req, err := f.Format(ev)
	if err != nil {
		t.Fatal(err)
	}

	var payload struct {
		Markdown struct {
			Content string `json:"content"`
		} `json:"markdown"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if got := len(payload.Markdown.Content); got > wecomMarkdownMaxBytes {
		t.Fatalf("markdown content %d bytes exceeds wecom limit %d", got, wecomMarkdownMaxBytes)
	}
	if !utf8.ValidString(payload.Markdown.Content) {
		t.Fatal("truncated markdown is not valid UTF-8")
	}
	if !strings.HasSuffix(payload.Markdown.Content, truncSuffix) {
		t.Fatal("truncated markdown should carry the truncation marker")
	}
	// 截断发生在 markdown 装饰之后，故头部结构应完整保留
	if !strings.HasPrefix(payload.Markdown.Content, "## 【断联报警】设备 dev1") {
		t.Fatalf("markdown head was damaged by truncation:\n%s", payload.Markdown.Content)
	}
}

// 未超限时不加截断标记。
func TestWeComNoTruncationWhenShort(t *testing.T) {
	cfg := cfgOf(TypeWeCom, "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=k")
	f, _ := NewFormatter(cfg)
	req, _ := f.Format(testOfflineEvent())

	var payload struct {
		Markdown struct {
			Content string `json:"content"`
		} `json:"markdown"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload.Markdown.Content, truncSuffix) {
		t.Fatal("short message should not be marked as truncated")
	}
}

// 企微恢复通知走同一套渲染。
func TestWeComRecoverEvent(t *testing.T) {
	cfg := cfgOf(TypeWeCom, "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=k")
	f, _ := NewFormatter(cfg)
	req, err := f.Format(testRecoverEvent())
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Markdown struct {
			Content string `json:"content"`
		} `json:"markdown"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload.Markdown.Content, "【恢复通知】推送通道 mqtt1") {
		t.Fatalf("unexpected recover content:\n%s", payload.Markdown.Content)
	}
	if !strings.Contains(payload.Markdown.Content, "**恢复时间**：2026-09-24 10:05:00") {
		t.Fatalf("recover content should carry 恢复时间:\n%s", payload.Markdown.Content)
	}
}
