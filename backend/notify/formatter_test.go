// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"iot-gateway/alarm"
	"iot-gateway/model/po"
)

// testOfflineEvent 标准的设备断联事件。
func testOfflineEvent() alarm.Event {
	return alarm.Event{
		AlarmID:        "a-1",
		TargetID:       "dev-1",
		TargetName:     "dev1",
		TargetType:     alarm.TypeDevice,
		AlarmType:      alarm.TypeOffline,
		Level:          "warning",
		Content:        "设备 dev1 断联",
		Status:         alarm.StatusActive,
		FirstOccurTime: "2026-09-24 10:00:00",
		LastOccurTime:  "2026-09-24 10:00:00",
		OccurredAt:     time.Now(),
	}
}

// testRecoverEvent 标准的通道恢复事件。
func testRecoverEvent() alarm.Event {
	return alarm.Event{
		AlarmID:        "a-2",
		TargetID:       "ch-1",
		TargetName:     "mqtt1",
		TargetType:     alarm.TypeChannel,
		AlarmType:      alarm.TypeRecover,
		Level:          "warning",
		Content:        "推送通道 mqtt1 恢复通信",
		Status:         alarm.StatusCleared,
		FirstOccurTime: "2026-09-24 10:05:00",
		LastOccurTime:  "2026-09-24 10:05:00",
		ClearTime:      "2026-09-24 10:05:00",
		OccurredAt:     time.Now(),
	}
}

func cfgOf(typ, url string) *po.AlarmWebhook {
	return &po.AlarmWebhook{ID: "w-1", Name: "测试群", Type: typ, URL: url}
}

func TestValidateConfigRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		cfg  *po.AlarmWebhook
	}{
		{"unsupported type", cfgOf("telegram", "https://example.com/hook")},
		{"empty url", cfgOf(TypeDingTalk, "")},
		{"non-http scheme", cfgOf(TypeDingTalk, "file:///etc/passwd")},
		{"missing host", cfgOf(TypeDingTalk, "https:///robot/send")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateConfig(c.cfg); err == nil {
				t.Fatalf("expected validation error for %s, got nil", c.name)
			}
		})
	}
}

// 企微群机器人不支持 @：应在保存时明确报错，而不是静默忽略
// （静默会让值班人员以为已经 @ 到人）。
func TestValidateConfigRejectsAtForWeCom(t *testing.T) {
	cfg := cfgOf(TypeWeCom, "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=k")
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("plain wecom config should be valid, got %v", err)
	}

	cfg.AtAll = 1
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for wecom atAll, got nil")
	}

	cfg.AtAll = 0
	cfg.AtList = `["13800000000"]`
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for wecom atList, got nil")
	}
}

func TestValidateConfigRejectsMsgTypeNotSupported(t *testing.T) {
	cfg := cfgOf(TypeDingTalk, "https://oapi.dingtalk.com/robot/send?access_token=t")
	cfg.MsgType = MsgTypeCard // 钉钉没有卡片格式
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for dingtalk card msgType, got nil")
	}

	// 飞书支持 card
	cfg = cfgOf(TypeFeishu, "https://open.feishu.cn/open-apis/bot/v2/hook/x")
	cfg.MsgType = MsgTypeCard
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("feishu card msgType should be valid, got %v", err)
	}
}

