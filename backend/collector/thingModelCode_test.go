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
)

// openThingModelTestDB 打开一个临时文件库并迁移物模型相关表。
// 用文件库而非 :memory:：内存 SQLite 每个连接一份，跨连接不可见。
func openThingModelTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "thingmodel.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { sqlDB.Close() })
	}
	return db
}

// TestToRecordsCarriesThingModelCodes 采集记录必须带上点位属性编码与属性所属模型编码。
func TestToRecordsCarriesThingModelCodes(t *testing.T) {
	task := &gatewayTask{
		protocols:      map[string]string{"dev-1": "ModBus.TCP"},
		addrModelCodes: map[string]string{"addr-1": "th_sensor"},
		addrPropCodes:  map[string]string{"addr-1": "temperature"},
	}
	dev := &po.Device{ID: "dev-1", Name: "dev1"}
	addrs := []po.DeviceAddress{
		{ID: "addr-1", Name: "40001", Label: "温度"},
		{ID: "addr-2", Name: "40002", Label: "未绑定"},
	}
	results := []driver.ReadResult{
		{DeviceAddressID: "addr-1", Value: "23.5", DataType: "float32", Kind: driver.KindFloat, Quality: 192},
		{DeviceAddressID: "addr-2", Value: "1", DataType: "bool", Kind: driver.KindBool, Quality: 192},
	}

	recs := task.toRecords(dev, addrs, results)
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2", len(recs))
	}
	if recs[0].ThingModelCode != "th_sensor" || recs[0].PropertyCode != "temperature" {
		t.Errorf("bound record codes = (%q,%q), want (th_sensor,temperature)",
			recs[0].ThingModelCode, recs[0].PropertyCode)
	}
	if recs[1].ThingModelCode != "" || recs[1].PropertyCode != "" {
		t.Errorf("unbound record codes = (%q,%q), want (\"\",\"\")",
			recs[1].ThingModelCode, recs[1].PropertyCode)
	}

	// 索引未对齐的防御路径（异常驱动）也必须能按点位 ID 取到属性编码。
	reordered := []driver.ReadResult{results[1], results[0]}
	recs = task.toRecords(dev, addrs, reordered)
	if recs[1].PropertyCode != "temperature" {
		t.Errorf("fallback path property code = %q, want temperature", recs[1].PropertyCode)
	}
}

// TestLoadThingModelCodes 属性编码解析：全部属性都加载，并带上所属模型编码；
// 设备不再参与（点位绑定的属性可以来自任意模型）。
func TestLoadThingModelCodes(t *testing.T) {
	db := openThingModelTestDB(t, &po.ThingModel{}, &po.ThingModelProperty{})
	for _, m := range []po.ThingModel{
		{ID: "m1", Code: "th_sensor", Name: "温湿度传感器"},
		{ID: "m2", Code: "th_meter", Name: "电表"},
	} {
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
	props := []po.ThingModelProperty{
		{ID: "p1", ModelID: "m1", Identifier: "temperature", Name: "温度", Kind: driver.KindFloat},
		{ID: "p2", ModelID: "m2", Identifier: "voltage", Name: "电压", Kind: driver.KindFloat},
	}
	if err := db.Create(&props).Error; err != nil {
		t.Fatal(err)
	}

	propCodes := loadThingModelCodes(db)
	if p := propCodes["p1"]; p.code != "temperature" || p.modelCode != "th_sensor" {
		t.Errorf("p1 = %+v, want {th_sensor temperature}", p)
	}
	if p := propCodes["p2"]; p.code != "voltage" || p.modelCode != "th_meter" {
		t.Errorf("p2 = %+v, want {th_meter voltage}", p)
	}
}

// TestLoadThingModelCodesMissingTables 物模型表不存在（旧库/单测库）时只退化为
// 没有编码，绝不返回错误让采集停摆。
func TestLoadThingModelCodesMissingTables(t *testing.T) {
	db := openThingModelTestDB(t, &po.Device{})
	propCodes := loadThingModelCodes(db)
	if len(propCodes) != 0 {
		t.Errorf("missing tables should degrade to an empty map, got %v", propCodes)
	}
}
