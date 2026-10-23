// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import "testing"

// TestMigration017MqttSubscriptionDevicePointSchema 钉住 017 迁移建出的
// 订阅设备/点位信息索引表（含自然键）。
func TestMigration017MqttSubscriptionDevicePointSchema(t *testing.T) {
	db := newTestDB(t)

	assertTableExists(t, db, "mqtt_subscription_device_point", []string{
		"device_id", "device_address_id",
		"device_name", "device_address_label",
		"kind", "source", "created_at", "updated_at",
	})

	var cols []struct {
		Name       string
		PK         int
		NotNull    int    `gorm:"column:notnull"`
		DefaultVal string `gorm:"column:dflt_value"`
	}
	if err := db.Raw("PRAGMA table_info(mqtt_subscription_device_point)").Scan(&cols).Error; err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	pk := make(map[string]int, len(cols))
	sourceFound := false
	for _, c := range cols {
		pk[c.Name] = c.PK
		if c.Name == "source" {
			sourceFound = true
			if c.NotNull != 1 {
				t.Errorf("source.NotNull = %d, want 1", c.NotNull)
			}
			if c.DefaultVal != "'auto'" {
				t.Errorf("source default = %q, want 'auto'", c.DefaultVal)
			}
		}
	}
	if pk["device_id"] == 0 || pk["device_address_id"] == 0 {
		t.Fatalf("device_id 与 device_address_id 应组成联合主键，实际 pk=%+v", pk)
	}
	if !sourceFound {
		t.Fatal("source 列不存在")
	}

	// 不显式写 source 时落默认值（自动上报路径依赖它区分手工行）。
	if err := db.Exec("INSERT INTO mqtt_subscription_device_point (device_id, device_address_id) VALUES ('d1', 'a1')").Error; err != nil {
		t.Fatalf("insert row: %v", err)
	}
	var source string
	if err := db.Raw("SELECT source FROM mqtt_subscription_device_point WHERE device_id = 'd1'").Scan(&source).Error; err != nil {
		t.Fatalf("select source: %v", err)
	}
	if source != "auto" {
		t.Errorf("source = %q, want auto", source)
	}
}
