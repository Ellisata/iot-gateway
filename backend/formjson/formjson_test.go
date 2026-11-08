// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package formjson

import (
	"encoding/json"
	"testing"
)

const sampleForm = `{
  "rule": [
    {"type":"input","field":"host","title":"{{$t.host_title}}","$required":true},
    {"type":"inputNumber","field":"port","title":"{{$t.port_title}}","$required":true,"value":9600},
    {"type":"select","field":"byteOrder","title":"{{$t.byteOrder_title}}","$required":false,"value":"BIG_ENDIAN",
     "options":[{"label":"{{$t.opt_default}}","value":""},{"label":"{{$t.opt_big}}","value":"BIG_ENDIAN"},{"label":"High","value":"BIG_ENDIAN"},{"label":"Enabled","value":true}]},
    {"type":"input","field":"password","title":"密码","props":{"type":"password"}},
    {"type":"group","field":"network","title":"网络","children":[
      {"type":"input","field":"gateway","title":"网关"}
    ]},
    {"type":"checkbox","field":"tags","title":"标签","options":[{"label":"A","value":"a"}]},
    {"type":"divider","title":"分隔线"}
  ],
  "options": {
    "language": {
      "zh-cn": {"host_title":"设备IP","port_title":"设备端口","byteOrder_title":"字节序","opt_default":"默认","opt_big":"高字在前"},
      "en": {"host_title":"Device IP","port_title":"Device port","byteOrder_title":"Byte order","opt_default":"Default","opt_big":"High word first"},
      "ja": {"host_title":"端末IP"}
    }
  }
}`

func TestParseLocalizesAndClassifies(t *testing.T) {
	doc, err := Parse([]byte(sampleForm), "en")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(doc.Fields) != 4 {
		t.Fatalf("Fields = %d, want 4: %+v", len(doc.Fields), doc.Fields)
	}
	// 保持 rule 顺序，且跳过纯布局规则
	if doc.Fields[0].Key != "host" || doc.Fields[0].Title != "Device IP" {
		t.Errorf("host 标题 = %q, want Device IP", doc.Fields[0].Title)
	}
	if !doc.Fields[0].Required {
		t.Error("host 应为必填")
	}
	if !doc.Fields[1].IsNumber() {
		t.Error("port 应识别为数字")
	}
	if string(doc.Fields[1].Default) != "9600" {
		t.Errorf("port 默认值 = %s, want 9600", doc.Fields[1].Default)
	}
	if !doc.Fields[3].Secret {
		t.Error("password 字段应识别为密码")
	}
	if doc.Fields[3].Title != "密码" {
		t.Errorf("无多语言引用的中文标题应原样保留，实际 %q", doc.Fields[3].Title)
	}

	// select 选项的 label 本地化、value 保留原始类型
	selectField := doc.Fields[2]
	if len(selectField.Options) != 4 {
		t.Fatalf("选项数 = %d, want 4", len(selectField.Options))
	}
	if selectField.Options[0].Label != "Default" || StringValue(selectField.Options[0].Value) != "" {
		t.Errorf("选项0 = %+v", selectField.Options[0])
	}
	if KindOf(selectField.Options[3].Value) != KindBool {
		t.Errorf("布尔选项值类型 = %v, want bool，raw=%s", KindOf(selectField.Options[3].Value), selectField.Options[3].Value)
	}

	// group 与 checkbox 属于无法用单格表达的类型
	if len(doc.Unsupported) < 2 {
		t.Fatalf("Unsupported = %+v, want >= 2", doc.Unsupported)
	}
	keys := map[string]bool{}
	for _, f := range doc.Unsupported {
		keys[f.Key] = true
	}
	if !keys["network"] || !keys["gateway"] || !keys["tags"] {
		t.Errorf("Unsupported 应包含 network/gateway/tags，实际 %+v", doc.Unsupported)
	}
}

func TestParseLanguageFallback(t *testing.T) {
	tests := []struct {
		lang string
		want string
	}{
		{"en", "Device IP"},
		{"zh", "设备IP"},
		{"zh-hk", "设备IP"}, // 无 zh-hk 时回退 zh-cn
		{"fr", "设备IP"},    // 未知语言回退 zh-cn
	}
	for _, tt := range tests {
		doc, err := Parse([]byte(sampleForm), tt.lang)
		if err != nil {
			t.Fatalf("%s: %v", tt.lang, err)
		}
		if got := doc.Fields[0].Title; got != tt.want {
			t.Errorf("lang=%s 标题 = %q, want %q", tt.lang, got, tt.want)
		}
	}
}

func TestParseMissingTranslationReturnsKey(t *testing.T) {
	raw := `{"rule":[{"type":"input","field":"host","title":"{{$t.host_title}}"}]}`
	doc, err := Parse([]byte(raw), "en")
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Fields[0].Title; got != "host_title" {
		t.Errorf("缺译文标题 = %q, want host_title", got)
	}
}

func TestParseAcceptsArrayAndStringRule(t *testing.T) {
	arrayForm := `[{"type":"input","field":"host","title":"设备IP"}]`
	doc, err := Parse([]byte(arrayForm), "zh")
	if err != nil {
		t.Fatalf("顶层数组: %v", err)
	}
	if len(doc.Fields) != 1 || doc.Fields[0].Key != "host" {
		t.Fatalf("顶层数组解析 = %+v", doc.Fields)
	}

	stringRule := `{"rule":"[{\"type\":\"input\",\"field\":\"port\"}]"}`
	doc, err = Parse([]byte(stringRule), "zh")
	if err != nil {
		t.Fatalf("字符串 rule: %v", err)
	}
	if len(doc.Fields) != 1 || doc.Fields[0].Key != "port" {
		t.Fatalf("字符串 rule 解析 = %+v", doc.Fields)
	}
}

func TestParseEmptyAndInvalid(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", `{"rule":[]}`} {
		doc, err := Parse([]byte(raw), "zh")
		if err != nil || len(doc.Fields) != 0 {
			t.Errorf("raw=%q: doc=%+v err=%v", raw, doc, err)
		}
	}
	if _, err := Parse([]byte("{不是 JSON"), "zh"); err == nil {
		t.Error("非法 JSON 应返回错误")
	}
	if _, err := Parse([]byte(`{"rule":"not-array"}`), "zh"); err == nil {
		t.Error("rule 不是数组时应返回错误")
	}
}

func TestRawBoolLoose(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{`true`, true}, {`false`, false},
		{`"true"`, true}, {`"false"`, false},
		{`1`, true}, {`0`, false},
	}
	for _, tt := range tests {
		if got := rawBool(json.RawMessage(tt.raw)); got != tt.want {
			t.Errorf("rawBool(%s) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}
