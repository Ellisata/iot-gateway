// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"encoding/json"
	"testing"
)

// TestMigration032MQTTTopicTemplateSelect 钉住 MQTT 主题字段在真实迁移路径上
// 变成“模板下拉 + 手动输入”：select 类型、allowCreate、两个模板选项。
func TestMigration032MQTTTopicTemplateSelect(t *testing.T) {
	db := newTestDB(t)

	var raw string
	if err := db.Table("push_channel_form").Select("form_json").
		Where("name = ?", "mqtt").Scan(&raw).Error; err != nil {
		t.Fatalf("query mqtt form: %v", err)
	}

	var form struct {
		// qos 的 options.value 是数字，不能用一个统一的字符串选项结构解析整个表单。
		Rule    []json.RawMessage `json:"rule"`
		Options struct {
			Language map[string]map[string]string `json:"language"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(raw), &form); err != nil {
		t.Fatalf("unmarshal form: %v", err)
	}

	type rule struct {
		Type    string         `json:"type"`
		Field   string         `json:"field"`
		Props   map[string]any `json:"props"`
		Options []struct {
			Label string `json:"label"`
			Value string `json:"value"`
		} `json:"options"`
	}
	var topic *rule
	for _, rawRule := range form.Rule {
		var r rule
		if err := json.Unmarshal(rawRule, &r); err != nil {
			t.Fatalf("unmarshal rule: %v", err)
		}
		if r.Field == "topic" {
			topic = &r
			break
		}
	}
	if topic == nil {
		t.Fatal("mqtt 表单缺少 topic 字段")
	}
	if topic.Type != "select" {
		t.Fatalf("topic type = %q, want select", topic.Type)
	}
	if allow, _ := topic.Props["allowCreate"].(bool); !allow {
		t.Fatalf("topic props.allowCreate = %v, want true (手动输入需要)", topic.Props["allowCreate"])
	}
	if filterable, _ := topic.Props["filterable"].(bool); !filterable {
		t.Fatalf("topic props.filterable = %v, want true", topic.Props["filterable"])
	}

	wantValues := map[string]bool{
		"iot/${thingModelCode}/${deviceCode}/${groupCode}": true,
		"iot/${deviceCode}/${groupCode}":                   true,
	}
	if len(topic.Options) != len(wantValues) {
		t.Fatalf("topic options = %+v, want %d 个模板", topic.Options, len(wantValues))
	}
	for _, opt := range topic.Options {
		if !wantValues[opt.Value] || opt.Label != opt.Value {
			t.Errorf("topic option = %+v, want 模板值同时作为 label", opt)
		}
	}

	if got := form.Options.Language["zh-cn"]["topic_placeholder"]; got != "选择模板或手动输入；多个主题用英文逗号分隔" {
		t.Errorf("zh-cn topic_placeholder = %q", got)
	}
	if got := form.Options.Language["en"]["topic_placeholder"]; got != "Pick a template or type a topic, separate multiple topics with commas" {
		t.Errorf("en topic_placeholder = %q", got)
	}
}
