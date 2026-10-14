// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// fakeClient gopcua 客户端替身：控制 Read/Send 行为，验证驱动逻辑。
type fakeClient struct {
	readFunc   func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error)
	sendFunc   func(ctx context.Context, req ua.Request) (ua.Response, error)
	browseFunc func(ctx context.Context, req *ua.BrowseRequest) (*ua.BrowseResponse, error)
	// nextFunc 省掉的续页请求次数由 browseNextCalls 记着，供分页测试断言。
	nextFunc        func(ctx context.Context, req *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error)
	browseNextCalls int
}

func (f *fakeClient) Node(id *ua.NodeID) *opcua.Node {
	return opcua.NewNode(id, f)
}

func (f *fakeClient) NodeFromExpandedNodeID(id *ua.ExpandedNodeID) *opcua.Node {
	if id == nil || id.NodeID == nil {
		return nil
	}
	return opcua.NewNode(id.NodeID, f)
}

func (f *fakeClient) Read(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
	return f.readFunc(ctx, req)
}

func (f *fakeClient) Send(ctx context.Context, req ua.Request, h func(ua.Response) error) error {
	resp, err := f.sendFunc(ctx, req)
	if err != nil {
		return err
	}
	return h(resp)
}

func (f *fakeClient) RequestTimeout() time.Duration { return 5 * time.Second }

func (f *fakeClient) Close(context.Context) error { return nil }

// Browse / BrowseNext 默认回"服务不支持"——与库自带的服务器一致
// （它实现了 Browse，但 TranslateBrowsePathsToNodeIds 是 serviceUnsupported），
// 既有的采集路径测试因此不受影响；点位扫描的测试按需注入 browseFunc/nextFunc。
func (f *fakeClient) Browse(ctx context.Context, req *ua.BrowseRequest) (*ua.BrowseResponse, error) {
	if f.browseFunc == nil {
		return nil, ua.StatusBadServiceUnsupported
	}
	return f.browseFunc(ctx, req)
}

func (f *fakeClient) BrowseNext(ctx context.Context, req *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
	f.browseNextCalls++
	if f.nextFunc == nil {
		return nil, ua.StatusBadServiceUnsupported
	}
	return f.nextFunc(ctx, req)
}
func (f *fakeClient) ForgetSubscription(context.Context, uint32) {}

// newTestDriver 构建注入 fake client 的驱动（已连接）。
func newTestDriver(c *fakeClient) *opcuaDriver {
	d := newOpcUaDriver()
	d.newClient = func(ctx context.Context, cfg *OpcUaConfig) (*OpcUaClient, error) {
		oc := &OpcUaClient{c: c}
		oc.connected.Store(true)
		return oc, nil
	}
	return d
}

// connect 用最小配置连接测试驱动。
func (d *opcuaDriver) connect(t *testing.T) {
	t.Helper()
	if err := d.Connect(`{"host":"127.0.0.1"}`); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
}

func testAddrs(dts ...string) []po.DeviceAddress {
	addrs := make([]po.DeviceAddress, len(dts))
	for i, dt := range dts {
		addrs[i] = po.DeviceAddress{
			ID:       fmt.Sprintf("id-%d", i),
			Name:     fmt.Sprintf("ns=2;i=%d", 100+i),
			DataType: dt,
		}
	}
	return addrs
}

// dv 构造指定值/状态码的 DataValue。
func dv(v any, status ua.StatusCode) *ua.DataValue {
	return &ua.DataValue{Value: ua.MustVariant(v), Status: status}
}

// assertQuality 断言一个点位的质量码。成因并入 Quality 之后，那条“192 带成因”的
// 非法组合已经不可表达，故只比对质量码本身。
func assertQuality(t *testing.T, r driver.ReadResult, id string, want int) {
	t.Helper()
	if r.Quality != want {
		t.Errorf("%s Quality = %d, want %d", id, r.Quality, want)
	}
}

