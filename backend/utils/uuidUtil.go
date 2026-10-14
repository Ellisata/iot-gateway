// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package utils

import (
	"strings"

	"github.com/google/uuid"
)

// GenerateUUID 生成无横线的 32 位 UUID (UUID v4)
func GenerateUUID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")
}

// GenerateUUIDWithDash 生成带横线的标准 UUID (UUID v4)
func GenerateUUIDWithDash() string {
	return uuid.New().String()
}

// GenerateUUIDV7 生成 UUID v7（基于时间戳+随机数，无横线 32 位）
// UUID v7 前 48 位是毫秒级 Unix 时间戳，保证趋势递增，适合做数据库主键
func GenerateUUIDV7() string {
	id, err := uuid.NewV7()
	if err != nil {
		// 极端情况下降级为 v4
		return GenerateUUID()
	}
	return strings.ReplaceAll(id.String(), "-", "")
}

// GenerateUUIDV7WithDash 生成带横线的标准 UUID v7
func GenerateUUIDV7WithDash() string {
	id, err := uuid.NewV7()
	if err != nil {
		return GenerateUUIDWithDash()
	}
	return id.String()
}
