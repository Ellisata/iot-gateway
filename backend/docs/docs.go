// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package docs

import (
	"bytes"
	_ "embed"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// 开放接口说明文档（go:embed 嵌入二进制，离线可用），
// 管理端「开放接口文档」页面经 /openApiSecret/doc 接口取渲染后的 HTML 展示。
//
//go:embed openApi.md
var openApiMD []byte

var renderOnce = sync.OnceValue(func() string {
	md := goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough))
	var buf bytes.Buffer
	if err := md.Convert(openApiMD, &buf); err != nil {
		// 渲染失败兜底：原文档为内置静态资源，正常不会发生
		return "<pre>" + string(openApiMD) + "</pre>"
	}
	return buf.String()
})

// RenderOpenApiDocHTML 返回开放接口文档渲染后的 HTML（进程内缓存，仅渲染一次）
func RenderOpenApiDocHTML() string {
	return renderOnce()
}
