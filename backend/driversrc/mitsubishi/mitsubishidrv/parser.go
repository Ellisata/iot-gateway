// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"encoding/binary"
	"fmt"
)

// ParseMCValue 将读取到的原始字节解码为指定类型的 Go 值。
//
// raw 为该点位所在的数据切片：
//   - 字模式：连续字（每字 2 字节，MC 线上小端）；位模式：1 字节（0x00/0x01）。
//   - 字设备 bool（地址携带 .bit 或默认取 bit 0）：从所在字中提取该位。
//   - string：整体传给解码函数（字节流即正序 ASCII）。
func ParseMCValue(raw []byte, addr MCAddress, dataType string) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("mc parser: empty raw data")
	}

	dt, ok := mcLookup(dataType)
	if !ok {
		return nil, fmt.Errorf("mc parser: unsupported data type: %q", dataType)
	}

	// 字设备位访问：从 2 字节字中提取所在位。
	// MC 字低字节在前：位 0..7 位于 raw[0]，位 8..15 位于 raw[1]，字内位序 LSB-first。
	if addr.IsBit() {
		if len(raw) < 2 {
			return nil, fmt.Errorf("mc parser: bool needs a word (2 bytes), got %d", len(raw))
		}
		byteIdx := 0
		if addr.Bit >= 8 {
			byteIdx = 1
		}
		bit := (raw[byteIdx] >> uint(addr.Bit%8)) & 0x01
		return dt.Decode([]byte{bit}, binary.LittleEndian)
	}

	// 动态长度类型（STRING）：整体传给解码函数
	if dt.Size == 0 {
		return dt.Decode(raw, binary.LittleEndian)
	}

	// 常规类型：截取类型大小字节（小端）。
	// 单字节类型（uint8/int8，及位模式的 bool）取首字节——MC 字节数据存于字的低字节。
	if len(raw) < dt.Size {
		return nil, fmt.Errorf("mc parser: %s needs %d bytes, got %d", dataType, dt.Size, len(raw))
	}
	data := raw[:dt.Size]
	if dt.Size == 1 {
		data = raw[:1]
	}
	return dt.Decode(data, binary.LittleEndian)
}

// FormatMCValue 将解码后的值格式化为字符串（用于存储和展示）。
func FormatMCValue(v any, dataType string) string {
	dt, ok := mcLookup(dataType)
	if !ok || dt.Format == nil {
		return fmt.Sprintf("%v", v)
	}
	return dt.Format(v)
}
