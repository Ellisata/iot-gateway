// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"github.com/Ellisata/iot-gateway-sdk/pluginwire"
)

// ==================== 测试替身 ====================

// delayDriver 按构造序号递减延迟，使并发应答天然乱序。
type delayDriver struct {
	delay time.Duration
	tag   string
}

func (d *delayDriver) Connect(string) error { return nil }
func (d *delayDriver) Ping(string) error    { return nil }
func (d *delayDriver) IsConnected() bool    { return true }
func (d *delayDriver) Close() error         { return nil }
func (d *delayDriver) Read(addrs []driver.DeviceAddress) ([]driver.ReadResult, error) {
	time.Sleep(d.delay)
	out := make([]driver.ReadResult, len(addrs))
	for i, a := range addrs {
		out[i] = driver.ReadResult{DeviceAddressID: a.ID, Value: d.tag, DataType: a.DataType}
	}
	return out, nil
}

// testProtocol 本文件全部测试共用的单个协议名。
const testProtocol = "Test.Plugin.Mux"

// testFactories 把单协议工厂包成 SDK 需要的按协议名索引的形式。
func testFactories(mk driversdk.Factory) driversdk.Factories {
	return driversdk.Factories{testProtocol: mk}
}

func testMeta() driversdk.Meta {
	return driversdk.Meta{
		APIVersion: driversdk.APIVersion,
		Protocols:  []driversdk.Protocol{{Name: testProtocol}},
		Scope:      "testplugin_mux",
		Types:      []*driver.DataType{{Name: "int16", Kind: driver.KindInt, Size: 2}},
		TypeMap:    map[string]string{"Short": "int16"},
	}
}

// startSDKServer 起一个真正的插件服务端（完整协议栈），返回客户端侧连接。
func startSDKServer(t *testing.T, factory driversdk.Factory, meta driversdk.Meta) net.Conn {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- driversdk.ServeConn(serverSide, testFactories(factory), meta, driversdk.Capabilities{})
	}()
	t.Cleanup(func() {
		_ = clientSide.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("ServeConn 未在 2s 内返回")
		}
	})
	return clientSide
}

func newTestClient(t *testing.T, rw net.Conn) *client {
	t.Helper()
	c := newClient(rw)
	// 必须 fail 之后再 wait：读协程阻塞在 ReadFrame 上，不先标记下线
	// 就会一直等不到退出。真实路径同样如此——监管杀掉进程后调用 client.fail。
	t.Cleanup(func() {
		c.fail(ErrProcessDown)
		c.wait()
	})
	return c
}

// silentServer 读取请求但永不应答，用于测试超时与进程下线。
func silentServer(t *testing.T) net.Conn {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := pluginwire.ReadFrame(serverSide); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = serverSide.Close()
		<-done
	})
	return clientSide
}

func waitGoroutines(t *testing.T, want int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if runtime.NumGoroutine() <= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("协程数 = %d, 期望回落到 <= %d（协程泄漏）", runtime.NumGoroutine(), want)
}

// ==================== 测试 ====================

// TestCorrelationOutOfOrder 固化"按 id 配对而非按序配对"：
// 先发出的慢请求后应答，两个调用方都必须拿到自己的结果。
func TestCorrelationOutOfOrder(t *testing.T) {
	var n atomic.Int64
	meta := testMeta()
	conn := startSDKServer(t, func() driver.Driver {
		i := n.Add(1)
		// 第一个实例慢 150ms，第二个不等待 → 会话 2 的应答先到。
		if i == 1 {
			return &delayDriver{delay: 150 * time.Millisecond, tag: "first"}
		}
		return &delayDriver{tag: "second"}
	}, meta)
	c := newTestClient(t, conn)

	ctx := context.Background()
	var sess1, sess2 uint64
	var cr driversdk.ConnectResult
	if err := c.call(ctx, pluginwire.MethodConnect, driversdk.ConnectParams{Protocol: testProtocol}, &cr); err != nil {
		t.Fatalf("connect 1: %v", err)
	}
	sess1 = cr.Session
	if err := c.call(ctx, pluginwire.MethodConnect, driversdk.ConnectParams{Protocol: testProtocol}, &cr); err != nil {
		t.Fatalf("connect 2: %v", err)
	}
	sess2 = cr.Session

	addrs := []driversdk.WireAddr{{ID: "a1", Name: "p", DataType: "int16"}}

	var (
		wg         sync.WaitGroup
		val1, val2 string
		err1, err2 error
		started    = make(chan struct{})
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		var r driversdk.ReadReply
		err1 = c.call(ctx, pluginwire.MethodRead, driversdk.ReadParams{Session: sess1, Addrs: addrs}, &r)
		if len(r.Results) > 0 {
			val1 = r.Results[0].Value
		}
	}()
	go func() {
		defer wg.Done()
		<-started
		var r driversdk.ReadReply
		err2 = c.call(ctx, pluginwire.MethodRead, driversdk.ReadParams{Session: sess2, Addrs: addrs}, &r)
		if len(r.Results) > 0 {
			val2 = r.Results[0].Value
		}
	}()
	// 让慢请求先发出，保证应答顺序与请求顺序相反。
	time.Sleep(20 * time.Millisecond)
	close(started)
	wg.Wait()

	if err1 != nil || err2 != nil {
		t.Fatalf("read 错误: %v, %v", err1, err2)
	}
	if val1 != "first" {
		t.Errorf("会话 1（先发出、后应答）拿到 %q, want %q —— 应答未按 id 配对", val1, "first")
	}
	if val2 != "second" {
		t.Errorf("会话 2 拿到 %q, want %q", val2, "second")
	}
}

