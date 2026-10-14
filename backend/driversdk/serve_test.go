// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driversdk_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
	"iot-gateway/model/po"
	"iot-gateway/pluginwire"
)

// ==================== 测试替身 ====================

// stubDriver 可编程的 Driver 实现，用于驱动 Serve 的各条语义分支。
type stubDriver struct {
	mu        sync.Mutex
	connected bool
	readErr   error
	pingErr   error
	readCount atomic.Int64
	closed    atomic.Int64
	// gate 非 nil 时 Read 会阻塞直到它被关闭（用于并发/超时场景）。
	gate chan struct{}
	// panicOnRead 为真时 Read 直接 panic（模拟插件驱动的确定性缺陷）。
	panicOnRead bool
	// closeEntered/closeGate 用于确定性地构造"Close 已持会话锁、Read 在锁外排队"
	// 这个交错：Close 进入后先关闭 closeEntered，再阻塞在 closeGate 上。
	closeEntered chan struct{}
	closeGate    chan struct{}
}

func (d *stubDriver) Connect(string) error {
	d.mu.Lock()
	d.connected = true
	d.mu.Unlock()
	return nil
}

func (d *stubDriver) Ping(string) error { return d.pingErr }

func (d *stubDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	if d.panicOnRead {
		panic("stub driver 故意 panic")
	}
	if d.gate != nil {
		<-d.gate
	}
	d.readCount.Add(1)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.readErr != nil {
		return nil, d.readErr
	}
	out := make([]driver.ReadResult, len(addrs))
	for i, a := range addrs {
		out[i] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           fmt.Sprintf("v-%s", a.Name),
			DataType:        a.DataType,
			Kind:            driver.KindInt,
			Quality:         192,
		}
	}
	return out, nil
}

func (d *stubDriver) IsConnected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.connected
}

func (d *stubDriver) Close() error {
	d.closed.Add(1)
	if d.closeGate != nil {
		close(d.closeEntered)
		<-d.closeGate
	}
	d.mu.Lock()
	d.connected = false
	d.mu.Unlock()
	return nil
}

// testProtocol 本文件全部测试共用的单个协议名。
// 多协议场景由 pluginhost 包的 modbus 插件集成测试覆盖。
const testProtocol = "Test.Plugin.Serve"

// testFactories 把单协议工厂包成 SDK 需要的按协议名索引的形式。
func testFactories(mk driversdk.Factory) driversdk.Factories {
	return driversdk.Factories{testProtocol: mk}
}

func testMeta() driversdk.Meta {
	return driversdk.Meta{
		APIVersion: driversdk.APIVersion,
		Protocols:  []driversdk.Protocol{{Name: testProtocol}},
		Scope:      "testplugin_serve",
		Types: []*driver.DataType{
			{Name: "bool", Kind: driver.KindBool, Size: 1},
			{Name: "int16", Kind: driver.KindInt, Size: 2},
		},
		TypeMap: map[string]string{"Boolean": "bool", "Short": "int16"},
	}
}

// startServer 在 net.Pipe 上跑一个插件服务端，返回客户端侧的连接。
// 零子进程：完整协议栈在进程内跑通。
func startServer(t *testing.T, mk func() driver.Driver, meta driversdk.Meta) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- driversdk.ServeConn(server, testFactories(mk), meta, driversdk.Capabilities{}) }()

	t.Cleanup(func() {
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("ServeConn 未在 2s 内返回（可能协程泄漏）")
		}
	})
	return client
}

// ==================== 测试客户端 ====================

// rpcClient 最小客户端：串行化调用（每个 call 内部写+读到自己的 id）。
// 网关侧真正带多路复用的客户端在 pluginhost 包中实现与测试。
type rpcClient struct {
	t      *testing.T
	rw     io.ReadWriteCloser
	mu     sync.Mutex
	nextID atomic.Uint64
	// buffered 存放「读到了但当前等的不是它」的应答。
	//
	// 不能丢弃：线协议按 id 多路复用，**应答顺序无保证** ——
	// 两个请求可以同时在途（如 send 出去的 close 与 read），服务端各在自己的
	// goroutine 里回，先拿到会话锁的那个先回。丢弃乱序应答会让后来的 await
	// 永远等一个其实已经到过的帧（表现为测试挂死，且只在特定调度下复现）。
	buffered map[uint64]pluginwire.Frame
}

