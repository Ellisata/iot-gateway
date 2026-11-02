// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/model/po"
	"iot-gateway/testutil/drivertest"
	"iot-gateway/workerPool"
)

// TestNewGatewayTaskSkipsWriteOnlyPoints 只写点位（rw_permission=W）只参与控制下发，
// 不得进入采集调度：驱动契约里没有权限字段，轮询它会在设备侧报错、把整台设备拖入
// 退避/离线，也会让「只写」这个语义自相矛盾。R 与 RW 必须照常采集。
func TestNewGatewayTaskSkipsWriteOnlyPoints(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "writeonly.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	for _, m := range []interface{}{&po.Device{}, &po.DeviceAddress{}, &po.IotProtocol{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: drivertest.TestProtocol}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&po.Device{
		ID:           "dev-1",
		Name:         "dev1",
		ProtocolID:   "proto-1",
		ProtocolJSON: "{}",
		Status:       1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	addrs := []po.DeviceAddress{
		{ID: "a-r", DeviceID: "dev-1", Name: "40001", DataType: "int16", RwPermission: "R", ScanFrequency: 1000, Status: 1},
		{ID: "a-rw", DeviceID: "dev-1", Name: "40002", DataType: "int16", RwPermission: "RW", ScanFrequency: 1000, Status: 1},
		{ID: "a-w", DeviceID: "dev-1", Name: "40003", DataType: "int16", RwPermission: "W", ScanFrequency: 1000, Status: 1},
	}
	for i := range addrs {
		if err := db.Create(&addrs[i]).Error; err != nil {
			t.Fatalf("create address %s: %v", addrs[i].ID, err)
		}
	}

	task, err := newGatewayTask(db, mockSink{}, workerPool.NewWorkerPool(2, 16), nil, nil, nil)
	if err != nil {
		t.Fatalf("newGatewayTask: %v", err)
	}

	var got []string
	for _, g := range task.groups {
		for _, addr := range g.addrGroups["dev-1"] {
			got = append(got, addr.ID)
		}
	}
	sort.Strings(got)
	want := []string{"a-r", "a-rw"}
	if len(got) != len(want) {
		t.Fatalf("采集点位 = %v, want %v（W 点位不应进入调度）", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("采集点位 = %v, want %v（W 点位不应进入调度）", got, want)
		}
	}
}
