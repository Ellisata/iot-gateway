// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBuildReadFrameUDP(t *testing.T) {
	// 读 DM 字 100 共 1 字；DA1=0x32, DA2=0x00, SA1=0x39, SA2=0x00, SID=0x01
	frame := buildReadFrame(AreaDM, 100, 1, 0x32, 0x00, 0x39, 0x00, 0x01)
	want := []byte{
		0x80, 0x00, 0x02, 0x00, 0x32, 0x00, 0x00, 0x39, 0x00, 0x01, // FINS 头
		0x01, 0x01, // 命令码：内存区读取
		0x82,       // 内存区：DM
		0x00, 0x64, // 字地址 100
		0x00,       // 位：字访问
		0x00, 0x01, // 数量 1 字
	}
	if !bytes.Equal(frame, want) {
		t.Errorf("buildReadFrame:\n got %X\nwant %X", frame, want)
	}
}

func TestParseReadResponseSuccess(t *testing.T) {
	// 响应：FINS 头（SID=0x01）+ 命令回显 + 结束码 0000 + 1 字数据 [00 64]
	resp := []byte{
		0xC0, 0x00, 0x02, 0x00, 0x32, 0x00, 0x00, 0x39, 0x00, 0x01,
		0x01, 0x01, 0x00, 0x00,
		0x00, 0x64,
	}
	data, err := parseReadResponse(resp, 0x01)
	if err != nil {
		t.Fatalf("parseReadResponse = %v", err)
	}
	if !bytes.Equal(data, []byte{0x00, 0x64}) {
		t.Errorf("data = %X, want %X", data, []byte{0x00, 0x64})
	}
}

func TestParseReadResponseEndCode(t *testing.T) {
	// 结束码 0x1101（非法区域）→ finsEndCodeError
	resp := []byte{
		0xC0, 0x00, 0x02, 0x00, 0x32, 0x00, 0x00, 0x39, 0x00, 0x01,
		0x01, 0x01, 0x11, 0x01,
	}
	_, err := parseReadResponse(resp, 0x01)
	if err == nil {
		t.Fatalf("expected end code error")
	}
	if !IsEndCodeError(err) {
		t.Errorf("IsEndCodeError = false, want true (%v)", err)
	}
}

func TestParseReadResponseSIDMismatch(t *testing.T) {
	resp := []byte{
		0xC0, 0x00, 0x02, 0x00, 0x32, 0x00, 0x00, 0x39, 0x00, 0x02,
		0x01, 0x01, 0x00, 0x00,
	}
	if _, err := parseReadResponse(resp, 0x01); err == nil {
		t.Fatalf("expected SID mismatch error")
	}
}

func TestParseReadResponseShort(t *testing.T) {
	if _, err := parseReadResponse([]byte{0x01, 0x02}, 0x01); err == nil {
		t.Fatalf("expected short response error")
	}
}

func TestBuildTCPHeader(t *testing.T) {
	// 数据发送命令，FINS 载荷 18 字节（10 头 + 8 命令体）→ 长度字段 = 8 + 18 = 26，命令码 = 0x00000002
	hdr := buildTCPHeader(finsTCPCmdDataSend, 0, 18)
	want := []byte{'F', 'I', 'N', 'S', 0x00, 0x00, 0x00, 0x1A, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00}
	if !bytes.Equal(hdr, want) {
		t.Errorf("buildTCPHeader:\n got %X\nwant %X", hdr, want)
	}
}

func TestBuildConnectFrame(t *testing.T) {
	// 连接请求：FINS + 长度 12 + 命令 0x00000000 + 错误码 0 + 4 字节节点号 0x90
	frame := buildConnectFrame(0x90)
	want := []byte{
		'F', 'I', 'N', 'S', 0x00, 0x00, 0x00, 0x0C,
		0x00, 0x00, 0x00, 0x00, // 命令：连接请求
		0x00, 0x00, 0x00, 0x00, // 错误码
		0x00, 0x00, 0x00, 0x90, // 节点号
	}
	if !bytes.Equal(frame, want) {
		t.Errorf("buildConnectFrame:\n got %X\nwant %X", frame, want)
	}
}

func TestParseTCPFrame(t *testing.T) {
	frame := make([]byte, 16+18)
	copy(frame[0:4], []byte{'F', 'I', 'N', 'S'})
	binary.BigEndian.PutUint32(frame[4:8], 8+18)
	binary.BigEndian.PutUint32(frame[8:12], finsTCPCmdDataResp)
	binary.BigEndian.PutUint32(frame[12:16], 0)
	cmd, errCode, payload, err := parseTCPFrame(frame)
	if err != nil {
		t.Fatalf("parseTCPFrame = %v", err)
	}
	if cmd != finsTCPCmdDataResp || errCode != 0 || len(payload) != 18 {
		t.Errorf("cmd=%X err=%X payload=%d", cmd, errCode, len(payload))
	}
}

func TestBuildFINSHeader(t *testing.T) {
	hdr := buildFINSHeader(0x32, 0x00, 0x39, 0x00, 0x05)
	want := []byte{0x80, 0x00, 0x02, 0x00, 0x32, 0x00, 0x00, 0x39, 0x00, 0x05}
	if !bytes.Equal(hdr, want) {
		t.Errorf("buildFINSHeader:\n got %X\nwant %X", hdr, want)
	}
}
