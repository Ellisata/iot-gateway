package cip

import (
	"fmt"
	"testing"

	"iot-gateway/driver"
	cipcore "iot-gateway/driver/cip"
	"iot-gateway/model/po"
)

// mockCIPTransport 注入用 mock 传输层（cipTransport 接口实现）。
type mockCIPTransport struct {
	connected   bool
	readFn      func(tag string, code uint16) ([]byte, error)
	readTagsFn  func(specs []TagSpec) ([]TagResult, error)
	closed      bool
	batchCalls  int // 批量读调用次数（统计批量是否生效）
	singleCalls int // 单读调用次数
}

func (m *mockCIPTransport) ReadTag(tag string, code uint16) ([]byte, error) {
	m.singleCalls++
	if m.readFn == nil {
		return nil, fmt.Errorf("mock: no readFn configured")
	}
	return m.readFn(tag, code)
}

func (m *mockCIPTransport) ReadTags(specs []TagSpec) ([]TagResult, error) {
	m.batchCalls++
	if m.readTagsFn == nil {
		return nil, fmt.Errorf("mock: no readTagsFn configured")
	}
	return m.readTagsFn(specs)
}

func (m *mockCIPTransport) IsConnected() bool { return m.connected }

func (m *mockCIPTransport) Close() error { m.closed = true; return nil }

// mkCIPAddr 构造测试点位。
func mkCIPAddr(id, name, dataType, commonType string) po.DeviceAddress {
	return po.DeviceAddress{ID: id, Name: name, DataType: dataType, CommonDataType: commonType}
}

// assertResult 断言单个 ReadResult。
func assertResult(t *testing.T, r driver.ReadResult, id, value, dt string, quality int) {
	t.Helper()
	if r.DeviceAddressID != id {
		t.Errorf("DeviceAddressID = %q, want %q", r.DeviceAddressID, id)
	}
	if r.Value != value {
		t.Errorf("%s Value = %q, want %q", id, r.Value, value)
	}
	if r.DataType != dt {
		t.Errorf("%s DataType = %q, want %q", id, r.DataType, dt)
	}
	if r.Quality != quality {
		t.Errorf("%s Quality = %d, want %d", id, r.Quality, quality)
	}
}

func TestDriverRegistered(t *testing.T) {
	d, err := driver.Create(ProtocolCIP)
	if err != nil {
		t.Fatalf("driver.Create(%s) = %v", ProtocolCIP, err)
	}
	if d == nil {
		t.Fatal("driver.Create returned nil")
	}
	if _, ok := d.(*cipDriver); !ok {
		t.Fatalf("driver.Create(%s) = %T, want *cipDriver", ProtocolCIP, d)
	}
	if _, err := driver.Create("No.Such.Protocol"); err == nil {
		t.Fatal("expected error for unknown protocol")
	}
}

func TestLookupDataType(t *testing.T) {
	cases := []struct{ common, want string }{
		{"Boolean", "bool"},
		{"String", "string"},
		{"Byte", "uint8"},
		{"Char", "int8"},
		{"Short", "int16"},
		{"Word", "word"},
		{"DWord", "uint32"},
		{"Long", "int32"},
		{"Float", "float32"},
		{"Double", "float64"},
		{"BCD", "bcd"},
		{"LBCD", "lbcd"},
	}
	for _, c := range cases {
		got, ok := driver.LookupDataType(ProtocolCIP, c.common)
		if !ok {
			t.Errorf("LookupDataType(%s, %s) not found", ProtocolCIP, c.common)
			continue
		}
		if got != c.want {
			t.Errorf("LookupDataType(%s, %s) = %s, want %s", ProtocolCIP, c.common, got, c.want)
		}
	}
	// Date 不支持（CIP 数据表读取无 DATE 类型码）
	if _, ok := driver.LookupDataType(ProtocolCIP, "Date"); ok {
		t.Error("Date should not be supported for CIP")
	}
	// 大小写不敏感
	if got, ok := driver.LookupDataType("omron.cip", "short"); !ok || got != "int16" {
		t.Errorf("case-insensitive lookup = %q, %v, want int16, true", got, ok)
	}
}

