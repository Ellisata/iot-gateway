// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
	"iot-gateway/model/po"
)

// ==================== 测试替身 ====================

// ctlDriver 可控的插件侧驱动，用于驱动 remoteDriver 的各条语义分支。
type ctlDriver struct {
	mu        sync.Mutex
	connected bool
	readErr   error
	// resultCount < 0 表示按 addrs 数量返回；否则固定返回该数量（用于测试索引对齐校验）。
	resultCount int
	readCalls   atomic.Int64
}

func (d *ctlDriver) Connect(string) error {
	d.mu.Lock()
	d.connected = true
	d.mu.Unlock()
	return nil
}
func (d *ctlDriver) Ping(string) error { return nil }
func (d *ctlDriver) Close() error {
	d.mu.Lock()
	d.connected = false
	d.mu.Unlock()
	return nil
}
func (d *ctlDriver) IsConnected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.connected
}
func (d *ctlDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	d.readCalls.Add(1)
	d.mu.Lock()
	err, n := d.readErr, d.resultCount
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if n < 0 {
		n = len(addrs)
	}
	out := make([]driver.ReadResult, n)
	for i := 0; i < n && i < len(addrs); i++ {
		out[i] = driver.ReadResult{DeviceAddressID: addrs[i].ID, Value: "val-" + addrs[i].Name, DataType: addrs[i].DataType, Kind: driver.KindInt}
	}
	return out, nil
}

func (d *ctlDriver) set(fn func(*ctlDriver)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	fn(d)
}

// fakeProcess 实现 processLink，支持模拟进程崩溃与重启。
type fakeProcess struct {
	mu       sync.Mutex
	cl       *client
	up       bool
	ep       uint64
	clients  []*client
	timeouts Timeouts
	restarts int
}

func newFakeProcess(t *testing.T) *fakeProcess {
	p := &fakeProcess{timeouts: DefaultTimeouts()}
	t.Cleanup(func() {
		p.mu.Lock()
		clients := p.clients
		p.mu.Unlock()
		for _, c := range clients {
			c.fail(ErrProcessDown)
			c.wait()
		}
	})
	return p
}

func (p *fakeProcess) acquire(context.Context) (*client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.up || p.cl == nil {
		return nil, ErrProcessDown
	}
	return p.cl, nil
}

func (p *fakeProcess) isUp() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.up && p.cl != nil && p.cl.isUp()
}

func (p *fakeProcess) epoch() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ep
}

// start 拉起（或重启）插件进程：换入新客户端并递增世代号。
//
// 必须把世代号烙进客户端，与真实 pluginProcess.markUp 的不变量
// 「cl.epoch == p.epoch()」保持一致：remoteDriver 的会话有效性判据就建在
// 客户端自带的世代号上（见 client.epoch），替身漏了这一步会让所有
// remoteDriver 测试在"拿到客户端后再核一次世代号"那一步上失败。
func (p *fakeProcess) start(t *testing.T, factory driversdk.Factory, meta driversdk.Meta) {
	t.Helper()
	conn := startSDKServer(t, factory, meta)
	cl := newClient(conn)

	p.mu.Lock()
	if p.cl != nil {
		p.cl.fail(ErrProcessDown)
	}
	p.cl = cl
	p.up = true
	p.ep++
	cl.epoch.Store(p.ep)
	p.clients = append(p.clients, cl)
	p.restarts++
	p.mu.Unlock()
}

// kill 模拟进程崩溃：客户端失联，世代号不变（重启时才递增）。
func (p *fakeProcess) kill() {
	p.mu.Lock()
	cl := p.cl
	p.cl = nil
	p.up = false
	p.mu.Unlock()
	if cl != nil {
		cl.fail(ErrProcessDown)
	}
}

func newTestRemote(t *testing.T, stub *ctlDriver, serial bool) (*remoteDriver, *fakeProcess) {
	t.Helper()
	meta := testMeta()
	p := newFakeProcess(t)
	p.start(t, func() driver.Driver { return stub }, meta)
	return newRemoteDriver(p, testProtocol, serial, DefaultTimeouts()), p
}

func testAddrs() []po.DeviceAddress {
	return []po.DeviceAddress{
		{ID: "a1", Name: "p1", DataType: "int16"},
		{ID: "a2", Name: "p2", DataType: "int16"},
	}
}

// ==================== 测试 ====================

