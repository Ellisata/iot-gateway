// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"iot-gateway/collector"
)

// fakeAgg 记录已聚合批次的负载构造器：Payload 为设备 ID 的拼接，便于断言分组结果。
// conflictOn 非空时模拟 tdengine 的子表冲突 —— 同一设备再次出现在**非空**负载中即拒绝。
type fakeAgg struct {
	conflictOn string
	batches    []PushBatch
}

func (a *fakeAgg) Append(b PushBatch) bool {
	if a.conflictOn != "" && b.DeviceID == a.conflictOn && len(a.batches) > 0 {
		return false
	}
	a.batches = append(a.batches, b)
	return true
}

func (a *fakeAgg) Rows() int {
	n := 0
	for _, b := range a.batches {
		n += len(b.Records)
	}
	return n
}

func (a *fakeAgg) Empty() bool { return len(a.batches) == 0 }

func (a *fakeAgg) Payload() string {
	ids := make([]string, 0, len(a.batches))
	for _, b := range a.batches {
		ids = append(ids, b.DeviceID)
	}
	return strings.Join(ids, ",")
}

// sink 记录每次下发的负载，fail 置位后一律返回失败。
type sink struct {
	mu       sync.Mutex
	payloads []string
	fail     bool
}

func (s *sink) write(payload string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return false
	}
	s.payloads = append(s.payloads, payload)
	return true
}

func (s *sink) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.payloads...)
}

// batch 造一个含 n 条记录的设备批次。
func batch(dev string, n int) PushBatch {
	recs := make([]collector.CollectedRecord, n)
	for i := range recs {
		recs[i] = collector.CollectedRecord{
			DeviceID:        dev,
			DeviceAddressID: fmt.Sprintf("a%d", i),
			Value:           "1",
			Quality:         192,
		}
	}
	return PushBatch{DeviceID: dev, CollectedAt: "t", Records: recs}
}

// runWriter 在后台运行写协程，返回的 stop 按 Stop 的顺序收尾。
func runWriter(o *Outbox, w *AggregateWriter) (stop func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(o)
	}()
	return func() {
		o.Close()
		<-done
		o.Wait()
	}
}

// waitFor 轮询等待条件成立。
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// ==================== 攒批触发 ====================

func TestAggregateWriterFlushesAtMaxRows(t *testing.T) {
	o, _ := newTestOutbox(t, connStub(true), 16)
	s := &sink{}
	// Interval 设得极长：只由行数触发，排除时间窗干扰
	w := &AggregateWriter{
		New:      func() Aggregator { return &fakeAgg{} },
		Write:    s.write,
		MaxRows:  3,
		Interval: time.Hour,
	}
	stop := runWriter(o, w)
	defer stop()

	for _, dev := range []string{"d1", "d2", "d3"} {
		o.Enqueue(batch(dev, 1))
	}

	waitFor(t, func() bool { return len(s.got()) == 1 }, "满窗刷出")
	if got := s.got()[0]; got != "d1,d2,d3" {
		t.Fatalf("payload = %q, want d1,d2,d3", got)
	}
}

func TestAggregateWriterFlushesOnInterval(t *testing.T) {
	o, _ := newTestOutbox(t, connStub(true), 16)
	s := &sink{}
	// 行数上限设得极高：只由时间窗触发
	w := &AggregateWriter{
		New:      func() Aggregator { return &fakeAgg{} },
		Write:    s.write,
		MaxRows:  1000,
		Interval: 20 * time.Millisecond,
	}
	stop := runWriter(o, w)
	defer stop()

	o.Enqueue(batch("d1", 1))
	waitFor(t, func() bool { return len(s.got()) == 1 }, "时间窗刷出")
}

func TestAggregateWriterCountsRecordsNotBatches(t *testing.T) {
	// 行数上限按记录数而非批次数计：一批 3 条即达上限，不得攒到 3 批才刷
	o, _ := newTestOutbox(t, connStub(true), 16)
	s := &sink{}
	w := &AggregateWriter{
		New:      func() Aggregator { return &fakeAgg{} },
		Write:    s.write,
		MaxRows:  3,
		Interval: time.Hour,
	}
	stop := runWriter(o, w)
	defer stop()

	o.Enqueue(batch("d1", 3))
	waitFor(t, func() bool { return len(s.got()) == 1 }, "按记录数满窗刷出")
}

// ==================== 冲突切分 ====================

