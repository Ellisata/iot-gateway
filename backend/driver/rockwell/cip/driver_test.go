package cip

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"testing"

	"github.com/iceisfun/goindustrial/protocol/ethernetip/cip"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// mockTransport 注入用 mock 传输层（rockwellTransport 接口实现）。
type mockTransport struct {
	connected bool
	readFn    func(tag string) ([]byte, error)
	readElems func(tag string, count uint16) ([]byte, error)
	closed    bool
	calls     map[string]int
}

func (m *mockTransport) ReadTag(tag string) ([]byte, error) {
	if m.calls == nil {
		m.calls = map[string]int{}
	}
	m.calls[tag]++
	if m.readFn == nil {
		return nil, fmt.Errorf("mock: no readFn configured")
	}
	return m.readFn(tag)
}

func (m *mockTransport) ReadTagElements(tag string, count uint16) ([]byte, error) {
	if m.calls == nil {
		m.calls = map[string]int{}
	}
	m.calls[tag+"#"+strconv.Itoa(int(count))]++
	if m.readElems == nil {
		return nil, fmt.Errorf("mock: no readElems configured")
	}
	return m.readElems(tag, count)
}

func (m *mockTransport) IsConnected() bool { return m.connected }

func (m *mockTransport) Close() error { m.closed = true; return nil }

// mkAddr 构造测试点位。
func mkAddr(id, name, dataType, commonType string) po.DeviceAddress {
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

// mkResp 构造带类型码前缀的 0x4C 响应数据区（模拟 goindustrial ReadTag 返回）。
func mkResp(code uint16, payload ...byte) []byte {
	data := make([]byte, 2, 2+len(payload))
	data[0] = byte(code)
	data[1] = byte(code >> 8)
	return append(data, payload...)
}

func TestDriverRegistered(t *testing.T) {
	d, err := driver.Create(ProtocolRockwellCIP)
	if err != nil {
		t.Fatalf("driver.Create(%s) = %v", ProtocolRockwellCIP, err)
	}
	if d == nil {
		t.Fatal("driver.Create returned nil")
	}
	if _, ok := d.(*rockwellDriver); !ok {
		t.Fatalf("driver.Create(%s) = %T, want *rockwellDriver", ProtocolRockwellCIP, d)
	}
	if _, err := driver.Create("No.Such.Protocol"); err == nil {
		t.Fatal("expected error for unknown protocol")
	}
}

func TestLookupDataType(t *testing.T) {
	cases := []struct{ common, want string }{
		{"Boolean", "bool"},
		{"String", "string"},
		{"Char", "int8"},
		{"Short", "int16"},
		{"Long", "int32"},
		{"Float", "float32"},
		{"Double", "float64"},
	}
	for _, c := range cases {
		got, ok := driver.LookupDataType(ProtocolRockwellCIP, c.common)
		if !ok {
			t.Errorf("LookupDataType(%s, %s) not found", ProtocolRockwellCIP, c.common)
			continue
		}
		if got != c.want {
			t.Errorf("LookupDataType(%s, %s) = %s, want %s", ProtocolRockwellCIP, c.common, got, c.want)
		}
	}
	// Logix 数值原生有符号，无 CIP 标准无符号类型码；Date/BCD/LBCD 无原生类型
	for _, unsupported := range []string{"Date", "Byte", "Word", "DWord", "BCD", "LBCD"} {
		if _, ok := driver.LookupDataType(ProtocolRockwellCIP, unsupported); ok {
			t.Errorf("%s should not be supported for Rockwell", unsupported)
		}
	}
	// 大小写不敏感
	if got, ok := driver.LookupDataType("rockwell.cip", "short"); !ok || got != "int16" {
		t.Errorf("case-insensitive lookup = %q, %v, want int16, true", got, ok)
	}
}

func TestDriverReadPipeline(t *testing.T) {
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	d.client = &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		switch tag {
		case "MotorSpeed": // DINT
			return mkResp(cipTypeDINT, 0x64, 0x00, 0x00, 0x00), nil
		case "Temperature": // REAL = 3.14
			return mkResp(cipTypeREAL, 0xC3, 0xF5, 0x48, 0x40), nil
		default:
			return nil, fmt.Errorf("unexpected tag %q", tag)
		}
	}}

	addrs := []po.DeviceAddress{
		mkAddr("a1", "MotorSpeed", "int16", "Short"),
		mkAddr("a2", "MotorSpeed", "int32", "Long"), // 同标签异类型解码
		mkAddr("a3", "Temperature", "float32", "Float"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}
	// 结果按原始 addrs 顺序重组，各自按类型解码
	assertResult(t, results[0], "a1", "100", "int16", 192)
	assertResult(t, results[1], "a2", "100", "int32", 192)
	assertResult(t, results[2], "a3", "3.14", "float32", 192)
}

