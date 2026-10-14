// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"math"
	"testing"
)

func devD() mcDevice { d, _ := lookupDevice("D"); return d }
func devM() mcDevice { m, _ := lookupDevice("M"); return m }

// TestParseMCValue 小端解码各类型。
func TestParseMCValue(t *testing.T) {
	cases := []struct {
		name     string
		addr     MCAddress
		dataType string
		raw      []byte
		want     string
	}{
		{"int16 0x1234", MCAddress{Device: devD(), Number: 100, Bit: -1}, "int16", []byte{0x34, 0x12}, "4660"},
		{"int16 负数", MCAddress{Device: devD(), Number: 100, Bit: -1}, "int16", []byte{0x00, 0x80}, "-32768"},
		{"uint32 100", MCAddress{Device: devD(), Number: 100, Bit: -1}, "uint32", []byte{0x64, 0x00, 0x00, 0x00}, "100"},
		{"int32 负数", MCAddress{Device: devD(), Number: 100, Bit: -1}, "int32", []byte{0x00, 0x00, 0x00, 0x80}, "-2147483648"},
		{"float32 1.5", MCAddress{Device: devD(), Number: 100, Bit: -1}, "float32", []byte{0x00, 0x00, 0xC0, 0x3F}, "1.5"},
		{"float64 1.0", MCAddress{Device: devD(), Number: 100, Bit: -1}, "float64", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xF0, 0x3F}, "1"},
		{"bool 字设备取位", MCAddress{Device: devD(), Number: 100, Bit: 5}, "bool", []byte{0x34, 0x12}, "1"},
		{"bool 字设备位0", MCAddress{Device: devD(), Number: 100, Bit: 0}, "bool", []byte{0x00, 0x12}, "0"},
		{"bool 字设备高位", MCAddress{Device: devD(), Number: 100, Bit: 15}, "bool", []byte{0x00, 0x80}, "1"},
		{"bool 位设备", MCAddress{Device: devM(), Number: 10, Bit: -1}, "bool", []byte{0x01}, "1"},
		{"bool 位设备0", MCAddress{Device: devM(), Number: 11, Bit: -1}, "bool", []byte{0x00}, "0"},
		{"string", MCAddress{Device: devD(), Number: 100, Bit: -1}, "string", []byte("ABC\x00\x00"), "ABC"},
		{"string 空格填充", MCAddress{Device: devD(), Number: 100, Bit: -1}, "string", []byte("AB\x20\x20"), "AB"},
		{"bcd 1234", MCAddress{Device: devD(), Number: 100, Bit: -1}, "bcd", []byte{0x34, 0x12}, "1234"},
		{"lbcd 12345678", MCAddress{Device: devD(), Number: 100, Bit: -1}, "lbcd", []byte{0x78, 0x56, 0x34, 0x12}, "12345678"},
		{"uint8 低字节", MCAddress{Device: devD(), Number: 100, Bit: -1}, "uint8", []byte{0xAB, 0x00}, "171"},
	}
	for _, c := range cases {
		val, err := ParseMCValue(c.raw, c.addr, c.dataType)
		if err != nil {
			t.Errorf("%s: ParseMCValue = %v", c.name, err)
			continue
		}
		if got := FormatMCValue(val, c.dataType); got != c.want {
			t.Errorf("%s: format = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestParseMCValueFloat32Bits 验证 float32 字节序（MC 小端，与 math.Float32bits 一致）。
func TestParseMCValueFloat32Bits(t *testing.T) {
	v := float32(123.456)
	bits := math.Float32bits(v)
	raw := []byte{byte(bits), byte(bits >> 8), byte(bits >> 16), byte(bits >> 24)}
	addr := MCAddress{Device: devD(), Number: 100, Bit: -1}
	val, err := ParseMCValue(raw, addr, "float32")
	if err != nil {
		t.Fatalf("ParseMCValue = %v", err)
	}
	if got, want := val.(float32), v; got != want {
		t.Errorf("float32 = %v, want %v", got, want)
	}
}

// TestParseMCValueErrors 错误路径。
func TestParseMCValueErrors(t *testing.T) {
	if _, err := ParseMCValue(nil, MCAddress{Device: devD(), Number: 100, Bit: -1}, "int16"); err == nil {
		t.Errorf("expected empty raw error")
	}
	if _, err := ParseMCValue([]byte{0x00}, MCAddress{Device: devD(), Number: 100, Bit: -1}, "int16"); err == nil {
		t.Errorf("expected short raw error")
	}
	if _, err := ParseMCValue([]byte{0x34, 0x12}, MCAddress{Device: devD(), Number: 100, Bit: 5}, "bool"); err != nil {
		t.Errorf("bool bit access should accept 2-byte word, got %v", err)
	}
	if _, err := ParseMCValue([]byte{0x01}, MCAddress{Device: devD(), Number: 100, Bit: -1}, "nope"); err == nil {
		t.Errorf("expected unsupported data type error")
	}
}
