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

// 属性标识符改名也必须改变配置校验和。
//
// 记录里的 propertyCode 是任务构建期从 thing_model_property 快照下来的；
// 改名不会改动 device_address 任何列，只靠地址表算校验和就感知不到，推送与
// 订阅会一直用旧标识符。与 TestLoadConfigChecksumIncludesByteOrder 同一条约束。
func TestLoadConfigChecksumIncludesPropertyIdentifier(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "checksum_propertycode.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		defer sqlDB.Close() // 先于 t.TempDir 清理执行，否则 Windows 上删不掉文件
	}
	for _, m := range []interface{}{
		&po.Device{}, &po.DeviceAddress{}, &po.ThingModelProperty{}, &po.CollectorGroup{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	if err := db.Create(&po.Device{ID: "dev-1", Name: "dev1", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&po.ThingModelProperty{
		ID: "prop-1", ModelID: "model-1", Identifier: "temperature",
		Name: "温度", Kind: "float", AccessMode: "R", Status: 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&po.DeviceAddress{
		ID: "addr-1", DeviceID: "dev-1", Name: "40001",
		DataType: "float32", RwPermission: "R", Status: 1, PropertyID: "prop-1",
	}).Error; err != nil {
		t.Fatal(err)
	}

	engine := NewEngine(db, nil, nil, nil, nil, nil)
	before := engine.loadConfigChecksum()

	if err := db.Model(&po.ThingModelProperty{}).Where("id = ?", "prop-1").
		Update("identifier", "temp_c").Error; err != nil {
		t.Fatal(err)
	}
	after := engine.loadConfigChecksum()

	if before == after {
		t.Fatal("属性标识符未参与配置校验和，热加载检测不到改名")
	}
}
