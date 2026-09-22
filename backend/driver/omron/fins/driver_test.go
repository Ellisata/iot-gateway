// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"fmt"
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// mockTransport 测试用传输层，返回预置数据
type mockTransport struct {
	fn func(area finsArea, word, count uint16) ([]byte, error)
}

func (m *mockTransport) Read(area finsArea, word, count uint16) ([]byte, error) {
	return m.fn(area, word, count)
}
func (m *mockTransport) IsConnected() bool { return true }
func (m *mockTransport) Close() error      { return nil }

func TestDriverRegistered(t *testing.T) {
	for _, name := range []string{ProtocolFINSUDP, ProtocolFINSTCP, ProtocolFINSSerial} {
		d, err := driver.Create(name)
		if err != nil {
			t.Fatalf("driver.Create(%s) = %v", name, err)
		}
		if d == nil {
			t.Fatalf("driver.Create(%s) returned nil", name)
		}
	}
	// 旧的单一协议名不再注册
	if _, err := driver.Create("Omron.Net.FINS"); err == nil {
		t.Fatalf("expected error for legacy protocol name Omron.Net.FINS")
	}
}

func TestDriverUnknown(t *testing.T) {
	if _, err := driver.Create("No.Such.Protocol"); err == nil {
		t.Fatalf("expected error for unknown protocol")
	}
}

func TestDriverTransportBinding(t *testing.T) {
	// 协议注册名固定传输层
	cases := []struct {
		protocol string
		want     string
	}{
		{ProtocolFINSUDP, TransportUDP},
		{ProtocolFINSTCP, TransportTCP},
		{ProtocolFINSSerial, TransportSerial},
	}
	for _, c := range cases {
		d, err := driver.Create(c.protocol)
		if err != nil {
			t.Fatalf("driver.Create(%s) = %v", c.protocol, err)
		}
		fd, ok := d.(*finsDriver)
		if !ok {
			t.Fatalf("driver.Create(%s) = %T, want *finsDriver", c.protocol, d)
		}
		if fd.transport != c.want {
			t.Errorf("transport(%s) = %s, want %s", c.protocol, fd.transport, c.want)
		}
	}
}

func TestSerialExclusive(t *testing.T) {
	// 串口协议：独占
	if !newFINSDriver(TransportSerial).SerialExclusive() {
		t.Errorf("serial transport should be exclusive")
	}

	// 以太网协议：非独占
	if newFINSDriver(TransportUDP).SerialExclusive() {
		t.Errorf("udp transport should not be exclusive")
	}
	if newFINSDriver(TransportTCP).SerialExclusive() {
		t.Errorf("tcp transport should not be exclusive")
	}
}

func TestLookupDataType(t *testing.T) {
	cases := map[string]string{
		"Boolean": "bool",
		"Short":   "int16",
		"Word":    "word",
		"Long":    "int32",
		"Float":   "float32",
		"Double":  "float64",
		"String":  "string",
	}
	// 三种协议共享同一套 FINS 类型注册
	for _, protocol := range []string{ProtocolFINSUDP, ProtocolFINSTCP, ProtocolFINSSerial} {
		for common, want := range cases {
			got, ok := driver.LookupDataType(protocol, common)
			if !ok {
				t.Errorf("LookupDataType(%s, %s) not found", protocol, common)
				continue
			}
			if got != want {
				t.Errorf("LookupDataType(%s, %s) = %s, want %s", protocol, common, got, want)
			}
		}
	}
	// 大小写不敏感
	if got, ok := driver.LookupDataType("omron.fins.udp", "short"); !ok || got != "int16" {
		t.Errorf("case-insensitive lookup = %s/%v", got, ok)
	}
}