func TestAggregateWriterSplitsOnConflict(t *testing.T) {
	// 批间冲突（tdengine 子表同名）时须先刷出当前负载再用同一批次重试，
	// 否则该批次会被批内去重丢弃。
	o, _ := newTestOutbox(t, connStub(true), 16)
	s := &sink{}
	w := &AggregateWriter{
		New:      func() Aggregator { return &fakeAgg{conflictOn: "d1"} },
		Write:    s.write,
		MaxRows:  1000,
		Interval: time.Hour,
	}
	stop := runWriter(o, w)

	o.Enqueue(batch("d1", 1))
	o.Enqueue(batch("d2", 1))
	o.Enqueue(batch("d1", 1)) // 与已聚合负载冲突 → 先刷出 d1,d2 再追加

	waitFor(t, func() bool { return len(s.got()) == 1 }, "冲突时刷出前一窗")
	if got := s.got()[0]; got != "d1,d2" {
		t.Fatalf("first payload = %q, want d1,d2", got)
	}

	stop() // 收尾刷出第二窗
	if got := s.got(); len(got) != 2 || got[1] != "d1" {
		t.Fatalf("payloads = %v, want [d1,d2 d1] (冲突批次必须重试而非丢弃)", got)
	}
}

// ==================== 写失败的去向 ====================

func TestAggregateWriterOffloadsWindowOnWriteFailure(t *testing.T) {
	// 写入失败且启用缓存：整窗（而非单批）转投缓存，且不计入 dropped
	o, spool := newTestOutbox(t, connStub(true), 16)
	s := &sink{fail: true}
	w := &AggregateWriter{
		New:      func() Aggregator { return &fakeAgg{} },
		Write:    s.write,
		MaxRows:  2,
		Interval: time.Hour,
	}
	stop := runWriter(o, w)

	o.Enqueue(batch("d1", 1))
	o.Enqueue(batch("d2", 1)) // 满窗 → 写入失败 → 整窗转投缓存

	waitSpoolCount(t, spool, 2)
	stop()

	if got := o.Dropped(); got != 0 {
		t.Fatalf("dropped = %d, want 0 (整窗转投缓存)", got)
	}
	if n, _ := spool.Count(); n != 2 {
		t.Fatalf("spool count = %d, want 2", n)
	}
}

func TestAggregateWriterDropsWindowWhenSpoolDisabled(t *testing.T) {
	o := NewOutbox(OutboxConfig{Tag: "test", ID: "c", Name: "c", Conn: connStub(true), QueueSize: 16})
	o.Start()
	s := &sink{fail: true}
	w := &AggregateWriter{
		New:      func() Aggregator { return &fakeAgg{} },
		Write:    s.write,
		MaxRows:  2,
		Interval: time.Hour,
	}
	stop := runWriter(o, w)
	defer stop()

	o.Enqueue(batch("d1", 1))
	o.Enqueue(batch("d2", 1)) // 满窗 → 写入失败 → 未启用缓存，本窗丢弃

	waitFor(t, func() bool { return o.Dropped() == 2 }, "整窗计入 dropped")
}

// ==================== 停服排空 ====================

func TestAggregateWriterDrainsOutboxOnQuit(t *testing.T) {
	// 停服时队列里还没到刷出条件的批次不能丢：写协程须继续消费直至队列空，
	// 写失败的照常转投缓存（由 Outbox.Wait 保证落盘）。
	o, spool := newTestOutbox(t, connStub(true), 16)
	s := &sink{fail: true} // 收尾时写入失败 → 转投缓存，便于断言「没被丢在内存里」
	w := &AggregateWriter{
		New:      func() Aggregator { return &fakeAgg{} },
		Write:    s.write,
		MaxRows:  1000,
		Interval: time.Hour,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(o)
	}()

	for _, dev := range []string{"d1", "d2"} {
		o.Enqueue(batch(dev, 1))
	}
	o.Close() // 队列里至少还有未到刷出条件的批次
	<-done
	o.Wait()

	n, err := spool.Count()
	if err != nil {
		t.Fatalf("spool count failed: %v", err)
	}
	if n != 2 {
		t.Fatalf("spool count = %d, want 2 (停服排空后不得遗留内存批次)", n)
	}
	if got := o.Dropped(); got != 0 {
		t.Fatalf("dropped = %d, want 0", got)
	}
}

func TestAggregateWriterEmptyWindowWritesNothing(t *testing.T) {
	// 空窗（没有任何批次，或批次全被聚合器判空）不得产生下发
	o, _ := newTestOutbox(t, connStub(true), 16)
	s := &sink{}
	w := &AggregateWriter{
		New:      func() Aggregator { return &fakeAgg{} },
		Write:    s.write,
		MaxRows:  1000,
		Interval: 10 * time.Millisecond,
	}
	stop := runWriter(o, w)

	time.Sleep(80 * time.Millisecond) // 空转若干时间窗
	stop()

	if got := s.got(); len(got) != 0 {
		t.Fatalf("payloads = %v, want none", got)
	}
}
