// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mapping

import (
	"fmt"
	"strings"
)

// topicSeg 主题模板的一段：字面量或 {name} 命名段。
type topicSeg struct {
	literal string
	name    string
}

// compileTopicPattern 把 topicPattern 编译成段列表。
//
//	"factory/{deviceId}/telemetry" → ["factory", {deviceId}, "telemetry"]
//
// 命名段必须唯一；整串按 "/" 分段后必须与主题段数一致（不支持通配符）。
func (r *Rule) compileTopicPattern() error {
	r.topicSegs = nil
	pattern := strings.TrimSpace(r.TopicPattern)
	if pattern == "" {
		return nil
	}

	parts := strings.Split(pattern, "/")
	segs := make([]topicSeg, 0, len(parts))
	names := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := strings.TrimSpace(part[1 : len(part)-1])
			if name == "" {
				return fmt.Errorf("topicPattern %q 含空的命名段", pattern)
			}
			if strings.ContainsAny(name, "{} /") {
				return fmt.Errorf("topicPattern %q 的命名段 %q 非法", pattern, name)
			}
			if _, dup := names[name]; dup {
				return fmt.Errorf("topicPattern %q 的命名段 %q 重复", pattern, name)
			}
			names[name] = struct{}{}
			segs = append(segs, topicSeg{name: name})
			continue
		}
		if strings.ContainsAny(part, "{}") {
			return fmt.Errorf("topicPattern %q 的段 %q 花括号不完整", pattern, part)
		}
		segs = append(segs, topicSeg{literal: part})
	}
	r.topicSegs = segs
	return nil
}

// topicVars 按模板从具体主题中提取命名段。
// 未配置模板时返回 nil；模板不匹配是报文级错误（该条报文无法满足身份字段）。
func (r *Rule) topicVars(topic string) (map[string]string, error) {
	if len(r.topicSegs) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(topic) == "" {
		return nil, fmt.Errorf("报文主题为空，无法按模板 %q 取值", r.TopicPattern)
	}

	parts := strings.Split(topic, "/")
	if len(parts) != len(r.topicSegs) {
		return nil, fmt.Errorf("主题 %q 不匹配模板 %q", topic, r.TopicPattern)
	}
	vars := make(map[string]string, len(r.topicSegs))
	for i, seg := range r.topicSegs {
		if seg.name != "" {
			vars[seg.name] = parts[i]
			continue
		}
		if seg.literal != parts[i] {
			return nil, fmt.Errorf("主题 %q 不匹配模板 %q", topic, r.TopicPattern)
		}
	}
	return vars, nil
}

func (r *Rule) hasTopicVar(name string) bool {
	name = strings.TrimSpace(name)
	for _, seg := range r.topicSegs {
		if seg.name == name {
			return true
		}
	}
	return false
}

// renderTemplate 渲染形如 "gw_{deviceId}_{tagId}" 的模板。
//
// {path} 先按 JSON 路径从文档中取值，取不到时回落到 topicPattern 的命名段；
// 其余字符原样保留。"{{" 与 "}}" 表示字面量花括号。
//
// 模板中任一字段缺失即整体报错 —— 残缺的子表名会静默写进一张错误子表，
// 比直接失败更难排查。
func renderTemplate(doc any, tpl string, topicVars map[string]string) (string, error) {
	var b strings.Builder
	b.Grow(len(tpl))

	for i := 0; i < len(tpl); {
		switch tpl[i] {
		case '{':
			if i+1 < len(tpl) && tpl[i+1] == '{' {
				b.WriteByte('{')
				i += 2
				continue
			}
			end := strings.IndexByte(tpl[i+1:], '}')
			if end < 0 {
				b.WriteByte('{')
				i++
				continue
			}
			path := strings.TrimSpace(tpl[i+1 : i+1+end])
			v, ok := lookupTemplateValue(doc, topicVars, path)
			if !ok {
				return "", fmtFieldMissing(tpl, path)
			}
			b.WriteString(toString(v))
			i += end + 2

		case '}':
			if i+1 < len(tpl) && tpl[i+1] == '}' {
				b.WriteByte('}')
				i += 2
				continue
			}
			b.WriteByte('}')
			i++

		default:
			b.WriteByte(tpl[i])
			i++
		}
	}
	return b.String(), nil
}

func lookupTemplateValue(doc any, topicVars map[string]string, path string) (any, bool) {
	if strings.HasPrefix(path, "topic.") {
		v, ok := topicVars[strings.TrimPrefix(path, "topic.")]
		return v, ok
	}
	if v, ok := lookupDocValue(doc, path); ok {
		return v, true
	}
	v, ok := topicVars[path]
	return v, ok
}
