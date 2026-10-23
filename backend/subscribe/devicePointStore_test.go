// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/model/po"
)

func newDevicePointTestStore(t *testing.T) (*devicePointStore, *gorm.DB) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "subscribe_device_point_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&po.MqttSubscriptionDevicePoint{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return newDevicePointStore(db), db
}

// TestDevicePointStoreUpsertsMetadata 首次出现的点位写入完整元信息，
// Stop 时队列里的条目必须被刷出（停机不丢）。
func TestDevicePointStoreUpsertsMetadata(t *testing.T) {
	store, db := newDevicePointTestStore(t)
	store.Start()

	store.Record([]collector.CollectedRecord{{
		DeviceID:           "d1",
		DeviceName:         "电表1",
		DeviceAddressID:    "a1",
		DeviceAddressLabel: "A相电流",
		Kind:               "float",
		Value:              "10.2",
		Quality:            192,
	}})
	store.Stop()

	var rows []po.MqttSubscriptionDevicePoint
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("find rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.DeviceID != "d1" || got.DeviceAddressID != "a1" {
		t.Errorf("natural key = %s/%s, want d1/a1", got.DeviceID, got.DeviceAddressID)
	}
	if got.DeviceName != "电表1" || got.DeviceAddressLabel != "A相电流" || got.Kind != "float" {
		t.Errorf("metadata not persisted: %+v", got)
	}
	if got.CreatedAt == "" || got.UpdatedAt == "" {
		t.Error("created_at / updated_at should be set")
	}
	if got.Source != po.MqttSubscriptionDevicePointSourceAuto {
		t.Errorf("source = %q, want auto", got.Source)
	}
}

// TestDevicePointStoreProtectsManualRows 手工维护的行是运维的最终确认：
// 后续订阅报文不得覆盖元信息与 updated_at。
func TestDevicePointStoreProtectsManualRows(t *testing.T) {
	store, db := newDevicePointTestStore(t)
	manual := po.MqttSubscriptionDevicePoint{
		DeviceID:           "d1",
		DeviceAddressID:    "a1",
		DeviceName:         "手工名",
		DeviceAddressLabel: "手工标签",
		Kind:               "int",
		Source:             po.MqttSubscriptionDevicePointSourceManual,
		CreatedAt:          "2020-01-01 00:00:00",
		UpdatedAt:          "2020-01-01 00:00:00",
	}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("seed manual row: %v", err)
	}

	store.Start()
	store.Record([]collector.CollectedRecord{{
		DeviceID:           "d1",
		DeviceAddressID:    "a1",
		DeviceName:         "报文名",
		DeviceAddressLabel: "报文标签",
		Kind:               "float",
	}})
	store.Stop()

	var rows []po.MqttSubscriptionDevicePoint
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("find rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.DeviceName != "手工名" || got.DeviceAddressLabel != "手工标签" || got.Kind != "int" {
		t.Errorf("manual metadata must not be overwritten: %+v", got)
	}
	if got.Source != po.MqttSubscriptionDevicePointSourceManual {
		t.Errorf("source = %q, want manual", got.Source)
	}
	if got.UpdatedAt != "2020-01-01 00:00:00" {
		t.Errorf("updated_at = %q, want untouched", got.UpdatedAt)
	}
}

// TestDevicePointStoreKeepsNonEmptyMetadata 元信息变空时不得清掉库里已有的非空值
// （宽松加载的历史订阅可能没有配置名称字段），但非空的新值要正常覆盖。
func TestDevicePointStoreKeepsNonEmptyMetadata(t *testing.T) {
	store, db := newDevicePointTestStore(t)
	store.Start()

	base := collector.CollectedRecord{
		DeviceID:           "d1",
		DeviceName:         "电表1",
		DeviceAddressID:    "a1",
		DeviceAddressLabel: "A相电流",
		Kind:               "float",
	}
	store.Record([]collector.CollectedRecord{base})
	store.Stop() // 先落库，让第二次写入走 ON CONFLICT 更新路径

	empty := base
	empty.DeviceName = ""
	empty.DeviceAddressLabel = ""
	empty.Kind = "int" // 触发一次元信息变化
	store = newDevicePointStore(db)
	store.Start()
	store.Record([]collector.CollectedRecord{empty})
	store.Stop()

	var rows []po.MqttSubscriptionDevicePoint
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("find rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.DeviceName != "电表1" || got.DeviceAddressLabel != "A相电流" {
		t.Errorf("empty metadata must not overwrite existing names: %+v", got)
	}
	if got.Kind != "int" {
		t.Errorf("kind = %q, want int", got.Kind)
	}
}

