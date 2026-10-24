// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mappingdsl

import (
	"errors"
	"fmt"
	"strings"
)

// 取值方式。
const (
	SourcePath     = "path"
	SourceConst    = "const"
	SourceTemplate = "template"
	// SourceMap 读取一个值，再按 Mapping 翻译。
	SourceMap = "map"
	// SourceTopic 从 topicPattern 的命名段取值（仅入站使用）。
	SourceTopic = "topic"
)

// ValueSource 单个目标字段的取值方式。
//
//	Source:
//	  ""/"path"   按 JSON 路径取值，如 "deviceId" / "data.list[0].id"
//	  "const"     Value 直接作为固定值
//	  "template"  Value 是形如 "gw_{deviceId}" 的模板，按 {路径} 从文档中取值拼接
//	  "map"       按 Value 路径取值后查 Mapping 翻译
//	  "topic"     从 topicPattern 的命名段取值
//
// Mapping / Default：
//   - source=map：Mapping 未命中时使用 Default；未配置则保留原值。
//   - source=path：路径不存在时使用 Default（可选）。
//
// Transform 按顺序作用在取值结果上。
type ValueSource struct {
	Source string `json:"source"`
	Value  string `json:"value"`
	// Mapping 原始值 → 目标值。键比较大小写不敏感，纯数字键按数值比较。
	Mapping map[string]string `json:"mapping,omitempty"`
	// Default 未命中映射/路径不存在时的回落值。
	Default string `json:"default,omitempty"`
	// Transform 值变换流水线，如 scale(0.1) → round(2)。
	Transform []Transform `json:"transform,omitempty"`
}

// Validate 校验取值方式与变换参数是否自洽。
func (s ValueSource) Validate() error {
	switch strings.ToLower(strings.TrimSpace(s.Source)) {
	case "", SourcePath, SourceConst, SourceTemplate:
	case SourceMap:
		if strings.TrimSpace(s.Value) == "" {
			return errors.New("值映射需要填写取值路径")
		}
		if len(s.Mapping) == 0 {
			return errors.New("值映射至少需要一条映射")
		}
	case SourceTopic:
		if strings.TrimSpace(s.Value) == "" {
			return errors.New("主题取值需要填写命名段")
		}
	default:
		return fmt.Errorf("未知的取值方式 %q", s.Source)
	}
	for i := range s.Transform {
		if err := s.Transform[i].Validate(); err != nil {
			return fmt.Errorf("第 %d 个变换: %w", i+1, err)
		}
	}
	return nil
}

// IsConst 判断是否常量取值。
func (s ValueSource) IsConst() bool {
	return strings.EqualFold(strings.TrimSpace(s.Source), SourceConst)
}

// IsTemplate 判断是否模板取值。
func (s ValueSource) IsTemplate() bool {
	return strings.EqualFold(strings.TrimSpace(s.Source), SourceTemplate)
}

// IsMap 判断是否值映射取值。
func (s ValueSource) IsMap() bool {
	return strings.EqualFold(strings.TrimSpace(s.Source), SourceMap)
}

// IsTopic 判断是否主题命名段取值。
func (s ValueSource) IsTopic() bool {
	return strings.EqualFold(strings.TrimSpace(s.Source), SourceTopic)
}

// Resolve 按取值方式从单条文档中取值，再应用 transform 流水线。
// 未命中返回 ErrValueMissing（配置了 Default 时回落）。
func (s ValueSource) Resolve(doc any, vars map[string]string) (any, error) {
	v, err := s.resolveRaw(doc, vars)
	if err != nil {
		return nil, err
	}
	return ApplyTransforms(v, s.Transform)
}

func (s ValueSource) resolveRaw(doc any, vars map[string]string) (any, error) {
	switch {
	case s.IsConst():
		return s.Value, nil
	case s.IsTemplate():
		return RenderTemplate(doc, s.Value, vars)
	case s.IsTopic():
		name := strings.TrimSpace(s.Value)
		v, ok := vars[name]
		if !ok {
			return nil, fmt.Errorf("主题变量 %q 不存在", name)
		}
		return v, nil
	case s.IsMap():
		v, ok := LookupPath(doc, s.Value)
		if !ok {
			if s.Default != "" {
				return s.Default, nil
			}
			return nil, fmt.Errorf("字段 %q %w", s.Value, ErrValueMissing)
		}
		raw := strings.TrimSpace(ToString(v))
		if mapped, ok := s.lookup(raw); ok {
			return mapped, nil
		}
		if s.Default != "" {
			return s.Default, nil
		}
		// 未配置 default：保留原值。对 kind 而言这会走「未知类别」的自动推断，
		// 比静默丢弃整条记录更容易在数据里发现问题。
		return raw, nil
	default:
		v, ok := LookupPath(doc, s.Value)
		if !ok {
			if s.Default != "" {
				return s.Default, nil
			}
			return nil, fmt.Errorf("字段 %q %w", s.Value, ErrValueMissing)
		}
		return v, nil
	}
}

// lookup 在 Mapping 中查找原始值对应的目标值。
//
// 键比较按以下顺序：精确匹配 → 忽略大小写与首尾空白 → 数值相等（避免外部系统
// 发 1 与 1.0、字符串 "1" 与数字 1 之间的无谓失配）。
func (s ValueSource) lookup(raw string) (string, bool) {
	if len(s.Mapping) == 0 {
		return "", false
	}
	if dst, ok := s.Mapping[raw]; ok {
		return dst, true
	}
	for k, dst := range s.Mapping {
		if strings.EqualFold(strings.TrimSpace(k), raw) {
			return dst, true
		}
	}
	if f, ok := ToFloat64(raw); ok {
		for k, dst := range s.Mapping {
			if kf, ok := ToFloat64(k); ok && kf == f {
				return dst, true
			}
		}
	}
	return "", false
}
