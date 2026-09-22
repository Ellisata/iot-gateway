// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"bytes"
	"testing"
)

// TestBuildReadFrameWord D100 字单位读帧（21 字节，含请求数据长度与监视定时器）。
// 3E/SLMP 二进制帧多字节字段均小端：命令 01 04、I/O FF 03、长度 0C 00、定时器 10 00。
//
// 期望字节取自真机实测：这个请求打在真 PLC 上回 0x0000 + 数据；
// 换成「设备码 2 字节在前、首地址号在后」的旧布局，同一地址回 0xC056/C05A。
func TestBuildReadFrameWord(t *testing.T) {
	dev, _ := lookupDevice("D")
	frame := buildReadFrame(dev, 100, 1, false)
	want := []byte{
		0x50, 0x00, // 子头：读
		0x00, 0xFF, 0xFF, 0x03, 0x00, // 网络/PC/IO/站（I/O 0x03FF 小端）
		0x0C, 0x00, // 请求数据长度：监视定时器(2) + 请求体(10)（小端）
		0x10, 0x00, // 监视定时器：4s（小端）
		0x01, 0x04, // 命令：批量读（小端 0x0401）
		0x00, 0x00, // 子命令：字单位
		0x64, 0x00, 0x00, // 首地址 100（LE 3 字节）
		0xA8,       // 设备码 D（1 字节）
		0x01, 0x00, // 点数 1（LE）
	}
	if !bytes.Equal(frame, want) {
		t.Errorf("buildReadFrame(D,100,1) =\n % X\n want\n % X", frame, want)
	}
}

// TestBuildReadFrameBit M10 位单位读帧（子命令 0x0001，设备码 M=0x90）。
func TestBuildReadFrameBit(t *testing.T) {
	dev, _ := lookupDevice("M")
	frame := buildReadFrame(dev, 10, 1, true)
	want := []byte{
		0x50, 0x00,
		0x00, 0xFF, 0xFF, 0x03, 0x00,
		0x0C, 0x00,
		0x10, 0x00,
		0x01, 0x04,
		0x01, 0x00, // 子命令：位单位（小端 0x0001）
		0x0A, 0x00, 0x00, // 首地址 10（3 字节 LE）
		0x90,       // 设备码 M（1 字节）
		0x01, 0x00,
	}
	if !bytes.Equal(frame, want) {
		t.Errorf("buildReadFrame(M,10,1,bit) =\n % X\n want\n % X", frame, want)
	}
}

// TestParseTCPEndCode 响应头结束码解析（11 字节：子头 2 + 网络/PC/IO/站 5 + 响应数据长度 2 + 结束码 2）。
// 结束码为小端：0xC056 线上为 56 C0。
func TestParseTCPEndCode(t *testing.T) {
	// 正常响应头：长度 0x0004（结束码 2 + 数据 2）+ 结束码 0x0000
	normal := []byte{0xD0, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x00, 0x04, 0x00, 0x00, 0x00}
	if end, err := parseTCPEndCode(normal); err != nil || end != 0 {
		t.Errorf("parseTCPEndCode(normal) = %d, %v", end, err)
	}
	if dl := tcpResponseDataLen(normal); dl != 4 {
		t.Errorf("tcpResponseDataLen(normal) = %d, want 4", dl)
	}
	// 错误响应头：长度 0x0004 + 结束码 0xC056（地址范围外，小端）
	errHdr := []byte{0xD0, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x00, 0x04, 0x00, 0x56, 0xC0}
	if _, err := parseTCPEndCode(errHdr); err == nil || !IsEndCodeError(err) {
		t.Errorf("parseTCPEndCode(err) = %v, want end code error", err)
	}
	// 子头非 0xD0
	bad := []byte{0x00, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x00, 0x04, 0x00, 0x00, 0x00}
	if _, err := parseTCPEndCode(bad); err == nil || IsEndCodeError(err) {
		t.Errorf("parseTCPEndCode(bad subheader) = %v, want non-endcode error", err)
	}
	// 短响应
	if _, err := parseTCPEndCode([]byte{0xD0}); err == nil {
		t.Errorf("expected short response error")
	}
}

