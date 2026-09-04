// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBuildEncapHeader(t *testing.T) {
	h := BuildEncapHeader(CmdSendRRData, 0x12345678, 100)
	if len(h) != EncapHeaderSize {
		t.Fatalf("len = %d, want %d", len(h), EncapHeaderSize)
	}
	want := []byte{
		0x6F, 0x00, // cmd = 0x006F LE
		0x64, 0x00, // length = 100 LE
		0x78, 0x56, 0x34, 0x12, // session = 0x12345678 LE
		0x00, 0x00, 0x00, 0x00, // status = 0
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // senderContext
		0x00, 0x00, 0x00, 0x00, // options
	}
	if !bytes.Equal(h, want) {
		t.Fatalf("header = % X, want % X", h, want)
	}
}

func TestParseEncapResponse(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04}
	frame := append(BuildEncapHeader(CmdRegisterSession, 0x12345678, len(data)), data...)

	cmd, session, status, got, err := ParseEncapResponse(frame)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd != CmdRegisterSession {
		t.Errorf("cmd = 0x%04X, want 0x0065", cmd)
	}
	if session != 0x12345678 {
		t.Errorf("session = 0x%08X, want 0x12345678", session)
	}
	if status != 0 {
		t.Errorf("status = 0x%08X, want 0", status)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("data = % X, want % X", got, data)
	}
}

func TestParseEncapResponseShort(t *testing.T) {
	if _, _, _, _, err := ParseEncapResponse([]byte{0x01}); err == nil {
		t.Fatal("expected error for short frame")
	}
}

func TestBuildRegisterSession(t *testing.T) {
	frame := BuildRegisterSession(0, EncapProtocolVersion)
	if len(frame) != 28 {
		t.Fatalf("len = %d, want 28", len(frame))
	}
	want := []byte{
		0x65, 0x00, // cmd = 0x0065
		0x04, 0x00, // length = 4
		0x00, 0x00, 0x00, 0x00, // session = 0
		0x00, 0x00, 0x00, 0x00, // status
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // senderContext
		0x00, 0x00, 0x00, 0x00, // options
		0x01, 0x00, // protocolVersion = 1 LE
		0x00, 0x00, // options
	}
	if !bytes.Equal(frame, want) {
		t.Fatalf("frame = % X, want % X", frame, want)
	}
}

func TestBuildSendRRData(t *testing.T) {
	cipData := []byte{0x4C, 0x04}
	frame := BuildSendRRData(0x12345678, cipData)
	// 头 24 字节 + 载荷 18 字节 = 42
	if len(frame) != 42 {
		t.Fatalf("len = %d, want 42", len(frame))
	}

	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != CmdSendRRData {
		t.Errorf("cmd = 0x%04X, want 0x006F", cmd)
	}
	if length := binary.LittleEndian.Uint16(frame[2:4]); length != 18 {
		t.Errorf("length = %d, want 18", length)
	}
	if session := binary.LittleEndian.Uint32(frame[4:8]); session != 0x12345678 {
		t.Errorf("session = 0x%08X, want 0x12345678", session)
	}

	body := frame[24:]
	want := []byte{
		0x00, 0x00, 0x00, 0x00, // interfaceHandle
		0x00, 0x00, // timeout
		0x02, 0x00, // itemCount = 2
		0x00, 0x00, 0x00, 0x00, // Null 地址项（type 0x0000 + length 0）
		0xB2, 0x00, 0x02, 0x00, // 数据项 type + length
		0x4C, 0x04, // cipData
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("body = % X, want % X", body, want)
	}
}

func TestParseSendRRData(t *testing.T) {
	resp := []byte{
		0x00, 0x00, 0x00, 0x00, // interfaceHandle
		0x00, 0x00, // timeout
		0x02, 0x00, // itemCount = 2
		0x00, 0x00, 0x00, 0x00, // Null 地址项
		0xB2, 0x00, 0x04, 0x00, 0xCC, 0x00, 0x00, 0x00, // 数据项（CIP 响应）
	}
	got, err := ParseSendRRData(resp)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	want := []byte{0xCC, 0x00, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("data = % X, want % X", got, want)
	}
}

func TestParseSendRRDataNoCIPItem(t *testing.T) {
	resp := []byte{
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00,
		0x01, 0x00, // itemCount = 1
		0x00, 0x00, 0x00, 0x00, // Null 地址项，无数据项
	}
	if _, err := ParseSendRRData(resp); err == nil {
		t.Fatal("expected error when no 0x00B2 item present")
	}
}
