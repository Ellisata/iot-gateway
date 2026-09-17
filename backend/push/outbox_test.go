// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/model/po"
)

// connStub 固定返回指定连通状态的 Connectivity，
// 用于把入队路由（直投队列 / 落盘 / 丢最旧）与真实通道解耦。
type connStub bool

func (c connStub) Connected() bool { return bool(c) }

// newTestOutbox 构造带 SQLite 断网缓存的 Outbox 并启动（不运行 worker 组）。
// 返回的 Outbox 由 closeOutbox 收尾。
func newTestOutbox(t *testing.T, conn Connectivity, queueSize int) (*Outbox, *SqliteSpool) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB failed: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&po.PushOutbox{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}

	o := NewOutbox(OutboxConfig{
		Tag:          "test",
		ID:           "c1",
		Name:         "test",
		DB:           db,
		SpoolEnabled: true,
		SpoolCap:     100,
		Conn:         conn,
		QueueSize:    queueSize,
	})
	o.Start()

	spool, ok := o.Spool().(*SqliteSpool)
	if !ok {
		t.Fatalf("spool type = %T, want *SqliteSpool", o.Spool())
	}
	return o, spool
}

// closeOutbox 按 Stop 的顺序收尾：本测试不运行 worker 组，故 Close 后直接 Wait。
func closeOutbox(o *Outbox) {
	o.Close()
	o.Wait()
}

// waitSpoolCount 轮询等待本地缓存批次数量达到期望值（落盘协程异步写入）
func waitSpoolCount(t *testing.T, spool *SqliteSpool, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, err := spool.Count(); err == nil && n == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	n, _ := spool.Count()
	t.Fatalf("spool count = %d, want %d", n, want)
}

// ==================== 入队路由 ====================

func TestOutboxEnqueueDropOldestWhenSpoolDisabled(t *testing.T) {
	o := NewOutbox(OutboxConfig{Tag: "test", ID: "c", Name: "c", Conn: connStub(true), QueueSize: 2})
	o.Start()
	defer closeOutbox(o)

	o.Enqueue(PushBatch{DeviceID: "a"})
	o.Enqueue(PushBatch{DeviceID: "b"})
	// 队列已满且未启用缓存：丢弃最旧 "a"，保留最新
	o.Enqueue(PushBatch{DeviceID: "c"})

	if got := <-o.outbox; got.DeviceID != "b" {
		t.Errorf("after drop-oldest, first = %s, want b", got.DeviceID)
	}
	if got := <-o.outbox; got.DeviceID != "c" {
		t.Errorf("after drop-oldest, second = %s, want c", got.DeviceID)
	}
	if got := o.Dropped(); got != 1 {
		t.Errorf("dropped = %d, want 1", got)
	}
	if o.SpoolEnabled() {
		t.Error("spool should be disabled without SpoolEnabled")
	}
}

func TestOutboxEnqueueSpoolWhenDisconnected(t *testing.T) {
	o, spool := newTestOutbox(t, connStub(false), 4)
	defer closeOutbox(o)

	// 断连时入队应直接落盘，不进内存队列
	o.Enqueue(PushBatch{DeviceID: "d1", CollectedAt: "t1"})
	o.Enqueue(PushBatch{DeviceID: "d1", CollectedAt: "t2"})

	waitSpoolCount(t, spool, 2)
	if got := len(o.outbox); got != 0 {
		t.Errorf("queue depth = %d, want 0 (disconnected → spool)", got)
	}
}

func TestOutboxEnqueueSpillsToSpoolWhenQueueFull(t *testing.T) {
	o, spool := newTestOutbox(t, connStub(true), 2)
	defer closeOutbox(o)

	o.Enqueue(PushBatch{DeviceID: "a"})
	o.Enqueue(PushBatch{DeviceID: "b"}) // 内存队列已满
	o.Enqueue(PushBatch{DeviceID: "c"}) // 溢写本地缓存而非丢最旧

	waitSpoolCount(t, spool, 1)
	if got := len(o.outbox); got != 2 {
		t.Errorf("queue depth = %d, want 2", got)
	}
	if got := o.Dropped(); got != 0 {
		t.Errorf("dropped = %d, want 0 (spill to spool)", got)
	}
}

func TestOutboxSpoolEnqueueNoopWhenDisabled(t *testing.T) {
	// 未启用缓存时转投必须是 no-op：既不阻塞也不虚增丢弃计数
	o := NewOutbox(OutboxConfig{Tag: "test", ID: "c", Name: "c", Conn: connStub(false)})
	o.SpoolEnqueue(PushBatch{DeviceID: "a"})

	if got := o.Dropped(); got != 0 {
		t.Errorf("dropped = %d, want 0", got)
	}
}

