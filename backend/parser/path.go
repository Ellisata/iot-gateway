// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package parser

import (
	"strconv"
	"strings"
)

// LookupPath 按路径从已解析的 JSON 文档中取值。
//
// 支持以下写法（可混用）：
//
//	"a.b.c"    对象逐级下钻
//	"a[0].b"   数组下标
//	"a.0.b"    数组下标（点号写法）
//	"$.a.b"    带根符号
//	"$" / ""   文档本身
//
// 返回 (值, 是否命中)。路径不存在或中途类型不匹配时返回 (nil, false)。
func LookupPath(doc any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" || path == "$" {
		return doc, true
	}

	segs := SplitPath(strings.TrimPrefix(strings.TrimPrefix(path, "$"), "."))
	cur := doc
	for _, seg := range segs {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			// 中途遇到标量却还要继续下钻
			return nil, false
		}
	}
	return cur, true
}

// SplitPath 将路径拆分为层级片段。
//
//	"a.b"    → ["a" "b"]
//	"a[0].b" → ["a" "0" "b"]
//
// 分隔符 "." "[" "]" 均不产生空片段，因此 "a..b" 与 "a.b" 等价。
func SplitPath(path string) []string {
	segs := make([]string, 0, 4)
	var b strings.Builder

	flush := func() {
		if b.Len() > 0 {
			segs = append(segs, b.String())
			b.Reset()
		}
	}

	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '.', '[', ']':
			flush()
		default:
			b.WriteByte(path[i])
		}
	}
	flush()
	return segs
}