func TestDriverReadTagDedup(t *testing.T) {
	// 同一标签多个点位只发一次请求
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	mock := &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		return mkResp(cipTypeINT, 0x64, 0x00), nil
	}}
	d.client = mock
	addrs := []po.DeviceAddress{
		mkAddr("a1", "T1", "int16", "Short"),
		mkAddr("a2", "T2", "int16", "Short"),
		mkAddr("a3", "T1", "int16", "Short"),
	}
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if len(mock.calls) != 2 {
		t.Errorf("distinct tags read = %d, want 2", len(mock.calls))
	}
	for tag, n := range mock.calls {
		if n != 1 {
			t.Errorf("tag %q read %d times, want 1", tag, n)
		}
	}
}

func TestDriverReadCIPStatusError(t *testing.T) {
	// 单标签被设备拒绝（如标签不存在）→ 该标签 Quality=0，不影响其它标签
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	d.client = &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		if tag == "Missing" {
			return nil, cip.Error{Status: cip.StatusPathDestinationUnknown}
		}
		return mkResp(cipTypeINT, 0x64, 0x00), nil
	}}
	addrs := []po.DeviceAddress{
		mkAddr("a1", "T1", "int16", "Short"),
		mkAddr("a2", "Missing", "int16", "Short"),
		mkAddr("a3", "T2", "int16", "Short"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read should not return error on CIP status error, got: %v", err)
	}
	assertResult(t, results[0], "a1", "100", "int16", 192)
	assertResult(t, results[1], "a2", "", "int16", 0)
	assertResult(t, results[2], "a3", "100", "int16", 192)
}

func TestDriverReadNetworkError(t *testing.T) {
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	d.client = &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		return nil, fmt.Errorf("network down")
	}}
	addrs := []po.DeviceAddress{mkAddr("a1", "T1", "int16", "Short")}
	if _, err := d.Read(addrs); err == nil {
		t.Fatal("expected error on network failure")
	}
}

func TestDriverReadShortResponse(t *testing.T) {
	// 响应数据区残缺（类型码头/数据不足）→ Quality=0，不中断整机
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	d.client = &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		if tag == "Broken" {
			return []byte{0xC3, 0x00}, nil // 类型码后无数据
		}
		return mkResp(cipTypeINT, 0x64, 0x00), nil
	}}
	addrs := []po.DeviceAddress{
		mkAddr("a1", "T1", "int16", "Short"),
		mkAddr("a2", "Broken", "int32", "Long"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	assertResult(t, results[0], "a1", "100", "int16", 192)
	assertResult(t, results[1], "a2", "", "int32", 0)
}

func TestDriverReadNotConnected(t *testing.T) {
	d := newRockwellDriver()
	addrs := []po.DeviceAddress{mkAddr("a1", "T1", "int16", "Short")}
	if _, err := d.Read(addrs); err == nil {
		t.Fatal("expected error when not connected")
	}
}

func TestDriverPingReuseEmptyTag(t *testing.T) {
	// 复用已有连接，pingTag 为空 → 仅需建连成功即可
	d := newRockwellDriver()
	cfg, err := ParseRockwellConfig(`{"host":"1.2.3.4","port":44818}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	d.config = cfg
	d.client = &mockTransport{connected: true}
	if err := d.Ping(`{"host":"1.2.3.4","port":44818}`); err != nil {
		t.Fatalf("Ping (reused, empty pingTag) error: %v", err)
	}
}

func TestDriverPingReuseTagDenied(t *testing.T) {
	// pingTag 设备可达但拒绝（标签不存在）→ 仍视为连通
	d := newRockwellDriver()
	cfg, err := ParseRockwellConfig(`{"host":"1.2.3.4","port":44818,"pingTag":"P"}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	d.config = cfg
	d.client = &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		return nil, cip.Error{Status: cip.StatusPathDestinationUnknown}
	}}
	if err := d.Ping(`{"host":"1.2.3.4","port":44818,"pingTag":"P"}`); err != nil {
		t.Fatalf("Ping (tag denied) error: %v", err)
	}
}

