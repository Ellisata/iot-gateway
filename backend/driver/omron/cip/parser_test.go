package cip

import (
	"testing"
)

func TestParseCIPValueScalars(t *testing.T) {
	cfg := &CIPConfig{StringLen: defaultStringLen}
	cases := []struct {
		name     string
		dataType string
		raw      []byte
		want     any
	}{
		{"bool true", "bool", []byte{0x01}, true},
		{"bool false", "bool", []byte{0x00}, false},
		{"int16", "int16", []byte{0x9C, 0xFF}, int16(-100)},
		{"uint16", "uint16", []byte{0x64, 0x00}, uint16(100)},
		{"word", "word", []byte{0x64, 0x00}, uint16(100)},
		{"int32", "int32", []byte{0x9C, 0xFF, 0xFF, 0xFF}, int32(-100)},
		{"uint32", "uint32", []byte{0x64, 0x00, 0x00, 0x00}, uint32(100)},
		{"float32", "float32", []byte{0x00, 0x00, 0xC0, 0x3F}, float32(1.5)},
		{"float64", "float64", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xF8, 0x3F}, float64(1.5)},
		{"uint8", "uint8", []byte{0x64}, uint8(100)},
		{"int8", "int8", []byte{0x9C}, int8(-100)},
		{"bcd", "bcd", []byte{0x34, 0x12}, 1234},
		{"lbcd", "lbcd", []byte{0x78, 0x56, 0x34, 0x12}, 12345678},
	}
	for _, c := range cases {
		got, err := ParseCIPValue(c.raw, c.dataType, cfg)
		if err != nil {
			t.Errorf("%s: parse error: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %v (%T), want %v (%T)", c.name, got, got, c.want, c.want)
		}
	}
}

func TestParseCIPValueString(t *testing.T) {
	// 尾部 \x00 去除
	cfg := &CIPConfig{StringLen: defaultStringLen}
	got, err := ParseCIPValue([]byte{0x41, 0x42, 0x00, 0x00}, "string", cfg)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if got != "AB" {
		t.Errorf("string = %q, want AB", got)
	}
}

func TestParseCIPValueStringTruncate(t *testing.T) {
	cfg := &CIPConfig{StringLen: 3}
	got, err := ParseCIPValue([]byte{0x41, 0x42, 0x43, 0x44}, "string", cfg)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if got != "ABC" {
		t.Errorf("string = %q, want ABC", got)
	}
}

func TestParseCIPValueShort(t *testing.T) {
	cfg := &CIPConfig{StringLen: defaultStringLen}
	if _, err := ParseCIPValue([]byte{0x01}, "int32", cfg); err == nil {
		t.Fatal("expected error for short raw data")
	}
	if _, err := ParseCIPValue(nil, "int16", cfg); err == nil {
		t.Fatal("expected error for empty raw data")
	}
}

func TestParseCIPValueUnsupported(t *testing.T) {
	cfg := &CIPConfig{StringLen: defaultStringLen}
	if _, err := ParseCIPValue([]byte{0x01, 0x00}, "date", cfg); err == nil {
		t.Fatal("expected error for unsupported data type")
	}
}

func TestFormatCIPValue(t *testing.T) {
	if got := FormatCIPValue(true, "bool"); got != "1" {
		t.Errorf("FormatCIPValue(true, bool) = %q, want 1", got)
	}
	if got := FormatCIPValue(false, "bool"); got != "0" {
		t.Errorf("FormatCIPValue(false, bool) = %q, want 0", got)
	}
	if got := FormatCIPValue(float32(1.5), "float32"); got != "1.5" {
		t.Errorf("FormatCIPValue(1.5, float32) = %q, want 1.5", got)
	}
	if got := FormatCIPValue(int16(-100), "int16"); got != "-100" {
		t.Errorf("FormatCIPValue(-100, int16) = %q, want -100", got)
	}
}
