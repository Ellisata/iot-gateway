// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mapping

import (
	"strings"
	"testing"
)

func TestTopicSource(t *testing.T) {
	rule := mustParse(t, `{
		"topicPattern": "factory/{deviceId}/telemetry/{tag}",
		"fields": {
			"deviceId":        {"source": "topic", "value": "deviceId"},
			"deviceName":      {"source": "template", "value": "gw-{deviceId}"},
			"deviceAddressId": {"source": "topic", "value": "tag"},
			"value":           {"source": "path", "value": "v"}
		}
	}`)

	res, err := rule.ConvertWithTopic("factory/line1/telemetry/temp", []byte(`{"v":12}`))
	if err != nil {
		t.Fatalf("ConvertWithTopic: %v", err)
	}
	if len(res.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(res.Records))
	}
	rec := res.Records[0]
	if rec.DeviceID != "line1" || rec.DeviceAddressID != "temp" {
		t.Errorf("identity = %q/%q, want line1/temp", rec.DeviceID, rec.DeviceAddressID)
	}
	// 模板先查报文，取不到时回落到主题命名段。
	if rec.DeviceName != "gw-line1" {
		t.Errorf("deviceName = %q, want gw-line1", rec.DeviceName)
	}

	if _, err := rule.ConvertWithTopic("factory/line1/other/temp", []byte(`{"v":12}`)); err == nil {
		t.Fatal("主题不匹配时应返回错误")
	}
	if _, err := rule.ConvertWithTopic("", []byte(`{"v":12}`)); err == nil {
		t.Fatal("主题为空时应返回错误")
	}
}

// 模板填了但没有任何字段引用命名段时，主题不匹配不能把报文丢掉 ——
// 否则运维在表单里随手填一个模板，所有报文都会静默失败。
func TestUnusedTopicPatternDoesNotFilter(t *testing.T) {
	rule := mustParse(t, `{
		"topicPattern": "factory/{deviceId}/telemetry",
		"fields": {
			"deviceId":        {"source": "path", "value": "id"},
			"deviceAddressId": {"source": "const", "value": "temp"},
			"value":           {"source": "path", "value": "v"}
		}
	}`)

	res, err := rule.ConvertWithTopic("other/topic/x", []byte(`{"id":"d1","v":1}`))
	if err != nil {
		t.Fatalf("未被引用时主题不匹配不应报错: %v", err)
	}
	if len(res.Records) != 1 || res.Records[0].DeviceID != "d1" {
		t.Fatalf("records = %+v, want one record for d1", res.Records)
	}

	// 空主题同理：没有字段需要从主题取值，就不该要求主题存在。
	if _, err := rule.ConvertWithTopic("", []byte(`{"id":"d1","v":1}`)); err != nil {
		t.Fatalf("未被引用时空主题不应报错: %v", err)
	}
}

// 只有模板引用了命名段（没有 source=topic）时，模板仍然生效且主题必须匹配。
func TestTemplateTopicVarUsesPattern(t *testing.T) {
	rule := mustParse(t, `{
		"topicPattern": "factory/{deviceId}/telemetry",
		"fields": {
			"deviceId":        {"source": "path", "value": "id"},
			"deviceName":      {"source": "template", "value": "gw-{deviceId}"},
			"deviceAddressId": {"source": "const", "value": "temp"},
			"value":           {"source": "path", "value": "v"}
		}
	}`)

	res, err := rule.ConvertWithTopic("factory/line1/telemetry", []byte(`{"id":"d1","v":1}`))
	if err != nil {
		t.Fatalf("ConvertWithTopic: %v", err)
	}
	if len(res.Records) != 1 || res.Records[0].DeviceName != "gw-line1" {
		t.Fatalf("records = %+v, want deviceName gw-line1", res.Records)
	}

	if _, err := rule.ConvertWithTopic("factory/line1/other", []byte(`{"id":"d1","v":1}`)); err == nil {
		t.Fatal("模板引用命名段时，主题不匹配应返回错误")
	}
}

func TestTopicSourceRequiresPattern(t *testing.T) {
	_, err := Parse([]byte(`{
		"fields": {
			"deviceId":        {"source": "topic", "value": "deviceId"},
			"deviceAddressId": {"source": "const", "value": "a"},
			"value":           {"source": "path", "value": "v"}
		}
	}`))
	if err == nil || !strings.Contains(err.Error(), "topicPattern") {
		t.Fatalf("Parse error = %v, want it to mention topicPattern", err)
	}
}