// TestReadBasic 验证基础读取：值格式化、按序回填、质量、Kind 取配置类型。
func TestReadBasic(t *testing.T) {
	c := &fakeClient{
		readFunc: func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
			return &ua.ReadResponse{Results: []*ua.DataValue{
				dv(int64(42), ua.StatusOK),
				dv(21.5, ua.StatusOK),
				dv(true, ua.StatusOK),
				dv("hello", ua.StatusOK),
			}}, nil
		},
	}
	d := newTestDriver(c)
	d.connect(t)

	addrs := testAddrs("int64", "float64", "bool", "string")
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ val, kind string }{
		{"42", "int"}, {"21.5", "float"}, {"1", "bool"}, {"hello", "string"},
	}
	for i, w := range want {
		if results[i].DeviceAddressID != addrs[i].ID {
			t.Errorf("result[%d].id = %q, want %q", i, results[i].DeviceAddressID, addrs[i].ID)
		}
		if results[i].Value != w.val {
			t.Errorf("result[%d].Value = %q, want %q", i, results[i].Value, w.val)
		}
		if results[i].Kind != w.kind {
			t.Errorf("result[%d].Kind = %q, want %q", i, results[i].Kind, w.kind)
		}
		if results[i].Quality != 192 {
			t.Errorf("result[%d].Quality = %d, want 192", i, results[i].Quality)
		}
	}
}

// TestReadKindFallback 验证配置类型未注册时 Kind 按实际值推断。
func TestReadKindFallback(t *testing.T) {
	c := &fakeClient{
		readFunc: func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
			return &ua.ReadResponse{Results: []*ua.DataValue{dv(uint64(7), ua.StatusOK)}}, nil
		},
	}
	d := newTestDriver(c)
	d.connect(t)

	results, err := d.Read(testAddrs("unknown"))
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Kind != "uint" {
		t.Errorf("Kind = %q, want uint (from actual value)", results[0].Kind)
	}
	if results[0].Value != "7" {
		t.Errorf("Value = %q, want 7", results[0].Value)
	}
}

// TestReadQualityMapping 验证状态码 severity → 质量映射。
func TestReadQualityMapping(t *testing.T) {
	c := &fakeClient{
		readFunc: func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
			return &ua.ReadResponse{Results: []*ua.DataValue{
				dv(int64(1), ua.StatusOK),
				dv(int64(2), ua.StatusUncertain),
				dv(int64(3), ua.StatusBadNodeIDUnknown),
				nil, // 服务器未返回该节点
			}}, nil
		},
	}
	d := newTestDriver(c)
	d.connect(t)

	results, err := d.Read(testAddrs("int64", "int64", "int64", "int64"))
	if err != nil {
		t.Fatal(err)
	}
	wantQ := []int{driver.QualityGood, driver.QualityUncertain, driver.QualityBadAddress, driver.QualityBad}
	for i, q := range wantQ {
		if results[i].Quality != q {
			t.Errorf("result[%d].Quality = %d, want %d", i, results[i].Quality, q)
		}
	}
	if results[2].Value != "" {
		t.Errorf("result[2].Value = %q, want empty (bad status)", results[2].Value)
	}
	// BadNodeIDUnknown = 服务器地址空间里没这个节点 → 地址级（配置错，永不恢复）。
	assertQuality(t, results[2], "bad-node", driver.QualityBadAddress)
	// 128(Uncertain) 不是故障、Good 就是 Good、服务器没返回的槽位无从判断（保持未归类）。
	assertQuality(t, results[0], "good", driver.QualityGood)
	assertQuality(t, results[1], "uncertain", driver.QualityUncertain)
	assertQuality(t, results[3], "missing", driver.QualityBad)
}

