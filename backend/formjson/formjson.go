// Package formjson 解析 form-create 表单 schema（form_json）。
//
// 协议、推送通道、报警 Webhook 等表单都是运行期数据：rule 由插件或运维提供，
// 文案用 {{$t.key}} 引用表单自带的 options.language。本包只做「把一份表单
// 读成字段清单」这一件事，不感知任何业务，供导入模板生成等场景复用。
//
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package formjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Kind 字段默认值/选项值的 JSON 类型归类。
type Kind int

const (
	KindNone Kind = iota
	KindString
	KindNumber
	KindBool
	KindArray
	KindObject
)

// Option 一个下拉/单选选项。Label 已本地化，Value 保留原始 JSON 类型。
type Option struct {
	Label string
	Value json.RawMessage
}

// Field 表单里的一个可数据化字段。
type Field struct {
	Key      string          // rule.field
	Type     string          // input / inputNumber / select / textarea ...
	Title    string          // 已本地化的标题；缺失时回退 Key
	Info     string          // 已本地化的说明
	Required bool            // rule.$required
	Default  json.RawMessage // rule.value，保留原始 JSON 类型
	Options  []Option
	Props    map[string]any
	Secret   bool // props.type == "password"
}

// IsNumber 该字段是否应写成 JSON 数字。
func (f Field) IsNumber() bool {
	return f.Type == "inputNumber" || f.Type == "number"
}

// IsSelect 该字段是否从固定选项里取值。
func (f Field) IsSelect() bool {
	return f.Type == "select" || f.Type == "radio"
}

// IsBool 该字段是否是布尔开关。
func (f Field) IsBool() bool {
	return f.Type == "switch"
}

// Document 一份表单的解析结果。
type Document struct {
	// Fields 顶层、可数据化的标量字段（保持 rule 中的顺序）。
	Fields []Field
	// Unsupported 无法用单个 Excel 单元格表达的字段（嵌套 children、数组/对象、
	// 多选等）。调用方应据此拒绝生成导入模板，而不是静默丢字段。
	Unsupported []Field
}

// Parse 解析 form_json。raw 可以是 {"rule":[...],"options":{...}}，也可以是
// 顶层数组；rule 是字符串时按前端相同的兼容逻辑二次解析。
// lang 取后端 i18n.LangFromCtx 的值（zh / en / zh-hk），内部映射到表单的
// zh-cn / en 语言键。
func Parse(raw []byte, lang string) (*Document, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return &Document{}, nil
	}

	var ruleRaw json.RawMessage
	var language map[string]map[string]string

	if trimmed[0] == '[' {
		ruleRaw = trimmed
	} else if trimmed[0] == '{' {
		var envelope struct {
			Rule    json.RawMessage `json:"rule"`
			Options struct {
				Language map[string]map[string]string `json:"language"`
			} `json:"options"`
		}
		if err := json.Unmarshal(trimmed, &envelope); err != nil {
			return nil, fmt.Errorf("表单配置不是合法 JSON：%w", err)
		}
		ruleRaw = envelope.Rule
		language = envelope.Options.Language
	} else {
		return nil, fmt.Errorf("表单配置不是合法的 JSON 对象或数组")
	}

	ruleRaw = bytes.TrimSpace(ruleRaw)
	if len(ruleRaw) == 0 || bytes.Equal(ruleRaw, []byte("null")) {
		return &Document{}, nil
	}
	// 前端允许 rule 被序列化成字符串，这里同样兼容。
	if ruleRaw[0] == '"' {
		var text string
		if err := json.Unmarshal(ruleRaw, &text); err != nil {
			return nil, fmt.Errorf("表单 rule 不是合法字符串：%w", err)
		}
		ruleRaw = bytes.TrimSpace([]byte(text))
	}

	var rules []map[string]json.RawMessage
	if err := json.Unmarshal(ruleRaw, &rules); err != nil {
		return nil, fmt.Errorf("表单 rule 不是合法数组：%w", err)
	}

	doc := &Document{}
	loc := localizer{lang: formLanguage(lang), language: language}
	for _, rule := range rules {
		parseRule(rule, loc, doc)
	}
	return doc, nil
}

// ResolveText 解析一段表单文案（供需要单独展示标题/说明的场景使用）。
func ResolveText(text, lang string, language map[string]map[string]string) string {
	return localizer{lang: formLanguage(lang), language: language}.text(text)
}

var languageToken = regexp.MustCompile(`^\{\{\s*\$t\.([\w.$-]+)\s*\}\}$`)

type localizer struct {
	lang     string
	language map[string]map[string]string
}

func (l localizer) text(text string) string {
	if text == "" {
		return ""
	}
	m := languageToken.FindStringSubmatch(text)
	if m == nil {
		return text
	}
	key := m[1]

	// 当前语言 → zh-cn → 其余语言（按键名排序，保证结果稳定）。
	candidates := []string{l.lang}
	if l.lang != "zh-cn" {
		candidates = append(candidates, "zh-cn")
	}
	if l.lang == "zh-hk" {
		candidates = append(candidates, "zh-hk")
	}
	for _, candidate := range candidates {
		if value := l.language[candidate][key]; value != "" {
			return value
		}
	}
	names := make([]string, 0, len(l.language))
	for name := range l.language {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if value := l.language[name][key]; value != "" {
			return value
		}
	}
	// 一个译文都没有时显示 key，比空白更容易排查。
	return key
}

func formLanguage(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "en":
		return "en"
	case "zh-hk":
		return "zh-hk"
	default:
		return "zh-cn"
	}
}