// TestConcurrentCallsManyGoroutines 高并发下每个应答都精确配对，-race 下验证无竞争。
func TestConcurrentCallsManyGoroutines(t *testing.T) {
	meta := testMeta()
	conn := startSDKServer(t, func() driver.Driver { return &delayDriver{} }, meta)
	c := newTestClient(t, conn)

	const (
		workers = 50
		perCall = 20
	)
	var wg sync.WaitGroup
	var failures atomic.Int64

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			ctx := context.Background()
			for i := 0; i < perCall; i++ {
				var got driversdk.Meta
				if err := c.call(ctx, pluginwire.MethodDescribe, struct{}{}, &got); err != nil {
					t.Errorf("worker %d 调用 %d: %v", w, i, err)
					failures.Add(1)
					return
				}
				if len(got.Protocols) != 1 || got.Protocols[0].Name != testProtocol {
					t.Errorf("worker %d 拿到协议 %+v, want [%s] —— 应答串扰", w, got.Protocols, testProtocol)
					failures.Add(1)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	if failures.Load() != 0 {
		t.Fatalf("%d 个 worker 失败", failures.Load())
	}
}

// TestTimeoutDropsPendingEntry 超时后 pending 表必须清空，
// 否则每个超时调用都会永久泄漏一个通道。
func TestTimeoutDropsPendingEntry(t *testing.T) {
	c := newTestClient(t, silentServer(t))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := c.call(ctx, pluginwire.MethodDescribe, struct{}{}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应超时，得到 %v", err)
	}

	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("超时后 pending 残留 %d 条", n)
	}

	// 超时计入卡死统计。
	if c.timeoutStreak.Load() != 1 {
		t.Errorf("timeoutStreak = %d, want 1", c.timeoutStreak.Load())
	}
}

// TestProcessDownWakesAllWaiters 管道中途关闭：所有在途调用立即拿到
// ErrProcessDown（而不是各自等到超时），且不留下读协程。
func TestProcessDownWakesAllWaiters(t *testing.T) {
	before := runtime.NumGoroutine()

	clientSide, serverSide := net.Pipe()
	c := newClient(clientSide)

	const n = 20
	var (
		wg   sync.WaitGroup
		got  = make([]error, n)
		done = make(chan struct{})
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 用长超时，确保唤醒来自 closed 通道而非 ctx。
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			got[i] = c.call(ctx, pluginwire.MethodDescribe, struct{}{}, nil)
		}(i)
	}

	// 等所有调用都挂到 pending 上再断开。
	time.Sleep(100 * time.Millisecond)
	_ = serverSide.Close()

	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("管道关闭后仍有调用未被唤醒（应立即可返回，而非等超时）")
	}

	for i, err := range got {
		if !errors.Is(err, ErrProcessDown) {
			t.Errorf("调用 %d 错误 = %v, want ErrProcessDown", i, err)
		}
	}

	c.wait()
	waitGoroutines(t, before+1) // +1 容许测试框架自身协程
}

