// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package alarm

import (
	"sync"
	"testing"
	"time"

	"iot-gateway/push"
)

// sinkSpy 记录收到的通知事件，供断言。
type sinkSpy struct {
	mu  sync.Mutex
	evs []Event
}

func (s *sinkSpy) Notify(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evs = append(s.evs, ev)
}

// events 返回已收到事件的副本。
func (s *sinkSpy) events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.evs...)
}

func TestNotifyOnOfflineEdgeOnly(t *testing.T) {
	db := newTestDB(t)
	spy := &sinkSpy{}
	e := NewEngine(db, spy)
	e.tr.cfg.ConsecutiveFailures = 3

	// 未达阈值：不通知
	e.ReportDevicePoll("dev-1", "dev1", false)
	e.ReportDevicePoll("dev-1", "dev1", false)
	if n := len(spy.events()); n != 0 {
		t.Fatalf("expected no notification before threshold, got %d", n)
	}

	// 第 3 次失败：边沿触发，恰好 1 条
	e.ReportDevicePoll("dev-1", "dev1", false)
	evs := spy.events()
	if len(evs) != 1 {
		t.Fatalf("expected exactly 1 notification on offline edge, got %d", len(evs))
	}
	ev := evs[0]
	if ev.AlarmType != TypeOffline || ev.Status != StatusActive {
		t.Fatalf("unexpected alarm type/status: %s/%s", ev.AlarmType, ev.Status)
	}
	if ev.TargetType != TypeDevice || ev.TargetName != "dev1" {
		t.Fatalf("unexpected target: %s/%s", ev.TargetType, ev.TargetName)
	}
	if ev.Level != defaultLevel {
		t.Fatalf("expected level %q, got %q", defaultLevel, ev.Level)
	}
	if ev.Content != "设备 dev1 断联" {
		t.Fatalf("unexpected content: %q", ev.Content)
	}
	if ev.AlarmID == "" || ev.FirstOccurTime == "" || ev.OccurredAt.IsZero() {
		t.Fatalf("expected id/firstOccurTime/occurredAt populated, got %+v", ev)
	}

	// 持续失败（含 touch 节流刷新）：不再重复通知
	e.ReportDevicePoll("dev-1", "dev1", false)
	e.ReportDevicePoll("dev-1", "dev1", false)
	if n := len(spy.events()); n != 1 {
		t.Fatalf("expected still 1 notification while offline, got %d", n)
	}
}

func TestNotifyOnRecoverEdge(t *testing.T) {
	db := newTestDB(t)
	spy := &sinkSpy{}
	e := NewEngine(db, spy)
	e.tr.cfg.ConsecutiveFailures = 3

	for i := 0; i < 3; i++ {
		e.ReportDevicePoll("dev-1", "dev1", false)
	}
	e.ReportDevicePoll("dev-1", "dev1", true) // 恢复

	evs := spy.events()
	if len(evs) != 2 {
		t.Fatalf("expected 2 notifications (offline + recover), got %d", len(evs))
	}
	rec := evs[1]
	if rec.AlarmType != TypeRecover || rec.Status != StatusCleared {
		t.Fatalf("unexpected recover type/status: %s/%s", rec.AlarmType, rec.Status)
	}
	if rec.ClearTime == "" {
		t.Fatalf("expected clearTime populated on recover, got %+v", rec)
	}
	if rec.Content != "设备 dev1 恢复通信" {
		t.Fatalf("unexpected content: %q", rec.Content)
	}
}

func TestNotifySilentOnTransientFailure(t *testing.T) {
	db := newTestDB(t)
	spy := &sinkSpy{}
	e := NewEngine(db, spy)
	e.tr.cfg.ConsecutiveFailures = 3

	// 2 次失败后恢复：去抖生效，全程无通知
	e.ReportDevicePoll("dev-1", "dev1", false)
	e.ReportDevicePoll("dev-1", "dev1", false)
	e.ReportDevicePoll("dev-1", "dev1", true)

	if n := len(spy.events()); n != 0 {
		t.Fatalf("expected no notification for transient failure, got %d", n)
	}
}

