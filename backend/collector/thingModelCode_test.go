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

// TestToRecordsCarriesThingModelCodes 采集记录必须带上设备物模型编码与点位属性编码。
func TestToRecordsCarriesThingModelCodes(t *testing.T) {
	task := &gatewayTask{
		protocols:     map[string]string{"dev-1": "ModBus.TCP"},
		modelCodes:    map[string]string{"dev-1": "th_sensor"},
		addrPropCodes: map[string]string{"addr-1": "temperature"},
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
	if recs[1].ThingModelCode != "th_sensor" || recs[1].PropertyCode != "" {
		t.Errorf("model-only record codes = (%q,%q), want (th_sensor,\"\")",
			recs[1].ThingModelCode, recs[1].PropertyCode)
	}

	// 索引未对齐的防御路径（异常驱动）也必须能按点位 ID 取到属性编码。
	reordered := []driver.ReadResult{results[1], results[0]}
	recs = task.toRecords(dev, addrs, reordered)
	if recs[1].PropertyCode != "temperature" {
		t.Errorf("fallback path property code = %q, want temperature", recs[1].PropertyCode)
	}
}

// TestLoadThingModelCodes 绑定关系解析：只取设备所绑模型的编码；属性必须属于
// 该模型才生效（防历史数据/直接改库留下的跨模型绑定）。
func TestLoadThingModelCodes(t *testing.T) {
	db := openThingModelTestDB(t, &po.Device{}, &po.ThingModel{}, &po.ThingModelProperty{})
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
	devices := []po.Device{
		{ID: "dev-1", Name: "sensor", ModelID: "m1"},
		{ID: "dev-2", Name: "unbound"},
		{ID: "dev-3", Name: "dangling", ModelID: "missing-model"},
	}

	modelCodes, propCodes := loadThingModelCodes(db, devices)
	if modelCodes["dev-1"] != "th_sensor" {
		t.Errorf("dev-1 model code = %q, want th_sensor", modelCodes["dev-1"])
	}
	if _, ok := modelCodes["dev-2"]; ok {
		t.Error("unbound device must not have a model code")
	}
	if _, ok := modelCodes["dev-3"]; ok {
		t.Error("device bound to a missing model must not have a model code")
	}
	if p := propCodes["p1"]; p.code != "temperature" || p.modelID != "m1" {
		t.Errorf("p1 = %+v, want {m1 temperature}", p)
	}
	// 查询按「设备所绑模型」收敛：没有任何设备绑 m2，故 p2 不该被加载
	// （这也是避免按上千个属性 ID 拼 IN 的原因）。
	if _, ok := propCodes["p2"]; ok {
		t.Error("p2 belongs to an unbound model and should not be loaded")
	}
}

// TestLoadThingModelCodesMissingTables 物模型表不存在（旧库/单测库）时只退化为
// 没有编码，绝不返回错误让采集停摆。
func TestLoadThingModelCodesMissingTables(t *testing.T) {
	db := openThingModelTestDB(t, &po.Device{})
	modelCodes, propCodes := loadThingModelCodes(db, []po.Device{{ID: "dev-1", ModelID: "m1"}})
	if len(modelCodes) != 0 || len(propCodes) != 0 {
		t.Errorf("missing tables should degrade to empty maps, got %v / %v", modelCodes, propCodes)
	}
}
