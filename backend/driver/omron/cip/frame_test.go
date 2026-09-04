// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

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

func TestBuildMultipleDataTableRead(t *testing.T) {
	specs := []TagSpec{{Name: "MotorSpeed", Code: cipTypeINT}, {Name: "Temp", Code: cipTypeDINT}}
	req := buildMultipleDataTableRead(specs)
	want := append([]byte{0x0A, 0x02}, buildDataTableRead("MotorSpeed", cipTypeINT)...)
	want = append(want, buildDataTableRead("Temp", cipTypeDINT)...)
	if !bytes.Equal(req, want) {
		t.Fatalf("req = % X, want % X", req, want)
	}
}

func TestParseMultipleDataTableReadSuccess(t *testing.T) {
	// 2 个标签（int16=2 字节、int32=4 字节），各子响应均为成功 0xCC
	resp := []byte{
		0x8A, 0x02,
		0xCC, 0x00, 0x00, 0xC3, 0x00, 0x00, 0x64, // int16: 值 0x6400
		0xCC, 0x00, 0x00, 0xC4, 0x00, 0x00, 0x00, 0x00, 0x01, // int32
	}
	results, err := parseMultipleDataTableRead(resp, []int{2, 4})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Err != nil || len(results[0].Data) != 2 {
		t.Errorf("result[0] = %+v, want 2-byte data", results[0])
	}
	if results[1].Err != nil || len(results[1].Data) != 4 {
		t.Errorf("result[1] = %+v, want 4-byte data", results[1])
	}
}

func TestParseMultipleDataTableReadMixedStatus(t *testing.T) {
	// 第 1 个标签被拒绝（4 字节错误响应），第 2 个成功 —— 验证错误子响应边界自定位
	resp := []byte{
		0x8A, 0x02,
		0xCC, 0x00, 0x04, 0x00, // 错误：状态 0x04 tag not found，附加状态字数 0
		0xCC, 0x00, 0x00, 0xC3, 0x00, 0x00, 0x64,
	}
	results, err := parseMultipleDataTableRead(resp, []int{2, 2})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cipcore.IsGeneralStatusError(results[0].Err) {
		t.Errorf("result[0] should carry GeneralStatusError, got %v", results[0].Err)
	}
	if results[1].Err != nil || len(results[1].Data) != 2 {
		t.Errorf("result[1] should be success with 2-byte data, got %+v", results[1])
	}
}

func TestParseMultipleDataTableReadErrors(t *testing.T) {
	// 服务回显错误
	if _, err := parseMultipleDataTableRead([]byte{0x4C, 0x01, 0xCC}, []int{2}); err == nil {
		t.Fatal("expected error for bad service echo")
	}
	// 数量不匹配
	if _, err := parseMultipleDataTableRead([]byte{0x8A, 0x03, 0xCC}, []int{2}); err == nil {
		t.Fatal("expected error for count mismatch")
	}
	// 截断
	if _, err := parseMultipleDataTableRead([]byte{0x8A, 0x01, 0xCC, 0x00}, []int{2}); err == nil {
		t.Fatal("expected error for truncated response")
	}
}
