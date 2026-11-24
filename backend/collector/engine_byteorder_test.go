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

// 只改点位级字节序也必须改变配置校验和。
//
// 这是「改了布局没生效」的直接成因：采集任务持有构建期那一份 po.DeviceAddress，
// 校验和不变就不会热加载，驱动一直按旧布局（通常回落设备级）解码。与
// TestLoadConfigChecksumIncludesTransform 同一条约束，字段不同而已。
func TestLoadConfigChecksumIncludesByteOrder(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "checksum_byteorder.db")
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
		DataType: "float32", RwPermission: "R", Status: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	engine := NewEngine(db, nil, nil, nil, nil, nil)
	before := engine.loadConfigChecksum()

	if err := db.Model(&po.DeviceAddress{}).Where("id = ?", "addr-1").
		Update("byte_order", "CDAB").Error; err != nil {
		t.Fatal(err)
	}
	after := engine.loadConfigChecksum()

	if before == after {
		t.Fatal("byte_order 未参与配置校验和，热加载检测不到布局变更")
	}
}
