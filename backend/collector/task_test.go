// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
	"iot-gateway/workerPool"
)

// mockDriver 模拟一个 Read 会阻塞片刻的协议驱动，用于制造"采集进行中调用 Stop"的场景。
type mockDriver struct {
	readDelay time.Duration
	closed    bool
}

func (d *mockDriver) Connect(protocolJSON string) error { return nil }
func (d *mockDriver) Ping(protocolJSON string) error    { return nil }
func (d *mockDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	if d.readDelay > 0 {
		time.Sleep(d.readDelay)
	}
	results := make([]driver.ReadResult, 0, len(addrs))
	for _, a := range addrs {
		results = append(results, driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           "1",
			DataType:        a.DataType,
			Quality:         192,
		})
	}
	return results, nil
}
func (d *mockDriver) IsConnected() bool { return true }
func (d *mockDriver) Close() error      { d.closed = true; return nil }

// mockSink 静默消费采集结果
type mockSink struct{}

func (mockSink) PushRecords(records []CollectedRecord) {}

// newTestTask 构建一个含单个频率分组的 gatewayTask，采集协程周期性阻塞在 Read 上。
func newTestTask(readDelay time.Duration) (*gatewayTask, *mockDriver) {
	drv := &mockDriver{readDelay: readDelay}
	addr := po.DeviceAddress{ID: "addr-1", DeviceID: "dev-1", Name: "40001", DataType: "int16", Status: 1}

	task := &gatewayTask{
		devices:     []po.Device{{ID: "dev-1", Name: "dev1", Status: 1}},
		drivers:     map[string]driver.Driver{"dev-1": drv},
		addrNameMap: map[string]map[string]string{"dev-1": {"addr-1": "A1"}},
		sink:        mockSink{},
		pool:        workerPool.NewWorkerPool(2, 16),
		quit:        make(chan struct{}),
	}
	group := &frequencyGroup{
		interval:   50 * time.Millisecond,
		addrGroups: map[string][]po.DeviceAddress{"dev-1": {addr}},
		task:       task,
		inflight:   make(map[string]bool),
		quit:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	task.groups = []*frequencyGroup{group}
	return task, drv
}

// TestGatewayTaskStopNoDeadlock 回归测试：热刷新/停采时，Stop 可能在采集协程
// 正阻塞于驱动 Read 时被调用。Stop 若持有 t.mu 等待 g.done 会与 doPoll 的取锁互相
// 等待而永久死锁，导致旧任务（含已停用地址）持续采集。修复后 Stop 必须在
// 有限时间内返回。
func TestGatewayTaskStopNoDeadlock(t *testing.T) {
	task, _ := newTestTask(200 * time.Millisecond)
	task.Start()

	// 等一次轮询进入阻塞 Read 之后再调用 Stop，命中"采集进行中停止"。
	time.Sleep(120 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		task.Stop()
		close(done)
	}()

	select {
	case <-done:
		// 成功：Stop 在采集协程退出后返回
	case <-time.After(3 * time.Second):
		t.Fatal("task.Stop() deadlocked: poll goroutine blocked on task mutex and never exited")
	}
}

// TestGatewayTaskStopIdle 正常路径：任务空闲时 Stop 应立即返回，且驱动被关闭。
func TestGatewayTaskStopIdle(t *testing.T) {
	task, drv := newTestTask(0)
	task.Start()
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		task.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("task.Stop() did not return on idle task")
	}

	if !drv.closed {
		t.Fatal("driver Close() should have been called after Stop")
	}
}

// batchMockDriver 记录每次 Read 的调用次数与点位数量。
type batchMockDriver struct {
	callCount int
	callSizes []int // 每次 Read 收到的点位数量
}

func (d *batchMockDriver) Connect(protocolJSON string) error { return nil }
func (d *batchMockDriver) Ping(protocolJSON string) error    { return nil }
func (d *batchMockDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	d.callCount++
	d.callSizes = append(d.callSizes, len(addrs))
	results := make([]driver.ReadResult, 0, len(addrs))
	for _, a := range addrs {
		results = append(results, driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           "1",
			DataType:        a.DataType,
			Quality:         192,
		})
	}
	return results, nil
}
func (d *batchMockDriver) IsConnected() bool { return true }
func (d *batchMockDriver) Close() error      { return nil }

