// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"testing"

	"iot-gateway/collector"
)

// groupStubChannel 在 stubChannel 基础上实现 GroupSelector，用于验证按组过滤。
type groupStubChannel struct {
	stubChannel
	groups []string
}

func (s *groupStubChannel) AcceptsGroup(_ string, groupID string) bool {
	return AcceptsGroupID(s.groups, groupID)
}

func newEngineWith(chans ...Channel) *Engine {
	e := NewEngine(nil, nil)
	e.mu.Lock()
	for _, ch := range chans {
		e.channels[ch.ChannelID()] = ch
	}
	e.mu.Unlock()
	return e
}

// TestPushRecordsBucketsByGroup 同一设备的两个组必须拆成两个批次，互不合并。
func TestPushRecordsBucketsByGroup(t *testing.T) {
	ch := &stubChannel{id: "c1"}
	e := newEngineWith(ch)

	e.PushRecords([]collector.CollectedRecord{
		{DeviceID: "d1", GroupID: "g1", GroupName: "fast", GroupIntervalMs: 500, DeviceAddressID: "a1", Value: "1", Quality: 192},
		{DeviceID: "d1", GroupID: "g1", GroupName: "fast", GroupIntervalMs: 500, DeviceAddressID: "a2", Value: "2", Quality: 192},
		{DeviceID: "d1", GroupID: "g2", GroupName: "slow", GroupIntervalMs: 5000, DeviceAddressID: "a3", Value: "3", Quality: 192},
		{DeviceID: "d2", DeviceAddressID: "b1", Value: "4", Quality: 192}, // 未分组
	})

	batches := ch.drain()
	if len(batches) != 3 {
		t.Fatalf("batches = %d, want 3", len(batches))
	}
	if batches[0].DeviceID != "d1" || batches[0].GroupID != "g1" || len(batches[0].Records) != 2 {
		t.Errorf("batch 0 = %+v, want d1/g1 with 2 records", batches[0])
	}
	if batches[0].GroupName != "fast" || batches[0].GroupIntervalMs != 500 {
		t.Errorf("batch 0 group identity = (%q,%d)", batches[0].GroupName, batches[0].GroupIntervalMs)
	}
	if batches[1].DeviceID != "d1" || batches[1].GroupID != "g2" {
		t.Errorf("batch 1 = %+v, want d1/g2", batches[1])
	}
	if batches[2].DeviceID != "d2" || batches[2].GroupID != "" {
		t.Errorf("batch 2 = %+v, want d2 ungrouped", batches[2])
	}
}

// TestPushRecordsGroupSelectorFilters 非空订阅只收匹配组，未分组记录不接收；
// 未实现 GroupSelector 的通道收全部。
func TestPushRecordsGroupSelectorFilters(t *testing.T) {
	chAll := &stubChannel{id: "all"}
	chFast := &groupStubChannel{stubChannel: stubChannel{id: "fast"}, groups: []string{"g1"}}
	e := newEngineWith(chAll, chFast)

	e.PushRecords([]collector.CollectedRecord{
		{DeviceID: "d1", GroupID: "g1", DeviceAddressID: "a1", Value: "1", Quality: 192},
		{DeviceID: "d1", GroupID: "g2", DeviceAddressID: "a2", Value: "2", Quality: 192},
		{DeviceID: "d2", DeviceAddressID: "b1", Value: "3", Quality: 192},
	})

	all := chAll.drain()
	if len(all) != 3 {
		t.Fatalf("all-channel batches = %d, want 3", len(all))
	}
	fast := chFast.drain()
	if len(fast) != 1 || fast[0].GroupID != "g1" {
		t.Fatalf("fast-channel batches = %+v, want only g1", fast)
	}
}

// TestPushRecordsNoAcceptingChannelDoesNotAdvanceChangeTracker 没有任何通道接收时，
// 变化追踪不能把点位记为"已投递"，否则后续订阅该组的通道永远拿不到初值。
func TestPushRecordsNoAcceptingChannelDoesNotAdvanceChangeTracker(t *testing.T) {
	onlySlow := &groupStubChannel{stubChannel: stubChannel{id: "slow"}, groups: []string{"g2"}}
	e := newEngineWith(onlySlow)
	e.changeOnly = true

	recs := []collector.CollectedRecord{
		{DeviceID: "d1", GroupID: "g1", DeviceAddressID: "a1", Value: "1", Quality: 192},
	}
	e.PushRecords(recs)
	if got := onlySlow.drain(); len(got) != 0 {
		t.Fatalf("slow channel got %d batches, want 0", len(got))
	}

	// 现在加入一个接收全部组的通道；同样的值必须还能投递（tracker 未被推进）。
	chAll := &stubChannel{id: "all"}
	e.mu.Lock()
	e.channels["all"] = chAll
	e.mu.Unlock()

	e.PushRecords(recs)
	if got := chAll.drain(); len(got) != 1 {
		t.Fatalf("all channel batches = %d, want 1 (tracker must not advance without receiver)", len(got))
	}
}

// TestChangeTrackerRedeliversOnGroupMove 点位换组后，即使值没变也要重新投递：
// 下游的 topic/帧语义已经变了。
func TestChangeTrackerRedeliversOnGroupMove(t *testing.T) {
	ch := &stubChannel{id: "c1"}
	e := newEngineWith(ch)
	e.changeOnly = true

	rec := collector.CollectedRecord{
		DeviceID: "d1", GroupID: "g1", GroupName: "fast", GroupIntervalMs: 500,
		DeviceAddressID: "a1", Value: "1", Quality: 192,
	}
	e.PushRecords([]collector.CollectedRecord{rec})
	if got := ch.drain(); len(got) != 1 {
		t.Fatalf("first push batches = %d, want 1", len(got))
	}

	// 值不变、组不变：被变化过滤掉。
	e.PushRecords([]collector.CollectedRecord{rec})
	if got := ch.drain(); len(got) != 0 {
		t.Fatalf("unchanged push batches = %d, want 0", len(got))
	}

	// 值不变、换到另一个组：必须重新投递。
	moved := rec
	moved.GroupID = "g2"
	moved.GroupName = "slow"
	moved.GroupIntervalMs = 5000
	e.PushRecords([]collector.CollectedRecord{moved})
	if got := ch.drain(); len(got) != 1 || got[0].GroupID != "g2" {
		t.Fatalf("moved push batches = %+v, want one g2 batch", got)
	}
}