func TestRemoteDriverLifecycle(t *testing.T) {
	stub := &ctlDriver{resultCount: -1}
	rd, _ := newTestRemote(t, stub, false)

	if rd.IsConnected() {
		t.Error("未连接时 IsConnected 应为 false")
	}
	if err := rd.Connect(`{"host":"127.0.0.1"}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !rd.IsConnected() {
		t.Error("Connect 后 IsConnected 应为 true")
	}

	addrs := testAddrs()
	got, err := rd.Read(addrs)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != len(addrs) {
		t.Fatalf("结果数 = %d, want %d", len(got), len(addrs))
	}
	for i := range addrs {
		if got[i].DeviceAddressID != addrs[i].ID {
			t.Errorf("结果[%d].DeviceAddressID = %q, want %q（索引必须对齐）", i, got[i].DeviceAddressID, addrs[i].ID)
		}
		if got[i].Value != "val-"+addrs[i].Name {
			t.Errorf("结果[%d].Value = %q, want %q", i, got[i].Value, "val-"+addrs[i].Name)
		}
	}

	if err := rd.Ping(`{}`); err != nil {
		t.Errorf("Ping: %v", err)
	}
	if err := rd.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if rd.IsConnected() {
		t.Error("Close 后 IsConnected 应为 false")
	}
}

func TestRemoteDriverSerialExclusive(t *testing.T) {
	for _, want := range []bool{true, false} {
		rd, _ := newTestRemote(t, &ctlDriver{resultCount: -1}, want)
		var se driver.SerialExclusive = rd
		if got := se.SerialExclusive(); got != want {
			t.Errorf("SerialExclusive() = %v, want %v", got, want)
		}
	}
}

// TestRemoteDriverReadBeforeConnect 未连接时 Read 不得到插件进程里乱打。
func TestRemoteDriverReadBeforeConnect(t *testing.T) {
	stub := &ctlDriver{resultCount: -1}
	rd, _ := newTestRemote(t, stub, false)

	if _, err := rd.Read(testAddrs()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("未连接时 Read 应返回 ErrNotConnected，得到 %v", err)
	}
	if n := stub.readCalls.Load(); n != 0 {
		t.Errorf("未连接时不应调用插件 Read，实际调用 %d 次", n)
	}
}

// TestRemoteDriverReadIndexAlignment 插件返回数量与 addrs 不符时必须报错：
// 采集引擎的 toRecords 依赖按索引一一对应，放过会导致记录错位。
func TestRemoteDriverReadIndexAlignment(t *testing.T) {
	stub := &ctlDriver{resultCount: 1} // 请求 2 个点位但只回 1 个
	rd, _ := newTestRemote(t, stub, false)

	if err := rd.Connect(`{}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_, err := rd.Read(testAddrs())
	if err == nil {
		t.Fatal("结果数量不符应报错")
	}
	if !strings.Contains(err.Error(), "results for") {
		t.Fatalf("错误信息不明确: %v", err)
	}
}

// TestRemoteDriverConnectedFollowsDriverError 是本设计最关键的语义：
// 驱动 Read 报错且自身 IsConnected()==false 时，remoteDriver.IsConnected()
// 必须随之变为 false —— 否则采集引擎永不重连（Read 报错本身不触发重连），
// 表现为永久静默丢数据。
func TestRemoteDriverConnectedFollowsDriverError(t *testing.T) {
	stub := &ctlDriver{resultCount: -1}
	rd, _ := newTestRemote(t, stub, false)

	if err := rd.Connect(`{}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !rd.IsConnected() {
		t.Fatal("前置条件：连接后应为已连接")
	}

	// 模拟链路掉线：驱动自身转为未连接并让 Read 报错。
	stub.set(func(d *ctlDriver) {
		d.connected = false
		d.readErr = errors.New("link down")
	})

	if _, err := rd.Read(testAddrs()); err == nil {
		t.Fatal("驱动报错应向上返回")
	}
	if rd.IsConnected() {
		t.Error("驱动报错且 IsConnected()==false 后，remoteDriver.IsConnected() 必须为 false，否则永不重连")
	}
}

// TestRemoteDriverProcessDeathFlipsConnected 进程崩溃（管道断开）后
// IsConnected() 必须为 false，Read 返回 ErrProcessDown。
func TestRemoteDriverProcessDeathFlipsConnected(t *testing.T) {
	stub := &ctlDriver{resultCount: -1}
	rd, proc := newTestRemote(t, stub, false)

	if err := rd.Connect(`{}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !rd.IsConnected() {
		t.Fatal("前置条件：连接后应为已连接")
	}

	proc.kill()

	if rd.IsConnected() {
		t.Error("进程下线后 IsConnected() 必须为 false")
	}
	if _, err := rd.Read(testAddrs()); !errors.Is(err, ErrProcessDown) {
		t.Errorf("进程下线后 Read 应返回 ErrProcessDown，得到 %v", err)
	}
}

// TestRemoteDriverRPCFailureFlipsConnected 驱动级错误**不**翻状态（帧完好，
// 由应答的 connected 决定）；只有 RPC 层失败才强制置为未连接。
func TestRemoteDriverRPCFailureFlipsConnected(t *testing.T) {
	stub := &ctlDriver{resultCount: -1}
	rd, proc := newTestRemote(t, stub, false)

	if err := rd.Connect(`{}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// 驱动级错误：帧完好，应答里 connected 仍为 true（驱动只是读失败但仍连着）。
	stub.set(func(d *ctlDriver) { d.readErr = errors.New("transient") })
	if _, err := rd.Read(testAddrs()); err == nil {
		t.Fatal("驱动报错应向上返回")
	}
	if !rd.IsConnected() {
		t.Error("驱动级错误但驱动仍报告已连接时，不应翻转为未连接")
	}

	// RPC 层失败：进程被 kill，代理本身坏了。
	proc.kill()
	if rd.IsConnected() {
		t.Error("RPC 层失败后 IsConnected() 必须为 false")
	}
}

// TestRemoteDriverEpochRestartForcesReconnect 插件进程重启后必须强制重连。
//
// 为什么不能只靠"未知会话"错误兜底：插件进程重启后会话 id 从 1 重新分配，
// 陈旧的会话 id 极可能恰好指向新进程里**另一个设备的会话**，
// 于是既不会报错也不会被察觉 —— 会静默读到别的设备的数据。
// 世代号对比是唯一可靠的判定，因此是必需的而非保险。
func TestRemoteDriverEpochRestartForcesReconnect(t *testing.T) {
	stub := &ctlDriver{resultCount: -1}
	meta := testMeta()

	p := newFakeProcess(t)
	p.start(t, func() driver.Driver { return stub }, meta)

	rd := newRemoteDriver(p, testProtocol, false, DefaultTimeouts())
	if err := rd.Connect(`{}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !rd.IsConnected() {
		t.Fatal("前置条件：连接后应为已连接")
	}

	// 模拟进程崩溃后重启：世代号递增。
	p.kill()
	p.start(t, func() driver.Driver { return stub }, meta)

	if rd.IsConnected() {
		t.Fatal("进程重启后 IsConnected() 必须为 false，否则会向新进程发陈旧会话 id")
	}
	// Read 必须拒绝，而不是把陈旧会话 id 发出去。
	if _, err := rd.Read(testAddrs()); err == nil {
		t.Fatal("进程重启后 Read 应被拒绝（陈旧会话作废），不得发往新进程")
	}

	// 重新 Connect 后恢复，且用的是新进程的会话。
	if err := rd.Connect(`{}`); err != nil {
		t.Fatalf("重启后重连失败: %v", err)
	}
	if !rd.IsConnected() {
		t.Fatal("重连后 IsConnected() 应为 true")
	}
	got, err := rd.Read(testAddrs())
	if err != nil {
		t.Fatalf("重连后 Read: %v", err)
	}
	if len(got) != len(testAddrs()) {
		t.Fatalf("重连后结果数 = %d, want %d", len(got), len(testAddrs()))
	}
}

// TestRemoteDriverCloseOnDeadProcessReturnsImmediately
// 采集任务的 Stop() 会逐个驱动调用 Close()，进程已死时绝不能阻塞。
func TestRemoteDriverCloseOnDeadProcessReturnsImmediately(t *testing.T) {
	stub := &ctlDriver{resultCount: -1}
	rd, proc := newTestRemote(t, stub, false)

	if err := rd.Connect(`{}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	proc.kill()

	done := make(chan error, 1)
	go func() { done <- rd.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("进程已死时 Close 应返回 nil，得到 %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("进程已死时 Close 阻塞了，会卡死停机流程")
	}
}

// TestRemoteDriverCloseWithoutConnect 从未连接过的驱动 Close 必须立即返回。
func TestRemoteDriverCloseWithoutConnect(t *testing.T) {
	rd, _ := newTestRemote(t, &ctlDriver{resultCount: -1}, false)

	done := make(chan struct{})
	go func() { _ = rd.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("未连接时 Close 阻塞了")
	}
}

// TestRemoteDriverReconnectReleasesOldSession 重连必须先释放旧会话，
// 否则插件的会话表会随重连次数无界增长。
func TestRemoteDriverReconnectReleasesOldSession(t *testing.T) {
	stub := &ctlDriver{resultCount: -1}
	rd, _ := newTestRemote(t, stub, false)

	if err := rd.Connect(`{}`); err != nil {
		t.Fatalf("Connect 1: %v", err)
	}
	rd.mu.Lock()
	first := rd.session
	rd.mu.Unlock()

	if err := rd.Connect(`{}`); err != nil {
		t.Fatalf("Connect 2: %v", err)
	}
	rd.mu.Lock()
	second := rd.session
	rd.mu.Unlock()

	if first == 0 || second == 0 {
		t.Fatalf("会话 id 不应为 0：first=%d second=%d", first, second)
	}
	if first == second {
		t.Errorf("重连后应分配新会话（旧会话已被释放）：first=%d second=%d", first, second)
	}
}

// TestRemoteDriverAsDriverInterface 固化 remoteDriver 可被 driver 包消费。
func TestRemoteDriverAsDriverInterface(t *testing.T) {
	rd, _ := newTestRemote(t, &ctlDriver{resultCount: -1}, true)
	var _ driver.Driver = rd
	var _ driver.SerialExclusive = rd
}
