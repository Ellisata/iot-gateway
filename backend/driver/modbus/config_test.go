// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbus

import "testing"

// TestParseModbusTcpConfig_Order 验证 byteOrder/wordOrder 独立配置。
// TCP 配置已移除 headSortType 推导（随新前端迁移），字节序/字序只由显式字段决定，不区分大小写。
func TestParseModbusTcpConfig_Order(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		wantByte string
		wantWord string
	}{
		{"empty json defaults", "", ByteOrderBigEndian, WordOrderBigEndian},
		// 仅独立配置 byteOrder：byte 覆盖为 LITTLE，word 保持默认 BIG
		{"byteOrder alone overrides byte only", `{"byteOrder":"LITTLE_ENDIAN"}`, ByteOrderLittleEndian, WordOrderBigEndian},
		// 仅独立配置 wordOrder：word 覆盖为 LITTLE，byte 保持默认 BIG
		{"wordOrder alone overrides word only", `{"wordOrder":"LITTLE_ENDIAN"}`, ByteOrderBigEndian, WordOrderLittleEndian},
		// 独立配置组合 CD AB（字节大端 + 字小端）
		{"byte big word little", `{"byteOrder":"BIG_ENDIAN","wordOrder":"LITTLE_ENDIAN"}`, ByteOrderBigEndian, WordOrderLittleEndian},
		// 不区分大小写
		{"case insensitive", `{"byteOrder":"big_endian","wordOrder":"little_endian"}`, ByteOrderBigEndian, WordOrderLittleEndian},
		// 非法值回退为大端（与 FINS 行为一致）
		{"invalid value falls back big", `{"byteOrder":"UNKNOWN","wordOrder":"UNKNOWN"}`, ByteOrderBigEndian, WordOrderBigEndian},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseModbusTcpConfig(tt.json)
			if err != nil {
				t.Fatalf("ParseModbusTcpConfig(%q) error: %v", tt.json, err)
			}
			if cfg.ByteOrder != tt.wantByte || cfg.WordOrder != tt.wantWord {
				t.Errorf("ParseModbusTcpConfig(%q) byte/word = %s/%s, want %s/%s",
					tt.json, cfg.ByteOrder, cfg.WordOrder, tt.wantByte, tt.wantWord)
			}
		})
	}
}

// TestParseModbusRTUConfig_Order RTU 与 TCP 一致：字节序/字序只由显式 byteOrder/wordOrder 决定
// （headSortType 推导已随迁移移除），不区分大小写。
func TestParseModbusRTUConfig_Order(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		wantByte string
		wantWord string
	}{
		{"empty json defaults", "", ByteOrderBigEndian, WordOrderBigEndian},
		// 仅独立配置 byteOrder：byte 覆盖为 LITTLE，word 保持默认 BIG
		{"byteOrder alone overrides byte only", `{"byteOrder":"LITTLE_ENDIAN"}`, ByteOrderLittleEndian, WordOrderBigEndian},
		// 仅独立配置 wordOrder：word 覆盖为 LITTLE，byte 保持默认 BIG
		{"wordOrder alone overrides word only", `{"wordOrder":"LITTLE_ENDIAN"}`, ByteOrderBigEndian, WordOrderLittleEndian},
		// 独立配置组合 CD AB（字节大端 + 字小端）
		{"byte big word little", `{"byteOrder":"BIG_ENDIAN","wordOrder":"LITTLE_ENDIAN"}`, ByteOrderBigEndian, WordOrderLittleEndian},
		// 不区分大小写
		{"case insensitive", `{"byteOrder":"big_endian","wordOrder":"little_endian"}`, ByteOrderBigEndian, WordOrderLittleEndian},
		// 非法值回退为大端
		{"invalid value falls back big", `{"byteOrder":"UNKNOWN","wordOrder":"UNKNOWN"}`, ByteOrderBigEndian, WordOrderBigEndian},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseModbusRTUConfig(tt.json)
			if err != nil {
				t.Fatalf("ParseModbusRTUConfig(%q) error: %v", tt.json, err)
			}
			if cfg.ByteOrder != tt.wantByte || cfg.WordOrder != tt.wantWord {
				t.Errorf("ParseModbusRTUConfig(%q) byte/word = %s/%s, want %s/%s",
					tt.json, cfg.ByteOrder, cfg.WordOrder, tt.wantByte, tt.wantWord)
			}
		})
	}
}