// TestDevicePointStoreDedupesBatch 同一批里同一自然键只落一行，
// 且后一条的空元信息不会覆盖前一条的非空值。
func TestDevicePointStoreDedupesBatch(t *testing.T) {
	store, db := newDevicePointTestStore(t)
	store.Start()

	named := collector.CollectedRecord{
		DeviceID:        "d1",
		DeviceAddressID: "a1",
		DeviceName:      "电表1",
		Kind:            "float",
		Value:           "1",
	}
	blank := named
	blank.DeviceName = ""
	blank.Kind = "int"
	store.Record([]collector.CollectedRecord{named, blank, blank})
	store.Stop()

	var rows []po.MqttSubscriptionDevicePoint
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("find rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].DeviceName != "电表1" {
		t.Errorf("device_name = %q, want 电表1", rows[0].DeviceName)
	}
	if rows[0].Kind != "int" {
		t.Errorf("kind = %q, want int", rows[0].Kind)
	}
}

// TestDevicePointStoreForgetReRecords 作废指纹后，即使报文元信息没变，下一次
// Record 也会重新入队 —— 恢复自动收录 / 删除手工行后的即时生效依赖这一点。
func TestDevicePointStoreForgetReRecords(t *testing.T) {
	store, db := newDevicePointTestStore(t)
	rec := collector.CollectedRecord{
		DeviceID:        "d1",
		DeviceAddressID: "a1",
		DeviceName:      "报文名",
		Kind:            "float",
	}

	store.Start()
	store.Record([]collector.CollectedRecord{rec})
	store.Stop()

	// 模拟运维接管：改成手工维护并改了名称。指纹不变时 Record 必须跳过。
	if err := db.Model(&po.MqttSubscriptionDevicePoint{}).
		Where("device_id = ? AND device_address_id = ?", "d1", "a1").
		Updates(map[string]interface{}{
			"device_name": "手工名",
			"source":      po.MqttSubscriptionDevicePointSourceManual,
		}).Error; err != nil {
		t.Fatalf("simulate manual takeover: %v", err)
	}
	store.Start()
	store.Record([]collector.CollectedRecord{rec})
	store.Stop()

	var got po.MqttSubscriptionDevicePoint
	load := func() po.MqttSubscriptionDevicePoint {
		t.Helper()
		var row po.MqttSubscriptionDevicePoint
		if err := db.Where("device_id = ? AND device_address_id = ?", "d1", "a1").First(&row).Error; err != nil {
			t.Fatalf("reload row: %v", err)
		}
		return row
	}
	if got = load(); got.DeviceName != "手工名" {
		t.Fatalf("unchanged signature must not overwrite manual name, got %q", got.DeviceName)
	}

	// 模拟恢复自动收录：source 置回 auto 并作废指纹，报文值应重新覆盖。
	if err := db.Model(&po.MqttSubscriptionDevicePoint{}).
		Where("device_id = ? AND device_address_id = ?", "d1", "a1").
		Update("source", po.MqttSubscriptionDevicePointSourceAuto).Error; err != nil {
		t.Fatalf("simulate restore: %v", err)
	}
	store.Start()
	store.Forget([]DevicePointKey{{DeviceID: "d1", DeviceAddressID: "a1"}})
	store.Record([]collector.CollectedRecord{rec})
	store.Stop()

	if got = load(); got.DeviceName != "报文名" || got.Source != po.MqttSubscriptionDevicePointSourceAuto {
		t.Errorf("after Forget row = %+v, want name=报文名 source=auto", got)
	}
}

// TestDevicePointStoreSkipsIncompleteRecords 缺少设备/点位身份的记录不能建索引。
func TestDevicePointStoreSkipsIncompleteRecords(t *testing.T) {
	store, db := newDevicePointTestStore(t)
	store.Start()

	store.Record([]collector.CollectedRecord{
		{DeviceID: "d1", Value: "1"},
		{DeviceAddressID: "a1", Value: "1"},
	})
	store.Stop()

	var count int64
	if err := db.Model(&po.MqttSubscriptionDevicePoint{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("rows = %d, want 0", count)
	}
}
