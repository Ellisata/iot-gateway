// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"fmt"
	"testing"

	"iot-gateway/driver"
	cipcore "iot-gateway/driversrc/omroncip/cipcore"
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

// assertQuality 断言一个点位的质量码。成因并入 Quality 之后，那条“192 带成因”的
// 非法组合已经不可表达，故只比对质量码本身。
func assertQuality(t *testing.T, r driver.ReadResult, id string, want int) {
	t.Helper()
	if r.Quality != want {
		t.Errorf("%s Quality = %d, want %d", id, r.Quality, want)
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
		got, ok := LookupCommonType(c.common)
		if !ok {
			t.Errorf("LookupCommonType(%s) not found", c.common)
			continue
		}
		if got != c.want {
			t.Errorf("LookupCommonType(%s) = %s, want %s", c.common, got, c.want)
		}
	}
	// Date 不支持（CIP 数据表读取无 DATE 类型码）
	if _, ok := LookupCommonType("Date"); ok {
		t.Error("Date should not be supported for CIP")
	}
	// 大小写不敏感
	if got, ok := LookupCommonType("short"); !ok || got != "int16" {
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
	assertResult(t, results[0], "a1", "100", "int16", driver.QualityGood)
	assertResult(t, results[1], "a2", "100", "word", driver.QualityGood)
	assertResult(t, results[2], "a3", "100", "int32", driver.QualityGood)
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
	assertResult(t, results[0], "a1", "", "int16", driver.QualityBadAddress)
	assertResult(t, results[1], "a2", "", "int16", driver.QualityBadAddress)
	// 0x04 路径段错误 = 设备在说"这个标签路径我这儿没有" → 地址级（配置错，永不恢复）
	assertQuality(t, results[0], "a1", driver.QualityBadAddress)
	assertQuality(t, results[1], "a2", driver.QualityBadAddress)
}

// TestDriverReadQualityClassification 钉住"通用状态码 → 成因"的判据本身。
//
// 这是本次变更的核心断言：同一个 markTagZero 分支下，0x04/0x05/0x16（设备说
// "这个标签不存在"）归地址级 —— 配置错，设备状态怎么恢复都不会变；其余通用
// 状态（资源不可用、部分传输…）归通信级 —— 只是设备这一轮拒绝，下轮原样重试
// 就可能拿到值。混为一谈正是这次要修的毛病。
func TestDriverReadQualityClassification(t *testing.T) {
	cases := []struct {
		status byte
		want   int
		why    string
	}{
		{0x04, driver.QualityBadAddress, "路径段错误：标签路径解析不了"},
		{0x05, driver.QualityBadAddress, "路径目标未知：标签不存在"},
		{0x16, driver.QualityBadAddress, "对象不存在"},
		{0x02, driver.QualityBadComms, "资源不可用：设备暂时拒绝"},
		{0x06, driver.QualityBadComms, "部分传输：设备暂时拒绝"},
		{0x13, driver.QualityBadComms, "数据过长：设备暂时拒绝"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("status_0x%02X", c.status), func(t *testing.T) {
			d := newCIPDriver()
			d.config = DefaultCIPConfig()
			d.config.MaxTagsPerRequest = 0 // 单读路径
			d.client = &mockCIPTransport{connected: true, readFn: func(tag string, code uint16) ([]byte, error) {
				return nil, cipcore.NewGeneralStatusError(c.status)
			}}
			results, err := d.Read([]po.DeviceAddress{mkCIPAddr("a1", "T", "int16", "Short")})
			if err != nil {
				t.Fatalf("Read error: %v", err)
			}
			assertResult(t, results[0], "a1", "", "int16", c.want)
			if c.why == "" {
				t.Fatal("case 缺少 why")
			}
		})
	}
}

// TestDriverReadParseQuality 设备答了、字节也收到了，但本地按配置的类型解不出来
// → 成因是解析级，**不是**地址级（地址是对的，链路也是通的）。
func TestDriverReadParseQuality(t *testing.T) {
	d := newCIPDriver()
	d.config = DefaultCIPConfig()
	d.config.MaxTagsPerRequest = 0 // 单读路径
	d.client = &mockCIPTransport{connected: true, readFn: func(tag string, code uint16) ([]byte, error) {
		return []byte{0x64, 0x00}, nil // 只回 2 字节，而 int32 需要 4 字节
	}}
	results, err := d.Read([]po.DeviceAddress{mkCIPAddr("a1", "T", "int32", "Long")})
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	assertResult(t, results[0], "a1", "", "int32", driver.QualityBadParse)
	assertQuality(t, results[0], "a1", driver.QualityBadParse)
}

// TestDriverReadUnsupportedTypeQuality 配置的数据类型本网关根本不支持 → 请求都
// 没发出去，与地址写错同一性质（改配置才能好）→ 地址级。
func TestDriverReadUnsupportedTypeQuality(t *testing.T) {
	d := newCIPDriver()
	d.config = DefaultCIPConfig()
	d.client = &mockCIPTransport{connected: true, readFn: func(tag string, code uint16) ([]byte, error) {
		return []byte{0x64, 0x00}, nil
	}}
	results, err := d.Read([]po.DeviceAddress{mkCIPAddr("a1", "T", "NoSuchType", "None")})
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	assertResult(t, results[0], "a1", "", "NoSuchType", driver.QualityBadAddress)
	assertQuality(t, results[0], "a1", driver.QualityBadAddress)
}

// TestDriverReadWholeBatchRejectedQuality 整批被拒绝后的逐标签回退也失败时，
// 每个点位各自带通信级成因（设备答的是"服务不支持"，不是"这个标签不存在"）。
func TestDriverReadWholeBatchRejectedQuality(t *testing.T) {
	d := newCIPDriver()
	cfg := DefaultCIPConfig()
	cfg.MaxTagsPerRequest = 3
	d.config = cfg
	mock := &mockCIPTransport{
		connected: true,
		readTagsFn: func(specs []TagSpec) ([]TagResult, error) {
			return nil, cipcore.NewGeneralStatusError(0x08) // 服务不支持
		},
		readFn: func(tag string, code uint16) ([]byte, error) {
			return nil, cipcore.NewGeneralStatusError(0x02) // 资源不可用
		},
	}
	d.client = mock

	addrs := []po.DeviceAddress{
		mkCIPAddr("a1", "T1", "int16", "Short"),
		mkCIPAddr("a2", "T2", "int16", "Short"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("设备拒绝不该让 Read 报错，got: %v", err)
	}
	for i := range results {
		assertResult(t, results[i], fmt.Sprintf("a%d", i+1), "", "int16", driver.QualityBadComms)
		assertQuality(t, results[i], fmt.Sprintf("a%d", i+1), driver.QualityBadComms)
	}
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
		assertResult(t, results[i], fmt.Sprintf("a%d", i+1), "100", "int16", driver.QualityGood)
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
	assertResult(t, results[0], "a1", "100", "int16", driver.QualityGood)
	assertResult(t, results[1], "a2", "", "int16", driver.QualityBadAddress)
	assertResult(t, results[2], "a3", "100", "int16", driver.QualityGood)
	// 被拒的那个点位是地址级，健康的兄弟点位必须是 QualityGood（不许"整批一起标"）
	assertQuality(t, results[0], "a1", driver.QualityGood)
	assertQuality(t, results[1], "a2", driver.QualityBadAddress)
	assertQuality(t, results[2], "a3", driver.QualityGood)
}

// TestDriverReadSingleTagBatchDenied 一个点位（定长类型、默认批量开）走批量路径
// 被设备拒绝时，行为必须与单读路径完全一致：Read 不报错，该点 Quality=0。
//
// 这是真机 2026-09-29 现场的形状：10.62.100.16 上只有一个名为 Recipe.Setpoint 的
// word 点位，设备回通用状态 0x05。此前 ReadTags 的单标签分支把该错误升级为函数级
// 错误，采集引擎据此把整台设备置为离线并反复重连 —— 而 PLC 一直是通的。
func TestDriverReadSingleTagBatchDenied(t *testing.T) {
	d := newCIPDriver()
	d.config = DefaultCIPConfig() // MaxTagsPerRequest=64：word 是定长类型，走批量路径
	d.client = &mockCIPTransport{
		connected: true,
		readTagsFn: func(specs []TagSpec) ([]TagResult, error) {
			if len(specs) != 1 {
				t.Fatalf("len(specs) = %d, want 1", len(specs))
			}
			// 修正后的单标签语义：设备拒绝 → 逐标签 Err，不是函数级错误
			return []TagResult{{Err: cipcore.NewGeneralStatusError(0x05)}}, nil
		},
	}

	results, err := d.Read([]po.DeviceAddress{mkCIPAddr("a1", "Recipe.Setpoint", "word", "Word")})
	if err != nil {
		t.Fatalf("设备拒绝一个点位不该让整台设备读取失败，got: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	assertResult(t, results[0], "a1", "", "word", driver.QualityBadAddress)
	// 0x05 路径目标未知 = 标签不存在（真机上的 Recipe.Setpoint 就是这个码）→ 地址级
	assertQuality(t, results[0], "a1", driver.QualityBadAddress)
}

// TestDriverReadWholeBatchRejected 整批被设备拒绝时，回退逐标签单读取值，
// 而不是把整批点位置 0 或让 Read 报错 —— 连接是通的，只是"批量"这个形态不成立。
//
// 用例喂的是**通用状态错误**这种函数级错误形态（真机上 0x0A 的拒绝会走
// errBatchUnusable 哨兵，见 batchfallback_test.go 的 TestParseMultipleReadMarksBatchUnusable）。
// 两种形态走同一条回退路径：这里钉住的是哨兵之外的那条来路，别让它在将来某次
// 重构里被悄悄删掉。
func TestDriverReadWholeBatchRejected(t *testing.T) {
	d := newCIPDriver()
	cfg := DefaultCIPConfig()
	cfg.MaxTagsPerRequest = 3
	d.config = cfg
	mock := &mockCIPTransport{
		connected: true,
		readTagsFn: func(specs []TagSpec) ([]TagResult, error) {
			return nil, cipcore.NewGeneralStatusError(0x08)
		},
		readFn: func(tag string, code uint16) ([]byte, error) {
			return []byte{0x64, 0x00}, nil
		},
	}
	d.client = mock

	addrs := []po.DeviceAddress{
		mkCIPAddr("a1", "T1", "int16", "Short"),
		mkCIPAddr("a2", "T2", "int16", "Short"),
		mkCIPAddr("a3", "T3", "int16", "Short"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("设备拒绝整批不该让 Read 报错，got: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}
	for i := range results {
		assertResult(t, results[i], fmt.Sprintf("a%d", i+1), "100", "int16", driver.QualityGood)
	}
	// 整批只试一次，随后整批的 3 个标签逐个单读
	if mock.batchCalls != 1 {
		t.Errorf("ReadTags called %d times, want 1", mock.batchCalls)
	}
	if mock.singleCalls != 3 {
		t.Errorf("ReadTag called %d times, want 3（整批回退为逐个单读）", mock.singleCalls)
	}
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
