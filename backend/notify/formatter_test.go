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

	// 恢复消息：展示恢复时间，且不重复展示同值的首次发生；
	// 标题带原始报警类别（通道恢复 = 断联报警的恢复）
	rec := buildMessage(testRecoverEvent(), false, nil)
	if rec.Title != "【断联报警-恢复通知】推送通道 mqtt1" {
		t.Fatalf("unexpected recover title: %q", rec.Title)
	}
	if rec.Rows[4][0] != "恢复时间" || rec.Rows[4][1] != "2026-09-24 10:05:00" {
		t.Fatalf("recover message should carry 恢复时间, got %v", rec.Rows[4])
	}
	if !rec.IsRecover {
		t.Fatal("recover message should set IsRecover")
	}
}

// TestAlarmTitlePrefix 钉住标题前缀：恢复记录靠 target_type 反推原始报警类别。
func TestAlarmTitlePrefix(t *testing.T) {
	cases := []struct {
		name      string
		alarmType string
		target    string
		want      string
	}{
		{"断联报警", alarm.TypeOffline, alarm.TypeDevice, "【断联报警】"},
		{"质量报警", alarm.TypeQuality, alarm.TypePoint, "【质量报警】"},
		{"超限报警", alarm.TypeThreshold, alarm.TypePointThreshold, "【超限报警】"},
		{"设备恢复", alarm.TypeRecover, alarm.TypeDevice, "【断联报警-恢复通知】"},
		{"通道恢复", alarm.TypeRecover, alarm.TypeChannel, "【断联报警-恢复通知】"},
		{"质量恢复", alarm.TypeRecover, alarm.TypePoint, "【质量报警-恢复通知】"},
		{"超限恢复", alarm.TypeRecover, alarm.TypePointThreshold, "【超限报警-恢复通知】"},
		// 未知类别宁可不带前缀，也不要顶着错误的类别发出去
		{"未知目标类型的恢复", alarm.TypeRecover, "snmp", "【恢复通知】"},
		{"未知报警类型回落断联", "something-new", alarm.TypeDevice, "【断联报警】"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := alarmTitlePrefix(c.alarmType, c.target); got != c.want {
				t.Fatalf("前缀期望 %q，实际 %q", c.want, got)
			}
		})
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

// TestBuildTestEventWording 钉住「测试发送」预览的文案。
//
// 预览与真实报警共用 alarm 的 Spec（DeviceSpec / ChannelSpec / PointSpec），
// 这里逐项断言，Spec 的一处改动若让两边说法分叉就会在这里暴露。
func TestBuildTestEventWording(t *testing.T) {
	cases := []struct {
		name         string
		opts         TestEventOptions
		wantTarget   string
		wantAlarm    string
		wantName     string
		wantContent  string
		wantTitleHas string
	}{
		{
			name:         "零值回落设备断联",
			opts:         TestEventOptions{},
			wantTarget:   alarm.TypeDevice,
			wantAlarm:    alarm.TypeOffline,
			wantName:     "测试设备",
			wantContent:  "设备 测试设备 断联",
			wantTitleHas: "【断联报警】设备 测试设备",
		},
		{
			name:         "推送通道",
			opts:         TestEventOptions{TargetType: alarm.TypeChannel},
			wantTarget:   alarm.TypeChannel,
			wantAlarm:    alarm.TypeOffline,
			wantName:     "测试设备",
			wantContent:  "推送通道 测试设备 断联",
			wantTitleHas: "【断联报警】推送通道 测试设备",
		},
		{
			// 点位自动取 quality：预览要反映该目标**真正**会收到的那种报警
			name:         "点位质量报警",
			opts:         TestEventOptions{TargetType: alarm.TypePoint},
			wantTarget:   alarm.TypePoint,
			wantAlarm:    alarm.TypeQuality,
			wantName:     "测试点位",
			wantContent:  "点位 测试点位 采集质量异常",
			wantTitleHas: "【质量报警】点位 测试点位",
		},
		{
			name:         "点位恢复",
			opts:         TestEventOptions{TargetType: alarm.TypePoint, AlarmType: alarm.TypeRecover},
			wantTarget:   alarm.TypePoint,
			wantAlarm:    alarm.TypeRecover,
			wantName:     "测试点位",
			wantContent:  "点位 测试点位 恢复采集",
			wantTitleHas: "【质量报警-恢复通知】点位 测试点位",
		},
		{
			// 条件报警是独立 target_type，标签仍用「点位」，靠标题前缀区分。
			// 谓语是通用「告警」而非「超限」：条件同时覆盖数值区间、布尔状态与文本集合，
			// 「超限」对后两者是事实错误。具体条件由正文括号里的 detail 说清。
			name:         "点位条件报警",
			opts:         TestEventOptions{TargetType: alarm.TypePointThreshold},
			wantTarget:   alarm.TypePointThreshold,
			wantAlarm:    alarm.TypeThreshold,
			wantName:     "测试点位",
			wantContent:  "点位 测试点位 告警",
			wantTitleHas: "【超限报警】点位 测试点位",
		},
		{
			name:         "点位条件恢复",
			opts:         TestEventOptions{TargetType: alarm.TypePointThreshold, AlarmType: alarm.TypeRecover},
			wantTarget:   alarm.TypePointThreshold,
			wantAlarm:    alarm.TypeRecover,
			wantName:     "测试点位",
			wantContent:  "点位 测试点位 恢复",
			wantTitleHas: "【超限报警-恢复通知】点位 测试点位",
		},
		{
			name:         "设备恢复沿用断联那套措辞",
			opts:         TestEventOptions{AlarmType: alarm.TypeRecover},
			wantTarget:   alarm.TypeDevice,
			wantAlarm:    alarm.TypeRecover,
			wantName:     "测试设备",
			wantContent:  "设备 测试设备 恢复通信",
			wantTitleHas: "【断联报警-恢复通知】设备 测试设备",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := BuildTestEvent(c.opts)
			if ev.TargetType != c.wantTarget || ev.AlarmType != c.wantAlarm {
				t.Fatalf("类型不符：targetType=%s alarmType=%s", ev.TargetType, ev.AlarmType)
			}
			if ev.TargetName != c.wantName || ev.Content != c.wantContent {
				t.Fatalf("文案不符：name=%q content=%q", ev.TargetName, ev.Content)
			}
			if got := buildMessage(ev, false, nil).Title; got != c.wantTitleHas {
				t.Fatalf("标题期望 %q，实际 %q", c.wantTitleHas, got)
			}
		})
	}
}
