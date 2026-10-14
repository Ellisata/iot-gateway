// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package utils

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// GenerateTableName 根据设备名称、ID 和前缀自动生成时序数据库表名。
//
// 规则：
//  1. 保留中文、字母、数字、下划线，其余字符替换为下划线
//  2. 连续下划线折叠为单个
//  3. 去掉首尾下划线
//  4. 若结果为空则兜底为 "dev"
//  5. 格式为 "{prefix}{slug}_{ID}"
//  6. 最终字符串截断至 maxTableNameBytes 字节（不破坏 UTF-8 字符）
//
// 示例：
//
//	GenerateTableName("1号车间温度传感器", 42, "dev_") → "dev_1号车间温度传感器_42"
//	GenerateTableName("PLC-Device A (3F/西区)", 7, "dev_") → "dev_PLC_Device_A_3F_西区_7"
//	GenerateTableName("@@@!!!", 99, "dev_") → "dev_99"
const maxTableNameBytes = 180

func GenerateTableName(name string, id uint, prefix string) string {
	// 1. 字符过滤
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if isTableNameAllowedRune(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	slug := b.String()

	// 2. 折叠连续下划线
	slug = collapseUnderscores(slug)

	// 3. 去掉首尾下划线
	slug = strings.Trim(slug, "_")

	// 4. 兜底
	if slug == "" {
		slug = "dev"
	}

	// 5. 拼装最终名称
	idStr := strconv.FormatUint(uint64(id), 10)
	result := prefix + slug + "_" + idStr

	// 6. 按字节截断（保护 UTF-8 多字节字符）
	if len(result) > maxTableNameBytes {
		// 为前缀和后缀预留空间，从 slug 部分截断
		reserve := len(prefix) + 1 + len(idStr) // "dev_" + "_" + "42"
		available := maxTableNameBytes - reserve
		if available < 1 {
			available = 1
		}
		slug = truncateUTF8Bytes(slug, available)
		slug = strings.TrimRight(slug, "_")
		if slug == "" {
			slug = "dev"
		}
		result = prefix + slug + "_" + idStr
	}

	return result
}

// isTableNameAllowedRune 判断字符是否允许出现在表名中。
// 允许：中文、字母、数字、下划线。
func isTableNameAllowedRune(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.IsLetter(r) ||
		unicode.IsDigit(r) ||
		r == '_'
}

// collapseUnderscores 将连续多个下划线替换为单个下划线。
func collapseUnderscores(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevUnderscore := false
	for _, r := range s {
		if r == '_' {
			if prevUnderscore {
				continue
			}
			prevUnderscore = true
		} else {
			prevUnderscore = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// truncateUTF8Bytes 将字符串截断到最多 maxBytes 字节，不破坏 UTF-8 编码。
func truncateUTF8Bytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// 向后搜索上一个 rune 起始字节
	raw := []byte(s)
	for i := maxBytes; i > 0; i-- {
		if utf8.RuneStart(raw[i-1]) {
			return string(raw[:i])
		}
	}
	return ""
}