// TestReadNodeStatusQuality 逐节点状态码的成因分类。
//
// 这是本次变更在 opcua 上的核心：同一个"某个节点读不到"的位置，服务器说的
// "这个节点不在我这儿"（BadNodeIDUnknown）与"我现在读不了"（链路类）性质相反。
func TestReadNodeStatusQuality(t *testing.T) {
	c := &fakeClient{
		readFunc: func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
			return &ua.ReadResponse{Results: []*ua.DataValue{
				dv(int64(1), ua.StatusBadNodeIDUnknown),
				dv(int64(2), ua.StatusBadCommunicationError),
				dv(int64(3), ua.StatusBadTimeout),
				dv(int64(4), ua.StatusBadNodeIDInvalid),
				dv(int64(5), ua.StatusBadAttributeIDInvalid),
				dv(int64(6), ua.StatusBadSessionIDInvalid), // 认不出来的码 → 不猜
			}}, nil
		},
	}
	d := newTestDriver(c)
	d.connect(t)

	results, err := d.Read(testAddrs("int64", "int64", "int64", "int64", "int64", "int64"))
	if err != nil {
		t.Fatal(err)
	}
	want := []int{
		driver.QualityBadAddress, // BadNodeIDUnknown
		driver.QualityBadComms,   // BadCommunicationError
		driver.QualityBadComms,   // BadTimeout
		driver.QualityBadAddress, // BadNodeIDInvalid
		driver.QualityBadAddress, // BadAttributeIDInvalid
		driver.QualityBad,        // BadSessionIDInvalid：非地址也非链路，不归因
	}
	for i, w := range want {
		assertQuality(t, results[i], fmt.Sprintf("r%d", i), w)
	}
}

// TestStatusQuality 直接钉住"状态码 → 质量码"的判据边界。
func TestStatusQuality(t *testing.T) {
	cases := []struct {
		code ua.StatusCode
		want int
	}{
		// Good / Uncertain severity：不是故障，归 QualityBad 之外的两类得由 severityQuality 判
		{ua.StatusOK, driver.QualityBad},
		{ua.StatusUncertain, driver.QualityBad},
		// 地址级：服务器说"这个节点不在我的地址空间里"
		{ua.StatusBadNodeIDUnknown, driver.QualityBadAddress},
		{ua.StatusBadNodeIDInvalid, driver.QualityBadAddress},
		{ua.StatusBadNotFound, driver.QualityBadAddress},
		{ua.StatusBadNoMatch, driver.QualityBadAddress},
		{ua.StatusBadAttributeIDInvalid, driver.QualityBadAddress},
		// 通信级：链路/服务器状态，下轮可能就好
		{ua.StatusBadTimeout, driver.QualityBadComms},
		{ua.StatusBadCommunicationError, driver.QualityBadComms},
		{ua.StatusBadServerNotConnected, driver.QualityBadComms},
		{ua.StatusBadConnectionClosed, driver.QualityBadComms},
		// 认不出来的 Bad 码：留未归类，不猜
		{ua.StatusBadSessionIDInvalid, driver.QualityBad},
		{ua.StatusBadTooManyOperations, driver.QualityBad},
	}
	for _, c := range cases {
		if got := statusQuality(c.code); got != c.want {
			// 打印原始数值：ua.StatusCode 的 %X 会走它自己的格式化器输出状态文本，
			// 失败信息会变成一长串英文，读不出来是哪个码。
			t.Errorf("statusQuality(0x%08X) = %d, want %d", uint32(c.code), got, c.want)
		}
	}
}

// TestReadSubBatch 验证 MaxBatch 子批拆分与调用次数。
func TestReadSubBatch(t *testing.T) {
	var sizes []int
	c := &fakeClient{
		readFunc: func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
			sizes = append(sizes, len(req.NodesToRead))
			results := make([]*ua.DataValue, len(req.NodesToRead))
			for i := range results {
				results[i] = dv(int64(i), ua.StatusOK)
			}
			return &ua.ReadResponse{Results: results}, nil
		},
	}
	d := newTestDriver(c)
	if err := d.Connect(`{"host":"127.0.0.1","maxBatch":2}`); err != nil {
		t.Fatal(err)
	}

	results, err := d.Read(testAddrs("int64", "int64", "int64", "int64", "int64"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 3 || sizes[0] != 2 || sizes[1] != 2 || sizes[2] != 1 {
		t.Errorf("Read calls sizes = %v, want [2 2 1]", sizes)
	}
	if len(results) != 5 {
		t.Errorf("results len = %d, want 5", len(results))
	}
}

// TestReadServiceError 验证服务级错误（StatusCode）：该子批 Quality=0，不断开。
func TestReadServiceError(t *testing.T) {
	readCalls := 0
	c := &fakeClient{
		readFunc: func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
			readCalls++
			if readCalls == 1 {
				return nil, ua.StatusBadTooManyOperations
			}
			return &ua.ReadResponse{Results: []*ua.DataValue{dv(int64(9), ua.StatusOK)}}, nil
		},
	}
	d := newTestDriver(c)
	d.connect(t)

	// 两批：MaxBatch=1，首批服务级错误，第二批正常
	if err := d.Connect(`{"host":"127.0.0.1","maxBatch":1}`); err != nil {
		t.Fatal(err)
	}
	results, err := d.Read(testAddrs("int64", "int64"))
	if err != nil {
		t.Fatalf("Read() want nil error for service-level failure, got %v", err)
	}
	if results[0].Quality != driver.QualityBadComms || results[1].Value != "9" {
		t.Errorf("results = %+v, want [QualityBadComms, 9]", results)
	}
	if !d.IsConnected() {
		t.Error("service-level error must not disconnect")
	}
	// 整段被拒（BadTooManyOperations）：服务器一个节点都没答，无从判定哪个点位
	// 有问题 → 整段归通信级，绝不猜地址级。成功的兄弟点位不受连坐。
	assertQuality(t, results[0], "rejected-batch", driver.QualityBadComms)
	assertQuality(t, results[1], "ok-batch", driver.QualityGood)
}

