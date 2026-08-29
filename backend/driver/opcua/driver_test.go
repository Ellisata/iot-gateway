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
	readFunc func(ctx context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error)
	sendFunc func(ctx context.Context, req ua.Request) (ua.Response, error)
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

func (f *fakeClient) Browse(context.Context, *ua.BrowseRequest) (*ua.BrowseResponse, error) {
	return nil, ua.StatusBadServiceUnsupported
}
func (f *fakeClient) BrowseNext(context.Context, *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
	return nil, ua.StatusBadServiceUnsupported
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
	wantQ := []int{192, 128, 0, 0}
	for i, q := range wantQ {
		if results[i].Quality != q {
			t.Errorf("result[%d].Quality = %d, want %d", i, results[i].Quality, q)
		}
	}
	if results[2].Value != "" {
		t.Errorf("result[2].Value = %q, want empty (bad status)", results[2].Value)
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
	if results[0].Quality != 0 || results[1].Value != "9" {
		t.Errorf("results = %+v, want [q0, 9]", results)
	}
	if !d.IsConnected() {
		t.Error("service-level error must not disconnect")
	}
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
	if results[0].Quality != 0 {
		t.Errorf("invalid address Quality = %d, want 0", results[0].Quality)
	}
	if results[1].Value != "5" {
		t.Errorf("valid address Value = %q, want 5", results[1].Value)
	}
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
	if results[0].Quality != 0 {
		t.Errorf("unresolvable path Quality = %d, want 0", results[0].Quality)
	}
	if !d.IsConnected() {
		t.Error("service-level path failure must not disconnect")
	}
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

// TestStatusQuality 直接验证 severity → 质量映射边界。
func TestStatusQuality(t *testing.T) {
	cases := []struct {
		code ua.StatusCode
		want int
	}{
		{ua.StatusOK, 192},
		{ua.StatusGood, 192},
		{ua.StatusGoodMoreData, 192},
		{ua.StatusUncertain, 128},
		{ua.StatusUncertainInitialValue, 128},
		{ua.StatusBad, 0},
		{ua.StatusBadNodeIDUnknown, 0},
		{ua.StatusBadTooManyOperations, 0},
	}
	for _, c := range cases {
		if got := statusQuality(c.code); got != c.want {
			t.Errorf("statusQuality(0x%08X) = %d, want %d", c.code, got, c.want)
		}
	}
}

// TestReadResultType 验证返回类型与 driver.ReadResult 兼容（编译期断言）。
var _ driver.Driver = (*opcuaDriver)(nil)
