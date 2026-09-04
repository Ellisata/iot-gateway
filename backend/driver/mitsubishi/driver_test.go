// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"fmt"
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// mockTransport 测试用传输层，返回预置数据。
type mockTransport struct {
	fn func(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error)
}

func (m *mockTransport) Read(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
	return m.fn(device, head, points, bitMode)
}
func (m *mockTransport) IsConnected() bool { return true }
func (m *mockTransport) Close() error      { return nil }

// TestDriverRegistered 两种协议均注册成功。
func TestDriverRegistered(t *testing.T) {
	for _, name := range []string{ProtocolMCTCP, ProtocolMCSerial} {
		d, err := driver.Create(name)
		if err != nil {
			t.Fatalf("driver.Create(%s) = %v", name, err)
		}
		if d == nil {
			t.Fatalf("driver.Create(%s) returned nil", name)
		}
	}
	if _, err := driver.Create("Mitsubishi.MC"); err == nil {
		t.Fatalf("expected error for unregistered name Mitsubishi.MC")
	}
}

// TestDriverTransportBinding 协议注册名固定传输层。
func TestDriverTransportBinding(t *testing.T) {
	cases := []struct {
		protocol string
		want     string
	}{
		{ProtocolMCTCP, TransportTCP},
		{ProtocolMCSerial, TransportSerial},
	}
	for _, c := range cases {
		d, err := driver.Create(c.protocol)
		if err != nil {
			t.Fatalf("driver.Create(%s) = %v", c.protocol, err)
		}
		md, ok := d.(*mcDriver)
		if !ok {
			t.Fatalf("driver.Create(%s) = %T, want *mcDriver", c.protocol, d)
		}
		if md.transport != c.want {
			t.Errorf("transport(%s) = %s, want %s", c.protocol, md.transport, c.want)
		}
	}
}

// TestSerialExclusive 串口独占，TCP 非独占。
func TestSerialExclusive(t *testing.T) {
	if !newMCDriver(TransportSerial).SerialExclusive() {
		t.Errorf("serial transport should be exclusive")
	}
	if newMCDriver(TransportTCP).SerialExclusive() {
		t.Errorf("tcp transport should not be exclusive")
	}
}