// TestReadTransportError 验证传输级错误：断开标记 + 返回 error。
func TestReadTransportError(t *testing.T) {
	c := &fakeClient{
		readFunc: func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
			return nil, io.EOF
		},
	}
	d := newTestDriver(c)
	d.connect(t)

	_, err := d.Read(testAddrs("int64"))
	if err == nil {
		t.Fatal("Read() want error for transport failure, got nil")
	}
	if d.IsConnected() {
		t.Error("IsConnected() = true, want false after transport error")
	}
}

// TestReadInvalidAddress 验证非法地址仅该点位 Quality=0，其它正常读取。
func TestReadInvalidAddress(t *testing.T) {
	c := &fakeClient{
		readFunc: func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
			return &ua.ReadResponse{Results: []*ua.DataValue{dv(int64(5), ua.StatusOK)}}, nil
		},
	}
	d := newTestDriver(c)
	d.connect(t)

	addrs := []po.DeviceAddress{
		{ID: "bad", Name: "ns=x;i=5", DataType: "int64"},
		{ID: "good", Name: "ns=2;i=200", DataType: "int64"},
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Quality != driver.QualityBadAddress {
		t.Errorf("invalid address Quality = %d, want %d", results[0].Quality, driver.QualityBadAddress)
	}
	if results[1].Value != "5" {
		t.Errorf("valid address Value = %q, want 5", results[1].Value)
	}
	// "ns=x;i=5" 本地就解析不出来（非法命名空间）→ 地址级，改点位表才能好。
	assertQuality(t, results[0], "bad", driver.QualityBadAddress)
	// 同批健康点位不许被连坐。
	assertQuality(t, results[1], "good", driver.QualityGood)
}

// TestReadBrowsePathCached 验证浏览路径解析：首次经 Send 解析并缓存，第二次命中缓存不再解析。
func TestReadBrowsePathCached(t *testing.T) {
	target := ua.NewNumericNodeID(2, 100)
	sendCalls := 0
	c := &fakeClient{
		sendFunc: func(ctx context.Context, req ua.Request) (ua.Response, error) {
			if _, ok := req.(*ua.TranslateBrowsePathsToNodeIDsRequest); !ok {
				return nil, ua.StatusBadServiceUnsupported
			}
			sendCalls++
			return &ua.TranslateBrowsePathsToNodeIDsResponse{Results: []*ua.BrowsePathResult{
				{StatusCode: ua.StatusOK, Targets: []*ua.BrowsePathTarget{
					{TargetID: ua.NewExpandedNodeID(target, "", 0)},
				}},
			}}, nil
		},
		readFunc: func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
			return &ua.ReadResponse{Results: []*ua.DataValue{dv(int64(77), ua.StatusOK)}}, nil
		},
	}
	d := newTestDriver(c)
	d.connect(t)

	addrs := []po.DeviceAddress{
		{ID: "path", Name: "Root/Objects/Device/Temp", DataType: "int64"},
	}
	for i := 0; i < 2; i++ {
		results, err := d.Read(addrs)
		if err != nil {
			t.Fatal(err)
		}
		if results[0].Value != "77" {
			t.Errorf("Read #%d Value = %q, want 77", i+1, results[0].Value)
		}
	}
	if sendCalls != 1 {
		t.Errorf("browse path resolve calls = %d, want 1 (cached on 2nd read)", sendCalls)
	}
}

