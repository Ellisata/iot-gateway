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

// newLiveSerialDriver 造一个「已连上串口」的驱动实例，配置来自同一份 JSON，
// 因此 MatchConnection/Ping 的复用分支应当命中。
func newLiveSerialDriver(t *testing.T, json string) (*mcDriver, *mockTransport) {
	t.Helper()
	cfg, err := ParseMCConfig(json)
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	// 与 Connect 一致：传输层由协议注册名固定，覆盖 JSON 里的 transport
	cfg.Transport = TransportSerial
	mt := &mockTransport{fn: func(mcDevice, uint32, uint16, bool) ([]byte, error) {
		return []byte{0x00, 0x00}, nil
	}}
	d := newMCDriver(TransportSerial)
	d.mu.Lock()
	d.config = cfg
	d.client = mt
	d.mu.Unlock()
	return d, mt
}

// MatchConnection 只在串口上成立：TCP 复用会把「对端早已失效但 connected 仍为真」的
// 长连接的失败，算到一次本可成功的新建连接头上，而新开一条 TCP 连接的代价为零。
func TestMatchConnectionSerialOnly(t *testing.T) {
	const cfgJSON = `{"comPort":"COM1"}`

	tcp := newMCDriver(TransportTCP)
	tcp.mu.Lock()
	cfg, _ := ParseMCConfig(cfgJSON)
	cfg.Transport = TransportTCP
	tcp.config = cfg
	tcp.client = &mockTransport{}
	tcp.mu.Unlock()
	if tcp.MatchConnection(cfgJSON) {
		t.Error("TCP 驱动不应参与串口连接复用")
	}

	d, _ := newLiveSerialDriver(t, cfgJSON)
	if !d.MatchConnection(cfgJSON) {
		t.Error("串口驱动参数一致时应判定可复用")
	}
}

// 表单里残留的 transport 字段不参与比较——传输层由协议注册名固定，
// 否则一台串口设备只要 JSON 里带着 transport=tcp 就永远匹配不上。
func TestMatchConnectionIgnoresStaleTransportField(t *testing.T) {
	d, _ := newLiveSerialDriver(t, `{"comPort":"COM1"}`)
	if !d.MatchConnection(`{"comPort":"COM1","transport":"tcp"}`) {
		t.Error("JSON 里的 transport 字段应被注册名覆盖，不该影响匹配")
	}
}

func TestMatchConnectionRejectsMismatch(t *testing.T) {
	d, _ := newLiveSerialDriver(t, `{"comPort":"COM1"}`)

	cases := map[string]string{
		"串口不同":    `{"comPort":"COM2"}`,
		"波特率不同":   `{"comPort":"COM1","baudRate":19200}`,
		"JSON 非法": "not json",
	}
	for name, in := range cases {
		if d.MatchConnection(in) {
			t.Errorf("%s：不应判定为可复用", name)
		}
	}

	if newMCDriver(TransportSerial).MatchConnection(`{"comPort":"COM1"}`) {
		t.Error("未连接过的实例不应判定为可复用")
	}
	if got := newMCDriver(TransportTCP).SerialResource(); got != "" {
		t.Errorf("TCP 驱动不持有串口，SerialResource = %q, want 空", got)
	}
}

// Ping 复用已有连接：把 ComPort 设成必然打不开的假串口，
// 一旦走了「开临时连接」那条路就必定失败。
func TestPingReusesLiveSerialConnection(t *testing.T) {
	const cfgJSON = `{"comPort":"COM_NOT_EXIST_1"}`
	d, mt := newLiveSerialDriver(t, cfgJSON)

	called := 0
	mt.fn = func(mcDevice, uint32, uint16, bool) ([]byte, error) {
		called++
		return []byte{0x00, 0x00}, nil
	}

	if err := d.Ping(cfgJSON); err != nil {
		t.Fatalf("Ping 应复用已有连接并成功，实际报错: %v", err)
	}
	if called == 0 {
		t.Error("复用路径没有向已有连接发出任何请求")
	}
}
