// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestComputeFCS(t *testing.T) {
	// 已知向量：'A'(0x41) ^ 'B'(0x42) = 0x03
	if got := computeFCS([]byte("AB")); got != "03" {
		t.Errorf("computeFCS(AB) = %s, want 03", got)
	}
	// 空 payload → 0x00
	if got := computeFCS(nil); got != "00" {
		t.Errorf("computeFCS(empty) = %s, want 00", got)
	}
}

func TestBuildSerialFrame(t *testing.T) {
	// 读 DM 字 100 共 1 字；unit=0, dstUnit=0, srcUnit=0, sid=0，BODY 模式（默认）
	finsBody := []byte{0x01, 0x01, 0x82, 0x00, 0x64, 0x00, 0x00, 0x01}
	frame := buildSerialFrame(fcsModeBody, 0, 0, 0, 0, finsBody)
	s := string(frame)

	if !strings.HasPrefix(s, "@00FA0") {
		t.Errorf("frame should start with @00FA0, got %q", s)
	}
	if !strings.HasSuffix(s, "*\r") {
		t.Errorf("frame should end with *\\r, got %q", s)
	}

	star := strings.IndexByte(s, '*')
	if star < 2 {
		t.Fatalf("missing '*' terminator")
	}
	// 校验 hex 体：@(1) 单元(2) FA(2) 等待(1) → ICF 起
	bodyHex := s[6 : star-2]
	wantHex := hexBytes([]byte{0x80, 0x00, 0x00, 0x00, 0x01, 0x01, 0x82, 0x00, 0x64, 0x00, 0x00, 0x01})
	if bodyHex != wantHex {
		t.Errorf("frame hex body = %s, want %s", bodyHex, wantHex)
	}
	// 校验 FCS（BODY：仅 hex 体，与 parse 端 serialFCS 取窗一致）
	wantFCS := computeFCS([]byte(s[6 : star-2]))
	if got := s[star-2 : star]; got != wantFCS {
		t.Errorf("FCS = %s, want %s", got, wantFCS)
	}
	// 全帧长度：@00FA0(6) + 24 hex + FCS(2) + *(1) + CR(1) = 34
	if len(s) != 34 {
		t.Errorf("frame length = %d, want 34", len(s))
	}
}

// TestBuildSerialFrameFullMode 校验手册标准 FULL 模式：FCS 从 @ 起至 FCS 前。
func TestBuildSerialFrameFullMode(t *testing.T) {
	finsBody := []byte{0x01, 0x01, 0x82, 0x00, 0x64, 0x00, 0x00, 0x01}
	frame := buildSerialFrame(fcsModeFull, 0, 0, 0, 0, finsBody)
	s := string(frame)

	star := strings.IndexByte(s, '*')
	if star < 2 {
		t.Fatalf("missing '*' terminator")
	}
	// 校验 FCS（FULL：含 @）
	wantFCS := computeFCS([]byte(s[0 : star-2]))
	if got := s[star-2 : star]; got != wantFCS {
		t.Errorf("FCS = %s, want %s", got, wantFCS)
	}
}

// buildTestResponse 构造一个 Host Link 响应帧（BODY 模式，与默认解析端一致）。
// 响应帧无「响应等待时间」位，且 FA 后带 1 字节 Host Link 响应码(00)。
// 数据体：响应码(00) ICF(40) DA2(00) SA2(00) SID cmd(0101) endCode 数据。
func buildTestResponse(t *testing.T, sid byte, endCode []byte, data []byte) []byte {
	t.Helper()
	body := []byte{0x00, 0x40, 0x00, 0x00, sid, 0x01, 0x01, endCode[0], endCode[1]}
	body = append(body, data...)
	hexBody := hexBytes(body)
	text := "@" + "00" + "FA" + hexBody
	return []byte(text + computeFCS([]byte(hexBody)) + "*" + "\r")
}

func TestParseSerialResponseSuccess(t *testing.T) {
	frame := buildTestResponse(t, 0x00, []byte{0x00, 0x00}, []byte{0x00, 0x64})
	data, err := parseSerialResponse(frame, 0x00, fcsModeBody)
	if err != nil {
		t.Fatalf("parseSerialResponse = %v", err)
	}
	if !strings.EqualFold(hex.EncodeToString(data), "0064") {
		t.Errorf("data = %X, want 0064", data)
	}
}