// TestDriverReadPipeline 读取流水线：合并区间、按原序重组、解码。
func TestDriverReadPipeline(t *testing.T) {
	// D100(int16) + D100.5(bool) + D102(int32) → 合并为 [100, points 4]
	cfg := DefaultMCConfig()
	mt := &mockTransport{fn: func(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
		if device.name != "D" || head != 100 || points != 4 || bitMode {
			t.Errorf("unexpected read: device=%s head=%d points=%d bitMode=%v", device.name, head, points, bitMode)
		}
		return []byte{0x34, 0x12, 0x00, 0x00, 0x64, 0x00, 0x00, 0x00}, nil
	}}
	d := &mcDriver{config: cfg, client: mt}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D100.5", "bool"),
		mkAddr("3", "D102", "int32"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	checks := []struct {
		id      string
		val     string
		quality int
	}{
		{"1", "4660", 192}, // 0x1234
		{"2", "1", 192},    // word 100 bit5 = 1
		{"3", "100", 192},  // int32 LE
	}
	for i, c := range checks {
		r := results[i]
		if r.DeviceAddressID != c.id || r.Value != c.val || r.Quality != c.quality {
			t.Errorf("result[%d] = %+v, want id=%s val=%s quality=%d", i, r, c.id, c.val, c.quality)
		}
	}
}

// TestDriverReadBitMode bool 位设备走位单位读取。
func TestDriverReadBitMode(t *testing.T) {
	cfg := DefaultMCConfig()
	mt := &mockTransport{fn: func(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
		if !bitMode || device.name != "M" || head != 10 || points != 3 {
			t.Errorf("unexpected read: %s %d %d bit=%v", device.name, head, points, bitMode)
		}
		return []byte{0x01, 0x00, 0x01}, nil
	}}
	d := &mcDriver{config: cfg, client: mt}

	addrs := []po.DeviceAddress{
		mkAddr("1", "M10", "bool"),
		mkAddr("2", "M11", "bool"),
		mkAddr("3", "M12", "bool"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}
	want := []string{"1", "0", "1"}
	for i, w := range want {
		if results[i].Value != w || results[i].Quality != 192 {
			t.Errorf("result[%d] = %+v, want value=%s", i, results[i], w)
		}
	}
}

// TestDriverReadEndCodeError 结束码错误：区间点位 Quality=0，整台设备读取不中断。
func TestDriverReadEndCodeError(t *testing.T) {
	cfg := DefaultMCConfig()
	mt := &mockTransport{fn: func(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
		return nil, newEndCodeError(endCodeAddrRange)
	}}
	d := &mcDriver{config: cfg, client: mt}

	addrs := []po.DeviceAddress{mkAddr("1", "D100", "int16")}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("end code error should not fail the whole read, got %v", err)
	}
	if len(results) != 1 || results[0].Quality != 0 || results[0].Value != "" {
		t.Errorf("expected quality=0 empty result, got %+v", results)
	}
}

// TestDriverReadNetworkError 网络错误：向上返回，触发采集引擎断线重连。
func TestDriverReadNetworkError(t *testing.T) {
	cfg := DefaultMCConfig()
	mt := &mockTransport{fn: func(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
		return nil, fmt.Errorf("boom")
	}}
	d := &mcDriver{config: cfg, client: mt}

	addrs := []po.DeviceAddress{mkAddr("1", "D100", "int16")}
	if _, err := d.Read(addrs); err == nil {
		t.Fatalf("expected network error to propagate")
	}
}

// TestDriverNotConnected 未连接时报错。
func TestDriverNotConnected(t *testing.T) {
	d := &mcDriver{}
	if _, err := d.Read([]po.DeviceAddress{mkAddr("1", "D100", "int16")}); err == nil {
		t.Fatalf("expected not connected error")
	}
	if d.IsConnected() {
		t.Errorf("fresh driver should not be connected")
	}
}

// TestDriverReadRangeCached 同批点位第二次 Read 命中区间指纹缓存，不再重复计算。
// 缓存只跳过区间计算，不跳过传输层 I/O（每轮仍按区间读取）。
func TestDriverReadRangeCached(t *testing.T) {
	cfg := DefaultMCConfig()
	readCalls := 0
	mt := &mockTransport{fn: func(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
		readCalls++
		return []byte{0x34, 0x12}, nil
	}}
	d := &mcDriver{config: cfg, client: mt}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D102", "int16"),
	}
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("first Read = %v", err)
	}
	if n := len(d.rangeCache); n != 1 {
		t.Fatalf("range cache entries after first read = %d, want 1", n)
	}

	// 同批内容：应命中缓存，缓存条数不增
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("second Read = %v", err)
	}
	if n := len(d.rangeCache); n != 1 {
		t.Errorf("range cache entries after same-batch second read = %d, want 1 (cache miss)", n)
	}

	// 不同批次：新增条目
	other := []po.DeviceAddress{mkAddr("3", "M10", "bool")}
	if _, err := d.Read(other); err != nil {
		t.Fatalf("other Read = %v", err)
	}
	if n := len(d.rangeCache); n != 2 {
		t.Errorf("range cache entries after different batch = %d, want 2", n)
	}

	// 传输层读取仍按轮执行（缓存只跳过区间计算，不跳过 I/O）
	if readCalls != 3 {
		t.Errorf("transport read calls = %d, want 3", readCalls)
	}
}

// TestDriverReadRangeCacheClearedOnConnect 重连（Connect）后区间缓存清空。
func TestDriverReadRangeCacheClearedOnConnect(t *testing.T) {
	d := &mcDriver{config: DefaultMCConfig(), client: &mockTransport{
		fn: func(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
			return []byte{0x34, 0x12}, nil
		},
	}}
	addrs := []po.DeviceAddress{mkAddr("1", "D100", "int16")}
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("Read = %v", err)
	}
	if n := len(d.rangeCache); n != 1 {
		t.Fatalf("range cache entries = %d, want 1", n)
	}

	// 用临时 TCP 连接路径模拟重连；连不上也没关系，关键验证缓存已被清空。
	// 短超时 + 本地无监听端口，拨号立即拒绝，不会拖慢测试。
	_ = d.Connect(`{"transport":"TCP","host":"127.0.0.1","port":1,"timeoutMs":"10"}`)
	d.rangeCacheMu.Lock()
	n := len(d.rangeCache)
	d.rangeCacheMu.Unlock()
	if n != 0 {
		t.Errorf("range cache entries after Connect = %d, want 0", n)
	}
}
