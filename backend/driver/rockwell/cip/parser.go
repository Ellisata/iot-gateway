// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"encoding/binary"
	"fmt"
)

// ParseRockwellValue 将 Read Tag（0x4C）响应数据区解码为指定类型的 Go 值。
//
// raw 为响应数据区（已由 stripTypeCodeHeader 去掉类型码头）。
// Logix 数据原生小端；无位地址、无字序交换逻辑（对比 FINS parser.go）。
func ParseRockwellValue(raw []byte, dataType string, cfg *RockwellConfig) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("rockwell parser: empty raw data")
	}

	dt, ok := rockwellLookup(dataType)
	if !ok {
		return nil, fmt.Errorf("rockwell parser: unsupported data type: %q", dataType)
	}

	// 动态长度类型（STRING）：数据区为 4 字节 LE 长度前缀 + 字符。
	// 长度字段越界时按实际数据长度容错；超过 stringLen 截断（真机核实：
	// Logix STRING 标准总长 88 字节，Micro800 等变体可能不同）。
	if dt.Size == 0 {
		if len(raw) < 4 {
			return nil, fmt.Errorf("rockwell parser: string needs 4 bytes length prefix, got %d", len(raw))
		}
		n := int(binary.LittleEndian.Uint32(raw[:4]))
		if n > len(raw)-4 {
			n = len(raw) - 4
		}
		maxLen := cfg.StringLen
		if maxLen <= 0 {
			maxLen = defaultStringLen
		}
		if n > maxLen {
			n = maxLen
		}
		return dt.Decode(raw[4:4+n], binary.LittleEndian)
	}

	// 常规类型：截取类型大小字节
	if len(raw) < dt.Size {
		return nil, fmt.Errorf("rockwell parser: %s needs %d bytes, got %d", dataType, dt.Size, len(raw))
	}
	return dt.Decode(raw[:dt.Size], binary.LittleEndian)
}

// FormatRockwellValue 将解码后的值格式化为字符串（用于存储和展示）。
func FormatRockwellValue(v any, dataType string) string {
	dt, ok := rockwellLookup(dataType)
	if !ok || dt.Format == nil {
		return fmt.Sprintf("%v", v)
	}
	return dt.Format(v)
}
