// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"context"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
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
	// listTags 是**采集契约之外**的可选能力（tagEnumerator，见 pointscan.go）。
	// 真传输层（goindustrialClient）也实现了它；没配的用例调到就是一次失败。
	listTags func(ctx context.Context) ([]cip.SymbolInstance, error)
	closed   bool
	calls    map[string]int
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

// ListTags 让 mockTransport 也满足 tagEnumerator（见 pointscan.go）。
// 注意它**不在 rockwellTransport 接口里**：枚举是扫描专有的可选能力。
func (m *mockTransport) ListTags(ctx context.Context) ([]cip.SymbolInstance, error) {
	if m.listTags == nil {
		return nil, fmt.Errorf("mock: no listTags configured")
	}
	return m.listTags(ctx)
}

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

// TestLookupDataType 类型映射已随实现搬进本包（typemap.go），不再查内置静态表
// driver.LookupDataType —— 那张表里已无 Rockwell 条目，插件进程也用不到它。
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
		got, ok := LookupCommonType(c.common)
		if !ok {
			t.Errorf("LookupCommonType(%s) not found", c.common)
			continue
		}
		if got != c.want {
			t.Errorf("LookupCommonType(%s) = %s, want %s", c.common, got, c.want)
		}
	}
	// Logix 数值原生有符号，无 CIP 标准无符号类型码；Date/BCD/LBCD 无原生类型
	for _, unsupported := range []string{"Date", "Byte", "Word", "DWord", "BCD", "LBCD"} {
		if _, ok := LookupCommonType(unsupported); ok {
			t.Errorf("%s should not be supported for Rockwell", unsupported)
		}
	}
	// 大小写不敏感（沿用内置时代 driver.LookupScopeType 的语义）
	if got, ok := LookupCommonType("short"); !ok || got != "int16" {
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
	assertResult(t, results[0], "a1", "100", "int16", driver.QualityGood)
	assertResult(t, results[1], "a2", "100", "int32", driver.QualityGood)
	assertResult(t, results[2], "a3", "3.14", "float32", driver.QualityGood)
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
	assertResult(t, results[0], "a1", "100", "int16", driver.QualityGood)
	assertResult(t, results[1], "a2", "", "int16", driver.QualityBadAddress)
	assertResult(t, results[2], "a3", "100", "int16", driver.QualityGood)
}

func TestDriverReadNetworkError(t *testing.T) {
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	d.client = &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		return nil, fmt.Errorf("network down")
	}}
	addrs := []po.DeviceAddress{mkAddr("a1", "T1", "int16", "Short")}
	_, err := d.Read(addrs)
	if err == nil {
		t.Fatal("expected error on network failure")
	}
	// 反向断言：真正的网络故障**不得**被说成"设备在线但拒绝命令"。
	// 与下面 TestDriverReadEncapRefused 是一对 —— 只改文案、方向误伤，这条就红。
	if strings.Contains(err.Error(), "封装层") {
		t.Fatalf("网络故障被误判成封装层拒绝: %v", err)
	}
}

// 设备在封装层拒绝命令（它答了、但拒绝执行）→ **仍然上抛**（采集侧据此判设备级失败，
// 该报警还是报警），但成因必须写清楚：这是"在线但不支持这条命令"，不是"断联"。
// 文案里的人话决定了运维会不会被引去查网络、重启 PLC —— 判据与取舍见 tagReadError。
func TestDriverReadEncapRefused(t *testing.T) {
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	d.client = &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		return nil, &encapError{status: 0x01, err: fmt.Errorf("operation failed after 1 retries")}
	}}
	addrs := []po.DeviceAddress{mkAddr("a1", "MotorSpeed", "int16", "Short")}

	_, err := d.Read(addrs)
	if err == nil {
		t.Fatal("封装层拒绝仍应上抛错误")
	}
	for _, want := range []string{"封装层", "0x01", "不接受", "不是网络故障"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误文案应含 %q，实际: %v", want, err)
		}
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
	assertResult(t, results[0], "a1", "100", "int16", driver.QualityGood)
	assertResult(t, results[1], "a2", "", "int32", driver.QualityBadParse)
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
		assertResult(t, results[0], "a1", "100", "int16", driver.QualityGood)
		assertResult(t, results[1], "a2", "100", "int16", driver.QualityGood)
	}
}

