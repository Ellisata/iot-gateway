// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"errors"
	"testing"

	"iot-gateway/collector"
)

// groupFilterStub 只接收 groupID == "g-1" 的批次。
type groupFilterStub struct {
	stubChannel
}

func (s *groupFilterStub) AcceptsGroup(_, groupID string) bool { return groupID == "g-1" }

func triggerTestRecords() []collector.CollectedRecord {
	return []collector.CollectedRecord{
		{DeviceID: "d1", DeviceAddressID: "a1", Value: "1", Quality: 192},
	}
}

func TestPushRecordsToChannelOnlyTargetsThatChannel(t *testing.T) {
	c1 := &stubChannel{id: "c1"}
	c2 := &stubChannel{id: "c2"}
	e := NewEngine(nil, nil)
	e.mu.Lock()
	e.running = true
	e.channels["c1"] = c1
	e.channels["c2"] = c2
	e.mu.Unlock()

	ok, err := e.PushRecordsToChannel("c1", triggerTestRecords())
	if err != nil || !ok {
		t.Fatalf("PushRecordsToChannel = %v/%v", ok, err)
	}
	if got := len(c1.drain()); got != 1 {
		t.Errorf("目标通道收到 %d 批，期望 1", got)
	}
	if got := len(c2.drain()); got != 0 {
		t.Errorf("非目标通道收到 %d 批，期望 0", got)
	}
}

func TestPushRecordsToChannelIgnoresChangeOnly(t *testing.T) {
	c1 := &stubChannel{id: "c1"}
	e := NewEngine(nil, nil)
	e.mu.Lock()
	e.running = true
	e.channels["c1"] = c1
	e.mu.Unlock()
	e.changeOnly = true

	recs := triggerTestRecords()
	e.PushRecords(recs) // 记录一次"已投递"
	c1.drain()

	ok, err := e.PushRecordsToChannel("c1", recs)
	if err != nil || !ok {
		t.Fatalf("PushRecordsToChannel = %v/%v", ok, err)
	}
	if got := len(c1.drain()); got != 1 {
		t.Errorf("事件推送被 changeOnly 抑制：收到 %d 批，期望 1", got)
	}
}

func TestPushRecordsToChannelBypassesGroupSelector(t *testing.T) {
	c1 := &groupFilterStub{stubChannel: stubChannel{id: "c1"}}
	e := NewEngine(nil, nil)
	e.mu.Lock()
	e.running = true
	e.channels["c1"] = c1
	e.mu.Unlock()

	recs := triggerTestRecords() // GroupID 为空
	e.PushRecords(recs)
	if got := len(c1.drain()); got != 0 {
		t.Fatalf("普通推送应被组过滤，实际收到 %d 批", got)
	}

	ok, err := e.PushRecordsToChannel("c1", recs)
	if err != nil || !ok {
		t.Fatalf("PushRecordsToChannel = %v/%v", ok, err)
	}
	if got := len(c1.drain()); got != 1 {
		t.Errorf("定向推送应绕过组过滤：收到 %d 批，期望 1", got)
	}
}

func TestPushRecordsToChannelErrors(t *testing.T) {
	e := NewEngine(nil, nil)
	if _, err := e.PushRecordsToChannel("missing", triggerTestRecords()); !errors.Is(err, ErrPushNotRunning) {
		t.Fatalf("未运行 err = %v，期望 ErrPushNotRunning", err)
	}

	e.mu.Lock()
	e.running = true
	e.mu.Unlock()
	if _, err := e.PushRecordsToChannel("missing", triggerTestRecords()); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("通道不存在 err = %v，期望 ErrChannelNotFound", err)
	}
}