// TestBuildSerialFrame D100 字单位串口帧（4C Format5，含和校验）。
// 4C Format5 二进制与 3E 同为小端：I/O FF 03、数据长度 0A 00、命令 01 04。
//
// 请求体与 3E 共用 buildMCBody（软元件顺序已按真机修正）；4C 的外部封帧
// 本身（DLE 填充、和校验、是否带监视定时器）尚未经真机核实，见 frameserial.go。
func TestBuildSerialFrame(t *testing.T) {
	dev, _ := lookupDevice("D")
	frame := buildSerialFrame(dev, 100, 1, false)
	want := []byte{
		0x10, 0x02, // DLE STX
		0x00, 0xFF, 0xFF, 0x03, 0x00, // 网络/PC/IO/站（I/O 小端）
		0x0A, 0x00, // 数据长度 = 请求体 10 字节（小端）
		0x01, 0x04, 0x00, 0x00, // 命令/子命令（字单位，小端）
		0x64, 0x00, 0x00, // 首地址 100（3 字节 LE）
		0xA8,       // 设备码 D（1 字节）
		0x01, 0x00, // 点数 1
		0x1D,       // 和校验（inner 累加低 8 位）
		0x10, 0x03, // DLE ETX
	}
	if !bytes.Equal(frame, want) {
		t.Errorf("buildSerialFrame(D,100,1) =\n % X\n want\n % X", frame, want)
	}
}

// TestSerialFrameDLEStuffing 数据含 0x10 时的 DLE 填充往返。
func TestSerialFrameDLEStuffing(t *testing.T) {
	// 响应：ID(FFFF) + 头 + len=4 + 结束码 0 + 数据 [0x10,0x00] + 和校验
	interior := []byte{
		0xFF, 0xFF, // 响应 ID
		0x00, 0xFF, 0xFF, 0x03, 0x00, // 网络/PC/IO/站（I/O 小端）
		0x04, 0x00, // 数据长度 = 结束码 2 + 数据 2（小端）
		0x00, 0x00, // 结束码：正常
		0x10, 0x00, // 数据（含 0x10）
		0x13, // 和校验（累加低 8 位）
	}
	// 填充后：0x10 数据字节前插入 0x10
	stuffed := dleStuff(interior)
	if !bytes.Equal(stuffed, []byte{
		0xFF, 0xFF, 0x00, 0xFF, 0xFF, 0x03, 0x00,
		0x04, 0x00, 0x00, 0x00, 0x10, 0x10, 0x00, 0x13,
	}) {
		t.Fatalf("dleStuff =\n % X", stuffed)
	}
	// 解填充还原
	if !bytes.Equal(dleUnstuff(stuffed), interior) {
		t.Fatalf("dleUnstuff(stuff(interior)) != interior")
	}
	// 整帧解析出数据
	data, err := parseSerialResponse(interior)
	if err != nil {
		t.Fatalf("parseSerialResponse = %v", err)
	}
	if !bytes.Equal(data, []byte{0x10, 0x00}) {
		t.Errorf("parseSerialResponse data = % X, want 10 00", data)
	}
}

// TestParseSerialResponseEndCode 串口响应结束码错误。
func TestParseSerialResponseEndCode(t *testing.T) {
	// 结束码 0xC056（地址范围外，小端 56 C0），数据 2 字节
	interior := []byte{
		0xFF, 0xFF,
		0x00, 0xFF, 0xFF, 0x03, 0x00,
		0x04, 0x00,
		0x56, 0xC0, // 结束码：地址范围外（小端）
		0x00, 0x00,
		0x19, // 和校验
	}
	if _, err := parseSerialResponse(interior); err == nil || !IsEndCodeError(err) {
		t.Errorf("parseSerialResponse(err) = %v, want end code error", err)
	}
}

// TestParseSerialResponseSumMismatch 和校验不匹配报错。
func TestParseSerialResponseSumMismatch(t *testing.T) {
	interior := []byte{
		0xFF, 0xFF,
		0x00, 0xFF, 0xFF, 0x03, 0x00,
		0x04, 0x00,
		0x00, 0x00,
		0x12, 0x34,
		0x00, // 错误和校验
	}
	if _, err := parseSerialResponse(interior); err == nil {
		t.Errorf("expected sum mismatch error")
	}
}