func newRPCClient(t *testing.T, rw io.ReadWriteCloser) *rpcClient {
	return &rpcClient{t: t, rw: rw, buffered: make(map[uint64]pluginwire.Frame)}
}

// readFor 读到指定 id 的应答；途中遇到的其它应答先存进 buffered。
// 调用方须持有 c.mu。
func (c *rpcClient) readFor(id uint64) pluginwire.Frame {
	c.t.Helper()
	if f, ok := c.buffered[id]; ok {
		delete(c.buffered, id)
		return f
	}
	for {
		f, err := pluginwire.ReadFrame(c.rw)
		if err != nil {
			c.t.Fatalf("ReadFrame(等待 id=%d): %v", id, err)
		}
		if f.ID == id {
			return f
		}
		c.t.Logf("应答乱序：等 id=%d，先收到 id=%d", id, f.ID)
		c.buffered[f.ID] = f
	}
}

func (c *rpcClient) call(method string, params any) pluginwire.Frame {
	c.t.Helper()
	// 与网关侧 client.call 同一套分发：载荷类型决定编码。
	body, codec, err := pluginwire.EncodeBody(params)
	if err != nil {
		c.t.Fatalf("encode params: %v", err)
	}
	return c.callRaw(method, codec, body)
}

// callRaw 以原始载荷发起调用，用于构造非法参数场景。
// codec 需与载荷类型匹配：ReadParams/ReadReply 是 CodecBinary，其余是 CodecJSON。
func (c *rpcClient) callRaw(method string, codec uint8, params []byte) pluginwire.Frame {
	c.t.Helper()
	id := c.nextID.Add(1)

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := pluginwire.WriteFrame(c.rw, pluginwire.Frame{ID: id, Method: method, Codec: codec, Params: params}); err != nil {
		c.t.Fatalf("WriteFrame(%s): %v", method, err)
	}
	return c.readFor(id)
}

// callOK 调用并要求无错误，返回解码后的结果。
func (c *rpcClient) callOK(method string, params, out any) {
	c.t.Helper()
	f := c.call(method, params)
	if f.Error != nil {
		c.t.Fatalf("%s 返回错误: %s", method, f.Error.Message)
	}
	if out != nil {
		if err := pluginwire.DecodeBody(f.Codec, f.Result, out); err != nil {
			c.t.Fatalf("%s 解码结果失败: %v (codec=%d raw=%x)", method, err, f.Codec, f.Result)
		}
	}
}

// send 只写帧、不等应答。用于构造"两个请求同时在途"的场景（call 会串行化）。
func (c *rpcClient) send(id uint64, method string, params any) {
	c.t.Helper()
	body, codec, err := pluginwire.EncodeBody(params)
	if err != nil {
		c.t.Fatalf("encode params: %v", err)
	}
	if err := pluginwire.WriteFrame(c.rw, pluginwire.Frame{ID: id, Method: method, Codec: codec, Params: body}); err != nil {
		c.t.Fatalf("WriteFrame(%s): %v", method, err)
	}
}

// await 读到指定 id 的应答。乱序到达的其它应答会被缓存而不是丢弃。
func (c *rpcClient) await(id uint64) pluginwire.Frame {
	c.t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readFor(id)
}

// ==================== 测试 ====================

func TestDescribeReturnsMeta(t *testing.T) {
	meta := testMeta()
	c := newRPCClient(t, startServer(t, func() driver.Driver { return &stubDriver{} }, meta))

	var got driversdk.Meta
	c.callOK(pluginwire.MethodDescribe, struct{}{}, &got)

	if len(got.Protocols) != 1 || got.Protocols[0].Name != testProtocol || got.Scope != meta.Scope {
		t.Errorf("describe 返回 %+v/%q, want [%s]/%q", got.Protocols, got.Scope, testProtocol, meta.Scope)
	}
	if len(got.Types) != 2 {
		t.Fatalf("Types 数量 = %d, want 2", len(got.Types))
	}
	// 编解码函数不出进程：网关侧拿到的是 funcs 为 nil 的 DataType。
	if got.Types[0].Decode != nil || got.Types[0].Encode != nil || got.Types[0].Format != nil {
		t.Error("过线后的 DataType 不应携带编解码函数")
	}
	if got.Types[0].Name != "bool" || got.Types[0].Kind != driver.KindBool || got.Types[0].Size != 1 {
		t.Errorf("Types[0] = %+v, want {bool bool 1}", *got.Types[0])
	}
	if got.TypeMap["Short"] != "int16" {
		t.Errorf("TypeMap[Short] = %q, want int16", got.TypeMap["Short"])
	}
}

