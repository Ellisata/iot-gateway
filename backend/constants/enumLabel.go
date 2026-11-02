// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package constants

import "strings"

// 固定枚举的「展示名（code）」模板辅助。
//
// Excel 模板里的下拉直接展示 code 对现场用户不可读（sensor / RW 这类），
// 统一改成「中文名（code）」：括号外给人看，括号里的 code 给机器读。
// 导入时 code、中文名、组合写法都接受，因此用户从下拉选或照别处填 code 都不会失败。

// BuildEnumOptions 生成「中文名（code）」形式的下拉项，顺序与 codes 一致。
// 没有中文名的 code（如协议扩展类型）直接显示 code，不拼出「（Int）」这种空壳。
func BuildEnumOptions(codes []string, labels map[string]string) []string {
	out := make([]string, 0, len(codes))
	for _, code := range codes {
		if label := labels[code]; label != "" {
			out = append(out, label+"（"+code+"）")
			continue
		}
		out = append(out, code)
	}
	return out
}

// ParseEnumValue 解析 code、展示名或「展示名（code）」组合，返回归一后的 code。
// code 比较忽略大小写；空串返回 ok=false（"允许留空"由调用方决定）。
func ParseEnumValue(raw string, codes []string, labels map[string]string) (code string, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	if code, ok := matchCode(codes, s); ok {
		return code, true
	}

	// 组合写法：兼容全角/半角括号，括号内取 code。
	norm := strings.NewReplacer("（", "(", "）", ")").Replace(s)
	if i := strings.LastIndex(norm, "("); i >= 0 && strings.HasSuffix(norm, ")") {
		inner := strings.TrimSpace(norm[i+1 : len(norm)-1])
		if code, ok := matchCode(codes, inner); ok {
			return code, true
		}
	}

	for code, label := range labels {
		if label == s {
			return code, true
		}
	}
	return "", false
}

// matchCode 在 codes 里忽略大小写查找，返回**定义里的原始大小写**
// （读写模式的 code 是 R/W/RW，不能把用户输入的 r 原样返回）。
func matchCode(codes []string, raw string) (string, bool) {
	for _, c := range codes {
		if strings.EqualFold(c, raw) {
			return c, true
		}
	}
	return "", false
}

func containsCode(codes []string, code string) bool {
	if code == "" {
		return false
	}
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}
