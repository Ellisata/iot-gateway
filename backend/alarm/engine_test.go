// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package alarm

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/model/po"
)

// newTestDB 构建临时文件 SQLite（文件库保证跨连接可见，与生产一致），AutoMigrate alarm 表。
func newTestDB(t *testing.T) *gorm.DB {
	dbPath := filepath.Join(t.TempDir(), "alarm.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	for _, m := range []interface{}{&po.Alarm{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return db
}

func countAlarms(t *testing.T, db *gorm.DB, deviceID, alarmType, status string) int64 {
	t.Helper()
	var n int64
	q := db.Model(&po.Alarm{}).Where("target_id = ?", deviceID)
	if alarmType != "" {
		q = q.Where("alarm_type = ?", alarmType)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&n).Error; err != nil {
		t.Fatalf("count alarms: %v", err)
	}
	return n
}

func TestAlarmEngineOfflineAfterThreshold(t *testing.T) {
	db := newTestDB(t)
	e := NewEngine(db)
	e.tr.cfg.ConsecutiveFailures = 3

	// 连续失败未达阈值：不产生报警
	e.ReportDevicePoll("dev-1", "dev1", false)
	e.ReportDevicePoll("dev-1", "dev1", false)
	if n := countAlarms(t, db, "dev-1", "", ""); n != 0 {
		t.Fatalf("expected no alarm before threshold, got %d rows", n)
	}

	// 第 3 次失败：判定离线，落一条 active 报警
	e.ReportDevicePoll("dev-1", "dev1", false)
	if n := countAlarms(t, db, "dev-1", TypeOffline, StatusActive); n != 1 {
		t.Fatalf("expected 1 active offline alarm, got %d", n)
	}

	var a po.Alarm
	if err := db.Where("target_id = ? AND alarm_type = ?", "dev-1", TypeOffline).
		First(&a).Error; err != nil {
		t.Fatal(err)
	}
	if a.TargetName != "dev1" || a.Status != StatusActive {
		t.Fatalf("unexpected alarm row: %+v", a)
	}
}

func TestAlarmEngineTransientFailureNoAlarm(t *testing.T) {
	db := newTestDB(t)
	e := NewEngine(db)
	e.tr.cfg.ConsecutiveFailures = 3

	// 2 次失败后恢复：去抖生效，全程不产生报警
	e.ReportDevicePoll("dev-1", "dev1", false)
	e.ReportDevicePoll("dev-1", "dev1", false)
	e.ReportDevicePoll("dev-1", "dev1", true)

	if n := countAlarms(t, db, "dev-1", "", ""); n != 0 {
		t.Fatalf("expected no alarm for transient failure, got %d rows", n)
	}
}

func TestAlarmEngineRecoverClearsAndWritesHistory(t *testing.T) {
	db := newTestDB(t)
	e := NewEngine(db)
	e.tr.cfg.ConsecutiveFailures = 3

	e.ReportDevicePoll("dev-1", "dev1", false)
	e.ReportDevicePoll("dev-1", "dev1", false)
	e.ReportDevicePoll("dev-1", "dev1", false) // 离线
	if n := countAlarms(t, db, "dev-1", TypeOffline, StatusActive); n != 1 {
		t.Fatalf("expected offline alarm, got %d active", n)
	}

	// 一次成功即恢复：清 active、写 recover 历史
	e.ReportDevicePoll("dev-1", "dev1", true)
	if n := countAlarms(t, db, "dev-1", TypeOffline, StatusActive); n != 0 {
		t.Fatalf("expected active offline alarm cleared, got %d", n)
	}
	if n := countAlarms(t, db, "dev-1", TypeOffline, StatusCleared); n != 1 {
		t.Fatalf("expected 1 cleared offline alarm, got %d", n)
	}
	if n := countAlarms(t, db, "dev-1", TypeRecover, ""); n != 1 {
		t.Fatalf("expected 1 recover record, got %d", n)
	}
}

func TestAlarmEngineNoDuplicateWhileOffline(t *testing.T) {
	db := newTestDB(t)
	e := NewEngine(db)
	e.tr.cfg.ConsecutiveFailures = 3

	for i := 0; i < 3; i++ {
		e.ReportDevicePoll("dev-1", "dev1", false)
	}
	// 已离线后持续失败：不重复报警，只刷新时间戳（节流下不改动 last_occur_time）
	e.ReportDevicePoll("dev-1", "dev1", false)
	e.ReportDevicePoll("dev-1", "dev1", false)

	if n := countAlarms(t, db, "dev-1", "", ""); n != 1 {
		t.Fatalf("expected single alarm row while offline, got %d", n)
	}

	var a po.Alarm
	if err := db.Where("target_id = ? AND alarm_type = ?", "dev-1", TypeOffline).
		First(&a).Error; err != nil {
		t.Fatal(err)
	}
	if a.LastOccurTime != a.FirstOccurTime {
		t.Fatalf("last_occur_time should be throttled (unchanged), first=%s last=%s",
			a.FirstOccurTime, a.LastOccurTime)
	}
}

func TestAlarmEngineIsolatesDevices(t *testing.T) {
	db := newTestDB(t)
	e := NewEngine(db)
	e.tr.cfg.ConsecutiveFailures = 3

	e.ReportDevicePoll("dev-1", "dev1", false)
	e.ReportDevicePoll("dev-1", "dev1", false)
	e.ReportDevicePoll("dev-1", "dev1", false) // dev-1 离线
	e.ReportDevicePoll("dev-2", "dev2", false) // dev-2 仅 1 次失败，不影响

	if n := countAlarms(t, db, "dev-1", TypeOffline, StatusActive); n != 1 {
		t.Fatalf("dev-1 expected offline, got %d active", n)
	}
	if n := countAlarms(t, db, "dev-2", "", ""); n != 0 {
		t.Fatalf("dev-2 expected no alarm, got %d rows", n)
	}
}