func TestConnectReadCloseLifecycle(t *testing.T) {
	mk := func() driver.Driver { return &stubDriver{} }
	c := newRPCClient(t, startServer(t, mk, testMeta()))

	var cr driversdk.ConnectResult
	c.callOK(pluginwire.MethodConnect, driversdk.ConnectParams{Protocol: testProtocol, ProtocolJSON: `{"host":"127.0.0.1"}`}, &cr)
	if cr.Session == 0 {
		t.Fatal("connect 应返回非 0 会话 id")
	}
	if !cr.Connected {
		t.Error("connect 后 Connected 应为 true")
	}

	addrs := []driversdk.WireAddr{
		{ID: "a1", Name: "point1", DataType: "int16"},
		{ID: "a2", Name: "point2", DataType: "bool"},
	}
	var rr driversdk.ReadReply
	c.callOK(pluginwire.MethodRead, driversdk.ReadParams{Session: cr.Session, Addrs: addrs}, &rr)

	if len(rr.Results) != len(addrs) {
		t.Fatalf("结果数 = %d, want %d（索引必须与 addrs 对齐）", len(rr.Results), len(addrs))
	}
	for i, r := range rr.Results {
		if r.DeviceAddressID != addrs[i].ID {
			t.Errorf("结果[%d].DeviceAddressID = %q, want %q", i, r.DeviceAddressID, addrs[i].ID)
		}
		if r.Value != "v-"+addrs[i].Name {
			t.Errorf("结果[%d].Value = %q, want %q", i, r.Value, "v-"+addrs[i].Name)
		}
		if r.Kind != driver.KindInt {
			t.Errorf("结果[%d].Kind = %q, want %q", i, r.Kind, driver.KindInt)
		}
	}
	if !rr.Connected {
		t.Error("read 应答应携带 Connected")
	}

	var ic driversdk.IsConnectedReply
	c.callOK(pluginwire.MethodIsConnected, driversdk.SessionParams{Session: cr.Session}, &ic)
	if !ic.Connected {
		t.Error("isConnected 应为 true")
	}

	c.callOK(pluginwire.MethodClose, driversdk.SessionParams{Session: cr.Session}, nil)
}

// TestCloseUnknownSessionSucceeds 关闭未知会话静默成功，
// 等价于对"从未连接过的进程内驱动"调用 Close。
func TestCloseUnknownSessionSucceeds(t *testing.T) {
	c := newRPCClient(t, startServer(t, func() driver.Driver { return &stubDriver{} }, testMeta()))
	c.callOK(pluginwire.MethodClose, driversdk.SessionParams{Session: 999}, nil)
}

func TestReadUnknownSessionErrors(t *testing.T) {
	c := newRPCClient(t, startServer(t, func() driver.Driver { return &stubDriver{} }, testMeta()))
	f := c.call(pluginwire.MethodRead, driversdk.ReadParams{Session: 999})
	if f.Error == nil {
		t.Fatal("未知会话的 read 应报错")
	}
}

// TestReadErrorStillCarriesConnected 是本设计最关键的一条语义：
// 驱动 Read 报错且 IsConnected()==false 时，应答必须同时带 error 和
// Connected=false —— 这是网关感知链路掉线的唯一通道
// （采集引擎只在 IsConnected()==false 时重连，Read 报错本身不触发）。
func TestReadErrorStillCarriesConnected(t *testing.T) {
	stub := &stubDriver{readErr: errors.New("link down")}
	mk := func() driver.Driver { return stub }
	c := newRPCClient(t, startServer(t, mk, testMeta()))

	var cr driversdk.ConnectResult
	c.callOK(pluginwire.MethodConnect, driversdk.ConnectParams{Protocol: testProtocol}, &cr)
	if !cr.Connected {
		t.Fatal("前置条件：connect 后应为已连接")
	}

	// 模拟链路掉线：驱动自身转为未连接并让 Read 报错。
	stub.mu.Lock()
	stub.connected = false
	stub.mu.Unlock()

	f := c.call(pluginwire.MethodRead, driversdk.ReadParams{Session: cr.Session, Addrs: []driversdk.WireAddr{{ID: "a1", Name: "p", DataType: "int16"}}})
	if f.Error == nil {
		t.Fatal("驱动报错应回传 Error")
	}
	if f.Error.Message != "link down" {
		t.Errorf("Error.Message = %q, want %q", f.Error.Message, "link down")
	}
	if len(f.Result) == 0 {
		t.Fatal("报错时也必须回传 Result（携带 connected 状态）")
	}
	var rr driversdk.ReadReply
	if err := pluginwire.DecodeBody(f.Codec, f.Result, &rr); err != nil {
		t.Fatalf("解码报错应答的 result: %v", err)
	}
	if rr.Connected {
		t.Error("链路掉线时报错应答必须携带 Connected=false，否则网关永不重连")
	}
}

