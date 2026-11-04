// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package triggerrule

import (
	"encoding/json"
	"time"

	"iot-gateway/collector"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// 触发记录状态。success 只表示"已进入通道 outbox 队列"，不是已送达下游。
const (
	StatusSuccess    = "success"
	StatusOffline    = "offline"
	StatusReadFailed = "read_failed"
	StatusPushFailed = "push_failed"
	StatusDropped    = "dropped"
	// StatusTimeout 在规则超时前没拿到设备锁（设备正被慢采集/重连占用），
	// 或读取在超时后才结束、已放弃推送。此时没有发生本次设备 I/O，
	// 与"读到了但驱动报错"的 read_failed 区分，便于判断该调冷却还是查设备。
	StatusTimeout = "timeout"
)

// 触发记录保留策略：与 alarm 越限记录同一取舍 —— 只影响"留证多少"，
// 不影响触发正确性；清理挂在写入路径上按间隔惰性执行。
const (
	recordRetention       = 7 * 24 * time.Hour
	recordMaxRows         = 20000
	recordCleanupInterval = time.Hour
)

// conditionSnapshot 触发时刻的条件与样本快照：规则改名/改条件后历史仍可自证。
type conditionSnapshot struct {
	Groups []conditionSnapshotGroup `json:"groups"`
}

type conditionSnapshotGroup struct {
	Conditions []conditionSnapshotCondition `json:"conditions"`
}

type conditionSnapshotCondition struct {
	AddressID   string   `json:"addressId"`
	AddressName string   `json:"addressName"`
	DeviceID    string   `json:"deviceId"`
	Op          string   `json:"op"`
	Values      []string `json:"values"`
	Value       string   `json:"value"`
	Kind        string   `json:"kind"`
	Quality     int      `json:"quality"`
	SampledAt   string   `json:"sampledAt"`
}

// actionResult 动作读取与推送的结果摘要。
type actionResult struct {
	DeviceID      string               `json:"deviceId"`
	AddressCount  int                  `json:"addressCount"`
	ReturnedCount int                  `json:"returnedCount"`
	ChannelID     string               `json:"channelId"`
	Records       []actionResultRecord `json:"records"`
}

type actionResultRecord struct {
	AddressID   string `json:"addressId"`
	AddressName string `json:"addressName"`
	Value       string `json:"value"`
	Kind        string `json:"kind"`
	Quality     int    `json:"quality"`
}

func buildConditionSnapshot(rule *compiledRule, samples map[string]sample, now time.Time) string {
	snap := conditionSnapshot{Groups: make([]conditionSnapshotGroup, 0, len(rule.groups))}
	for _, g := range rule.groups {
		out := conditionSnapshotGroup{Conditions: make([]conditionSnapshotCondition, 0, len(g.conditions))}
		for _, c := range g.conditions {
			item := conditionSnapshotCondition{
				AddressID:   c.addressID,
				AddressName: c.meta.addrName,
				DeviceID:    c.meta.deviceID,
				Op:          c.op,
				Values:      c.values,
			}
			if s, ok := samples[c.addressID]; ok {
				item.Value = s.value
				item.Kind = s.kind
				item.Quality = s.quality
				item.SampledAt = s.at.Format("2006-01-02 15:04:05.000")
			}
			out.Conditions = append(out.Conditions, item)
		}
		snap.Groups = append(snap.Groups, out)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		logger.Warn("triggerrule: marshal condition snapshot failed: %v", err)
		return ""
	}
	return string(raw)
}

func buildActionResult(deviceID, channelID string, records []collector.CollectedRecord) string {
	out := actionResult{
		DeviceID:      deviceID,
		AddressCount:  len(records),
		ReturnedCount: len(records),
		ChannelID:     channelID,
		Records:       make([]actionResultRecord, 0, len(records)),
	}
	for _, r := range records {
		out.Records = append(out.Records, actionResultRecord{
			AddressID:   r.DeviceAddressID,
			AddressName: r.DeviceAddressName,
			Value:       r.Value,
			Kind:        r.Kind,
			Quality:     r.Quality,
		})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		logger.Warn("triggerrule: marshal action result failed: %v", err)
		return ""
	}
	return string(raw)
}

// persistRecord 落一条触发历史；失败只记日志，绝不影响采集/推送。
func (e *Engine) persistRecord(rec *po.TriggerRuleRecord) {
	if e.db == nil {
		return
	}
	if rec.CreatedAt == "" {
		rec.CreatedAt = e.now().Format("2006-01-02 15:04:05")
	}
	if err := e.db.Create(rec).Error; err != nil {
		logger.Error("triggerrule: create trigger record for rule %s failed: %v", rec.RuleID, err)
		return
	}

	now := e.now()
	e.mu.Lock()
	needCleanup := e.lastCleanup.IsZero() || now.Sub(e.lastCleanup) >= e.cleanupInterval
	if needCleanup {
		e.lastCleanup = now
	}
	e.mu.Unlock()
	if needCleanup {
		e.cleanupRecords(now)
	}
}

func (e *Engine) cleanupRecords(now time.Time) {
	if err := e.db.Where("fired_at < ?", now.Add(-recordRetention).Format("2006-01-02 15:04:05")).
		Delete(&po.TriggerRuleRecord{}).Error; err != nil {
		logger.Warn("triggerrule: cleanup records by age failed: %v", err)
		return
	}
	var total int64
	if err := e.db.Model(&po.TriggerRuleRecord{}).Count(&total).Error; err != nil {
		logger.Warn("triggerrule: count records failed: %v", err)
		return
	}
	if excess := total - recordMaxRows; excess > 0 {
		if err := e.db.Where("id IN (?)",
			e.db.Model(&po.TriggerRuleRecord{}).Select("id").
				Order("fired_at ASC").Limit(int(excess))).
			Delete(&po.TriggerRuleRecord{}).Error; err != nil {
			logger.Warn("triggerrule: cleanup records by row cap failed: %v", err)
		}
	}
}
