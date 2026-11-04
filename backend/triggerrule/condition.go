// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package triggerrule 实现网关侧的事件触发采集规则：
// 采集记录喂进来的点位值构成样本，DNF 条件出现上升沿时异步读取动作点位，
// 再定向推送到一个通道并留下触发历史。
package triggerrule

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Ellisata/iot-gateway-sdk/driver"
)

// 条件规模上限。MVP 不做嵌套表达式，超过这些数量的配置直接拒绝。
const (
	MaxGroups          = 20
	MaxConditionsPer   = 50
	MaxConditionsTotal = 200
)

// Condition 单条比较条件。AddressID 是设备地址 ID（不是 PLC 地址）。
type Condition struct {
	AddressID string   `json:"addressId"`
	Op        string   `json:"op"`
	Values    []string `json:"values"`
}

// Group 一组必须同时满足的条件（组内 AND）。
type Group struct {
	Conditions []Condition `json:"conditions"`
}

// Conditions DNF：组间 OR、组内 AND。前端提交的 conditionsJson 就是本结构。
type Conditions struct {
	Groups []Group `json:"groups"`
}

// ParseConditions 解析并做结构层校验（非空、规模上限、ID/算子非空）。
//
// 比较符与操作数是否匹配 Kind 由服务层用 driver.OperatorAllowed 校验：
// 这里保持纯结构校验，便于规则引擎热加载时对坏行做防御性跳过。
func ParseConditions(raw []byte) (Conditions, error) {
	var c Conditions
	if len(strings.TrimSpace(string(raw))) == 0 {
		return c, fmt.Errorf("触发条件不能为空")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("触发条件不是合法 JSON: %w", err)
	}
	if len(c.Groups) == 0 {
		return c, fmt.Errorf("至少需要一个条件组")
	}
	if len(c.Groups) > MaxGroups {
		return c, fmt.Errorf("条件组不能超过 %d 个", MaxGroups)
	}
	total := 0
	for i := range c.Groups {
		g := &c.Groups[i]
		if len(g.Conditions) == 0 {
			return c, fmt.Errorf("第 %d 个条件组没有条件", i+1)
		}
		if len(g.Conditions) > MaxConditionsPer {
			return c, fmt.Errorf("每个条件组最多 %d 条条件", MaxConditionsPer)
		}
		total += len(g.Conditions)
		for j := range g.Conditions {
			cond := &g.Conditions[j]
			cond.AddressID = strings.TrimSpace(cond.AddressID)
			cond.Op = strings.TrimSpace(cond.Op)
			if cond.AddressID == "" {
				return c, fmt.Errorf("第 %d 组第 %d 条条件缺少点位", i+1, j+1)
			}
			if cond.Op == "" {
				return c, fmt.Errorf("第 %d 组第 %d 条条件缺少比较符", i+1, j+1)
			}
		}
	}
	if total > MaxConditionsTotal {
		return c, fmt.Errorf("条件总数不能超过 %d 条", MaxConditionsTotal)
	}
	return c, nil
}

// Compare 按 Kind 判断一个采集值是否满足条件。
//
// 第二个返回值 determinate 为 false 表示"认不出来"（类型/值/操作数不可解释）：
// 调用方必须把它当作中性，不得据此改变边沿状态。语义与 alarm 的阈值判定一致：
//
//	数值  range 是 v<min || v>max（空串表示该侧不检查），其余是单次比较
//	布尔  eq/ne，两侧归一化成真值再比
//	文本  eq/ne 精确比较（trim 后、大小写敏感），in 判集合成员
func Compare(value, kind, op string, values []string) (met, determinate bool) {
	switch {
	case driver.IsNumericKind(kind):
		return compareNumeric(value, op, values)
	case kind == driver.KindBool:
		return compareBool(value, op, values)
	case kind == driver.KindString:
		return compareString(value, op, values)
	default:
		// 时间类型与未知 Kind：不猜。
		return false, false
	}
}

func compareNumeric(value, op string, values []string) (bool, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return false, false
	}

	if op == driver.OpRange {
		if len(values) != 2 {
			return false, false
		}
		lo, ok := parseOptionalFloat(values[0])
		if !ok {
			return false, false
		}
		hi, ok := parseOptionalFloat(values[1])
		if !ok {
			return false, false
		}
		if lo == nil && hi == nil {
			return false, false
		}
		if hi != nil && v > *hi {
			return true, true
		}
		if lo != nil && v < *lo {
			return true, true
		}
		return false, true
	}

	x, ok := parseOptionalFloat(firstValue(values))
	if !ok || x == nil {
		return false, false
	}
	switch op {
	case driver.OpGt:
		return v > *x, true
	case driver.OpGe:
		return v >= *x, true
	case driver.OpLt:
		return v < *x, true
	case driver.OpLe:
		return v <= *x, true
	case driver.OpEq:
		return v == *x, true
	case driver.OpNe:
		return v != *x, true
	}
	return false, false
}

func compareBool(value, op string, values []string) (bool, bool) {
	v, ok := parseBool(value)
	if !ok {
		return false, false
	}
	w, ok := parseBool(firstValue(values))
	if !ok {
		return false, false
	}
	switch op {
	case driver.OpEq:
		return v == w, true
	case driver.OpNe:
		return v != w, true
	}
	return false, false
}

func compareString(value, op string, values []string) (bool, bool) {
	v := strings.TrimSpace(value)
	switch op {
	case driver.OpEq, driver.OpNe:
		w := strings.TrimSpace(firstValue(values))
		if w == "" {
			return false, false
		}
		if op == driver.OpEq {
			return v == w, true
		}
		return v != w, true
	case driver.OpIn:
		set := make([]string, 0, len(values))
		for _, raw := range values {
			if s := strings.TrimSpace(raw); s != "" {
				set = append(set, s)
			}
		}
		if len(set) == 0 {
			return false, false
		}
		for _, s := range set {
			if v == s {
				return true, true
			}
		}
		return false, true
	}
	return false, false
}

func parseOptionalFloat(s string) (*float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, false
	}
	return &v, true
}

func parseBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true":
		return true, true
	case "0", "false":
		return false, true
	}
	return false, false
}

func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
