// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"iot-gateway/model/po"
)

// batchDeleteAddr 造一条设备地址（ID 固定，便于断言）。
func batchDeleteAddr(id, deviceID, name string) po.DeviceAddress {
	return po.DeviceAddress{
		ID:             id,
		DeviceID:       deviceID,
		Name:           name,
		Label:          "测试点位",
		CommonDataType: "Float",
		DataType:       "modbus.float",
		RwPermission:   "R",
		ScanFrequency:  1000,
		Status:         1,
	}
}

// countAddresses 统计指定设备下剩余的地址数。
func countAddresses(t *testing.T, db *gorm.DB, deviceID string) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&po.DeviceAddress{}).Where("device_id = ?", deviceID).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestBatchDeleteDeviceAddressScopedToDevice 覆盖批量删除的两条要害：
// ① 删除范围被 deviceId 框住 —— 传入其它设备的 id 不得生效；
// ② 个别 id 已不存在时**不报错**，只返回实际删除的行数。
// 第 ② 条与单条删除刻意不同：单条删除遇到不存在的 id 会报 DeviceAddressNotExists，
// 批量场景下那是常态，照搬会让整批都删不掉。
func TestBatchDeleteDeviceAddressScopedToDevice(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	svc := NewDeviceAddressService(db, nil)
	ctx := context.Background()

	rows := []po.DeviceAddress{
		batchDeleteAddr("a1", "dev-1", "D100"),
		batchDeleteAddr("a2", "dev-1", "D101"),
		batchDeleteAddr("a3", "dev-1", "D102"),
		batchDeleteAddr("b1", "dev-2", "D200"),
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 勾选两条本设备的、一条不存在的、一条别的设备的
	deleted, err := svc.BatchDeleteDeviceAddress(ctx, "dev-1", []string{"a1", "a2", "ghost", "b1"})
	if err != nil {
		t.Fatalf("BatchDeleteDeviceAddress: %v", err)
	}
	if deleted != 2 {
		t.Errorf("删除行数 = %d, want 2（两条本设备；ghost 不存在、b1 属于别的设备）", deleted)
	}
	if n := countAddresses(t, db, "dev-1"); n != 1 {
		t.Errorf("dev-1 剩余 %d 条, want 1", n)
	}
	// 别的设备的点位必须原样保留：删错点位是静默生效的，不报任何错
	if n := countAddresses(t, db, "dev-2"); n != 1 {
		t.Errorf("dev-2 剩余 %d 条, want 1（不得被跨设备删除）", n)
	}

	// 空 id 列表：不算错误，删 0 条（前端未勾选时不该发出请求，这里兜住服务端调用方）
	deleted, err = svc.BatchDeleteDeviceAddress(ctx, "dev-1", nil)
	if err != nil {
		t.Fatalf("空 ids 应返回 nil 错误, got %v", err)
	}
	if deleted != 0 {
		t.Errorf("空 ids 删除行数 = %d, want 0", deleted)
	}
	if n := countAddresses(t, db, "dev-1"); n != 1 {
		t.Errorf("空 ids 调用后 dev-1 剩余 %d 条, want 1（不得误删）", n)
	}
}