func TestFanoutProducesOneRecordPerMetric(t *testing.T) {
	rule := mustParse(t, `{
		"fields": {
			"deviceId":   {"source": "path", "value": "id"},
			"deviceName": {"source": "path", "value": "name"},
			"kind":       {"source": "const", "value": "float"},
			"quality":    {"source": "const", "value": "192"}
		},
		"fanout": [
			{"deviceAddressId": {"source": "const", "value": "temp"},
			 "deviceAddressLabel": {"source": "const", "value": "温度"},
			 "value": {"source": "path", "value": "data.temp"}},
			{"deviceAddressId": {"source": "const", "value": "hum"},
			 "deviceAddressLabel": {"source": "const", "value": "湿度"},
			 "value": {"source": "path", "value": "data.hum"}}
		]
	}`)

	res, err := rule.Convert([]byte(`{"id":"d1","name":"一号机","data":{"temp":21.5,"hum":60}}`))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(res.Records) != 2 || res.Skipped != 0 {
		t.Fatalf("records=%d skipped=%d, want 2/0", len(res.Records), res.Skipped)
	}
	byAddr := map[string]string{}
	for _, r := range res.Records {
		byAddr[r.DeviceAddressID] = r.Value
	}
	if byAddr["temp"] != "21.5" || byAddr["hum"] != "60" {
		t.Errorf("values = %+v", byAddr)
	}

	// 缺一个测点时只跳过该条扇出项，其余照常产出。
	partial, err := rule.Convert([]byte(`{"id":"d1","name":"一号机","data":{"temp":22}}`))
	if err != nil {
		t.Fatalf("partial Convert: %v", err)
	}
	if len(partial.Records) != 1 || partial.Skipped != 1 || partial.FirstError == nil {
		t.Fatalf("partial records=%d skipped=%d err=%v", len(partial.Records), partial.Skipped, partial.FirstError)
	}
}

func TestValueTransforms(t *testing.T) {
	rule := mustParse(t, `{
		"fields": {
			"deviceId":        {"source": "const", "value": "d1"},
			"deviceAddressId": {"source": "const", "value": "a1"},
			"value": {"source": "path", "value": "raw", "transform": [
				{"op": "scale", "factor": 0.1},
				{"op": "round", "digits": 1}
			]}
		}
	}`)
	res, err := rule.Convert([]byte(`{"raw":1234}`))
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got := res.Records[0].Value; got != "123.4" {
		t.Errorf("scaled value = %q, want 123.4", got)
	}

	cases := []struct {
		name      string
		source    string
		payload   string
		wantValue string
	}{
		{
			name:      "default",
			source:    `{"source":"path","value":"missing","default":"7"}`,
			payload:   `{}`,
			wantValue: "7",
		},
		{
			name:      "bitfield",
			source:    `{"source":"path","value":"n","transform":[{"op":"bitfield","offset":4,"length":4}]}`,
			payload:   `{"n":240}`,
			wantValue: "15",
		},
		{
			name:      "hex",
			source:    `{"source":"path","value":"s","transform":[{"op":"hexDecode"}]}`,
			payload:   `{"s":"48656c6c6f"}`,
			wantValue: "Hello",
		},
		{
			name:      "regex",
			source:    `{"source":"path","value":"s","transform":[{"op":"regex","pattern":"(\\d+)","group":1}]}`,
			payload:   `{"s":"code-42"}`,
			wantValue: "42",
		},
		{
			name:      "trim",
			source:    `{"source":"path","value":"s","transform":[{"op":"trim"}]}`,
			payload:   `{"s":"  x  "}`,
			wantValue: "x",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := mustParse(t, `{
				"fields": {
					"deviceId":        {"source": "const", "value": "d1"},
					"deviceAddressId": {"source": "const", "value": "a1"},
					"value": `+tc.source+`
				}
			}`)
			res, err := rule.Convert([]byte(tc.payload))
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if got := res.Records[0].Value; got != tc.wantValue {
				t.Errorf("value = %q, want %q", got, tc.wantValue)
			}
		})
	}
}

func TestUnknownTransformRejected(t *testing.T) {
	_, err := Parse([]byte(`{
		"fields": {
			"deviceId":        {"source": "const", "value": "d1"},
			"deviceAddressId": {"source": "const", "value": "a1"},
			"value":           {"source": "path", "value": "v", "transform": [{"op": "eval"}]}
		}
	}`))
	if err == nil || !strings.Contains(err.Error(), "未知的变换") {
		t.Fatalf("Parse error = %v, want unknown transform", err)
	}
}