var errBatchReadFailed = fmt.Errorf("mock: read failed")

func mkBatchedAddrs(n int) []po.DeviceAddress {
	addrs := make([]po.DeviceAddress, n)
	for i := range addrs {
		addrs[i] = po.DeviceAddress{
			ID:       strconv.Itoa(i),
			DeviceID: "dev-1",
			Name:     "DB1.DBW0",
			DataType: "word",
			Status:   1,
		}
	}
	return addrs
}

func TestForEachBatch_Single(t *testing.T) {
	addrs := mkBatchedAddrs(maxPointsPerRead)

	var batches [][]po.DeviceAddress
	err := forEachBatch(addrs, func(batch []po.DeviceAddress) error {
		batches = append(batches, batch)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 {
		t.Fatalf("fn called %d times, want 1", len(batches))
	}
	if len(batches[0]) != len(addrs) {
		t.Fatalf("batch size = %d, want %d", len(batches[0]), len(addrs))
	}
}

func TestForEachBatch_Multi(t *testing.T) {
	n := maxPointsPerRead*3 + 17
	addrs := mkBatchedAddrs(n)

	var sizes []int
	var order []string
	err := forEachBatch(addrs, func(batch []po.DeviceAddress) error {
		sizes = append(sizes, len(batch))
		for _, a := range batch {
			order = append(order, a.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := (n + maxPointsPerRead - 1) / maxPointsPerRead
	if len(sizes) != wantCalls {
		t.Fatalf("fn called %d times, want %d", len(sizes), wantCalls)
	}
	// 每批大小：前 3 批满额，最后一批 17
	for i, size := range sizes {
		wantSize := maxPointsPerRead
		if i == len(sizes)-1 {
			wantSize = n - maxPointsPerRead*(len(sizes)-1)
		}
		if size != wantSize {
			t.Errorf("batch %d size = %d, want %d", i, size, wantSize)
		}
	}
	// 顺序完整
	for i, id := range order {
		if id != strconv.Itoa(i) {
			t.Fatalf("order[%d] = %s, want %d (order must be preserved)", i, id, i)
		}
	}
}

func TestForEachBatch_Error(t *testing.T) {
	addrs := mkBatchedAddrs(maxPointsPerRead*2 + 1)

	calls := 0
	err := forEachBatch(addrs, func(batch []po.DeviceAddress) error {
		calls++
		if calls == 2 {
			return errBatchReadFailed
		}
		return nil
	})
	if err == nil {
		t.Fatal("want error when a batch fn fails")
	}
	if calls != 2 {
		t.Fatalf("fn called %d times, want 2 (stop at failing batch)", calls)
	}
}

func TestForEachBatch_Empty(t *testing.T) {
	calls := 0
	err := forEachBatch(nil, func(batch []po.DeviceAddress) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("fn called %d times, want 0", calls)
	}
}

// failingDriver 模拟一台始终连不上的设备：IsConnected() 恒 false，Connect 恒失败。
type failingDriver struct{}

func (d *failingDriver) Connect(protocolJSON string) error { return fmt.Errorf("mock: connect failed") }
func (d *failingDriver) Ping(protocolJSON string) error    { return nil }
func (d *failingDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	return nil, fmt.Errorf("mock: not connected")
}
func (d *failingDriver) IsConnected() bool { return false }
func (d *failingDriver) Close() error      { return nil }

// TestReconnectBackoff_SkipsWithinWindow 验证断线重连退避：首次失败记录 errorCount，
// 退避窗口内不再发起连接尝试（errorCount 不重复累加），退避结束后恢复重试。
func TestReconnectBackoff_SkipsWithinWindow(t *testing.T) {
	drv := &failingDriver{}
	addr := po.DeviceAddress{ID: "addr-1", DeviceID: "dev-1", Name: "40001", DataType: "int16", Status: 1}
	task := &gatewayTask{
		devices:     []po.Device{{ID: "dev-1", Name: "dev1", Status: 1}},
		drivers:     map[string]driver.Driver{"dev-1": drv},
		addrNameMap: map[string]map[string]string{"dev-1": {"addr-1": "A1"}},
		sink:        mockSink{},
		quit:        make(chan struct{}),
	}
	group := &frequencyGroup{
		addrGroups: map[string][]po.DeviceAddress{"dev-1": {addr}},
		task:       task,
	}

	// 首次轮询：重连失败，errorCount=1，进入退避
	group.doPollDevice("dev-1", []po.DeviceAddress{addr})
	if task.errorCount != 1 {
		t.Fatalf("errorCount after first fail = %d, want 1", task.errorCount)
	}

	// 退避窗口内第二次轮询：跳过重连，errorCount 不累加
	group.doPollDevice("dev-1", []po.DeviceAddress{addr})
	if task.errorCount != 1 {
		t.Fatalf("errorCount within backoff window = %d, want still 1", task.errorCount)
	}

	// 手动把上次尝试时间拨回退避窗口之外，应恢复重试
	task.recMu.Lock()
	task.reconnect["dev-1"] = reconnectState{lastAttempt: time.Now().Add(-2 * time.Second), delay: 0}
	task.recMu.Unlock()
	group.doPollDevice("dev-1", []po.DeviceAddress{addr})
	if task.errorCount != 2 {
		t.Fatalf("errorCount after backoff expired = %d, want 2", task.errorCount)
	}
}

// recordingSink 记录每次 PushRecords 收到的记录数，用于验证流式分批推送。
type recordingSink struct {
	mu      sync.Mutex
	pushes  []int
	records []CollectedRecord
}

func (s *recordingSink) PushRecords(records []CollectedRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pushes = append(s.pushes, len(records))
	s.records = append(s.records, records...)
}

// blockingSink 首次 PushRecords 会阻塞在 release 通道上，用于验证设备锁是否跨推送阶段持锁。
// unblock 幂等，可安全用于清理路径。
type blockingSink struct {
	releaseOnce sync.Once
	entered     chan struct{} // 首次进入 PushRecords 时关闭（此刻该轮 poll 已释放/仍持设备锁）
	release     chan struct{} // 关闭后放行阻塞的 PushRecords
}

func newBlockingSink() *blockingSink {
	return &blockingSink{entered: make(chan struct{}), release: make(chan struct{})}
}

func (s *blockingSink) PushRecords(records []CollectedRecord) {
	close(s.entered)
	<-s.release
}

func (s *blockingSink) unblock() { s.releaseOnce.Do(func() { close(s.release) }) }

// exclusiveDriver 模拟独占串行总线驱动（如 Modbus RTU），整轮轮询必须串行。
type exclusiveDriver struct {
	*mockDriver
}

func (d *exclusiveDriver) SerialExclusive() bool { return true }

// newTaskWithDriverAndSink 构建含单设备单频率分组的 gatewayTask（不启动轮询），
// 供 pollDevice 同步调用路径的测试使用。
func newTaskWithDriverAndSink(drv driver.Driver, sink RecordSink, addrs []po.DeviceAddress) (*gatewayTask, *frequencyGroup) {
	task := &gatewayTask{
		devices:     []po.Device{{ID: "dev-1", Name: "dev1", Status: 1}},
		drivers:     map[string]driver.Driver{"dev-1": drv},
		addrNameMap: map[string]map[string]string{"dev-1": {"addr-1": "A1"}},
		sink:        sink,
		pool:        workerPool.NewWorkerPool(2, 16),
		quit:        make(chan struct{}),
	}
	group := &frequencyGroup{
		addrGroups: map[string][]po.DeviceAddress{"dev-1": addrs},
		task:       task,
		inflight:   make(map[string]bool),
		quit:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	task.groups = []*frequencyGroup{group}
	return task, group
}

// TestPollDeviceConcurrent_LockNotHeldAcrossPush 验证线程安全驱动（Modbus TCP/S7）的
// 锁粒度收敛到单次 Read：同设备慢频率分组阻塞在推送阶段时，不应占用设备锁，
// 快频率分组的轮询可并行进入（否则快点位实际周期会被慢分组拖长）。
func TestPollDeviceConcurrent_LockNotHeldAcrossPush(t *testing.T) {
	addr := po.DeviceAddress{ID: "addr-1", DeviceID: "dev-1", Name: "40001", DataType: "int16", Status: 1}
	sink := newBlockingSink()
	task, group := newTaskWithDriverAndSink(&mockDriver{}, sink, []po.DeviceAddress{addr})

	done := make(chan struct{})
	go func() {
		group.pollDevice("dev-1", []po.DeviceAddress{addr})
		close(done)
	}()

	// 等 poll 进入推送阶段（此刻应已释放设备锁）
	select {
	case <-sink.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("poll did not reach push phase")
	}

	// 非独占驱动不跨推送持锁：应能立即获取设备锁
	dm := task.deviceLock("dev-1")
	got := make(chan struct{})
	go func() { dm.Lock(); close(got); dm.Unlock() }()
	select {
	case <-got:
		// pass：推送期间设备锁可用
	case <-time.After(500 * time.Millisecond):
		sink.unblock()
		<-done
		t.Fatal("device lock held across push for non-exclusive driver: slow group blocks fast group")
	}

	sink.unblock()
	<-done
}

// TestPollDeviceExclusive_HoldsLockAcrossPush 验证独占串行总线驱动（Modbus RTU）
// 整轮持锁：慢频率分组阻塞在推送阶段时，设备锁仍被占用，快频率分组不能并发进入。
func TestPollDeviceExclusive_HoldsLockAcrossPush(t *testing.T) {
	addr := po.DeviceAddress{ID: "addr-1", DeviceID: "dev-1", Name: "40001", DataType: "int16", Status: 1}
	sink := newBlockingSink()
	task, group := newTaskWithDriverAndSink(&exclusiveDriver{mockDriver: &mockDriver{}}, sink, []po.DeviceAddress{addr})

	done := make(chan struct{})
	go func() {
		group.pollDevice("dev-1", []po.DeviceAddress{addr})
		close(done)
	}()

	select {
	case <-sink.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("poll did not reach push phase")
	}

	// 独占驱动整轮持锁：推送阻塞期间应获取不到设备锁
	dm := task.deviceLock("dev-1")
	got := make(chan struct{})
	go func() { dm.Lock(); close(got); dm.Unlock() }()
	select {
	case <-got:
		sink.unblock()
		<-done
		t.Fatal("device lock should be held across push for exclusive driver (RTU)")
	case <-time.After(200 * time.Millisecond):
		// 期望锁仍被占用
	}

	// 放行后 poll 结束，锁应被释放，占锁 goroutine 能获取并释放
	sink.unblock()
	<-done
	select {
	case <-got:
		// pass：poll 结束后锁已释放
	case <-time.After(2 * time.Second):
		t.Fatal("device lock not released after poll finished")
	}
}

// TestDoPollStreamsBatches 验证 doPoll 对大批量点位按批流式处理：
// 每批读取后立即推送，而不是整台设备攒齐后再推。
func TestDoPollStreamsBatches(t *testing.T) {
	drv := &batchMockDriver{}
	n := maxPointsPerRead*2 + 5
	addrs := mkBatchedAddrs(n)

	sink := &recordingSink{}
	task := &gatewayTask{
		devices:     []po.Device{{ID: "dev-1", Name: "dev1", Status: 1}},
		drivers:     map[string]driver.Driver{"dev-1": drv},
		addrNameMap: map[string]map[string]string{"dev-1": {}},
		sink:        sink,
		quit:        make(chan struct{}),
	}
	group := &frequencyGroup{
		interval:   time.Hour, // 仅用于构造，不会触发轮询
		addrGroups: map[string][]po.DeviceAddress{"dev-1": addrs},
		task:       task,
		quit:       make(chan struct{}),
		done:       make(chan struct{}),
	}

	group.doPoll()

	// 期望分 3 次推送：[2000, 2000, 5]，总量完整
	wantPushes := (n + maxPointsPerRead - 1) / maxPointsPerRead
	if len(sink.pushes) != wantPushes {
		t.Fatalf("pushes = %d, want %d", len(sink.pushes), wantPushes)
	}
	for i, size := range sink.pushes {
		if size > maxPointsPerRead {
			t.Errorf("push %d size = %d, want <= %d", i, size, maxPointsPerRead)
		}
	}
	if len(sink.records) != n {
		t.Fatalf("total records = %d, want %d", len(sink.records), n)
	}
}
