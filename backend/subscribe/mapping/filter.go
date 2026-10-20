// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mapping

import (
	"fmt"
	"strconv"
	"strings"
)

// 支持的过滤运算符（大小写不敏感，内部统一转小写）。
const (
	opEq        = "eq"
	opNeq       = "neq"
	opGt        = "gt"
	opGte       = "gte"
	opLt        = "lt"
	opLte       = "lte"
	opContains  = "contains"
	opIn        = "in"
	opNotIn     = "notin"
	opExists    = "exists"
	opNotExists = "notexists"
)

var validOps = map[string]bool{
	opEq: true, opNeq: true,
	opGt: true, opGte: true, opLt: true, opLte: true,
	opContains: true, opIn: true, opNotIn: true,
	opExists: true, opNotExists: true,
}

// FilterCondition 单条过滤条件。
//
//	{ "path": "quality",  "op": "eq", "value": "1" }
//	{ "path": "dataType", "op": "in", "values": ["Float", "Double"] }
//
// Value 与 Values 二选一：in / notIn 用 Values，exists / notExists 两者都不用。
type FilterCondition struct {
	Path   string   `json:"path"`
	Op     string   `json:"op"`
	Value  string   `json:"value"`
	Values []string `json:"values"`
}

func (c *FilterCondition) normalizeOp() string {
	op := strings.ToLower(strings.TrimSpace(c.Op))
	if op == "" {
		return opEq
	}
	return op
}

// Validate 校验单条过滤条件是否自洽。
func (c *FilterCondition) Validate() error {
	if strings.TrimSpace(c.Path) == "" {
		return fmt.Errorf("过滤条件缺少 path")
	}
	op := c.normalizeOp()
	if !validOps[op] {
		return fmt.Errorf("过滤条件 %q 使用了未知运算符 %q", c.Path, c.Op)
	}
	switch op {
	case opIn, opNotIn:
		if len(c.Values) == 0 {
			return fmt.Errorf("过滤条件 %q 的 %s 运算需要配置 values 列表", c.Path, op)
		}
	case opExists, opNotExists:
		// 只判断存在性，不需要取值
	case opContains:
		if c.Value == "" {
			return fmt.Errorf("过滤条件 %q 的 contains 运算不能为空值（空值会匹配所有记录）", c.Path)
		}
	}
	return nil
}

// match 判断单条文档是否满足本条件。
// 取值失败（路径不存在）一律视为不满足，不报错 —— 过滤的语义就是「留下满足条件的」。
func (c *FilterCondition) match(doc any) bool {
	op := c.normalizeOp()

	if op == opExists || op == opNotExists {
		_, ok := lookupPath(doc, c.Path)
		if op == opExists {
			return ok
		}
		return !ok
	}

	v, ok := lookupPath(doc, c.Path)
	if !ok {
		return false
	}

	switch op {
	case opEq:
		return valueEqual(v, c.Value)
	case opNeq:
		return !valueEqual(v, c.Value)
	case opGt, opGte, opLt, opLte:
		got, ok1 := toFloat64(v)
		want, ok2 := toFloat64(c.Value)
		if !ok1 || !ok2 {
			return false
		}
		switch op {
		case opGt:
			return got > want
		case opGte:
			return got >= want
		case opLt:
			return got < want
		default:
			return got <= want
		}
	case opIn:
		for _, item := range c.Values {
			if valueEqual(v, item) {
				return true
			}
		}
		return false
	case opNotIn:
		for _, item := range c.Values {
			if valueEqual(v, item) {
				return false
			}
		}
		return true
	case opContains:
		return strings.Contains(toString(v), c.Value)
	}
	return false
}

// valueEqual 类型宽容地比较取值与期望值：两侧都能解析成数字时按数值比较，
// 否则回退到字符串比较（报文里同一个量可能是 1、也可能是 "1" / "1.0"）。
func valueEqual(v any, want string) bool {
	if f, ok := toFloat64(v); ok {
		if g, err := strconv.ParseFloat(strings.TrimSpace(want), 64); err == nil {
			return f == g
		}
	}
	return toString(v) == want
}