func TestDriverPingReuseTagNetworkError(t *testing.T) {
	// pingTag 网络错误 → 不可达
	d := newRockwellDriver()
	cfg, err := ParseRockwellConfig(`{"host":"1.2.3.4","port":44818,"pingTag":"P"}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	d.config = cfg
	d.client = &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		return nil, fmt.Errorf("connection reset")
	}}
	if err := d.Ping(`{"host":"1.2.3.4","port":44818,"pingTag":"P"}`); err == nil {
		t.Fatal("expected ping error on network failure")
	}
}

// ==================== 数组合并读取与读取计划缓存 ====================

func TestSplitArrayIndex(t *testing.T) {
	cases := []struct {
		name string
		base string
		idx  int
		ok   bool
	}{
		{"Motor[3]", "Motor", 3, true},
		{"Motor[0]", "Motor", 0, true},
		{"Motor[123]", "Motor", 123, true},
		{"Motor", "", 0, false},            // 无下标
		{"Motor[x]", "", 0, false},         // 非数字
		{"Motor[-1]", "", 0, false},        // 负下标
		{"Motor[1.5]", "", 0, false},       // 小数
		{"Motor[3].Speed", "", 0, false},   // 成员访问
		{"Motor[2,3]", "", 0, false},       // 多维
		{"Motor.5", "", 0, false},          // 位访问
		{"[3]", "", 0, false},              // 空基础名
		{"Prog:M[1]", "Prog:M", 1, true},   // 程序作用域标签允许（含冒号）
		{"Motor_1[2]", "Motor_1", 2, true}, // 基础名含下划线
	}
	for _, c := range cases {
		base, idx, ok := splitArrayIndex(c.name)
		if ok != c.ok || (ok && (base != c.base || idx != c.idx)) {
			t.Errorf("splitArrayIndex(%q) = (%q,%d,%v), want (%q,%d,%v)",
				c.name, base, idx, ok, c.base, c.idx, c.ok)
		}
	}
}

func TestPlanCacheHit(t *testing.T) {
	// 同一点位集合重复 Read：第二次应命中 plan 缓存（结果一致即可，
	// 缓存正确性由 Read 结果断言间接验证）
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	mock := &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		return mkResp(cipTypeINT, 0x64, 0x00), nil
	}}
	d.client = mock
	addrs := []po.DeviceAddress{
		mkAddr("a1", "T1", "int16", "Short"),
		mkAddr("a2", "T2", "int16", "Short"),
	}
	for round := 0; round < 2; round++ {
		results, err := d.Read(addrs)
		if err != nil {
			t.Fatalf("round %d Read error: %v", round, err)
		}
		assertResult(t, results[0], "a1", "100", "int16", 192)
		assertResult(t, results[1], "a2", "100", "int16", 192)
	}
}

func TestDriverReadArrayRangeMerged(t *testing.T) {
	// Motor[0..3] 四个 int 数组点位合并为一次 ReadTagElements("Motor", 4)，
	// 各点位按元素下标切片解码
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	mock := &mockTransport{connected: true,
		readElems: func(tag string, count uint16) ([]byte, error) {
			if tag != "Motor" || count != 4 {
				return nil, fmt.Errorf("unexpected elements read: %s x%d", tag, count)
			}
			// 4 个 int16 元素：10, 20, 30, 40
			buf := make([]byte, 0, 8)
			for _, v := range []int16{10, 20, 30, 40} {
				b := mkResp(cipTypeINT, byte(v), byte(v>>8))
				buf = append(buf, b[2:]...) // 去掉类型码头，仅拼数据
			}
			return mkResp(cipTypeINT, buf...), nil
		},
	}
	d.client = mock

	addrs := []po.DeviceAddress{
		mkAddr("a0", "Motor[0]", "int16", "Short"),
		mkAddr("a2", "Motor[2]", "int16", "Short"),
		mkAddr("a1", "Motor[1]", "int16", "Short"),
		mkAddr("a3", "Motor[3]", "int16", "Short"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	assertResult(t, results[0], "a0", "10", "int16", 192)
	assertResult(t, results[1], "a2", "30", "int16", 192)
	assertResult(t, results[2], "a1", "20", "int16", 192)
	assertResult(t, results[3], "a3", "40", "int16", 192)
	// 合并后只有一次请求
	if len(mock.calls) != 1 {
		t.Errorf("requests = %d (%v), want 1", len(mock.calls), mock.calls)
	}
}

func TestDriverReadArrayRangeOversize(t *testing.T) {
	// 元素数超上限（> maxTagElements）→ 按下标切分为多个区间分块，逐块合并读
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	// offsetBase 模拟真实设备的绝对下标语义：每次请求返回「自上次调用以来
	// 覆盖的绝对下标」起连续 count 个元素（第一块 0..124，第二块 125）
	offsetBase := 0
	mock := &mockTransport{connected: true,
		readElems: func(tag string, count uint16) ([]byte, error) {
			if tag != "Big" {
				return nil, fmt.Errorf("unexpected elements read: %s x%d", tag, count)
			}
			buf := make([]byte, int(count)*2)
			for i := 0; i < int(count); i++ {
				binary.LittleEndian.PutUint16(buf[i*2:], uint16(offsetBase+i))
			}
			offsetBase += int(count)
			return mkResp(cipTypeINT, buf...), nil
		},
	}
	d.client = mock

	addrs := make([]po.DeviceAddress, 0, maxTagElements+1)
	for i := 0; i <= maxTagElements; i++ {
		addrs = append(addrs, mkAddr(fmt.Sprintf("a%d", i), fmt.Sprintf("Big[%d]", i), "int16", "Short"))
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	// 126 点切成 2 块（125 + 1）
	if len(mock.calls) != 2 {
		t.Errorf("requests = %d (%v), want 2 chunked reads", len(mock.calls), mock.calls)
	}
	// 各点位值 = 自身下标，验证分块切片解码未串位
	for i, r := range results {
		want := strconv.Itoa(i)
		if r.Value != want {
			t.Errorf("point %d: value=%q, want %q", i, r.Value, want)
		}
	}
}

func TestDriverReadArrayRangeSparse(t *testing.T) {
	// 稀疏下标（0 与 9）：仍合并为一次读 10 个元素，中间元素丢弃
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	mock := &mockTransport{connected: true,
		readElems: func(tag string, count uint16) ([]byte, error) {
			if tag != "S" || count != 10 {
				return nil, fmt.Errorf("unexpected elements read: %s x%d", tag, count)
			}
			buf := make([]byte, 0, 20)
			for i := 0; i < 10; i++ {
				buf = append(buf, byte(i), 0)
			}
			return mkResp(cipTypeINT, buf...), nil
		},
	}
	d.client = mock

	addrs := []po.DeviceAddress{
		mkAddr("a0", "S[0]", "int16", "Short"),
		mkAddr("a9", "S[9]", "int16", "Short"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	assertResult(t, results[0], "a0", "0", "int16", 192)
	assertResult(t, results[1], "a9", "9", "int16", 192)
}

func TestDriverReadArrayRangeShortResponse(t *testing.T) {
	// 设备实际数组比请求区间小 → 越界点位 Quality=0，在界点位正常
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	mock := &mockTransport{connected: true,
		readElems: func(tag string, count uint16) ([]byte, error) {
			// 只回 1 个元素（下标 0），下标 1 越界
			return mkResp(cipTypeINT, 0x07, 0x00), nil
		},
	}
	d.client = mock

	addrs := []po.DeviceAddress{
		mkAddr("a0", "S[0]", "int16", "Short"),
		mkAddr("a1", "S[1]", "int16", "Short"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	assertResult(t, results[0], "a0", "7", "int16", 192)
	assertResult(t, results[1], "a1", "", "int16", 0)
}

func TestDriverReadArrayMixedTypesNoMerge(t *testing.T) {
	// 组内类型不一致（int16 与 int32 混读同一数组）→ 不合并，逐标签读
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	mock := &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		return mkResp(cipTypeDINT, 0x64, 0x00, 0x00, 0x00), nil
	}}
	d.client = mock

	addrs := []po.DeviceAddress{
		mkAddr("a0", "M[0]", "int16", "Short"),
		mkAddr("a1", "M[1]", "int32", "Long"),
	}
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("Read error: %v", err)
	}
	// 未合并：2 次普通 ReadTag（readElems 未配置，若走到会报错）
	if len(mock.calls) != 2 {
		t.Errorf("requests = %d (%v), want 2 plain reads", len(mock.calls), mock.calls)
	}
}

func TestPlanKeyStable(t *testing.T) {
	a := []po.DeviceAddress{
		mkAddr("a1", "T1", "int16", "Short"),
		mkAddr("a2", "T12", "int16", "Short"),
	}
	b := []po.DeviceAddress{
		mkAddr("a1", "T1", "int16", "Short"),
		mkAddr("a2", "T12", "int16", "Short"),
	}
	if planKey(a) != planKey(b) {
		t.Error("planKey should be stable for identical address lists")
	}
	// 内容不同（含前缀歧义场景 "T1"+"2" vs "T12"+""）→ 指纹不同
	c := []po.DeviceAddress{
		mkAddr("a1", "T12", "int16", "Short"),
		mkAddr("a2", "", "int16", "Short"),
	}
	if planKey(a) == planKey(c) {
		t.Error("planKey collision between distinct address lists")
	}
}
