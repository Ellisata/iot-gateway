package cip

import (
	"bytes"
	"strings"
	"testing"

	cipcore "iot-gateway/driver/cip"
)

// EtherNet/IP 封装层测试（BuildEncapHeader / ParseEncapResponse / Register Session /
// SendRRData）已随封装层上移至 iot-gateway/driver/cip/encap_test.go。

func TestBuildDataTableRead(t *testing.T) {
	// "MotorSpeed" 长度 10（偶数）→ 路径无需补位
	req := buildDataTableRead("MotorSpeed", cipTypeINT)
	want := []byte{
		0x4C, 0x06, // service + path 字数(words)
		0x91, 0x0A, // ANSI Extended Symbol 段头 + 名字节数
		'M', 'o', 't', 'o', 'r', 'S', 'p', 'e', 'e', 'd',
		0x01, 0x00, // elementCount = 1 LE
	}
	if !bytes.Equal(req, want) {
		t.Fatalf("req = % X, want % X", req, want)
	}

	// "Motor" 长度 5（奇数）→ 路径补 0x00 到整字
	req = buildDataTableRead("Motor", cipTypeINT)
	want = []byte{
		0x4C, 0x04, // service + path 字数(4 words = 8 字节)
		0x91, 0x05, 'M', 'o', 't', 'o', 'r', 0x00, // 符号段 + 奇数补位
		0x01, 0x00, // elementCount = 1 LE
	}
	if !bytes.Equal(req, want) {
		t.Fatalf("odd req = % X, want % X", req, want)
	}
}

func TestParseDataTableReadSuccess(t *testing.T) {
	resp := []byte{0xCC, 0x00, 0x00, 0xC3, 0x00, 0x00, 0x64}
	got, err := parseDataTableRead(resp)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	want := []byte{0x00, 0x64}
	if !bytes.Equal(got, want) {
		t.Errorf("data = % X, want % X", got, want)
	}
}

func TestParseDataTableReadGeneralStatus(t *testing.T) {
	// 状态 0x04：路径错误/变量未找到
	resp := []byte{0xCC, 0x00, 0x04, 0xC3, 0x00}
	_, err := parseDataTableRead(resp)
	if err == nil {
		t.Fatal("expected general status error")
	}
	if !cipcore.IsGeneralStatusError(err) {
		t.Fatalf("expected GeneralStatusError, got %T: %v", err, err)
	}
}

func TestParseDataTableReadShort(t *testing.T) {
	if _, err := parseDataTableRead([]byte{0xCC}); err == nil {
		t.Fatal("expected error for short response")
	}
}

func TestParseDataTableReadGeneralStatusFourByte(t *testing.T) {
	// 真机 2026-08-17：NX1P2 对不存在的标签返回 4 字节错误响应 CC 00 04 00，
	// 此前被误报为 "short response"，掩盖真正的通用状态 0x04（标签未找到）。
	resp := []byte{0xCC, 0x00, 0x04, 0x00}
	_, err := parseDataTableRead(resp)
	if err == nil {
		t.Fatal("expected general status error")
	}
	if !cipcore.IsGeneralStatusError(err) {
		t.Fatalf("expected GeneralStatusError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "tag not found") {
		t.Errorf("error message should hint tag not found, got: %v", err)
	}
}

func TestParseDataTableReadBadServiceEcho(t *testing.T) {
	if _, err := parseDataTableRead([]byte{0x4C, 0x00, 0x00, 0xC3, 0x00}); err == nil {
		t.Fatal("expected error for bad service echo")
	}
}