func TestParseConfigTransportDefault(t *testing.T) {
	// 默认 transport 为 UDP，newTransport 应创建 UDP 客户端
	cfg := DefaultFINSConfig()
	tp, err := newTransport(cfg)
	if err != nil {
		t.Fatalf("newTransport(default) = %v", err)
	}
	if _, ok := tp.(*finsUDPClient); !ok {
		t.Errorf("newTransport default = %T, want *finsUDPClient", tp)
	}
	tp.Close()
}

func TestDriverReadPipeline(t *testing.T) {
	// D100(int16) + D100.05(bool) + D102(int32) → 合并为 [100, count=4]
	cfg := DefaultFINSConfig()
	cfg.MergeWindow = 10
	mt := &mockTransport{fn: func(area finsArea, word, count uint16) ([]byte, error) {
		if area != AreaDM || word != 100 || count != 4 {
			t.Errorf("unexpected read: area=0x%02X word=%d count=%d", area, word, count)
		}
		return []byte{0x12, 0x34, 0x00, 0x00, 0x00, 0x00, 0x00, 0x64}, nil
	}}
	d := &finsDriver{config: cfg, client: mt}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D100.05", "bool"),
		mkAddr("3", "D102", "int32"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	// 结果按原始 addrs 顺序重组
	checks := []struct {
		id      string
		val     string
		quality int
	}{
		{"1", "4660", 192}, // 0x1234
		{"2", "1", 192},    // word 100 bit5 = 1
		{"3", "100", 192},  // 0x64
	}
	for i, c := range checks {
		r := results[i]
		if r.DeviceAddressID != c.id || r.Value != c.val || r.Quality != c.quality {
			t.Errorf("result[%d] = %+v, want id=%s val=%s quality=%d", i, r, c.id, c.val, c.quality)
		}
	}
}

func TestDriverReadEndCodeError(t *testing.T) {
	// 结束码错误：该区间点位 Quality=0，整台设备读取不中断
	cfg := DefaultFINSConfig()
	mt := &mockTransport{fn: func(area finsArea, word, count uint16) ([]byte, error) {
		return nil, newEndCodeError(0x1101)
	}}
	d := &finsDriver{config: cfg, client: mt}

	addrs := []po.DeviceAddress{mkAddr("1", "D100", "int16")}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("end code error should not fail the whole read, got %v", err)
	}
	if len(results) != 1 || results[0].Quality != 0 {
		t.Errorf("expected quality=0 result, got %+v", results)
	}
}

func TestDriverReadNetworkError(t *testing.T) {
	// 网络错误：向上返回，触发采集引擎断线重连
	cfg := DefaultFINSConfig()
	mt := &mockTransport{fn: func(area finsArea, word, count uint16) ([]byte, error) {
		return nil, fmt.Errorf("boom")
	}}
	d := &finsDriver{config: cfg, client: mt}

	addrs := []po.DeviceAddress{mkAddr("1", "D100", "int16")}
	if _, err := d.Read(addrs); err == nil {
		t.Fatalf("expected network error to propagate")
	}
}

// newLiveSerialDriver 造一个「已连上串口」的驱动实例，配置来自同一份 JSON，
// 因此 MatchConnection/Ping 的复用分支应当命中。
func newLiveSerialDriver(t *testing.T, json string) (*finsDriver, *mockTransport) {
	t.Helper()
	cfg, err := ParseFINSConfig(json)
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	// 与 Connect 一致：传输层由协议注册名固定，覆盖 JSON 里的 transport
	cfg.Transport = TransportSerial
	mt := &mockTransport{fn: func(finsArea, uint16, uint16) ([]byte, error) {
		return []byte{0x00, 0x00}, nil
	}}
	d := newFINSDriver(TransportSerial)
	d.mu.Lock()
	d.config = cfg
	d.client = mt
	d.mu.Unlock()
	return d, mt
}