// TestPingAllocatesNoSession ping 不分配会话 —— driver.PingDevice 创建临时实例
// 调用 Ping 且从不 Close，若 Ping 分配会话资源则每次「测试连接」泄漏一个。
func TestPingAllocatesNoSession(t *testing.T) {
	stub := &stubDriver{}
	mk := func() driver.Driver { return stub }
	c := newRPCClient(t, startServer(t, mk, testMeta()))

	c.callOK(pluginwire.MethodPing, driversdk.PingParams{Protocol: testProtocol, ProtocolJSON: `{"host":"127.0.0.1"}`}, nil)

	// 未分配任何会话：会话 1 不存在。
	if f := c.call(pluginwire.MethodRead, driversdk.ReadParams{Session: 1}); f.Error == nil {
		t.Fatal("ping 不应分配会话，但会话 1 存在")
	}
	// 且临时实例已被 Close（不泄漏连接）。
	if got := stub.closed.Load(); got != 1 {
		t.Errorf("ping 后 Close 调用次数 = %d, want 1", got)
	}
}

func TestPingErrorPropagates(t *testing.T) {
	stub := &stubDriver{pingErr: errors.New("unreachable")}
	c := newRPCClient(t, startServer(t, func() driver.Driver { return stub }, testMeta()))

	f := c.call(pluginwire.MethodPing, driversdk.PingParams{Protocol: testProtocol})
	if f.Error == nil || f.Error.Message != "unreachable" {
		t.Fatalf("ping 错误应回传，得到 %v", f.Error)
	}
}

// TestConnectFailureAllocatesNoSession 连接失败不留下半初始化会话。
func TestConnectFailureAllocatesNoSession(t *testing.T) {
	stub := &failConnectDriver{}
	c := newRPCClient(t, startServer(t, func() driver.Driver { return stub }, testMeta()))

	f := c.call(pluginwire.MethodConnect, driversdk.ConnectParams{Protocol: testProtocol})
	if f.Error == nil {
		t.Fatal("connect 失败应回传错误")
	}
	var cr driversdk.ConnectResult
	if err := json.Unmarshal(f.Result, &cr); err != nil {
		t.Fatalf("解码 connect 失败应答: %v", err)
	}
	if cr.Session != 0 {
		t.Errorf("连接失败后 Session = %d, want 0", cr.Session)
	}
	if stub.closeCount.Load() != 1 {
		t.Errorf("连接失败后应 Close 一次以免泄漏，实际 %d", stub.closeCount.Load())
	}
}

type failConnectDriver struct {
	stubDriver
	closeCount atomic.Int64
}

func (d *failConnectDriver) Connect(string) error { return errors.New("dial failed") }
func (d *failConnectDriver) Close() error         { d.closeCount.Add(1); return nil }

