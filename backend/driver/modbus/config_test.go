// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbus

import (
	"testing"
	"time"
)

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

// 串口链路参数必须真的被解析进 cfg —— 它们此前只存在于结构体里，
// 解析器一行都没读，于是永远停在默认值，而表单也没有对应字段可供修改。
//
// 两种 JSON 形态都要能收：select 控件给字符串，inputNumber 给数字。
func TestParseModbusRTUConfig_SerialParams(t *testing.T) {
	cases := map[string]struct {
		in   string
		want struct {
			baud, data, stop int
			parity           string
		}
	}{
		"select 形态（字符串）": {
			`{"comPort":"COM3","baudRate":"19200","dataBits":"7","stopBits":"2","parity":"E"}`,
			struct {
				baud, data, stop int
				parity           string
			}{19200, 7, 2, "E"},
		},
		"inputNumber 形态（数字）": {
			`{"comPort":"COM3","baudRate":19200,"dataBits":7,"stopBits":2,"parity":"e"}`,
			struct {
				baud, data, stop int
				parity           string
			}{19200, 7, 2, "E"}, // 校验位大小写不敏感
		},
		"字段缺席用默认值": {
			`{"comPort":"COM3"}`,
			struct {
				baud, data, stop int
				parity           string
			}{9600, 8, 1, "N"},
		},
		"非法值回退默认而不是报错": {
			`{"comPort":"COM3","baudRate":0,"dataBits":9,"stopBits":3,"parity":"X"}`,
			struct {
				baud, data, stop int
				parity           string
			}{9600, 8, 1, "N"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseModbusRTUConfig(c.in)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if cfg.BaudRate != c.want.baud || cfg.DataBits != c.want.data ||
				cfg.StopBits != c.want.stop || cfg.Parity != c.want.parity {
				t.Errorf("串口参数 = %d/%d/%d/%s, want %d/%d/%d/%s",
					cfg.BaudRate, cfg.DataBits, cfg.StopBits, cfg.Parity,
					c.want.baud, c.want.data, c.want.stop, c.want.parity)
			}
			// 解析结果要能被连接复用判定用上（matchConfig 逐项比较这四项）
			if !rtuClientFrom(t, cfg).matchConfig(cfg) {
				t.Error("解析出的配置应能被同参数的客户端判定为可复用")
			}
		})
	}
}

// 单帧超时：显式配置才覆盖默认 5000。
//
// 调小要谨慎——过小会把应答帧截断，现象是「偶发 CRC 校验错」而不是明确的超时，
// 现场极难归因，所以这里把「0 与负值一律视为未配置」也钉住。
func TestParseModbusRTUConfig_Timeout(t *testing.T) {
	cases := map[string]struct {
		in   string
		want int
	}{
		"数字形态":    {`{"comPort":"COM3","timeoutMs":12000}`, 12000},
		"字符串形态":   {`{"comPort":"COM3","timeoutMs":"12000"}`, 12000},
		"字段缺席用默认": {`{"comPort":"COM3"}`, 5000},
		"0 视为未配置": {`{"comPort":"COM3","timeoutMs":0}`, 5000},
		"负值回退默认":  {`{"comPort":"COM3","timeoutMs":-1}`, 5000},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseModbusRTUConfig(c.in)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if cfg.TimeoutMS != c.want {
				t.Errorf("TimeoutMS = %d, want %d", cfg.TimeoutMS, c.want)
			}
			if want := time.Duration(c.want) * time.Millisecond; cfg.Timeout != want {
				t.Errorf("Timeout = %v, want %v", cfg.Timeout, want)
			}
		})
	}
}