func TestNotifySilentOnClearActive(t *testing.T) {
	db := newTestDB(t)
	spy := &sinkSpy{}
	e := NewEngine(db, spy)
	e.tr.cfg.ConsecutiveFailures = 3

	for i := 0; i < 3; i++ {
		e.ReportDevicePoll("dev-1", "dev1", false)
	}
	if n := len(spy.events()); n != 1 {
		t.Fatalf("expected 1 offline notification, got %d", n)
	}

	// 目标停用/删除：行政取消，不是恢复，不应通知
	e.tr.ClearActive("dev-1")
	if n := len(spy.events()); n != 1 {
		t.Fatalf("expected no notification on ClearActive, got %d total", n)
	}
}

func TestNotifyChannelTarget(t *testing.T) {
	db := newTestDB(t)
	spy := &sinkSpy{}
	src := &fakeStatusSource{}
	m := newChannelMonitorWithSink(db, src, spy)

	src.set([]push.ChannelStatusVO{channelStatus("ch-1", "mqtt1", true, false)})
	m.scan()
	m.scan() // 达阈值 -> 离线

	evs := spy.events()
	if len(evs) != 1 {
		t.Fatalf("expected 1 channel notification, got %d", len(evs))
	}
	if evs[0].TargetType != TypeChannel || evs[0].Content != "推送通道 mqtt1 断联" {
		t.Fatalf("unexpected channel event: %+v", evs[0])
	}

	// 恢复：写 recover 行并通知
	src.set([]push.ChannelStatusVO{channelStatus("ch-1", "mqtt1", true, true)})
	m.scan()
	evs = spy.events()
	if len(evs) != 2 || evs[1].AlarmType != TypeRecover {
		t.Fatalf("expected recover notification for channel, got %+v", evs)
	}
}

func TestNotifyNilSinkIsSafe(t *testing.T) {
	db := newTestDB(t)
	e := NewEngine(db, nil)
	e.tr.cfg.ConsecutiveFailures = 3

	// 未装配通知器：不 panic，落库照常
	for i := 0; i < 3; i++ {
		e.ReportDevicePoll("dev-1", "dev1", false)
	}
	e.ReportDevicePoll("dev-1", "dev1", true)

	if n := countAlarms(t, db, "dev-1", TypeOffline, StatusCleared); n != 1 {
		t.Fatalf("expected offline alarm cleared, got %d", n)
	}
	if n := countAlarms(t, db, "dev-1", TypeRecover, ""); n != 1 {
		t.Fatalf("expected 1 recover record, got %d", n)
	}
}

// TestNotifySinkMustNotBlock 契约回归守卫：
// 生产实现必须是非阻塞的（见 NotifySink 文档）。这里用一个与生产同构的
// 非阻塞 sink 并把它灌满，反复制造边沿触发 —— 若有人日后把出口改成阻塞
// 发送（或去掉了 select/default），本测试会在第一次边沿就挂死而失败。
func TestNotifySinkMustNotBlock(t *testing.T) {
	db := newTestDB(t)
	full := make(chan Event, 1)
	full <- Event{} // 预先灌满，后续发送必然丢弃
	sink := &nonBlockingSink{ch: full}

	e := NewEngine(db, sink)
	e.tr.cfg.ConsecutiveFailures = 1

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			// 反复在离线/恢复间翻转，制造边沿触发
			e.ReportDevicePoll("dev-1", "dev1", i%2 == 1)
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Report blocked: NotifySink implementation must not block (see NotifySink contract)")
	}
}

// nonBlockingSink 与 notify.Dispatcher 生产实现同构的最小非阻塞 sink。
type nonBlockingSink struct {
	ch chan Event
}

func (s *nonBlockingSink) Notify(ev Event) {
	select {
	case s.ch <- ev:
	default: // 满则丢弃：绝不阻塞报警状态机
	}
}
