// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package parser

import "iot-gateway/mappingdsl"

// LookupPath 按路径从已解析的 JSON 文档中取值。
//
// 实现已下沉到 mappingdsl（入站解析与出站映射共用）。保留本函数是为了
// 不改变 parser 契约包的既有 API（脚本解析器直接调用它）。
func LookupPath(doc any, path string) (any, bool) {
	return mappingdsl.LookupPath(doc, path)
}

// SplitPath 将路径拆分为层级片段。
func SplitPath(path string) []string {
	return mappingdsl.SplitPath(path)
}
