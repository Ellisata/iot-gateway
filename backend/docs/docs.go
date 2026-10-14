// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package docs

import (
	"bytes"
	"embed"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// 开放接口说明文档（go:embed 嵌入二进制，离线可用），
// 管理端「开放接口文档」页面经 /openApiSecret/doc 接口按用户语言取渲染后的 HTML 展示。
// 中文为主文档 openApi.md，英文翻译 openApi.en.md，两者章节结构保持一致。
//
//go:embed openApi.md openApi.en.md
var openApiMDFS embed.FS

const (
	docFileZh = "openApi.md"
	docFileEn = "openApi.en.md"
)

// 各语言渲染缓存：Markdown → HTML 为纯函数转换，进程内仅渲染一次
var (
	renderOnce sync.Once
	rendered   map[string]string
)

func renderDocs() {
	md := goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough))
	rendered = map[string]string{
		docFileZh: convertMD(md, docFileZh),
		docFileEn: convertMD(md, docFileEn),
	}
}

// convertMD 从嵌入 FS 读取指定语言文档并渲染为 HTML
// 渲染失败兜底输出原文（内置静态资源，正常不会发生）
func convertMD(md goldmark.Markdown, name string) string {
	data, err := openApiMDFS.ReadFile(name)
	if err != nil {
		return "<pre>doc " + name + " missing</pre>"
	}
	var buf bytes.Buffer
	if err := md.Convert(data, &buf); err != nil {
		return "<pre>" + string(data) + "</pre>"
	}
	return buf.String()
}

// RenderOpenApiDocHTML 按语言返回开放接口文档渲染后的 HTML（进程内缓存，仅渲染一次）
// lang 为 i18n 语言码（zh / en / zh-hk）；zh-hk 复用简体文档（仅文档层面，无繁体翻译）
func RenderOpenApiDocHTML(lang string) string {
	renderOnce.Do(renderDocs)
	switch lang {
	case "en":
		return rendered[docFileEn]
	default:
		return rendered[docFileZh]
	}
}
