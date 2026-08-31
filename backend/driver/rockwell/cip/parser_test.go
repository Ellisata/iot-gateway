package cip

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

func TestParseValueDINT(t *testing.T) {
	cfg := DefaultRockwellConfig()
	// DINT 小端：0x01020304 = 16909060
	raw := []byte{0x04, 0x03, 0x02, 0x01}
	v, err := ParseRockwellValue(raw, "int32", cfg)
	if err != nil {
		t.Fatalf("ParseRockwellValue error: %v", err)
	}
	if v.(int32) != 16909060 {
		t.Errorf("int32 = %v, want 16909060", v)
	}
}

func TestParseValueSigned(t *testing.T) {
	cfg := DefaultRockwellConfig()
	// SINT = -1
	v, err := ParseRockwellValue([]byte{0xFF}, "int8", cfg)
	if err != nil {
		t.Fatalf("int8 error: %v", err)
	}
	if v.(int8) != -1 {
		t.Errorf("int8 = %v, want -1", v)
	}
	// INT = -2
	v, err = ParseRockwellValue([]byte{0xFE, 0xFF}, "int16", cfg)
	if err != nil {
		t.Fatalf("int16 error: %v", err)
	}
	if v.(int16) != -2 {
		t.Errorf("int16 = %v, want -2", v)
	}
	// LINT = -3
	raw := make([]byte, 8)
	binary.LittleEndian.PutUint64(raw, math.MaxUint64-2) // -3 的补码
	v, err = ParseRockwellValue(raw, "int64", cfg)
	if err != nil {
		t.Fatalf("int64 error: %v", err)
	}
	if v.(int64) != -3 {
		t.Errorf("int64 = %v, want -3", v)
	}
}

func TestParseValueFloat(t *testing.T) {
	cfg := DefaultRockwellConfig()
	// REAL = 3.14
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, math.Float32bits(3.14))
	v, err := ParseRockwellValue(raw, "float32", cfg)
	if err != nil {
		t.Fatalf("float32 error: %v", err)
	}
	if v.(float32) != 3.14 {
		t.Errorf("float32 = %v, want 3.14", v)
	}
	// LREAL = 2.718281828
	raw8 := make([]byte, 8)
	binary.LittleEndian.PutUint64(raw8, math.Float64bits(2.718281828))
	v, err = ParseRockwellValue(raw8, "float64", cfg)
	if err != nil {
		t.Fatalf("float64 error: %v", err)
	}
	if v.(float64) != 2.718281828 {
		t.Errorf("float64 = %v, want 2.718281828", v)
	}
}

func TestParseValueBOOL(t *testing.T) {
	cfg := DefaultRockwellConfig()
	v, err := ParseRockwellValue([]byte{0x01}, "bool", cfg)
	if err != nil {
		t.Fatalf("bool error: %v", err)
	}
	if v.(bool) != true {
		t.Errorf("bool = %v, want true", v)
	}
	v, _ = ParseRockwellValue([]byte{0x00}, "bool", cfg)
	if v.(bool) != false {
		t.Errorf("bool = %v, want false", v)
	}
}

func TestParseValueSTRING(t *testing.T) {
	cfg := DefaultRockwellConfig()
	// Logix STRING：4 字节 LE 长度 + 字符
	raw := []byte{0x04, 0x00, 0x00, 0x00, 'P', 'L', 'C', '1'}
	v, err := ParseRockwellValue(raw, "string", cfg)
	if err != nil {
		t.Fatalf("string error: %v", err)
	}
	if v.(string) != "PLC1" {
		t.Errorf("string = %q, want %q", v, "PLC1")
	}
}

