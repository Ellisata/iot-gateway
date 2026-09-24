// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package alarm

import (
	"time"

	"iot-gateway/model/po"
)

// Event 报警通知事件：Tracker 在报警/恢复边沿落库成功后投递给通知器。
//
// 字段与 po.Alarm 一致，保证群消息内容与报警页面同源（同样的文案、同样的时间戳）。
type Event struct {
	AlarmID        string // alarm 行 ID，排障追踪用
	TargetID       string
	TargetName     string
	TargetType     string // TypeDevice | TypeChannel
	AlarmType      string // TypeOffline | TypeRecover
	Level          string
	Content        string // 如「设备 dev1 断联」
	Status         string // StatusActive | StatusCleared
	FirstOccurTime string
	LastOccurTime  string
	ClearTime      string
	OccurredAt     time.Time // 事件投递时刻（发送侧日志/耗时统计用）
}

// NotifySink 报警通知接收者，由 notify.Dispatcher 实现（wire 中经 wire.Bind 注入，
// 因此本包不依赖 notify 包）。
//
// 实现会在 Tracker.mu 持有期间被调用，故契约如下，实现方必须遵守：
//  1. Notify 必须立即返回，绝不能阻塞或做耗时 IO —— 生产实现仅做一次
//     select/default 的非阻塞入队。队列满应丢弃并计数，不得反压。
//  2. Notify 绝不能回调 Tracker（否则与 Tracker.mu 形成锁环）。
//
// 违反契约的后果只是采集协程延迟升高：状态迁移与报警落库在调用 Notify 前
// 均已完成，通知失败不会影响报警本身的正确性。
type NotifySink interface {
	Notify(ev Event)
}

// notifyLocked 在报警/恢复边沿落库成功后投递通知（调用方持 t.mu）。
// 仅做一次非阻塞发送，绝不阻塞状态机与采集协程。
func (t *Tracker) notifyLocked(a *po.Alarm) {
	if t.sink == nil {
		// 未装配通知器（单测/裁剪部署），静默跳过
		return
	}
	t.sink.Notify(Event{
		AlarmID:        a.ID,
		TargetID:       a.TargetID,
		TargetName:     a.TargetName,
		TargetType:     a.TargetType,
		AlarmType:      a.AlarmType,
		Level:          a.Level,
		Content:        a.Content,
		Status:         a.Status,
		FirstOccurTime: a.FirstOccurTime,
		LastOccurTime:  a.LastOccurTime,
		ClearTime:      a.ClearTime,
		OccurredAt:     time.Now(),
	})
}