// TestParseSerialResponseFullMode 校验手册标准 FULL 模式的响应解析（FCS 含 @，无 SID 排除）。
func TestParseSerialResponseFullMode(t *testing.T) {
	body := []byte{0x00, 0x40, 0x00, 0x00, 0x00, 0x01, 0x01, 0x00, 0x00, 0x00, 0x64}
	hexBody := hexBytes(body)
	text := "@00FA" + hexBody // 响应帧无等待时间位
	frame := []byte(text + computeFCS([]byte(text)) + "*" + "\r")

	data, err := parseSerialResponse(frame, 0x00, fcsModeFull)
	if err != nil {
		t.Fatalf("parseSerialResponse = %v", err)
	}
	if !strings.EqualFold(hex.EncodeToString(data), "0064") {
		t.Errorf("data = %X, want 0064", data)
	}
}

// TestParseSerialResponseNoSID 校验 NOSID 模式（含 @、排除 SID）的响应解析。
func TestParseSerialResponseNoSID(t *testing.T) {
	// 响应体：响应码(00) ICF(40) DA2(00) SA2(00) SID cmd(0101) endCode(0000) data(0064)
	body := []byte{0x00, 0x40, 0x00, 0x00, 0x02, 0x01, 0x01, 0x00, 0x00, 0x00, 0x64}
	hexBody := hexBytes(body)
	text := "@00FA" + hexBody
	// NOSID：FCS = 全帧（含 @）排除 SID（hexBody 第 8~10 字符）
	sidStart := len(text) - len(hexBody) + 8
	fcs := computeFCSExcl([]byte(text), sidStart, sidStart+2)
	frame := []byte(text + fcs + "*" + "\r")

	data, err := parseSerialResponse(frame, 0x02, fcsModeNoSID)
	if err != nil {
		t.Fatalf("parseSerialResponse = %v", err)
	}
	if !strings.EqualFold(hex.EncodeToString(data), "0064") {
		t.Errorf("data = %X, want 0064", data)
	}
}

// TestSerialFCSNoSID 用真机抓包样本校验 NOSID 取窗（两帧仅 SID 不同，FCS 恒定 43）。
func TestSerialFCSNoSID(t *testing.T) {
	frames := []string{
		"@00FA004000000101010000000043*", // SID=01
		"@00FA004000000201010000000043*", // SID=02
	}
	for _, raw := range frames {
		star := strings.IndexByte(raw, '*')
		if star < 0 {
			t.Fatalf("missing '*' in %q", raw)
		}
		full := []byte(raw[:star-2])
		sidStart, sidEnd := 5+8, 5+10
		if got := computeFCSExcl(full, sidStart, sidEnd); got != "43" {
			t.Errorf("NOSID FCS of %q = %s, want 43", raw, got)
		}
	}
}

func TestParseSerialResponseEndCode(t *testing.T) {
	frame := buildTestResponse(t, 0x00, []byte{0x11, 0x01}, nil)
	_, err := parseSerialResponse(frame, 0x00, fcsModeBody)
	if err == nil || !IsEndCodeError(err) {
		t.Fatalf("expected end code error, got %v", err)
	}
}

func TestParseSerialResponseSIDMismatch(t *testing.T) {
	frame := buildTestResponse(t, 0x00, []byte{0x00, 0x00}, nil)
	if _, err := parseSerialResponse(frame, 0x05, fcsModeBody); err == nil {
		t.Fatalf("expected SID mismatch error")
	}
}

func TestParseSerialResponseFCSMismatch(t *testing.T) {
	frame := buildTestResponse(t, 0x00, []byte{0x00, 0x00}, nil)
	// 篡改 FCS 一个字符
	star := strings.IndexByte(string(frame), '*')
	frame[star-1] ^= 0x01
	if _, err := parseSerialResponse(frame, 0x00, fcsModeBody); err == nil {
		t.Fatalf("expected FCS mismatch error")
	}
}

func TestParseSerialResponseShort(t *testing.T) {
	if _, err := parseSerialResponse([]byte("@00"), 0x00, fcsModeBody); err == nil {
		t.Fatalf("expected short response error")
	}
}