// TestConcurrentSessions 并发请求按 id 正确配对：用裸帧一次性发出 N 个请求，
// 再收集 N 个应答按 id 匹配。服务端为每个请求起协程，应答顺序不保证 ——
// 这正是网关侧必须做多路复用的原因（非独占驱动的 Read 可并发在途）。
func TestConcurrentSessions(t *testing.T) {
	const n = 50
	mk := func() driver.Driver { return &stubDriver{} }
	conn := startServer(t, mk, testMeta())

	// 每个会话独立：先顺序建立 n 个连接（connect 本身也是并发的，但
	// 建立后再并发读取，测的是 read 的并发派发）。
	// 这里直接用裸连接手写，避免 rpcClient 的串行化。
	var wmu sync.Mutex
	send := func(id uint64, method string, params any) {
		body, codec, err := pluginwire.EncodeBody(params)
		if err != nil {
			t.Errorf("encode params: %v", err)
			return
		}
		wmu.Lock()
		err = pluginwire.WriteFrame(conn, pluginwire.Frame{ID: id, Method: method, Codec: codec, Params: body})
		wmu.Unlock()
		if err != nil {
			t.Errorf("WriteFrame: %v", err)
		}
	}

	// 并发写入 n 个 describe 请求。
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			send(uint64(i+1), pluginwire.MethodDescribe, struct{}{})
		}(i)
	}
	wg.Wait()

	// 收集 n 个应答，按 id 断言全部配对且无重复。
	seen := make(map[uint64]bool, n)
	for i := 0; i < n; i++ {
		f, err := pluginwire.ReadFrame(conn)
		if err != nil {
			t.Fatalf("读第 %d 个应答失败: %v", i, err)
		}
		if f.ID < 1 || f.ID > n {
			t.Fatalf("应答 id = %d 越界", f.ID)
		}
		if seen[f.ID] {
			t.Fatalf("应答 id %d 重复", f.ID)
		}
		seen[f.ID] = true
		if f.Error != nil {
			t.Fatalf("应答 %d 报错: %s", f.ID, f.Error.Message)
		}
	}
	if len(seen) != n {
		t.Fatalf("收到 %d 个不同 id, want %d", len(seen), n)
	}
}

// TestDriverPanicBecomesRPCError 驱动 panic 必须降级为**一次** RPC 错误，
// 而不是带走整个插件进程。
//
// 每个请求都在自己的 goroutine 里跑，Go 的 panic 不可跨 goroutine 恢复 ——
// 没有顶层 recover 时，一个点位的确定性缺陷（空批次取 [0]、Close 后解引用、
// 类型断言失败）会让该插件下**所有协议、所有设备**一起断，监管按退避重启几次
// 之后永久放弃。这条测试没有 recover 时会直接把测试进程打死。
func TestDriverPanicBecomesRPCError(t *testing.T) {
	stub := &stubDriver{panicOnRead: true}
	c := newRPCClient(t, startServer(t, func() driver.Driver { return stub }, testMeta()))

	var cr driversdk.ConnectResult
	c.callOK(pluginwire.MethodConnect, driversdk.ConnectParams{Protocol: testProtocol}, &cr)

	f := c.call(pluginwire.MethodRead, driversdk.ReadParams{
		Session: cr.Session,
		Addrs:   []driversdk.WireAddr{{ID: "a1", Name: "p1", DataType: "int16"}},
	})
	if f.Error == nil {
		t.Fatalf("驱动 panic 必须回传错误帧，实际得到 %+v", f)
	}
	if !strings.Contains(f.Error.Message, "panic") {
		t.Errorf("错误信息应指明是驱动 panic，实际 %q", f.Error.Message)
	}

	// 服务端必须还活着：后续调用照常应答。
	var meta driversdk.Meta
	c.callOK(pluginwire.MethodDescribe, struct{}{}, &meta)
	if len(meta.Protocols) == 0 {
		t.Fatal("panic 之后服务端已不再提供协议 —— 进程可能已经被带走")
	}
}

// TestReadQueuedBehindCloseDoesNotTouchClosedDriver 在 Close 之后才拿到会话锁的
// Read 必须失败，而不是落在已拆解的驱动上。
//
// 「查会话表」与「拿会话锁」是两个动作：一个在途 Read 完全可能在 MethodClose
// 拆完驱动之后才轮到它拿锁（Close 已在拆、它在锁外排队）。此时读回的是驱动里
// 残留的上一轮值，会被当作新样本以 Quality=192 上报；驱动若是 Close 后把内部
// 客户端置 nil，则是 panic（进而带走整个进程）。
//
// 交错是确定性的：Close 进入驱动后先阻塞，等测试确认"锁已被持有"再放行，
// 而 Read 在这期间已经在锁外排队。
func TestReadQueuedBehindCloseDoesNotTouchClosedDriver(t *testing.T) {
	stub := &stubDriver{
		closeEntered: make(chan struct{}),
		closeGate:    make(chan struct{}),
	}
	c := newRPCClient(t, startServer(t, func() driver.Driver { return stub }, testMeta()))

	var cr driversdk.ConnectResult
	c.callOK(pluginwire.MethodConnect, driversdk.ConnectParams{Protocol: testProtocol}, &cr)

	c.send(901, pluginwire.MethodClose, driversdk.SessionParams{Session: cr.Session})
	<-stub.closeEntered // 此刻 Close 正持着会话锁，Read 若现在到达只能在锁外排队

	c.send(902, pluginwire.MethodRead, driversdk.ReadParams{
		Session: cr.Session,
		Addrs:   []driversdk.WireAddr{{ID: "a1", Name: "p1", DataType: "int16"}},
	})

	close(stub.closeGate) // 放行 Close：它拆完驱动并释放锁，排队的 Read 这才拿到锁

	if f := c.await(901); f.Error != nil {
		t.Fatalf("Close 不应报错: %s", f.Error.Message)
	}
	if f := c.await(902); f.Error == nil {
		t.Errorf("Close 已拆解驱动，排队的 Read 必须失败而不是返回读值: %s", f.Result)
	}
	if n := stub.readCount.Load(); n != 0 {
		t.Errorf("驱动的 Read 被执行了 %d 次 —— 读落在了已 Close 的驱动上", n)
	}
}

