// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"strings"
	"testing"

	"iot-gateway/model/po"
)

func baseRow() *po.MqttSubscription {
	return &po.MqttSubscription{
		ID:          "sub-1",
		Name:        "test",
		Broker:      "127.0.0.1",
		Port:        "1883",
		Topic:       "t",
		QoS:         1,
		MappingJSON: `{"fields":{"deviceId":{"source":"const","value":"d"},"deviceAddressId":{"source":"const","value":"a"},"value":{"source":"const","value":"1"}}}`,
	}
}

func TestParserConfigSourceByType(t *testing.T) {
	// 空 parser_type 按 builtin，读 mapping_json（历史行兼容）。
	legacy := baseRow()
	cfg, err := parseSubscription(legacy)
	if err != nil {
		t.Fatalf("parseSubscription(legacy): %v", err)
	}
	if cfg.parserType != "builtin" || cfg.prs == nil {
		t.Fatalf("legacy parserType=%q prs=%v", cfg.parserType, cfg.prs)
	}

	// script 读 parser_config，不碰 mapping_json。
	scripted := baseRow()
	scripted.ParserType = "script"
	scripted.ParserConfig = `{"script":"function parse(msg, ctx){ctx.emit({deviceId:\"d\",deviceAddressId:\"a\",value:\"1\"});}"}`
	scripted.MappingJSON = "{}"
	cfg, err = parseSubscription(scripted)
	if err != nil {
		t.Fatalf("parseSubscription(script): %v", err)
	}
	if cfg.parserType != "script" || cfg.prs == nil {
		t.Fatalf("script parserType=%q prs=%v", cfg.parserType, cfg.prs)
	}

	// 非 builtin 缺 parser_config 时应明确失败，而不是退回 mapping_json。
	missing := baseRow()
	missing.ParserType = "script"
	missing.ParserConfig = ""
	if _, err := parseSubscription(missing); err == nil || !strings.Contains(err.Error(), "配置为空") {
		t.Fatalf("parseSubscription(missing) error = %v", err)
	}
}

func TestValidateConfigStrictPaths(t *testing.T) {
	// 内置映射保存路径要求主字段齐全。
	legacy := baseRow()
	if err := ValidateConfig(legacy); err == nil {
		t.Fatal("宽松的 mapping 不应通过保存路径校验")
	}

	// 脚本编译失败在保存前被拒绝。
	bad := baseRow()
	bad.ParserType = "script"
	bad.ParserConfig = `{"script":"function parse( {"}`
	if err := ValidateConfig(bad); err == nil || !strings.Contains(err.Error(), "编译失败") {
		t.Fatalf("ValidateConfig(bad script) error = %v", err)
	}
}

func TestSplitTopics(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"single", "factory/+/telemetry", []string{"factory/+/telemetry"}},
		{"multiple", "a/b , c/d,", []string{"a/b", "c/d"}},
		{"dedupe keeps order", "a, b ,a", []string{"a", "b"}},
		{"only separators", " , ,", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitTopics(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("splitTopics(%q) = %q, want %q", tc.raw, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("splitTopics(%q) = %q, want %q", tc.raw, got, tc.want)
				}
			}
		})
	}

	// 只有分隔符/空白的 topic 不算有效配置。
	row := baseRow()
	row.Topic = " , "
	if _, err := parseSubscription(row); err == nil || !strings.Contains(err.Error(), "topic 不能为空") {
		t.Fatalf("parseSubscription(blank topic) error = %v", err)
	}
}

func TestParseSubscriptionMultipleTopics(t *testing.T) {
	row := baseRow()
	row.Topic = " a/b , c/d ,, a/b "
	cfg, err := parseSubscription(row)
	if err != nil {
		t.Fatalf("parseSubscription: %v", err)
	}

	// 展示/日志仍保留原文，运行期用去重后的过滤器。
	if cfg.topic != "a/b , c/d ,, a/b" {
		t.Errorf("cfg.topic = %q, want raw trimmed text", cfg.topic)
	}
	filters := cfg.topicFilters()
	if len(filters) != 2 || filters["a/b"] != 1 || filters["c/d"] != 1 {
		t.Fatalf("topicFilters() = %v, want a/b and c/d at qos 1", filters)
	}

	// 仅空白/重复项的差异不应触发重连。
	other := baseRow()
	other.Topic = "a/b,c/d"
	cfgOther, err := parseSubscription(other)
	if err != nil {
		t.Fatalf("parseSubscription(other): %v", err)
	}
	if cfg.connSig != cfgOther.connSig {
		t.Error("仅空白或重复项的差异不应改变连接签名")
	}
}

func TestParserSignatureSeparatesConfig(t *testing.T) {
	a := baseRow()
	b := baseRow()
	b.MappingJSON = `{"fields":{"deviceId":{"source":"const","value":"d2"},"deviceAddressId":{"source":"const","value":"a"},"value":{"source":"const","value":"1"}}}`

	cfgA, err := parseSubscription(a)
	if err != nil {
		t.Fatalf("parseSubscription(a): %v", err)
	}
	cfgB, err := parseSubscription(b)
	if err != nil {
		t.Fatalf("parseSubscription(b): %v", err)
	}
	if cfgA.connSig != cfgB.connSig {
		t.Error("仅解析配置变化时连接签名应保持不变")
	}
	if cfgA.parserSig == cfgB.parserSig {
		t.Error("解析配置变化后解析签名应不同")
	}
}
