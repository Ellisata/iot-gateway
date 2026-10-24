// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mappingdsl

import (
	"strings"
)

// RenderTemplate 渲染形如 "gw_{deviceId}_{tagId}" 的模板。
//
// {path} 先按 JSON 路径从文档中取值，取不到时回落到 vars（入站是 topicPattern
// 的命名段，出站可以放任意批级变量）；其余字符原样保留。"{{" 与 "}}" 表示字面量
// 花括号。
//
// 模板中任一字段缺失即整体报错 —— 残缺的子表名会静默写进一张错误子表，
// 比直接失败更难排查。
func RenderTemplate(doc any, tpl string, vars map[string]string) (string, error) {
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
			v, ok := lookupTemplateValue(doc, vars, path)
			if !ok {
				return "", fmtFieldMissing(tpl, path)
			}
			b.WriteString(ToString(v))
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

func lookupTemplateValue(doc any, vars map[string]string, path string) (any, bool) {
	if strings.HasPrefix(path, "topic.") {
		v, ok := vars[strings.TrimPrefix(path, "topic.")]
		return v, ok
	}
	if v, ok := LookupPath(doc, path); ok {
		return v, true
	}
	v, ok := vars[path]
	return v, ok
}