func TestDriverReadArrayRangeMerged(t *testing.T) {
	// Motor[0..3] 四个 int 数组点位合并为一次 Read Tag Elements，
	// 各点位按元素下标切片解码。
	//
	// 请求路径是 `Motor[0]`（带起始下标）而不是裸 `Motor`：CIP 的 Read Tag 请求里
	// 只有元素个数、没有偏移字段，偏移只能写在路径里 —— 发裸名等于每次都从元素 0 读，
	// 一旦分块（下标 > 0）就会静默串位。本块 offset=0，两种写法等价。
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	mock := &mockTransport{connected: true,
		readElems: func(tag string, count uint16) ([]byte, error) {
			if tag != "Motor[0]" || count != 4 {
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
	assertResult(t, results[0], "a0", "10", "int16", driver.QualityGood)
	assertResult(t, results[1], "a2", "30", "int16", driver.QualityGood)
	assertResult(t, results[2], "a1", "20", "int16", driver.QualityGood)
	assertResult(t, results[3], "a3", "40", "int16", driver.QualityGood)
	// 合并后只有一次请求
	if len(mock.calls) != 1 {
		t.Errorf("requests = %d (%v), want 1", len(mock.calls), mock.calls)
	}
}

func TestDriverReadArrayRangeOversize(t *testing.T) {
	// 元素数超上限（> maxTagElements）→ 按下标切分为多个区间分块，逐块合并读。
	//
	// 假 PLC 按**真实设备的语义**应答：Read Tag Elements 的请求里只有元素个数、
	// 没有偏移（CIP 规范如此，库的 NewReadTagRequest 也只写两字节元素数），偏移
	// 必须写在路径里 —— 假设备据此解析请求路径，返回「该路径起点起」count 个元素，
	// 元素值 = 它的绝对下标。路径不带下标时这里直接报错，而不是静默给出错值。
	//
	// 这条用例原先是按"设备记得上一次读到哪"的假语义写的（offsetBase 自增），
	// 于是无论请求路径对不对都绿；按真实语义则第二块起的点位会静默读到别的元素
	// 的值（300 个点实测 175 个错）。
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	var calls []string
	mock := &mockTransport{connected: true,
		readElems: func(tag string, count uint16) ([]byte, error) {
			calls = append(calls, fmt.Sprintf("%s x%d", tag, count))
			base, off, ok := splitArrayIndex(tag)
			if !ok || base != "Big" {
				return nil, fmt.Errorf("请求路径必须带起始下标（设备只按路径定位元素），got %q", tag)
			}
			buf := make([]byte, int(count)*2)
			for i := 0; i < int(count); i++ {
				binary.LittleEndian.PutUint16(buf[i*2:], uint16(off+i))
			}
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
	// 126 点切成 2 块（125 + 1），每块的请求路径带上自己的起始下标
	if len(mock.calls) != 2 {
		t.Errorf("requests = %d (%v), want 2 chunked reads", len(mock.calls), mock.calls)
	}
	wantCalls := []string{"Big[0] x125", "Big[125] x1"}
	if len(calls) != len(wantCalls) {
		t.Fatalf("请求 = %v, want %v", calls, wantCalls)
	}
	for i := range calls {
		if calls[i] != wantCalls[i] {
			t.Errorf("请求[%d] = %q, want %q", i, calls[i], wantCalls[i])
		}
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
			if tag != "S[0]" || count != 10 {
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
	assertResult(t, results[0], "a0", "0", "int16", driver.QualityGood)
	assertResult(t, results[1], "a9", "9", "int16", driver.QualityGood)
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
	assertResult(t, results[0], "a0", "7", "int16", driver.QualityGood)
	assertResult(t, results[1], "a1", "", "int16", driver.QualityBadParse)
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
