// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"testing"
)

func TestParseFINSValueWordTypes(t *testing.T) {
	cfg := DefaultFINSConfig()

	cases := []struct {
		name     string
		dataType string
		raw      []byte
		want     any
	}{
		{"int16 positive", "int16", []byte{0x00, 0x64}, int16(100)},
		{"int16 negative", "int16", []byte{0xFF, 0x9C}, int16(-100)},
		{"uint16", "uint16", []byte{0x03, 0xE8}, uint16(1000)},
		{"word", "word", []byte{0x03, 0xE8}, uint16(1000)},
		{"int32", "int32", []byte{0x00, 0x00, 0x00, 0x64}, int32(100)},
		{"uint32", "uint32", []byte{0x00, 0x00, 0x03, 0xE8}, uint32(1000)},
		{"float32", "float32", []byte{0x3F, 0xC0, 0x00, 0x00}, float32(1.5)},
		{"float64", "float64", []byte{0x3F, 0xF8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, float64(1.5)},
		{"string", "string", []byte{'A', 'B', 0x00, 0x00}, "AB"},
		{"bcd", "bcd", []byte{0x12, 0x34}, int(1234)},
		{"lbcd", "lbcd", []byte{0x12, 0x34, 0x56, 0x78}, int(12345678)},
		{"date", "date", []byte{0x00, 0x00, 0x00, 0x64}, int64(100)},
		{"uint8", "uint8", []byte{0x12, 0x34}, uint8(0x34)},
		{"int8", "int8", []byte{0x12, 0xFC}, int8(-4)},
	}

	for _, c := range cases {
		got, err := ParseFINSValue(c.raw, FINSAddress{Area: AreaDM, Word: 100, Bit: -1}, c.dataType, cfg)
		if err != nil {
			t.Errorf("%s: ParseFINSValue = %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %v (%T), want %v (%T)", c.name, got, got, c.want, c.want)
		}
	}
}

func TestParseFINSValueWordOrder(t *testing.T) {
	// 小端字序：int32 的两个字交换，原始字节 [00 64 00 00] 表示 100
	cfg := DefaultFINSConfig()
	cfg.WordOrder = WordOrderLittleEndian
	got, err := ParseFINSValue([]byte{0x00, 0x64, 0x00, 0x00}, FINSAddress{Area: AreaDM, Word: 100, Bit: -1}, "int32", cfg)
	if err != nil {
		t.Fatalf("ParseFINSValue = %v", err)
	}
	if got != int32(100) {
		t.Errorf("word-swapped int32 = %v, want 100", got)
	}

	// 大端字序（默认）下同样的原始字节不是 100
	gotBE, err := ParseFINSValue([]byte{0x00, 0x64, 0x00, 0x00}, FINSAddress{Area: AreaDM, Word: 100, Bit: -1}, "int32", DefaultFINSConfig())
	if err != nil {
		t.Fatalf("ParseFINSValue = %v", err)
	}
	if gotBE == int32(100) {
		t.Errorf("unexpected: big-endian should not equal 100 for swapped bytes")
	}
}

func TestParseFINSValueBoolBit(t *testing.T) {
	cfg := DefaultFINSConfig()

	cases := []struct {
		name string
		addr FINSAddress
		raw  []byte
		want bool
	}{
		{"bit5 set (low byte)", FINSAddress{Area: AreaDM, Word: 100, Bit: 5}, []byte{0x00, 0x20}, true},
		{"bit5 clear", FINSAddress{Area: AreaDM, Word: 100, Bit: 5}, []byte{0x00, 0x00}, false},
		{"bit8 set (high byte)", FINSAddress{Area: AreaDM, Word: 100, Bit: 8}, []byte{0x01, 0x00}, true},
		{"bit15 set (high byte)", FINSAddress{Area: AreaDM, Word: 100, Bit: 15}, []byte{0x80, 0x00}, true},
		{"bit15 clear", FINSAddress{Area: AreaDM, Word: 100, Bit: 15}, []byte{0x7F, 0xFF}, false},
		{"bit0 set", FINSAddress{Area: AreaDM, Word: 100, Bit: 0}, []byte{0x00, 0x01}, true},
	}

	for _, c := range cases {
		got, err := ParseFINSValue(c.raw, c.addr, "bool", cfg)
		if err != nil {
			t.Errorf("%s: ParseFINSValue = %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestParseFINSValueBoolWord(t *testing.T) {
	// bool 点位但地址无位后缀（Bit=-1）：以 word 地址处理，decodeBool 取低字节 bit0
	cfg := DefaultFINSConfig()
	got, err := ParseFINSValue([]byte{0x00, 0x01}, FINSAddress{Area: AreaDM, Word: 100, Bit: -1}, "bool", cfg)
	if err != nil {
		t.Fatalf("ParseFINSValue = %v", err)
	}
	if got != true {
		t.Errorf("bool word address bit0 = %v, want true", got)
	}
}

func TestParseFINSValueUnsupportedType(t *testing.T) {
	_, err := ParseFINSValue([]byte{0x00, 0x01}, FINSAddress{Area: AreaDM, Word: 100}, "banana", DefaultFINSConfig())
	if err == nil {
		t.Fatalf("expected unsupported type error")
	}
}

func TestFormatFINSValue(t *testing.T) {
	if got := FormatFINSValue(int16(123), "int16"); got != "123" {
		t.Errorf("FormatFINSValue(int16) = %s, want 123", got)
	}
	if got := FormatFINSValue(float32(1.5), "float32"); got != "1.5" {
		t.Errorf("FormatFINSValue(float32) = %s, want 1.5", got)
	}
	if got := FormatFINSValue(true, "bool"); got != "1" {
		t.Errorf("FormatFINSValue(bool) = %s, want 1", got)
	}
}
