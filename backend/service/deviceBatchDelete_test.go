// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"iot-gateway/model/po"
)

// seedDevices 造若干台设备（ID 固定，便于断言）。
func seedDevices(t *testing.T, db *gorm.DB, ids ...string) {
	t.Helper()
	for _, id := range ids {
		dev := po.Device{ID: id, Name: "设备-" + id, ProtocolID: "proto-1", ProtocolJSON: "{}", Status: 1}
		if err := db.Create(&dev).Error; err != nil {
			t.Fatalf("seed device %s: %v", id, err)
		}
	}
}

// countDevices 统计指定 id 的设备行数。
func countDevices(t *testing.T, db *gorm.DB, id string) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&po.Device{}).Where("id = ?", id).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestBatchDeleteDeviceCascadesAddresses 覆盖批量删除的三条要害：
// ① 个别 id 已不存在时**不报错**，只返回实际删除的行数 —— 与单条删除刻意不同，
// 单条删除遇到不存在的 id 会报 DeviceNotExists，照搬会让整批都删不掉；
// ② 点位随设备**一并删除**，而不是留成查不到的孤立行（表间无外键）；
// ③ 未勾选的设备及其点位一个都不能少 —— 删设备是静默生效的，删错了不报任何错。
func TestBatchDeleteDeviceCascadesAddresses(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	svc := NewDeviceService(db, nil, nil)
	ctx := context.Background()

	seedDevices(t, db, "dev-1", "dev-2")
	rows := []po.DeviceAddress{
		batchDeleteAddr("a1", "dev-1", "D100"),
		batchDeleteAddr("a2", "dev-1", "D101"),
		batchDeleteAddr("b1", "dev-2", "D200"),
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed addresses: %v", err)
	}

	// 勾选 dev-1、一个不存在的 ghost、以及一个同样不存在的 dev-3
	deleted, addresses, err := svc.BatchDeleteDevice(ctx, []string{"dev-1", "ghost", "dev-3"})
	if err != nil {
		t.Fatalf("BatchDeleteDevice: %v", err)
	}
	if deleted != 1 {
		t.Errorf("删除设备数 = %d, want 1（只有 dev-1 存在）", deleted)
	}
	if addresses != 2 {
		t.Errorf("连带删除点位数 = %d, want 2（dev-1 下的 a1/a2）", addresses)
	}
	if n := countDevices(t, db, "dev-1"); n != 0 {
		t.Errorf("dev-1 剩余 %d 行, want 0", n)
	}
	if n := countAddresses(t, db, "dev-1"); n != 0 {
		t.Errorf("dev-1 剩余 %d 个点位, want 0（点位必须随设备一并删除，否则成孤立行）", n)
	}
	// 未勾选的设备原样保留：点位被误删是静默生效的
	if n := countDevices(t, db, "dev-2"); n != 1 {
		t.Errorf("dev-2 剩余 %d 行, want 1（不得被误删）", n)
	}
	if n := countAddresses(t, db, "dev-2"); n != 1 {
		t.Errorf("dev-2 剩余 %d 个点位, want 1（不得被误删）", n)
	}

	// 空 id 列表：不算错误，删 0 条（前端未勾选时不该发出请求，这里兜住服务端调用方）
	deleted, addresses, err = svc.BatchDeleteDevice(ctx, nil)
	if err != nil {
		t.Fatalf("空 ids 应返回 nil 错误, got %v", err)
	}
	if deleted != 0 || addresses != 0 {
		t.Errorf("空 ids 删除 = (%d, %d), want (0, 0)", deleted, addresses)
	}
	if n := countDevices(t, db, "dev-2"); n != 1 {
		t.Errorf("空 ids 调用后 dev-2 剩余 %d 行, want 1（不得误删）", n)
	}
}

// TestDeleteDeviceCascadesAddresses 单条删除与批量删除必须收敛到同一语义：
// 都清掉点位。两条路径行为不一致时，用户会以为"单条删更安全"，而实际上只是
// 静默留下了孤立数据。
func TestDeleteDeviceCascadesAddresses(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	svc := NewDeviceService(db, nil, nil)
	ctx := context.Background()

	seedDevices(t, db, "dev-1")
	if err := db.Create(&po.DeviceAddress{
		ID: "a1", DeviceID: "dev-1", Name: "D100", Label: "测试点位",
		CommonDataType: "Float", DataType: "modbus.float", RwPermission: "R",
		ScanFrequency: 1000, Status: 1,
	}).Error; err != nil {
		t.Fatalf("seed address: %v", err)
	}

	if err := svc.DeleteDevice(ctx, "dev-1"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}
	if n := countAddresses(t, db, "dev-1"); n != 0 {
		t.Errorf("dev-1 剩余 %d 个点位, want 0", n)
	}

	// 设备本就不存在时仍报 DeviceNotExists（单条删除语义不变）
	if err := svc.DeleteDevice(ctx, "ghost"); err == nil {
		t.Error("删除不存在的设备应报错，单条删除的语义不能被批量删除带偏")
	}
}
