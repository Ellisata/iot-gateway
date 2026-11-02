// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"path/filepath"
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/model/po"
	"iot-gateway/testutil/drivertest"
	"iot-gateway/workerPool"
)

// newCollectorGroupTestDB 建一个带设备/点位/采集组的最小库。
func newCollectorGroupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "collector_group.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	for _, m := range []interface{}{
		&po.Device{}, &po.DeviceAddress{}, &po.IotProtocol{}, &po.CollectorGroup{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: drivertest.TestProtocol}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&po.Device{
		ID: "dev-1", Name: "dev1", ProtocolID: "proto-1", ProtocolJSON: "{}", Status: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

// TestNewGatewayTaskUsesCollectorGroupInterval 绑定组后组周期权威；组缺失/跨设备/
// 停用的点位不进入采集调度；未分组点位继续用 scan_frequency。
func TestNewGatewayTaskUsesCollectorGroupInterval(t *testing.T) {
	db := newCollectorGroupTestDB(t)

	groups := []po.CollectorGroup{
		{ID: "g-fast", DeviceID: "dev-1", Name: "fast", IntervalMs: 200, Status: 1},
		{ID: "g-slow", DeviceID: "dev-1", Name: "slow", IntervalMs: 3000, Status: 1},
		{ID: "g-off", DeviceID: "dev-1", Name: "off", IntervalMs: 100, Status: 0},
		{ID: "g-other", DeviceID: "dev-2", Name: "other", IntervalMs: 100, Status: 1},
	}
	for i := range groups {
		if err := db.Create(&groups[i]).Error; err != nil {
			t.Fatalf("create group %s: %v", groups[i].ID, err)
		}
	}
	// GORM 的 default:1 会把零值 status 写成 1，停用组必须显式更新：
	// 生产路径也是走 Update 把 status 置 0 的。
	if err := db.Model(&po.CollectorGroup{}).Where("id = ?", "g-off").Update("status", 0).Error; err != nil {
		t.Fatalf("disable group: %v", err)
	}

	addrs := []po.DeviceAddress{
		{ID: "a-free", DeviceID: "dev-1", Name: "40001", DataType: "int16", RwPermission: "R", ScanFrequency: 1000, Status: 1},
		{ID: "a-fast", DeviceID: "dev-1", GroupID: "g-fast", Name: "40002", DataType: "int16", RwPermission: "R", ScanFrequency: 9999, Status: 1},
		{ID: "a-slow", DeviceID: "dev-1", GroupID: "g-slow", Name: "40003", DataType: "int16", RwPermission: "R", ScanFrequency: 9999, Status: 1},
		{ID: "a-off", DeviceID: "dev-1", GroupID: "g-off", Name: "40004", DataType: "int16", RwPermission: "R", ScanFrequency: 1000, Status: 1},
		{ID: "a-missing", DeviceID: "dev-1", GroupID: "g-missing", Name: "40005", DataType: "int16", RwPermission: "R", ScanFrequency: 1000, Status: 1},
		{ID: "a-cross", DeviceID: "dev-1", GroupID: "g-other", Name: "40006", DataType: "int16", RwPermission: "R", ScanFrequency: 1000, Status: 1},
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

	scheduled := make(map[string]string) // addressID -> interval
	for _, g := range task.groups {
		for _, addr := range g.addrGroups["dev-1"] {
			scheduled[addr.ID] = g.interval.String()
		}
	}
	if len(scheduled) != 3 {
		t.Fatalf("scheduled = %v, want exactly free/fast/slow", scheduled)
	}
	if scheduled["a-free"] != "1s" {
		t.Errorf("a-free interval = %s, want 1s (scan_frequency)", scheduled["a-free"])
	}
	if scheduled["a-fast"] != "200ms" {
		t.Errorf("a-fast interval = %s, want 200ms (group interval)", scheduled["a-fast"])
	}
	if scheduled["a-slow"] != "3s" {
		t.Errorf("a-slow interval = %s, want 3s (group interval)", scheduled["a-slow"])
	}
	for _, id := range []string{"a-off", "a-missing", "a-cross"} {
		if _, ok := scheduled[id]; ok {
			t.Errorf("%s should not be scheduled (disabled/missing/cross-device group)", id)
		}
	}

	// 记录里必须带组身份，推送侧才能按组拆分。
	recs := task.toRecords(
		&po.Device{ID: "dev-1", Name: "dev1"},
		[]po.DeviceAddress{{ID: "a-fast", Name: "40002", Label: "温度", GroupID: "g-fast"}},
		[]driver.ReadResult{{DeviceAddressID: "a-fast", Value: "23", Quality: 192}},
	)
	if len(recs) != 1 || recs[0].GroupID != "g-fast" || recs[0].GroupName != "fast" || recs[0].GroupIntervalMs != 200 {
		t.Fatalf("record group identity = %+v, want g-fast/fast/200", recs)
	}

	// 索引未对齐的回退路径也要取得到组身份。
	recs = task.toRecords(
		&po.Device{ID: "dev-1", Name: "dev1"},
		[]po.DeviceAddress{
			{ID: "a-free", Name: "40001"},
			{ID: "a-fast", Name: "40002", GroupID: "g-fast"},
		},
		[]driver.ReadResult{
			{DeviceAddressID: "a-fast", Value: "23", Quality: 192},
			{DeviceAddressID: "a-free", Value: "1", Quality: 192},
		},
	)
	if recs[0].GroupID != "g-fast" || recs[1].GroupID != "" {
		t.Errorf("fallback group ids = (%q,%q), want (g-fast,\"\")", recs[0].GroupID, recs[1].GroupID)
	}
}
