// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package alarm

import (
	"testing"

	"gorm.io/gorm"

	"iot-gateway/model/po"
)

// firstAlarm 取目标的首条报警行（按创建序），可限定 alarm_type。
func firstAlarm(t *testing.T, db *gorm.DB, targetID, alarmType string) po.Alarm {
	t.Helper()
	var a po.Alarm
	q := db.Where("target_id = ?", targetID)
	if alarmType != "" {
		q = q.Where("alarm_type = ?", alarmType)
	}
	if err := q.Order("created_at ASC").First(&a).Error; err != nil {
		t.Fatalf("load alarm for %s/%s: %v", targetID, alarmType, err)
	}
	return a
}

// restart 模拟网关重启：同一张库上新建状态机（states 清空、normal 初值 true）。
func restart(t *testing.T, db *gorm.DB, sink NotifySink) *Engine {
	t.Helper()
	e := NewEngine(db, sink)
	e.tr.cfg.ConsecutiveFailures = 3
	return e
}

// TestReconcileClearsStaleActiveRowAfterRestart 回归：网关在设备离线期间重启、
// 设备于重启窗口内恢复时，上一进程写下的 active 行必须被清掉。
//
// 不修的话该行永远停在 active —— 新状态机以 normal=true 起步，首个成功信号走不到
// recover 分支（见 tracker.report），设备会被 AlarmService.IsDeviceOnline 与设备
// 列表永久判为离线。
func TestReconcileClearsStaleActiveRowAfterRestart(t *testing.T) {
	db := newTestDB(t)

	// 进程 1：设备离线，落一条 active 行
	e1 := restart(t, db, nil)
	for i := 0; i < 3; i++ {
		e1.ReportDevicePoll("dev-1", "dev1", false)
	}
	if n := countAlarms(t, db, "dev-1", TypeOffline, StatusActive); n != 1 {
		t.Fatalf("setup: expected 1 active alarm, got %d", n)
	}

	// 进程 2（重启）：首个信号即成功
	spy := &sinkSpy{}
	e2 := restart(t, db, spy)
	e2.ReportDevicePoll("dev-1", "dev1", true)

	if n := countAlarms(t, db, "dev-1", TypeOffline, StatusActive); n != 0 {
		t.Fatalf("stale active alarm not cleared after restart, got %d", n)
	}
	got := firstAlarm(t, db, "dev-1", TypeOffline)
	if got.Status != StatusCleared || got.ClearTime == "" {
		t.Fatalf("expected cleared row with clearTime, got status=%q clearTime=%q", got.Status, got.ClearTime)
	}
	// 刻意不伪造恢复事件（真实恢复时刻不可知）
	if n := countAlarms(t, db, "dev-1", TypeRecover, ""); n != 0 {
		t.Fatalf("expected no recover history row on restart reconcile, got %d", n)
	}
	if n := len(spy.events()); n != 0 {
		t.Fatalf("expected no notification on restart reconcile, got %d", n)
	}
}

// TestReconcileCoversRecoveryWithinDebounceWindow 重启后设备短暂抖动（首个信号失败、
// 未达去抖阈值即再次成功）：残留行同样要清掉。
//
// 这正是 reconciled 只在"确实接管了该行"时才置位的理由 —— 若首个失败信号就把
// reconciled 置位，这条路径会漏，残留行照样永久挂着。
func TestReconcileCoversRecoveryWithinDebounceWindow(t *testing.T) {
	db := newTestDB(t)

	e1 := restart(t, db, nil)
	for i := 0; i < 3; i++ {
		e1.ReportDevicePoll("dev-1", "dev1", false)
	}

	e2 := restart(t, db, nil)
	e2.ReportDevicePoll("dev-1", "dev1", false) // 未达阈值
	e2.ReportDevicePoll("dev-1", "dev1", false)
	e2.ReportDevicePoll("dev-1", "dev1", true) // 恢复

	if n := countAlarms(t, db, "dev-1", TypeOffline, StatusActive); n != 0 {
		t.Fatalf("stale active alarm survived debounce-window recovery, got %d", n)
	}
}

// TestReconcileKeepsOngoingFaultAfterRestart 重启后设备仍在故障：首个信号是失败，
// 残留 active 行必须保留 —— 首现时间不被截断，也不产生第二条报警行。
func TestReconcileKeepsOngoingFaultAfterRestart(t *testing.T) {
	db := newTestDB(t)

	e1 := restart(t, db, nil)
	for i := 0; i < 3; i++ {
		e1.ReportDevicePoll("dev-1", "dev1", false)
	}
	before := firstAlarm(t, db, "dev-1", TypeOffline)

	e2 := restart(t, db, nil)
	for i := 0; i < 3; i++ {
		e2.ReportDevicePoll("dev-1", "dev1", false)
	}

	if n := countAlarms(t, db, "dev-1", TypeOffline, StatusActive); n != 1 {
		t.Fatalf("expected still exactly 1 active alarm, got %d", n)
	}
	after := firstAlarm(t, db, "dev-1", TypeOffline)
	if after.ID != before.ID {
		t.Fatalf("expected same row reused, got %s -> %s", before.ID, after.ID)
	}
	if after.FirstOccurTime != before.FirstOccurTime {
		t.Fatalf("fault first_occur_time truncated: %s -> %s", before.FirstOccurTime, after.FirstOccurTime)
	}
}

// TestInProcessRecoverStillWritesRecoverRow 守卫：正常进程内的恢复路径行为不变
// （清行 + 写 recover 历史 + 通知），核对逻辑没有把这条路径吃掉。
func TestInProcessRecoverStillWritesRecoverRow(t *testing.T) {
	db := newTestDB(t)
	spy := &sinkSpy{}
	e := restart(t, db, spy)

	for i := 0; i < 3; i++ {
		e.ReportDevicePoll("dev-1", "dev1", false)
	}
	e.ReportDevicePoll("dev-1", "dev1", true)

	if n := countAlarms(t, db, "dev-1", TypeRecover, StatusCleared); n != 1 {
		t.Fatalf("expected 1 recover record, got %d", n)
	}
	if n := len(spy.events()); n != 2 {
		t.Fatalf("expected 2 notifications (offline + recover), got %d", n)
	}
}

// TestRecoverRowKeepsDeviceID 回归：点位报警的恢复行必须带上 device_id。
// 只有恢复行丢关联时，按 device_id 分组的历史查询会把它甩到空桶里。
func TestRecoverRowKeepsDeviceID(t *testing.T) {
	db := newTestDB(t)
	tr := NewTracker(db, PointSpec, nil)
	tr.cfg.ConsecutiveFailures = 1

	tr.ReportQuality("pt-1", "dev1.温度", causeTextAddress, "dev-1", false)
	tr.ReportQuality("pt-1", "dev1.温度", "", "dev-1", true)

	if got := firstAlarm(t, db, "pt-1", TypeQuality).DeviceID; got != "dev-1" {
		t.Fatalf("raise row: expected deviceId dev-1, got %q", got)
	}
	if got := firstAlarm(t, db, "pt-1", TypeRecover).DeviceID; got != "dev-1" {
		t.Fatalf("recover row: expected deviceId dev-1, got %q", got)
	}

	// 反向守卫：设备/通道行的 target_id 就是自己，device_id 恒为空串。
	trDev := NewTracker(db, DeviceSpec, nil)
	trDev.cfg.ConsecutiveFailures = 1
	trDev.Report("dev-9", "dev9", false)
	if got := firstAlarm(t, db, "dev-9", TypeOffline).DeviceID; got != "" {
		t.Fatalf("device row: expected empty deviceId, got %q", got)
	}
}
