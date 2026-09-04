// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package i18n

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"iot-gateway/logger"
	"iot-gateway/utils"
)

//go:embed *.json
var i18nFS embed.FS

var (
	translations map[string]map[string]string
	i18nOnce     sync.Once
	mu           sync.RWMutex
)

func initTranslations() {
	translations = make(map[string]map[string]string)

	files, err := fs.ReadDir(i18nFS, ".")
	if err != nil {
		logger.Warn("failed to read embedded i18n files: %v", err)
		return
	}

	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		lang := strings.TrimSuffix(file.Name(), ".json")
		data, err := i18nFS.ReadFile(file.Name())
		if err != nil {
			logger.Warn("failed to read embedded i18n file %s: %v", file.Name(), err)
			continue
		}
		var msgs map[string]string
		if err := json.Unmarshal(data, &msgs); err != nil {
			logger.Warn("failed to parse i18n file %s: %v", file.Name(), err)
			continue
		}
		translations[lang] = msgs
	}
}

// areaToLang 区域/语言代码到语言文件映射
// 兼容 BCP47（zh-CN / en-US）与自定义区域码（zh_hk / hk / tw 等）
func areaToLang(area string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(area), "_", "-"))
	if normalized == "" {
		return "zh"
	}

	primary := normalized
	if idx := strings.Index(normalized, "-"); idx > 0 {
		primary = normalized[:idx]
	}

	switch {
	case primary == "hk" || primary == "tw" || primary == "mo",
		normalized == "zh-hk" || normalized == "zh-tw" || normalized == "zh-mo":
		return "zh-hk"
	case primary == "en":
		return "en"
	default:
		return "zh"
	}
}

// InitI18n 显式初始化国际化翻译（推荐在 main 中调用）
func InitI18n() {
	i18nOnce.Do(initTranslations)
}

// T 根据语言代码翻译
func T(lang, key string) string {
	InitI18n()
	mu.RLock()
	defer mu.RUnlock()

	if msgs, ok := translations[lang]; ok {
		if msg, ok := msgs[key]; ok {
			return msg
		}
	}
	// 回退到中文
	if lang != "zh" {
		return T("zh", key)
	}
	return key
}

// Tf 翻译并格式化
func Tf(lang, key string, args ...interface{}) string {
	msg := T(lang, key)
	if len(args) > 0 {
		return formatMessage(msg, args...)
	}
	return msg
}

// TCtx 从 context 获取语言并翻译
func TCtx(ctx context.Context, key string) string {
	lang := LangFromCtx(ctx)
	return T(lang, key)
}

// TCtxf 翻译 + 格式化 + Context
func TCtxf(ctx context.Context, key string, args ...interface{}) string {
	lang := LangFromCtx(ctx)
	return Tf(lang, key, args...)
}

// LangFromCtx 从 context 中提取语言代码
// 区域值由 areaMiddleware 经 utils.SetAreaToCtx 注入（类型化 key）
func LangFromCtx(ctx context.Context) string {
	return areaToLang(utils.GetAreaFromCtx(ctx))
}

func formatMessage(msg string, args ...interface{}) string {
	result := msg
	for i, arg := range args {
		placeholder := "{" + itoa(i) + "}"
		result = strings.ReplaceAll(result, placeholder, toString(arg))
	}
	return result
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
