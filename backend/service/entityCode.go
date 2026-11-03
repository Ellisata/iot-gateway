// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"regexp"
	"strconv"
	"strings"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/utils"
)

// entityCodePattern 设备编码与采集组编码共用的格式：
// 字母开头，仅字母、数字、下划线、短横线，最长 64。与物模型 code 同一口径，
// 保证三个编码都能直接作为 MQTT 主题段使用。
var entityCodePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// validateEntityCode 判断编码是否满足对外稳定标识的格式要求。
func validateEntityCode(code string) bool {
	return entityCodePattern.MatchString(code)
}

// isReservedEntityCode 保留字判断。legacy 是未分组批次在主题里的占位段，
// 不能同时作为用户编码，否则未分组批次与某个组会撞到同一主题。
func isReservedEntityCode(code string) bool {
	return strings.EqualFold(code, "legacy")
}

func deviceCodeInvalidErr() error {
	return appError.NewAppErrorCtx(enums.DeviceCodeInvalidEnum.GetCode(),
		enums.DeviceCodeInvalidEnum.GetMessage(), enums.DeviceCodeInvalidEnum.GetMsgKey())
}

func deviceCodeExistsErr() error {
	return appError.NewAppErrorCtx(enums.DeviceCodeExistsEnum.GetCode(),
		enums.DeviceCodeExistsEnum.GetMessage(), enums.DeviceCodeExistsEnum.GetMsgKey())
}

func deviceCodeImmutableErr() error {
	return appError.NewAppErrorCtx(enums.DeviceCodeImmutableEnum.GetCode(),
		enums.DeviceCodeImmutableEnum.GetMessage(), enums.DeviceCodeImmutableEnum.GetMsgKey())
}

func collectorGroupCodeInvalidErr() error {
	return appError.NewAppErrorCtx(enums.CollectorGroupCodeInvalidEnum.GetCode(),
		enums.CollectorGroupCodeInvalidEnum.GetMessage(), enums.CollectorGroupCodeInvalidEnum.GetMsgKey())
}

func collectorGroupCodeExistsErr() error {
	return appError.NewAppErrorCtx(enums.CollectorGroupCodeExistsEnum.GetCode(),
		enums.CollectorGroupCodeExistsEnum.GetMessage(), enums.CollectorGroupCodeExistsEnum.GetMsgKey())
}

func collectorGroupCodeImmutableErr() error {
	return appError.NewAppErrorCtx(enums.CollectorGroupCodeImmutableEnum.GetCode(),
		enums.CollectorGroupCodeImmutableEnum.GetMessage(), enums.CollectorGroupCodeImmutableEnum.GetMsgKey())
}

// generatedEntityCode 把人类可读名称转成主题安全的候选编码。
//
// 这个函数只服务**没有用户输入编码**的创建路径（Excel 导入、扫描建库），
// 生成结果统一小写；手工新建仍由用户输入，大小写原样保留（与物模型 code 一致）。
// 名称里没有可用 ASCII 字母/数字时（如纯中文），退回随机后缀，保证编码可用。
func generatedEntityCode(name, fallbackPrefix string) string {
	var b strings.Builder
	b.Grow(len(name))
	hasAlnum := false
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			hasAlnum = true
			prevDash = false
		case r == '_':
			b.WriteRune(r)
			prevDash = false
		default:
			// 空格、短横线、点、中文等一律折叠成单个短横线。
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}

	code := strings.Trim(b.String(), "-_")
	if !hasAlnum {
		return fallbackPrefix + "_" + utils.GenerateUUIDV7()[:8]
	}
	if c := code[0]; !(c >= 'a' && c <= 'z') {
		code = fallbackPrefix + "-" + code
	}
	if len(code) > 64 {
		code = strings.TrimRight(code[:64], "-_")
	}
	if !validateEntityCode(code) {
		return fallbackPrefix + "_" + utils.GenerateUUIDV7()[:8]
	}
	return code
}

// uniqueEntityCode 在候选编码被占用时追加 -2、-3…，直到 taken 返回 false。
// taken 由调用方提供：单条创建直接查库，批量导入/扫描用批内集合，避免逐行查库。
func uniqueEntityCode(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		suffix := "-" + strconv.Itoa(i)
		trimmed := base
		if len(trimmed)+len(suffix) > 64 {
			trimmed = strings.TrimRight(trimmed[:64-len(suffix)], "-_")
		}
		candidate := trimmed + suffix
		if !taken(candidate) {
			return candidate
		}
	}
}
