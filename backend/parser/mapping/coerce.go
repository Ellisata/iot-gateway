// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mapping

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// toString 将任意 JSON 标量渲染为字符串。
//
// json.Number 走 String() 保留原始字面量，避免经 float64 中转丢精度。
func toString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// toInt 尽力转 int64，失败返回 (0, false)。
func toInt(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i, true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f), true
	}
	return 0, false
}

// toFloat64 尽力把 JSON 值转成 float64，供过滤条件的数值比较使用。
func toFloat64(v any) (float64, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint64:
		return float64(t), true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func fmtFieldMissing(tpl, path string) error {
	return fmt.Errorf("模板 %q 中的字段 %q %w", tpl, path, errValueMissing)
}