func parseRule(rule map[string]json.RawMessage, loc localizer, doc *Document) {
	field := Field{
		Key:      rawString(rule["field"]),
		Type:     rawString(rule["type"]),
		Title:    loc.text(rawString(rule["title"])),
		Info:     loc.text(rawString(rule["info"])),
		Required: rawBool(rule["$required"]),
		Default:  rawValue(rule["value"]),
	}
	if props := rule["props"]; len(props) > 0 {
		_ = json.Unmarshal(props, &field.Props)
		if field.Type == "" {
			if t, ok := field.Props["type"].(string); ok {
				field.Type = t
			}
		}
	}
	field.Secret = field.Type == "password"
	if field.Props != nil {
		if t, ok := field.Props["type"].(string); ok && t == "password" {
			field.Secret = true
		}
	}

	// 选项本地化；Value 缺失时退回 label 的 JSON 字符串，至少保证可回填。
	if rawOptions := rule["options"]; len(rawOptions) > 0 {
		var options []struct {
			Label string          `json:"label"`
			Value json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(rawOptions, &options); err == nil {
			for _, option := range options {
				label := loc.text(option.Label)
				value := normalizeRaw(option.Value)
				if len(value) == 0 {
					encoded, _ := json.Marshal(label)
					value = encoded
				}
				field.Options = append(field.Options, Option{Label: label, Value: value})
			}
		}
	}

	if strings.TrimSpace(field.Key) == "" {
		return // 纯布局规则（分隔线、容器等）
	}
	if strings.TrimSpace(field.Title) == "" {
		field.Title = field.Key
	}

	if hasNestedFields(rule["children"]) || isComposite(field) {
		doc.Unsupported = append(doc.Unsupported, field)
		doc.Unsupported = append(doc.Unsupported, collectNested(rule["children"], loc)...)
		return
	}
	doc.Fields = append(doc.Fields, field)
}

// collectNested 收集嵌套 children 中带 field 的规则。它们不能进 Fields：
// 嵌套字段在 protocol_json 里是另一个层级，扁平 Excel 列无法安全表达。
func collectNested(raw json.RawMessage, loc localizer) []Field {
	if len(raw) == 0 {
		return nil
	}
	var children []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &children); err != nil {
		return nil
	}
	var out []Field
	for _, child := range children {
		field := Field{
			Key:      rawString(child["field"]),
			Type:     rawString(child["type"]),
			Title:    loc.text(rawString(child["title"])),
			Info:     loc.text(rawString(child["info"])),
			Required: rawBool(child["$required"]),
			Default:  rawValue(child["value"]),
		}
		if props := child["props"]; len(props) > 0 {
			_ = json.Unmarshal(props, &field.Props)
		}
		if strings.TrimSpace(field.Key) != "" {
			if strings.TrimSpace(field.Title) == "" {
				field.Title = field.Key
			}
			out = append(out, field)
		}
		out = append(out, collectNested(child["children"], loc)...)
	}
	return out
}

func hasNestedFields(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var children []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &children); err != nil {
		return false
	}
	for _, child := range children {
		if strings.TrimSpace(rawString(child["field"])) != "" {
			return true
		}
		if hasNestedFields(child["children"]) {
			return true
		}
	}
	return false
}

var compositeTypes = map[string]bool{
	"checkbox": true, "checkboxGroup": true, "cascader": true, "tree": true,
	"transfer": true, "upload": true, "dateRange": true, "timeRange": true,
	"group": true, "subForm": true, "table": true, "object": true, "array": true,
}

func isComposite(field Field) bool {
	if compositeTypes[field.Type] {
		return true
	}
	if field.Props != nil {
		if multiple, ok := field.Props["multiple"].(bool); ok && multiple {
			return true
		}
	}
	switch KindOf(field.Default) {
	case KindArray, KindObject:
		return true
	}
	return false
}

// KindOf 判断一段 JSON 原始值的类型。
func KindOf(raw json.RawMessage) Kind {
	raw = normalizeRaw(raw)
	if len(raw) == 0 {
		return KindNone
	}
	switch raw[0] {
	case '"':
		return KindString
	case 't', 'f':
		return KindBool
	case '[':
		return KindArray
	case '{':
		return KindObject
	case 'n':
		return KindNone
	default:
		return KindNumber
	}
}

// StringValue 把字符串原始值解出来（非字符串返回 ""）。
func StringValue(raw json.RawMessage) string {
	if KindOf(raw) != KindString {
		return ""
	}
	var text string
	_ = json.Unmarshal(raw, &text)
	return text
}

func rawString(raw json.RawMessage) string {
	if KindOf(raw) != KindString {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	return text
}

func rawBool(raw json.RawMessage) bool {
	raw = normalizeRaw(raw)
	if len(raw) == 0 {
		return false
	}
	switch string(raw) {
	case "true":
		return true
	case "false":
		return false
	}
	// 兼容 "true" / "false" / 1 / 0。
	if KindOf(raw) == KindString {
		switch strings.ToLower(StringValue(raw)) {
		case "true", "1":
			return true
		}
		return false
	}
	if KindOf(raw) == KindNumber {
		value, err := strconv.ParseFloat(string(raw), 64)
		return err == nil && value != 0
	}
	return false
}

func rawValue(raw json.RawMessage) json.RawMessage {
	raw = normalizeRaw(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func normalizeRaw(raw json.RawMessage) json.RawMessage {
	return bytes.TrimSpace(raw)
}
