// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/alarm"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
)

// newAlarmTestDB 构建报警服务测试库（临时文件 SQLite，跨连接可见）。
func newAlarmTestDB(t *testing.T) *gorm.DB {
	dbPath := filepath.Join(t.TempDir(), "alarm_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.Alarm{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// seedAlarm 直接插入一条报警记录（模拟报警引擎已落库）。
func seedAlarm(db *gorm.DB, targetID, targetName, targetType, typ, status string) {
	a := &po.Alarm{
		TargetID:       targetID,
		TargetName:     targetName,
		TargetType:     targetType,
		AlarmType:      typ,
		Level:          "warning",
		Content:        "test",
		Status:         status,
		FirstOccurTime: "2026-08-20 10:00:00",
		LastOccurTime:  "2026-08-20 10:00:00",
		CreatedAt:      "2026-08-20 10:00:00",
		UpdatedAt:      "2026-08-20 10:00:00",
	}
	if err := db.Create(a).Error; err != nil {
		panic(err)
	}
}

func TestAlarmServiceListActiveAndOnline(t *testing.T) {
	db := newAlarmTestDB(t)
	// dev-1 离线（active），dev-2 无报警（在线），dev-3 已恢复（cleared），ch-1 通道离线
	seedAlarm(db, "dev-1", "dev1", alarm.TypeDevice, alarm.TypeOffline, alarm.StatusActive)
	seedAlarm(db, "dev-1", "dev1", alarm.TypeDevice, alarm.TypeRecover, alarm.StatusCleared)
	seedAlarm(db, "dev-3", "dev3", alarm.TypeDevice, alarm.TypeOffline, alarm.StatusCleared)
	seedAlarm(db, "ch-1", "mqtt1", alarm.TypeChannel, alarm.TypeOffline, alarm.StatusActive)

	svc := NewAlarmService(db)
	ctx := context.Background()

	// 全部活跃报警（设备 + 通道）
	all, err := svc.ListActiveAlarms(ctx)
	if err != nil {
		t.Fatalf("ListActiveAlarms: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("active alarms = %d, want 2 (dev-1 + ch-1)", len(all))
	}

	// 仅设备
	devices, err := svc.ListActiveTargets(ctx, alarm.TypeDevice)
	if err != nil {
		t.Fatalf("ListActiveTargets(device): %v", err)
	}
	if len(devices) != 1 || devices[0].TargetID != "dev-1" {
		t.Fatalf("device alarms = %+v, want only dev-1", devices)
	}

	if !svc.IsDeviceOnline(ctx, "dev-2") {
		t.Error("dev-2 should be online")
	}
	if svc.IsDeviceOnline(ctx, "dev-1") {
		t.Error("dev-1 should be offline")
	}
}

func TestAlarmServicePageAlarms(t *testing.T) {
	db := newAlarmTestDB(t)
	seedAlarm(db, "dev-1", "dev1", alarm.TypeDevice, alarm.TypeOffline, alarm.StatusActive)
	seedAlarm(db, "dev-2", "dev2", alarm.TypeDevice, alarm.TypeOffline, alarm.StatusCleared)
	seedAlarm(db, "ch-1", "mqtt1", alarm.TypeChannel, alarm.TypeRecover, alarm.StatusCleared)

	svc := NewAlarmService(db)
	ctx := context.Background()

	// 全部（3 条）
	page, err := svc.PageAlarms(ctx, &dto.PageAlarmDTO{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("PageAlarms all: %v", err)
	}
	if page.Total != 3 || len(page.Records) != 3 {
		t.Fatalf("total=%d records=%d, want 3/3", page.Total, len(page.Records))
	}

	// 按目标名过滤
	page, err = svc.PageAlarms(ctx, &dto.PageAlarmDTO{Page: 1, Size: 10, TargetName: "dev2"})
	if err != nil {
		t.Fatalf("PageAlarms filter name: %v", err)
	}
	if page.Total != 1 || page.Records[0].TargetID != "dev-2" {
		t.Fatalf("filtered total=%d, want 1 dev-2", page.Total)
	}

	// 按目标类型过滤
	page, err = svc.PageAlarms(ctx, &dto.PageAlarmDTO{Page: 1, Size: 10, TargetType: alarm.TypeChannel})
	if err != nil {
		t.Fatalf("PageAlarms filter type: %v", err)
	}
	if page.Total != 1 || page.Records[0].TargetID != "ch-1" {
		t.Fatalf("channel total=%d, want 1 ch-1", page.Total)
	}
}

func TestAlarmVOsIncludeChineseLabels(t *testing.T) {
	db := newAlarmTestDB(t)
	seedAlarm(db, "dev-1", "dev1", alarm.TypeDevice, alarm.TypeOffline, alarm.StatusActive)
	seedAlarm(db, "ch-1", "mqtt1", alarm.TypeChannel, alarm.TypeRecover, alarm.StatusCleared)

	svc := NewAlarmService(db)
	vos, err := svc.PageAlarms(context.Background(), &dto.PageAlarmDTO{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("PageAlarms: %v", err)
	}

	got := map[string]vo.AlarmVO{}
	for _, v := range vos.Records {
		got[v.TargetID] = v
	}
	offline := got["dev-1"]
	if offline.AlarmTypeName != "断联报警" || offline.StatusName != "未恢复" {
		t.Fatalf("dev-1 labels = %q/%q, want 断联报警/未恢复", offline.AlarmTypeName, offline.StatusName)
	}
	recover := got["ch-1"]
	if recover.AlarmTypeName != "恢复记录" || recover.StatusName != "已恢复" {
		t.Fatalf("ch-1 labels = %q/%q, want 恢复记录/已恢复", recover.AlarmTypeName, recover.StatusName)
	}
}

func TestAlarmServiceClearDeviceAndChannelAlarm(t *testing.T) {
	db := newAlarmTestDB(t)
	seedAlarm(db, "dev-1", "dev1", alarm.TypeDevice, alarm.TypeOffline, alarm.StatusActive)
	seedAlarm(db, "ch-1", "mqtt1", alarm.TypeChannel, alarm.TypeOffline, alarm.StatusActive)

	svc := NewAlarmService(db)
	ctx := context.Background()

	if err := svc.ClearDeviceAlarm(ctx, "dev-1"); err != nil {
		t.Fatalf("ClearDeviceAlarm: %v", err)
	}
	if !svc.IsDeviceOnline(ctx, "dev-1") {
		t.Error("dev-1 should be online after clear")
	}

	// 清设备报警不影响通道报警
	if err := svc.ClearChannelAlarm(ctx, "ch-1"); err != nil {
		t.Fatalf("ClearChannelAlarm: %v", err)
	}
	var n int64
	if err := db.Model(&po.Alarm{}).Where("status = ?", alarm.StatusActive).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("active alarms after clears = %d, want 0", n)
	}
}