func TestSupportedTypesAndMetasConsistent(t *testing.T) {
	types := SupportedTypes()
	if len(types) != 4 {
		t.Fatalf("expected 4 supported types, got %d: %v", len(types), types)
	}
	metas := SupportedTypeMetas()
	if len(metas) != len(types) {
		t.Fatalf("metas/type count mismatch: %d vs %d", len(metas), len(types))
	}
	// 每种类型都必须有元信息，且默认报文格式合法
	for _, m := range metas {
		if m.Label == "" {
			t.Fatalf("type %s missing label", m.Type)
		}
		if m.DefaultMsgType != "" && !contains(m.MsgTypes, m.DefaultMsgType) {
			t.Fatalf("type %s default msgType %q not in %v", m.Type, m.DefaultMsgType, m.MsgTypes)
		}
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func TestBuildMessageRows(t *testing.T) {
	msg := buildMessage(testOfflineEvent(), false, nil)
	if msg.Title != "【断联报警】设备 dev1" {
		t.Fatalf("unexpected title: %q", msg.Title)
	}
	if len(msg.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d: %v", len(msg.Rows), msg.Rows)
	}
	if msg.Rows[4][0] != "首次发生" {
		t.Fatalf("offline message should carry 首次发生, got %q", msg.Rows[4][0])
	}

	// 恢复消息：展示恢复时间，且不重复展示同值的首次发生
	rec := buildMessage(testRecoverEvent(), false, nil)
	if rec.Title != "【恢复通知】推送通道 mqtt1" {
		t.Fatalf("unexpected recover title: %q", rec.Title)
	}
	if rec.Rows[4][0] != "恢复时间" || rec.Rows[4][1] != "2026-09-24 10:05:00" {
		t.Fatalf("recover message should carry 恢复时间, got %v", rec.Rows[4])
	}
	if !rec.IsRecover {
		t.Fatal("recover message should set IsRecover")
	}
}

func TestPlainTextContainsAllRows(t *testing.T) {
	msg := buildMessage(testOfflineEvent(), false, nil)
	text := msg.PlainText()

	if !strings.HasPrefix(text, "【断联报警】设备 dev1\n") {
		t.Fatalf("unexpected text head: %q", text)
	}
	for _, want := range []string{"目标类型：设备", "目标名称：dev1", "报警级别：warning", "报警内容：设备 dev1 断联", "首次发生：2026-09-24 10:00:00"} {
		if !strings.Contains(text, want) {
			t.Fatalf("plain text missing %q, got:\n%s", want, text)
		}
	}
}

func TestMarkdownLinesPrefix(t *testing.T) {
	msg := buildMessage(testOfflineEvent(), false, nil)

	ding := msg.MarkdownLines("- ")
	if !strings.Contains(ding, "- **目标名称**：dev1") {
		t.Fatalf("unexpected dingtalk markdown lines:\n%s", ding)
	}
	wecom := msg.MarkdownLines("> ")
	if !strings.Contains(wecom, "> **目标名称**：dev1") {
		t.Fatalf("unexpected wecom markdown lines:\n%s", wecom)
	}
	feishu := msg.MarkdownLines("")
	if !strings.HasPrefix(feishu, "**目标类型**：设备") {
		t.Fatalf("unexpected feishu markdown lines:\n%s", feishu)
	}
}

func TestTruncateUTF8BytesKeepsRuneBoundary(t *testing.T) {
	// 全中文（每字 3 字节），构造一个远超上限的串
	s := strings.Repeat("报警", 3000)
	if len(s) <= 4096 {
		t.Fatalf("test fixture too short: %d bytes", len(s))
	}

	got := truncateUTF8Bytes(s, 4096)
	if len(got) > 4096 {
		t.Fatalf("truncated result exceeds cap: %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncated result is not valid UTF-8 (rune was split)")
	}
	if !strings.HasSuffix(got, truncSuffix) {
		t.Fatalf("truncated result should carry the truncation marker, got tail: %q", tail(got, 20))
	}

	// 未超限时不截断、不加标记
	short := "短消息"
	if got := truncateUTF8Bytes(short, 4096); got != short {
		t.Fatalf("short string should be untouched, got %q", got)
	}
}

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

func TestParseAtList(t *testing.T) {
	if got := parseAtList(&po.AlarmWebhook{AtList: ""}); got != nil {
		t.Fatalf("empty at_list should yield nil, got %v", got)
	}
	if got := parseAtList(&po.AlarmWebhook{AtList: "[]"}); got != nil {
		t.Fatalf("empty json array should yield nil, got %v", got)
	}
	if got := parseAtList(&po.AlarmWebhook{AtList: "not-json"}); got != nil {
		t.Fatalf("invalid json should yield nil, got %v", got)
	}
	got := parseAtList(&po.AlarmWebhook{AtList: `["13800000000", " ", "ou_x"]`})
	if len(got) != 2 || got[0] != "13800000000" || got[1] != "ou_x" {
		t.Fatalf("unexpected parsed at_list: %v", got)
	}
}