// TestShutdownClosesSessionsAndReturns shutdown 应答后服务端应立即返回，
// 并关闭全部会话。
func TestShutdownClosesSessionsAndReturns(t *testing.T) {
	stub := &stubDriver{}
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- driversdk.ServeConn(server, testFactories(func() driver.Driver { return stub }), testMeta(), driversdk.Capabilities{})
	}()

	c := newRPCClient(t, client)
	var cr driversdk.ConnectResult
	c.callOK(pluginwire.MethodConnect, driversdk.ConnectParams{Protocol: testProtocol}, &cr)

	// 必须把 shutdown 的应答读走：net.Pipe 是同步管道，
	// 服务端会一直阻塞在写应答上，读不到就不会继续走到 shutdown。
	if err := pluginwire.WriteFrame(client, pluginwire.Frame{ID: 999, Method: pluginwire.MethodShutdown}); err != nil {
		t.Fatalf("WriteFrame(shutdown): %v", err)
	}
	if _, err := pluginwire.ReadFrame(client); err != nil {
		t.Fatalf("读取 shutdown 应答: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeConn 应正常返回，得到 %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown 后 ServeConn 未返回")
	}

	if got := stub.closed.Load(); got != 1 {
		t.Errorf("shutdown 应关闭会话，Close 调用次数 = %d, want 1", got)
	}
}

// TestEOFExitsServe 模拟父进程被强杀：管道关闭 → 读端 EOF → 服务端正常退出。
// 这是防止孤儿进程的主要机制。
func TestEOFExitsServe(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- driversdk.ServeConn(server, testFactories(func() driver.Driver { return &stubDriver{} }), testMeta(), driversdk.Capabilities{})
	}()

	_ = client.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("对端关闭后 ServeConn 应返回 nil，得到 %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("对端关闭后 ServeConn 未返回")
	}
}

func TestUnknownMethodAndBadParams(t *testing.T) {
	c := newRPCClient(t, startServer(t, func() driver.Driver { return &stubDriver{} }, testMeta()))

	if f := c.call("noSuchMethod", struct{}{}); f.Error == nil {
		t.Error("未知方法应报错")
	}

	// read 走二进制：载荷被截断（只给了 session，缺 session 之后的字段）。
	// 这条同时验证「畸形二进制载荷必须报错而不是解出一堆零值」。
	if f := c.callRaw(pluginwire.MethodRead, pluginwire.CodecBinary, []byte{0x01}); f.Error == nil {
		t.Error("截断的二进制参数应报错")
	}

	// 编码标记与载荷类型对不上：声明 JSON 却把二进制塞进去，必须报错而不是静默解出零值。
	if f := c.callRaw(pluginwire.MethodRead, pluginwire.CodecJSON, []byte{0x01, 0x01, 0x00}); f.Error == nil {
		t.Error("用 JSON 声明二进制载荷应报错")
	}

	// 未知编码标记：必须报错，不能当成 JSON 蒙混过去。
	if f := c.callRaw(pluginwire.MethodRead, 0x7f, []byte{0x01}); f.Error == nil {
		t.Error("未知载荷编码应报错")
	}
}

// TestServeRejectsInvalidMeta 启动期校验必须拦住坏 Meta（等价的网关侧校验在
// pluginhost 加载插件时再做一遍）。
func TestServeRejectsInvalidMeta(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })

	meta := testMeta()
	meta.Protocols = nil // 非法
	err := driversdk.ServeConn(server, testFactories(func() driver.Driver { return &stubDriver{} }), meta, driversdk.Capabilities{})
	if err == nil {
		t.Fatal("非法 Meta 应导致 ServeConn 立即返回错误")
	}
}
