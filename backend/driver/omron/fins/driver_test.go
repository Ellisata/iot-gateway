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
	if got, ok := driver.LookupDataType("omron.net.fins.udp", "short"); !ok || got != "int16" {
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
