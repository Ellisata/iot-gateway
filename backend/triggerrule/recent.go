// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package triggerrule

import (
	"sync"
	"time"

	"iot-gateway/collector"
)

// RecentTriggerRingSize 引擎内存中保留的最近触发条数。
// 与接口 limit 的上限保持一致；不落库，进程重启即清空。
const RecentTriggerRingSize = 100

// RecentActionRecord 一次触发实际读到的动作点位。
type RecentActionRecord struct {
	AddressID   string
	AddressName string
	Value       string
	Kind        string
	Quality     int
}

// RecentTrigger 一次已经结束的触发（成功/离线/读失败/推送失败/丢弃）。
//
// 仅内存、仅观测：用于实时监控"哪个规则刚触发、读到了什么、推到了哪个通道"。
// 持久化与分页查询仍走 trigger_rule_record；这里只保留最近窗口，避免高频写入。
type RecentTrigger struct {
	Seq            uint64
	TriggerID      string
	RuleID         string
	RuleCode       string
	RuleName       string
	FiredAt        time.Time
	Status         string
	DurationMs     int
	Error          string
	ActionDeviceID string
	ChannelID      string
	Records        []RecentActionRecord
}

// recentTriggerRing 固定容量环形缓冲：写满后覆盖最旧一条。
//
// 写入发生在规则 worker / drop worker，读取发生在 HTTP 协程。锁只保护指针与
// 副本，临界区里不做 I/O。
type recentTriggerRing struct {
	mu    sync.Mutex
	items []RecentTrigger
	next  int
	count int
	seq   uint64
}

func newRecentTriggerRing(size int) *recentTriggerRing {
	if size <= 0 {
		size = 1
	}
	return &recentTriggerRing{items: make([]RecentTrigger, size)}
}

func (r *recentTriggerRing) add(ev RecentTrigger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	ev.Seq = r.seq
	r.items[r.next] = ev
	r.next = (r.next + 1) % len(r.items)
	if r.count < len(r.items) {
		r.count++
	}
}

// snapshot 返回新→旧的副本；ruleID 为空表示全部，limit ≤ 0 表示不限（受环容量约束）。
func (r *recentTriggerRing) snapshot(ruleID string, limit int) []RecentTrigger {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]RecentTrigger, 0, r.count)
	total := len(r.items)
	for i := 0; i < r.count; i++ {
		idx := (r.next - 1 - i + total) % total
		ev := r.items[idx]
		if ruleID != "" && ev.RuleID != ruleID {
			continue
		}
		out = append(out, ev)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// RecentTriggers 返回最近结束的触发（新→旧，仅内存）。
// ruleID 为空表示全部规则；limit ≤ 0 取全部（最多 RecentTriggerRingSize 条）。
func (e *Engine) RecentTriggers(ruleID string, limit int) []RecentTrigger {
	e.mu.RLock()
	ring := e.recent
	e.mu.RUnlock()
	if ring == nil {
		return []RecentTrigger{}
	}
	return ring.snapshot(ruleID, limit)
}

// recordRecent 把一次结束的触发写入内存环形缓冲。失败/丢弃事件同样记录，
// 让运维能看到"边沿发生了，但没推出去"。
func (e *Engine) recordRecent(j job, status string, durationMs int, errText string,
	records []collector.CollectedRecord) {
	e.mu.RLock()
	ring := e.recent
	e.mu.RUnlock()
	if ring == nil {
		return
	}

	ev := RecentTrigger{
		TriggerID:      j.triggerID,
		RuleID:         j.ruleID,
		RuleCode:       j.ruleCode,
		RuleName:       j.ruleName,
		FiredAt:        j.firedAt,
		Status:         status,
		DurationMs:     durationMs,
		Error:          errText,
		ActionDeviceID: j.actionDeviceID,
		ChannelID:      j.targetChannelID,
	}
	if len(records) > 0 {
		ev.Records = make([]RecentActionRecord, 0, len(records))
		for i := range records {
			r := &records[i]
			name := r.DeviceAddressName
			if name == "" {
				name = r.DeviceAddressLabel
			}
			ev.Records = append(ev.Records, RecentActionRecord{
				AddressID:   r.DeviceAddressID,
				AddressName: name,
				Value:       r.Value,
				Kind:        r.Kind,
				Quality:     r.Quality,
			})
		}
	}
	ring.add(ev)
}