// TestLateResponseIgnoredAndReaderAlive 迟到应答（调用方已超时放弃）
// 必须被静默丢弃，且读协程仍存活、后续调用仍正常。
func TestLateResponseIgnoredAndReaderAlive(t *testing.T) {
	meta := testMeta()
	// 驱动 Read 睡 200ms，让调用方能在应答到达前就超时放弃。
	conn := startSDKServer(t, func() driver.Driver {
		return &delayDriver{delay: 200 * time.Millisecond}
	}, meta)
	c := newTestClient(t, conn)

	var cr driversdk.ConnectResult
	if err := c.call(context.Background(), pluginwire.MethodConnect, driversdk.ConnectParams{Protocol: testProtocol}, &cr); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// 短超时 → 放弃这次调用，服务端稍后才应答。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	err := c.call(ctx, pluginwire.MethodRead, driversdk.ReadParams{
		Session: cr.Session,
		Addrs:   []driversdk.WireAddr{{ID: "a1", Name: "p", DataType: "int16"}},
	}, nil)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应超时，得到 %v", err)
	}

	// 等迟到应答到达并被静默丢弃（投递到无人接收的缓冲通道，不得阻塞读协程）。
	time.Sleep(300 * time.Millisecond)

	// 读协程必须还活着：后续调用正常。
	var got driversdk.Meta
	if err := c.call(context.Background(), pluginwire.MethodDescribe, struct{}{}, &got); err != nil {
		t.Fatalf("迟到应答之后调用失败（读协程可能已死）: %v", err)
	}
	if len(got.Protocols) != 1 || got.Protocols[0].Name != testProtocol {
		t.Fatalf("协议 = %+v, want [%s]", got.Protocols, testProtocol)
	}
}

// TestDriverErrorKeepsResultDecodable 驱动级错误仍须能取到 Result：
// read 的 connected 就靠这条通道回传。
func TestDriverErrorKeepsResultDecodable(t *testing.T) {
	meta := testMeta()
	conn := startSDKServer(t, func() driver.Driver { return &failingReadDriver{} }, meta)
	c := newTestClient(t, conn)

	ctx := context.Background()
	var cr driversdk.ConnectResult
	if err := c.call(ctx, pluginwire.MethodConnect, driversdk.ConnectParams{Protocol: testProtocol}, &cr); err != nil {
		t.Fatalf("connect: %v", err)
	}

	var reply driversdk.ReadReply
	err := c.call(ctx, pluginwire.MethodRead, driversdk.ReadParams{
		Session: cr.Session,
		Addrs:   []driversdk.WireAddr{{ID: "a1", Name: "p", DataType: "int16"}},
	}, &reply)

	var remoteErr *pluginwire.RemoteError
	if !errors.As(err, &remoteErr) {
		t.Fatalf("应为驱动级错误（*RemoteError），得到 %v", err)
	}
	if remoteErr.Message != "boom" {
		t.Errorf("错误信息 = %q, want boom", remoteErr.Message)
	}
	// Result 已被解码填充。
	if reply.Connected {
		t.Error("驱动报错且自身已断开时，应答的 Connected 应为 false")
	}
}

type failingReadDriver struct{ delayDriver }

func (d *failingReadDriver) Read([]driver.DeviceAddress) ([]driver.ReadResult, error) {
	return nil, errors.New("boom")
}
func (d *failingReadDriver) IsConnected() bool { return false }

// TestStuckDetection 固化"插件还活着但卡死"的判定：连续超时且久无成功才成立。
func TestStuckDetection(t *testing.T) {
	c := newTestClient(t, silentServer(t))

	if c.stuck() {
		t.Fatal("初始不应判为卡死")
	}

	for i := 0; i < stuckTimeoutStreak; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_ = c.call(ctx, pluginwire.MethodDescribe, struct{}{}, nil)
		cancel()
	}
	if c.stuck() {
		t.Fatal("近期仍有成功调用（进程刚起）时不应判为卡死")
	}

	// 把上次成功时间推到阈值之前，模拟长期无成功调用。
	c.lastSuccess.Store(time.Now().Add(-stuckAfter - time.Second).UnixNano())
	if !c.stuck() {
		t.Fatal("连续超时且长期无成功应判为卡死")
	}

	// 任一成功调用即清零。
	c.noteSuccess()
	if c.stuck() {
		t.Fatal("成功调用后不应再判为卡死")
	}
}

// ==================== 取消 vs 超时（卡死判定的输入） ====================

// hangDriver 的 Ping 一直不返回，用来稳定地制造"调用被放弃"的局面。
//
// 刻意用 Ping 而不是 Read：**它不带会话**，与扫描的 probe 是同一条路径
// （都走无会话分支），因此这里验的就是扫描取消所走的那段代码。
// Read 反而需要先 connect 出一个真会话，与本用例无关。
//
// release 由用例关闭以放行阻塞中的调用，避免留下泄漏的 goroutine。
type hangDriver struct {
	entered chan struct{}
	release chan struct{}
}

