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

// usesTopicVars 判断配置是否真的引用了主题命名段（source=topic，或模板里的
// {name} / {topic.name}）。
//
// 没有被任何字段引用时，topicPattern 只是填了但用不上的配置：此时不能因为
// 主题对不上就把报文整条丢掉 —— 那样运维只要在表单里随手填一个模板，
// 所有报文都会静默消失。
func (r *Rule) usesTopicVars() bool {
	if len(r.topicSegs) == 0 {
		return false
	}
	if r.fieldsUseTopic(r.Fields) {
		return true
	}
	for _, overlay := range r.Fanout {
		if r.fieldsUseTopic(overlay) {
			return true
		}
	}
	return false
}

func (r *Rule) fieldsUseTopic(fields map[string]ValueSource) bool {
	for _, src := range fields {
		if src.IsTopic() {
			return true
		}
		if src.IsTemplate() && r.templateUsesTopicVar(src.Value) {
			return true
		}
	}
	return false
}

// templateUsesTopicVar 按 RenderTemplate 的解析口径扫描模板占位符。
// 命名段既可能写成 {deviceId}，也可能写成 {topic.deviceId}。
func (r *Rule) templateUsesTopicVar(tpl string) bool {
	for i := 0; i < len(tpl); {
		if tpl[i] != '{' {
			i++
			continue
		}
		// "{{" 是字面量花括号，与 RenderTemplate 一致。
		if i+1 < len(tpl) && tpl[i+1] == '{' {
			i += 2
			continue
		}
		end := strings.IndexByte(tpl[i+1:], '}')
		if end < 0 {
			i++
			continue
		}
		path := strings.TrimSpace(tpl[i+1 : i+1+end])
		if strings.HasPrefix(path, "topic.") || r.hasTopicVar(path) {
			return true
		}
		i += end + 2
	}
	return false
}

// topicVars 按模板从具体主题中提取命名段。
// 未配置模板、或没有任何字段引用命名段时返回 nil（不做主题校验）；
// 引用后模板不匹配是报文级错误（该条报文无法满足引用命名段的字段）。
func (r *Rule) topicVars(topic string) (map[string]string, error) {
	if len(r.topicSegs) == 0 || !r.topicUsed {
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