func TestParseValueStringOverrunTolerance(t *testing.T) {
	cfg := DefaultRockwellConfig()
	// 长度字段越界（声明 100，实际只有 4 字符）→ 按实际长度容错
	raw := []byte{0x64, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D'}
	v, err := ParseRockwellValue(raw, "string", cfg)
	if err != nil {
		t.Fatalf("string overrun error: %v", err)
	}
	if v.(string) != "ABCD" {
		t.Errorf("string = %q, want %q", v, "ABCD")
	}
}

func TestParseValueStringTruncation(t *testing.T) {
	cfg := DefaultRockwellConfig()
	cfg.StringLen = 4 // 强制小上限
	raw := []byte{0x08, 0x00, 0x00, 0x00, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H'}
	v, err := ParseRockwellValue(raw, "string", cfg)
	if err != nil {
		t.Fatalf("string truncation error: %v", err)
	}
	if v.(string) != "ABCD" {
		t.Errorf("string = %q, want %q (truncated)", v, "ABCD")
	}
}

func TestParseValueErrors(t *testing.T) {
	cfg := DefaultRockwellConfig()
	cases := []struct {
		name     string
		raw      []byte
		dataType string
		wantSub  string
	}{
		{"empty raw", nil, "int32", "empty raw"},
		{"unsupported type", []byte{1, 2, 3, 4}, "uint32", "unsupported data type"},
		{"short buffer", []byte{1, 2}, "int32", "needs 4 bytes"},
		{"string short prefix", []byte{4, 0}, "string", "length prefix"},
	}
	for _, c := range cases {
		if _, err := ParseRockwellValue(c.raw, c.dataType, cfg); err == nil {
			t.Errorf("%s: expected error", c.name)
		} else if !strings.Contains(err.Error(), c.wantSub) {
			t.Errorf("%s: error = %q, want contain %q", c.name, err, c.wantSub)
		}
	}
}

func TestStripTypeCodeHeader(t *testing.T) {
	// 基础类型：2 字节头
	data := mkResp(cipTypeDINT, 0x01, 0x02, 0x03, 0x04)
	payload, err := stripTypeCodeHeader(data)
	if err != nil {
		t.Fatalf("strip header error: %v", err)
	}
	if len(payload) != 4 || payload[0] != 0x01 {
		t.Errorf("payload = % X, want 01 02 03 04", payload)
	}
	// STRUCT 型（STRING 0x00D0 ≥ 0x02A0？否——注意 0x00D0 < 0x02A0，走 2 字节头）
	data = mkResp(cipTypeSTRING, 0x04, 0x00, 0x00, 0x00, 'H', 'I')
	payload, err = stripTypeCodeHeader(data)
	if err != nil {
		t.Fatalf("strip STRING header error: %v", err)
	}
	if len(payload) != 6 {
		t.Errorf("STRING payload len = %d, want 6", len(payload))
	}
	// 数组/结构型（≥ TypeSTRUCT 0x02A0）：4 字节头（2 类型码 + 2 成员数）
	data = append([]byte{0xA0, 0x02, 0x01, 0x00}, 0xAA, 0xBB)
	payload, err = stripTypeCodeHeader(data)
	if err != nil {
		t.Fatalf("strip struct header error: %v", err)
	}
	if len(payload) != 2 || payload[0] != 0xAA {
		t.Errorf("struct payload = % X, want AA BB", payload)
	}
	// 过短响应
	if _, err := stripTypeCodeHeader([]byte{0xC1}); err == nil {
		t.Error("expected error for 1-byte response")
	}
}

func TestFormatValue(t *testing.T) {
	if got := FormatRockwellValue(true, "bool"); got != "1" {
		t.Errorf("FormatRockwellValue(bool true) = %q, want %q", got, "1")
	}
	if got := FormatRockwellValue(float32(3.14), "float32"); got != "3.14" {
		t.Errorf("FormatRockwellValue(float32) = %q, want %q", got, "3.14")
	}
	if got := FormatRockwellValue(int32(42), "int32"); got != "42" {
		t.Errorf("FormatRockwellValue(int32) = %q, want %q", got, "42")
	}
	if got := FormatRockwellValue("hello", "string"); got != "hello" {
		t.Errorf("FormatRockwellValue(string) = %q, want %q", got, "hello")
	}
}

func TestRangeCalc(t *testing.T) {
	addrs := []struct {
		id, name string
	}{{"a1", "TagA"}, {"a2", "TagB"}, {"a3", "TagA"}}
	_ = addrs
	// buildPlan 基础行为已由 driver_test.go 的去重/管线测试覆盖，
	// 此处仅验证空点位报错。
	if _, err := buildPlan(nil); err == nil {
		t.Error("expected error for empty addresses")
	}
}