func TestDriverReadPipeline(t *testing.T) {
	d := newCIPDriver()
	d.config = DefaultCIPConfig()
	d.config.MaxTagsPerRequest = 0 // 本测试走单读路径（mock 仅配 readFn）
	calls := map[string]int{}
	d.client = &mockCIPTransport{connected: true, readFn: func(tag string, code uint16) ([]byte, error) {
		calls[tag]++
		switch tag {
		case "MotorSpeed":
			if code != cipTypeINT {
				t.Errorf("MotorSpeed code = 0x%04X, want INT 0x%04X", code, cipTypeINT)
			}
			return []byte{0x64, 0x00}, nil // int16=100, word=100
		case "Other":
			if code != cipTypeDINT {
				t.Errorf("Other code = 0x%04X, want DINT 0x%04X", code, cipTypeDINT)
			}
			return []byte{0x64, 0x00, 0x00, 0x00}, nil // int32=100
		default:
			return nil, fmt.Errorf("unexpected tag %q", tag)
		}
	}}

	addrs := []po.DeviceAddress{
		mkCIPAddr("a1", "MotorSpeed", "int16", "Short"),
		mkCIPAddr("a2", "MotorSpeed", "word", "Word"),
		mkCIPAddr("a3", "Other", "int32", "Long"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}
	// 同标签去重：MotorSpeed 只请求一次
	if calls["MotorSpeed"] != 1 {
		t.Errorf("MotorSpeed read %d times, want 1", calls["MotorSpeed"])
	}
	if calls["Other"] != 1 {
		t.Errorf("Other read %d times, want 1", calls["Other"])
	}
	// 结果按原始 addrs 顺序重组，各自按类型解码
	assertResult(t, results[0], "a1", "100", "int16", 192)
	assertResult(t, results[1], "a2", "100", "word", 192)
	assertResult(t, results[2], "a3", "100", "int32", 192)
}

func TestDriverReadGeneralStatusError(t *testing.T) {
	d := newCIPDriver()
	d.config = DefaultCIPConfig()
	d.config.MaxTagsPerRequest = 0 // 单读路径（mock 仅配 readFn）
	d.client = &mockCIPTransport{connected: true, readFn: func(tag string, code uint16) ([]byte, error) {
		return nil, cipcore.NewGeneralStatusError(0x04)
	}}
	addrs := []po.DeviceAddress{
		mkCIPAddr("a1", "MotorSpeed", "int16", "Short"),
		mkCIPAddr("a2", "MotorSpeed", "int16", "Short"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read should not return error on general status error, got: %v", err)
	}
	assertResult(t, results[0], "a1", "", "int16", 0)
	assertResult(t, results[1], "a2", "", "int16", 0)
}

func TestDriverReadNetworkError(t *testing.T) {
	d := newCIPDriver()
	d.config = DefaultCIPConfig()
	d.client = &mockCIPTransport{connected: true, readFn: func(tag string, code uint16) ([]byte, error) {
		return nil, fmt.Errorf("network down")
	}}
	addrs := []po.DeviceAddress{mkCIPAddr("a1", "MotorSpeed", "int16", "Short")}
	if _, err := d.Read(addrs); err == nil {
		t.Fatal("expected error on network failure")
	}
}

func TestDriverReadTagDedup(t *testing.T) {
	d := newCIPDriver()
	d.config = DefaultCIPConfig()
	d.config.MaxTagsPerRequest = 0 // 单读路径（验证标签去重，不涉及批量）
	calls := 0
	d.client = &mockCIPTransport{connected: true, readFn: func(tag string, code uint16) ([]byte, error) {
		calls++
		return []byte{0x64, 0x00}, nil
	}}
	addrs := []po.DeviceAddress{
		mkCIPAddr("a1", "T1", "int16", "Short"),
		mkCIPAddr("a2", "T2", "int16", "Short"),
		mkCIPAddr("a3", "T1", "int16", "Short"),
	}
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if calls != 2 {
		t.Errorf("ReadTag called %d times, want 2", calls)
	}
}

func TestDriverReadBatchMaxTags(t *testing.T) {
	// maxTagsPerRequest=3：5 个定长标签应分 2 批（3+2）走 ReadTags，不走单读
	d := newCIPDriver()
	cfg := DefaultCIPConfig()
	cfg.MaxTagsPerRequest = 3
	d.config = cfg
	mock := &mockCIPTransport{connected: true, readTagsFn: func(specs []TagSpec) ([]TagResult, error) {
		res := make([]TagResult, len(specs))
		for i := range specs {
			res[i] = TagResult{Data: []byte{0x64, 0x00}}
		}
		return res, nil
	}}
	d.client = mock
	addrs := []po.DeviceAddress{
		mkCIPAddr("a1", "T1", "int16", "Short"),
		mkCIPAddr("a2", "T2", "int16", "Short"),
		mkCIPAddr("a3", "T3", "int16", "Short"),
		mkCIPAddr("a4", "T4", "int16", "Short"),
		mkCIPAddr("a5", "T5", "int16", "Short"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if mock.batchCalls != 2 {
		t.Errorf("ReadTags called %d times, want 2 (3+2)", mock.batchCalls)
	}
	if mock.singleCalls != 0 {
		t.Errorf("ReadTag called %d times, want 0 (all batched)", mock.singleCalls)
	}
	for i := range results {
		assertResult(t, results[i], fmt.Sprintf("a%d", i+1), "100", "int16", 192)
	}
}

func TestDriverReadBatchSkipsStringTag(t *testing.T) {
	// string 标签不参与批量：定长标签走 ReadTags，string 标签单读
	d := newCIPDriver()
	cfg := DefaultCIPConfig()
	cfg.MaxTagsPerRequest = 3
	d.config = cfg
	mock := &mockCIPTransport{
		connected: true,
		readTagsFn: func(specs []TagSpec) ([]TagResult, error) {
			res := make([]TagResult, len(specs))
			for i := range specs {
				res[i] = TagResult{Data: []byte{0x64, 0x00}}
			}
			return res, nil
		},
		readFn: func(tag string, code uint16) ([]byte, error) {
			return []byte{0x41, 0x42}, nil // string 数据
		},
	}
	d.client = mock
	addrs := []po.DeviceAddress{
		mkCIPAddr("a1", "T1", "int16", "Short"),
		mkCIPAddr("a2", "T2", "int16", "Short"),
		mkCIPAddr("a3", "S1", "string", "String"),
	}
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if mock.batchCalls != 1 {
		t.Errorf("ReadTags called %d times, want 1 (fixed tags batched, string single)", mock.batchCalls)
	}
	if mock.singleCalls != 1 {
		t.Errorf("ReadTag called %d times, want 1 (string tag single read)", mock.singleCalls)
	}
}

func TestDriverReadBatchGeneralStatusError(t *testing.T) {
	// 批量中某个标签被拒绝 → 该标签 Quality=0，其余正常，Read 不报错
	d := newCIPDriver()
	cfg := DefaultCIPConfig()
	cfg.MaxTagsPerRequest = 3
	d.config = cfg
	mock := &mockCIPTransport{connected: true, readTagsFn: func(specs []TagSpec) ([]TagResult, error) {
		res := make([]TagResult, len(specs))
		for i := range specs {
			if specs[i].Name == "T2" {
				res[i] = TagResult{Err: cipcore.NewGeneralStatusError(0x04)}
				continue
			}
			res[i] = TagResult{Data: []byte{0x64, 0x00}}
		}
		return res, nil
	}}
	d.client = mock
	addrs := []po.DeviceAddress{
		mkCIPAddr("a1", "T1", "int16", "Short"),
		mkCIPAddr("a2", "T2", "int16", "Short"),
		mkCIPAddr("a3", "T3", "int16", "Short"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read should not error on per-tag general status, got: %v", err)
	}
	assertResult(t, results[0], "a1", "100", "int16", 192)
	assertResult(t, results[1], "a2", "", "int16", 0)
	assertResult(t, results[2], "a3", "100", "int16", 192)
}

func TestDriverReadNotConnected(t *testing.T) {
	d := newCIPDriver()
	addrs := []po.DeviceAddress{mkCIPAddr("a1", "T1", "int16", "Short")}
	if _, err := d.Read(addrs); err == nil {
		t.Fatal("expected error when not connected")
	}
}

func TestDriverPingReuseEmptyTag(t *testing.T) {
	// 复用已有连接，pingTag 为空 → 仅需建连成功即可
	d := newCIPDriver()
	cfg, err := ParseCIPConfig(`{"host":"1.2.3.4","port":44818}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	d.config = cfg
	d.client = &mockCIPTransport{connected: true}
	if err := d.Ping(`{"host":"1.2.3.4","port":44818}`); err != nil {
		t.Fatalf("Ping (reused, empty pingTag) error: %v", err)
	}
}

func TestDriverPingReuseTagDenied(t *testing.T) {
	// pingTag 设备可达但拒绝（类型不符）→ 仍视为连通
	d := newCIPDriver()
	cfg, err := ParseCIPConfig(`{"host":"1.2.3.4","port":44818,"pingTag":"P"}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	d.config = cfg
	d.client = &mockCIPTransport{connected: true, readFn: func(tag string, code uint16) ([]byte, error) {
		if code != cipTypeWORD {
			t.Errorf("ping code = 0x%04X, want WORD 0x%04X", code, cipTypeWORD)
		}
		return nil, cipcore.NewGeneralStatusError(0x04)
	}}
	if err := d.Ping(`{"host":"1.2.3.4","port":44818,"pingTag":"P"}`); err != nil {
		t.Fatalf("Ping (tag denied) error: %v", err)
	}
}