// TestReadBrowsePathUnresolvable 验证浏览路径解析返回 StatusCode（服务级）：该点位 Quality=0，不断开。
func TestReadBrowsePathUnresolvable(t *testing.T) {
	c := &fakeClient{
		sendFunc: func(ctx context.Context, req ua.Request) (ua.Response, error) {
			return nil, ua.StatusBadNoMatch
		},
		readFunc: func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
			t.Fatal("Read should not be called when all addresses unresolvable")
			return nil, nil
		},
	}
	d := newTestDriver(c)
	d.connect(t)

	addrs := []po.DeviceAddress{
		{ID: "path", Name: "Root/Objects/NoSuchNode", DataType: "int64"},
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read() want nil error for service-level path failure, got %v", err)
	}
	if results[0].Quality != driver.QualityBadAddress {
		t.Errorf("unresolvable path Quality = %d, want %d", results[0].Quality, driver.QualityBadAddress)
	}
	if !d.IsConnected() {
		t.Error("service-level path failure must not disconnect")
	}
	// BadNoMatch = 浏览路径在服务器上找不到对应节点 → 地址级。
	assertQuality(t, results[0], "unresolvable", driver.QualityBadAddress)
}

// TestReadBrowsePathTransportQualityUnclassified 浏览路径解析的**链路类**状态码
// （BadTimeout）归通信级，与"路径不存在"（地址级）相反 —— 同一个解析失败位置，
// 成因由状态码决定，不能一律归地址级。
func TestReadBrowsePathTransportQualityUnclassified(t *testing.T) {
	c := &fakeClient{
		sendFunc: func(ctx context.Context, req ua.Request) (ua.Response, error) {
			return nil, ua.StatusBadTimeout
		},
		readFunc: func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
			t.Fatal("Read should not be called when all addresses unresolvable")
			return nil, nil
		},
	}
	d := newTestDriver(c)
	d.connect(t)

	results, err := d.Read([]po.DeviceAddress{
		{ID: "path", Name: "Root/Objects/Temp", DataType: "int64"},
	})
	if err != nil {
		t.Fatalf("Read() want nil error, got %v", err)
	}
	assertQuality(t, results[0], "timeout-on-resolve", driver.QualityBadComms)
}

// TestNotConnected 验证未连接时 Read 报错。
func TestNotConnected(t *testing.T) {
	d := newOpcUaDriver()
	if _, err := d.Read(testAddrs("int64")); err == nil {
		t.Fatal("Read() want error when not connected, got nil")
	}
	if d.IsConnected() {
		t.Error("IsConnected() = true before Connect")
	}
}

// TestSeverityQuality 直接验证 severity → 质量类别的映射边界。
func TestSeverityQuality(t *testing.T) {
	cases := []struct {
		code ua.StatusCode
		want int
	}{
		{ua.StatusOK, driver.QualityGood},
		{ua.StatusGood, driver.QualityGood},
		{ua.StatusGoodMoreData, driver.QualityGood},
		{ua.StatusUncertain, driver.QualityUncertain},
		{ua.StatusUncertainInitialValue, driver.QualityUncertain},
		{ua.StatusBad, driver.QualityBad},
		{ua.StatusBadNodeIDUnknown, driver.QualityBad},
		{ua.StatusBadTooManyOperations, driver.QualityBad},
	}
	for _, c := range cases {
		if got := severityQuality(c.code); got != c.want {
			t.Errorf("severityQuality(0x%08X) = %d, want %d", uint32(c.code), got, c.want)
		}
	}
}

// TestReadResultType 验证返回类型与 driver.ReadResult 兼容（编译期断言）。
var _ driver.Driver = (*opcuaDriver)(nil)