func (d *hangDriver) Connect(string) error { return nil }
func (d *hangDriver) IsConnected() bool    { return true }
func (d *hangDriver) Close() error         { return nil }
func (d *hangDriver) Read([]driver.DeviceAddress) ([]driver.ReadResult, error) {
	return nil, nil
}

func (d *hangDriver) Ping(string) error {
	select {
	case d.entered <- struct{}{}:
	default:
	}
	<-d.release
	return nil
}

// TestCancelDoesNotCountAsTimeout **本功能最要紧的一条回归**。
//
// client.call 的 ctx.Done 分支原先无条件 noteTimeout()。而"取消一个扫描任务"
// 会在同一瞬间掐掉所有在飞的 probe，于是一次取消就能顶满 stuckStreak；
// 又因为 stuck() 还要求"很久没有成功调用"—— 在一台**还没建设备**的新网关
// （运维第一件事就是扫描）上，这个插件进程除 probe 外没有任何成功调用 ——
// 看门狗随即杀掉进程，该插件下所有协议、所有设备一起断。
//
// 这条用例把阈值调到极小（正是 setStuckThreshold 这个测试缝的用途），
// 断言：纯取消不推进 streak，而真正的超时仍然推进。
func TestCancelDoesNotCountAsTimeout(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	clientSide, serverSide := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- driversdk.ServeConn(serverSide,
			testFactories(func() driver.Driver {
				return &hangDriver{entered: entered, release: release}
			}),
			testMeta(), driversdk.Capabilities{})
	}()
	t.Cleanup(func() { close(release); _ = clientSide.Close(); <-done })

	c := newClient(clientSide)
	t.Cleanup(func() { c.fail(errors.New("test over")) })

	// 阈值调到 1：任何一次计入的失败都会立刻让 stuck() 为真。
	c.setStuckThreshold(1, time.Nanosecond)

	startCall := func(ctx context.Context) chan error {
		res := make(chan error, 1)
		go func() {
			res <- c.call(ctx, pluginwire.MethodPing,
				driversdk.PingParams{Protocol: testProtocol}, nil)
		}()
		return res
	}

	// 1) 主动取消：不计入 streak。
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		res := startCall(ctx)
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("驱动没有被调用")
		}
		cancel()
		select {
		case err := <-res:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("第 %d 次取消返回 %v，期望 context.Canceled", i+1, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("取消后调用没有立即返回")
		}
	}
	if c.stuck() {
		t.Fatal("连续取消被判成了插件卡死 —— 一次扫描任务的取消会把该插件的全部协议一起杀掉")
	}
	if n := c.timeoutStreak.Load(); n != 0 {
		t.Fatalf("取消后 streak = %d，期望 0", n)
	}

	// 2) 真正的超时：仍然要计入，否则卡死的插件永远不被重启
	//   （IsConnected 恒 true、Read 永远超时，而采集引擎只在
	//   IsConnected()==false 时重连 —— 没有它就是一个无恢复的活锁）。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	res := startCall(ctx)
	select {
	case err := <-res:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("超时调用返回 %v，期望 DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时后调用没有返回")
	}
	if n := c.timeoutStreak.Load(); n != 1 {
		t.Fatalf("超时后 streak = %d，期望 1 —— 真超时必须计入", n)
	}
	if !c.stuck() {
		t.Fatal("达到阈值且无成功调用时应判为卡死")
	}
}

// TestSuccessfulCallResetsStreak 一次成功调用要清掉 streak（既有语义，顺带钉住）。
func TestSuccessfulCallResetsStreak(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- driversdk.ServeConn(serverSide,
			testFactories(func() driver.Driver { return &delayDriver{delay: 0, tag: "ok"} }),
			testMeta(), driversdk.Capabilities{})
	}()
	t.Cleanup(func() { _ = clientSide.Close(); <-done })

	c := newClient(clientSide)
	t.Cleanup(func() { c.fail(errors.New("test over")) })
	c.noteTimeout()
	c.noteTimeout()
	if c.timeoutStreak.Load() != 2 {
		t.Fatalf("streak = %d，期望 2", c.timeoutStreak.Load())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var res driversdk.ConnectResult
	if err := c.call(ctx, pluginwire.MethodConnect,
		driversdk.ConnectParams{Protocol: testProtocol}, &res); err != nil {
		t.Fatalf("connect 失败: %v", err)
	}
	if n := c.timeoutStreak.Load(); n != 0 {
		t.Fatalf("成功调用后 streak = %d，期望 0", n)
	}
}