// MatchConnection 只在 Serial 上成立：UDP/TCP/HostLinkTCP 都不独占端口，
// 复用一条可能早已失效的长连接只会误报失败，而新开一条连接的代价为零。
func TestMatchConnectionSerialOnly(t *testing.T) {
	const cfgJSON = `{"comPort":"COM1"}`

	for _, transport := range []string{TransportUDP, TransportTCP, TransportHostLinkTCP} {
		d := newFINSDriver(transport)
		cfg, _ := ParseFINSConfig(cfgJSON)
		cfg.Transport = transport
		d.mu.Lock()
		d.config = cfg
		d.client = &mockTransport{}
		d.mu.Unlock()
		if d.MatchConnection(cfgJSON) {
			t.Errorf("%s 传输不应参与串口连接复用", transport)
		}
	}

	d, _ := newLiveSerialDriver(t, cfgJSON)
	if !d.MatchConnection(cfgJSON) {
		t.Error("Serial 传输参数一致时应判定可复用")
	}
	// 表单里残留的 transport 字段不参与比较——传输层由协议注册名固定
	if !d.MatchConnection(`{"comPort":"COM1","transport":"tcp"}`) {
		t.Error("JSON 里的 transport 字段应被注册名覆盖，不该影响匹配")
	}
}

func TestMatchConnectionRejectsMismatch(t *testing.T) {
	d, _ := newLiveSerialDriver(t, `{"comPort":"COM1"}`)

	cases := map[string]string{
		"串口不同":     `{"comPort":"COM2"}`,
		"单元号不同":    `{"comPort":"COM1","unitNo":7}`,
		"FCS 模式不同": `{"comPort":"COM1","fcsMode":"FULL"}`,
		"JSON 非法":  "not json",
	}
	for name, in := range cases {
		if d.MatchConnection(in) {
			t.Errorf("%s：不应判定为可复用", name)
		}
	}

	if newFINSDriver(TransportSerial).MatchConnection(`{"comPort":"COM1"}`) {
		t.Error("未连接过的实例不应判定为可复用")
	}
	if got := newFINSDriver(TransportTCP).SerialResource(); got != "" {
		t.Errorf("TCP 传输不持有串口，SerialResource = %q, want 空", got)
	}
}

// H3 回归：串口链路参数（波特率/数据位/停止位/校验位）必须参与比较。
//
// finsSerialClient 的这些参数在打开串口时就固定死了，复用一条 9600/N 的连接
// 去测 19200/E 的配置，会给出一个与被测配置无关的成功结论。
func TestSameConfigSerialLinkParams(t *testing.T) {
	base, err := ParseFINSConfig(`{"comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"N"}`)
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	probe := func(mutate func(*FINSConfig)) *FINSConfig {
		c, _ := ParseFINSConfig(`{"comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"N"}`)
		mutate(c)
		return c
	}

	if !sameConfig(base, probe(func(*FINSConfig) {})) {
		t.Error("完全一致的配置应判定为一致")
	}
	cases := map[string]func(*FINSConfig){
		"波特率不同": func(c *FINSConfig) { c.BaudRate = 19200 },
		"数据位不同": func(c *FINSConfig) { c.DataBits = 7 },
		"停止位不同": func(c *FINSConfig) { c.StopBits = 2 },
		"校验位不同": func(c *FINSConfig) { c.Parity = "E" },
	}
	for name, mutate := range cases {
		if sameConfig(base, probe(mutate)) {
			t.Errorf("%s：串口链路参数不同不应判定为一致", name)
		}
	}
	if sameConfig(nil, base) || sameConfig(base, nil) {
		t.Error("nil 配置不应判定为一致")
	}
}

// Ping 复用已有连接：把 ComPort 设成必然打不开的假串口，
// 一旦走了「开临时连接」那条路就必定失败。
func TestPingReusesLiveSerialConnection(t *testing.T) {
	const cfgJSON = `{"comPort":"COM_NOT_EXIST_1"}`
	d, mt := newLiveSerialDriver(t, cfgJSON)

	called := 0
	mt.fn = func(finsArea, uint16, uint16) ([]byte, error) {
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