// ==================== 补发唤醒 ====================

func TestOutboxWriteSignalsDrain(t *testing.T) {
	// 落盘（含断连直落）应 ping 补发唤醒，事件驱动补发无需轮询
	o, _ := newTestOutbox(t, connStub(false), 4)
	defer closeOutbox(o)

	o.Enqueue(PushBatch{DeviceID: "d1", CollectedAt: "t1"})

	// 兜底 timer 是 spoolDrainBackstop(10s)，2s 内被唤醒即证明是落盘 ping 而非兜底
	done := make(chan bool, 1)
	go func() { done <- o.WaitDrain() }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("WaitDrain returned false on a running outbox")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("spool write did not signal drain")
	}
}

func TestOutboxWaitDrainWakeSemantics(t *testing.T) {
	o, _ := newTestOutbox(t, connStub(false), 4)
	defer o.Wait()

	// ping 立即唤醒等待
	o.Notify()
	if !o.WaitDrain() {
		t.Fatal("WaitDrain returned false after signal")
	}

	// quit 关闭后立即返回 false（停止信号）
	o.Close()
	if o.WaitDrain() {
		t.Fatal("WaitDrain returned true after quit")
	}
}

func TestOutboxNotifyWithoutSpoolDoesNotBlock(t *testing.T) {
	// 未启用缓存（drainNotify 为 nil）时 Notify 必须 no-op，不得阻塞
	o := NewOutbox(OutboxConfig{Tag: "test", ID: "c", Name: "c", Conn: connStub(false)})
	o.Start()
	defer closeOutbox(o)

	o.Notify() // 若阻塞则测试超时
	o.Notify()
}

// ==================== 停服收尾 ====================

func TestOutboxWaitReapsBatchesOffloadedAfterQuit(t *testing.T) {
	// 回归：通道停服时 worker 组还会把 outbox 中写失败的批次转投缓存
	//（见各通道 writeLoop 的 quit 分支）。落盘协程若在 quit 后立即收工返回，
	// 这些批次会留在内存通道里随进程退出丢失 —— 故 Wait 必须等 worker 组退出。
	o, spool := newTestOutbox(t, connStub(true), 4)

	o.Close() // 模拟通道停止：quit 关闭，落盘协程进入收尾等待

	// 模拟 worker 组停服收尾时转投的批次
	o.SpoolEnqueue(PushBatch{DeviceID: "d1", CollectedAt: "late"})

	o.Wait() // 必须在 worker 组退出后才收工

	n, err := spool.Count()
	if err != nil {
		t.Fatalf("spool count failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("spool count = %d, want 1 (在途批次必须落盘)", n)
	}
}

func TestOutboxStopIsIdempotent(t *testing.T) {
	o, _ := newTestOutbox(t, connStub(true), 4)

	o.Close()
	o.Close() // 重复停止不得 panic
	o.Wait()
	o.Wait()
}

// ==================== 状态快照 ====================

func TestOutboxFillStatus(t *testing.T) {
	o, _ := newTestOutbox(t, connStub(false), 4)
	defer closeOutbox(o)

	o.Enqueue(PushBatch{DeviceID: "d1", CollectedAt: "t1"}) // 断连 → 落盘
	o.MarkPublished(3)
	o.Drop(2)
	o.SetLastErr("boom")
	waitSpoolCount(t, o.Spool().(*SqliteSpool), 1)

	vo := ChannelStatusVO{Type: "test", Broker: "b", Topic: "t"}
	o.FillStatus(&vo)

	if vo.ID != "c1" || vo.Name != "test" {
		t.Errorf("id/name = %q/%q, want c1/test", vo.ID, vo.Name)
	}
	if !vo.Running {
		t.Error("running = false, want true")
	}
	if vo.Connected {
		t.Error("connected = true, want false")
	}
	if vo.SpoolDepth != 1 {
		t.Errorf("spoolDepth = %d, want 1", vo.SpoolDepth)
	}
	if vo.PublishCount != 3 || vo.DroppedCount != 2 {
		t.Errorf("counts = %d/%d, want 3/2", vo.PublishCount, vo.DroppedCount)
	}
	if vo.LastErr != "boom" {
		t.Errorf("lastErr = %q, want boom", vo.LastErr)
	}
	if vo.LastPublish == "" || vo.LastSuccess == "" {
		t.Error("lastPublish/lastSuccess should be set after MarkPublished")
	}
}
