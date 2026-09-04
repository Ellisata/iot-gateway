// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver_test

import (
	"testing"

	"iot-gateway/driver"
	_ "iot-gateway/driver/mitsubishi"
	_ "iot-gateway/driver/modbus"
	_ "iot-gateway/driver/omron/cip"
	_ "iot-gateway/driver/omron/fins"
	_ "iot-gateway/driver/s7"
)

// TestKindOfProtocolMapping 验证各协议注册的内部类型均声明了正确的类别 Kind,
// 且裸名经 ProtocolScope 前缀解析可命中。推送通道的类型化完全依赖此映射。
func TestKindOfProtocolMapping(t *testing.T) {
	cases := []struct {
		protocol string
		name     string
		want     string
	}{
		// modbus
		{"modbus", "bool", driver.KindBool},
		{"modbus", "int16", driver.KindInt},
		{"modbus", "int8", driver.KindInt},
		{"modbus", "uint16", driver.KindUInt},
		{"modbus", "word", driver.KindUInt},
		{"modbus", "bcd", driver.KindUInt},
		{"modbus", "lbcd", driver.KindUInt},
		{"modbus", "float32", driver.KindFloat},
		{"modbus", "float64", driver.KindFloat},
		{"modbus", "string", driver.KindString},
		{"modbus", "date", driver.KindTime},
		// s7(char 语义为字符 → string;时间族 → time)
		{"s7", "bool", driver.KindBool},
		{"s7", "byte", driver.KindUInt},
		{"s7", "word", driver.KindUInt},
		{"s7", "dword", driver.KindUInt},
		{"s7", "char", driver.KindString},
		{"s7", "int", driver.KindInt},
		{"s7", "dint", driver.KindInt},
		{"s7", "real", driver.KindFloat},
		{"s7", "lreal", driver.KindFloat},
		{"s7", "string", driver.KindString},
		{"s7", "time", driver.KindTime},
		{"s7", "tod", driver.KindTime},
		{"s7", "s5time", driver.KindTime},
		{"s7", "date", driver.KindTime},
		{"s7", "dt", driver.KindTime},
		// cip(无 date)
		{"cip", "bool", driver.KindBool},
		{"cip", "int16", driver.KindInt},
		{"cip", "uint8", driver.KindUInt},
		{"cip", "lbcd", driver.KindUInt},
		{"cip", "float32", driver.KindFloat},
		// fins(含 date)
		{"fins", "int32", driver.KindInt},
		{"fins", "float64", driver.KindFloat},
		{"fins", "date", driver.KindTime},
		// mitsubishi(无 date)
		{"mitsubishi", "bool", driver.KindBool},
		{"mitsubishi", "int16", driver.KindInt},
		{"mitsubishi", "int8", driver.KindInt},
		{"mitsubishi", "word", driver.KindUInt},
		{"mitsubishi", "bcd", driver.KindUInt},
		{"mitsubishi", "lbcd", driver.KindUInt},
		{"mitsubishi", "float32", driver.KindFloat},
		{"mitsubishi", "float64", driver.KindFloat},
		{"mitsubishi", "string", driver.KindString},
		// 未注册/其他协议裸名 → 空(推送通道回退为数值推断)
		{"modbus", "nope", ""},
		{"s7", "int16", ""}, // S7 无 int16(只有 int)
	}
	reg := driver.GetTypeRegistry()
	for _, c := range cases {
		if got := reg.ForProtocol(c.protocol).KindOf(c.name); got != c.want {
			t.Errorf("KindOf(%s, %q) = %q, want %q", c.protocol, c.name, got, c.want)
		}
	}
}

// TestKindOfUnregistered 空名/未知名返回空串,不 panic。
// 注意:注册表查询不区分大小写,"MODBUS.INT16" 会命中已注册的 "modbus.int16",故不在此列。
func TestKindOfUnregistered(t *testing.T) {
	reg := driver.GetTypeRegistry()
	for _, name := range []string{"", "void", "int16x"} {
		if got := reg.ForProtocol("modbus").KindOf(name); got != "" {
			t.Errorf("KindOf(modbus, %q) = %q, want empty", name, got)
		}
	}
}
