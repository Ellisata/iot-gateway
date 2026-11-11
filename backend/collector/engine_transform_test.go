// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/model/po"
)

// 只改 transform_json 也必须改变配置校验和 —— 否则 10s 热加载不会重建任务，
// 采集侧手里的流水线会一直是旧的，表现为"改了配置没生效"。
func TestLoadConfigChecksumIncludesTransform(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "checksum.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		defer sqlDB.Close() // 先于 t.TempDir 清理执行，否则 Windows 上删不掉文件
	}
	for _, m := range []interface{}{&po.Device{}, &po.DeviceAddress{}, &po.IotProtocol{}, &po.CollectorGroup{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	if err := db.Create(&po.Device{ID: "dev-1", Name: "dev1", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&po.DeviceAddress{
		ID: "addr-1", DeviceID: "dev-1", Name: "40001",
		DataType: "int16", RwPermission: "R", Status: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	engine := NewEngine(db, nil, nil, nil, nil, nil)
	before := engine.loadConfigChecksum()

	if err := db.Model(&po.DeviceAddress{}).Where("id = ?", "addr-1").
		Update("transform_json", `[{"op":"scale","factor":0.1}]`).Error; err != nil {
		t.Fatal(err)
	}
	after := engine.loadConfigChecksum()

	if before == after {
		t.Fatal("transform_json 未参与配置校验和，热加载检测不到变换配置变更")
	}
}
