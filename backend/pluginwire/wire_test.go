// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginwire_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"iot-gateway/pluginwire"
)

func TestFrameRoundTrip(t *testing.T) {
	params := json.RawMessage(`{"protocolJson":"{\"host\":\"127.0.0.1\"}"}`)
	sent := pluginwire.Frame{
		ID:     42,
		Method: pluginwire.MethodConnect,
		Params: params,
	}

	var buf bytes.Buffer
	if err := pluginwire.WriteFrame(&buf, sent); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := pluginwire.ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got.ID != sent.ID || got.Method != sent.Method {
		t.Errorf("往返后 ID/Method = %d/%q, want %d/%q", got.ID, got.Method, sent.ID, sent.Method)
	}
	if !bytes.Equal(got.Params, params) {
		t.Errorf("往返后 Params = %s, want %s", got.Params, params)
	}
}

func TestFrameEmptyPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := pluginwire.WriteFrame(&buf, pluginwire.Frame{ID: 1}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	// 头 8 字节 + 至少 "{}" 的 2 字节。
	if buf.Len() < pluginwire.HeaderSize+2 {
		t.Fatalf("帧长度 = %d, 过短", buf.Len())
	}
	if _, err := pluginwire.ReadFrame(&buf); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
}

// TestFrameSplitAcrossSmallReads 验证 io.ReadFull 路径：载荷被切成大量小读仍能解出。
func TestFrameSplitAcrossSmallReads(t *testing.T) {
	payload := strings.Repeat("x", 4096)
	var buf bytes.Buffer
	if err := pluginwire.WriteFrame(&buf, pluginwire.Frame{ID: 7, Result: json.RawMessage(`"` + payload + `"`)}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := pluginwire.ReadFrame(iotest.OneByteReader(&buf))
	if err != nil {
		t.Fatalf("ReadFrame(逐字节读): %v", err)
	}
	var s string
	if err := json.Unmarshal(got.Result, &s); err != nil {
		t.Fatalf("Unmarshal result: %v", err)
	}
	if s != payload {
		t.Errorf("载荷长度 = %d, want %d", len(s), len(payload))
	}
}

func TestReadFrameTruncatedHeader(t *testing.T) {
	_, err := pluginwire.ReadFrame(bytes.NewReader([]byte{0x49, 0x47, 0x50}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("截断帧头应返回 io.ErrUnexpectedEOF，得到 %v", err)
	}
}

func TestReadFrameTruncatedPayload(t *testing.T) {
	// 声明 100 字节载荷但只给 10 字节。
	header := make([]byte, pluginwire.HeaderSize)
	binary.BigEndian.PutUint32(header[0:4], pluginwire.FrameMagic)
	binary.BigEndian.PutUint32(header[4:8], 100)

	_, err := pluginwire.ReadFrame(io.MultiReader(bytes.NewReader(header), bytes.NewReader(make([]byte, 10))))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("截断载荷应返回 io.ErrUnexpectedEOF，得到 %v", err)
	}
}

// TestReadFrameBadMagic 固化失步探测：魔数不匹配必须立刻判定流已损坏，
// 而不是把垃圾当成 JSON 去解。
func TestReadFrameBadMagic(t *testing.T) {
	header := make([]byte, pluginwire.HeaderSize)
	binary.BigEndian.PutUint32(header[0:4], 0xdeadbeef)
	binary.BigEndian.PutUint32(header[4:8], 2)

	_, err := pluginwire.ReadFrame(io.MultiReader(bytes.NewReader(header), bytes.NewReader([]byte("{}"))))
	if !errors.Is(err, pluginwire.ErrFrameDesync) {
		t.Fatalf("错误魔数应返回 ErrFrameDesync，得到 %v", err)
	}
}

// TestReadFrameTooLarge 长度越界必须在分配内存前拒绝（防御性内存上界）。
func TestReadFrameTooLarge(t *testing.T) {
	header := make([]byte, pluginwire.HeaderSize)
	binary.BigEndian.PutUint32(header[0:4], pluginwire.FrameMagic)
	binary.BigEndian.PutUint32(header[4:8], pluginwire.MaxFrameBytes+1)

	_, err := pluginwire.ReadFrame(bytes.NewReader(header))
	if !errors.Is(err, pluginwire.ErrFrameTooLarge) {
		t.Fatalf("超限长度应返回 ErrFrameTooLarge，得到 %v", err)
	}
}

// TestReadFrameInvalidJSON 魔数对但载荷不是 JSON → 同样判失步。
func TestReadFrameInvalidJSON(t *testing.T) {
	var buf bytes.Buffer
	header := make([]byte, pluginwire.HeaderSize)
	binary.BigEndian.PutUint32(header[0:4], pluginwire.FrameMagic)
	binary.BigEndian.PutUint32(header[4:8], 5)
	buf.Write(header)
	buf.WriteString("hello")

	_, err := pluginwire.ReadFrame(&buf)
	if !errors.Is(err, pluginwire.ErrFrameDesync) {
		t.Fatalf("非 JSON 载荷应返回 ErrFrameDesync，得到 %v", err)
	}
}

// TestReadFrameEOF 对端正常关闭 → io.EOF（不是 ErrFrameDesync）。
func TestReadFrameEOF(t *testing.T) {
	if _, err := pluginwire.ReadFrame(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Fatalf("空流应返回 io.EOF，得到 %v", err)
	}
}
