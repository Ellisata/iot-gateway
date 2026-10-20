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
		"device_name", "device_address_name", "device_address_label",
		"data_type", "kind", "protocol",
		"first_seen_at", "last_seen_at", "created_at", "updated_at",
	})

	var cols []struct {
		Name string
		PK   int
	}
	if err := db.Raw("PRAGMA table_info(mqtt_subscription_device_point)").Scan(&cols).Error; err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	pk := make(map[string]int, len(cols))
	for _, c := range cols {
		pk[c.Name] = c.PK
	}
	if pk["device_id"] == 0 || pk["device_address_id"] == 0 {
		t.Fatalf("device_id 与 device_address_id 应组成联合主键，实际 pk=%+v", pk)
	}
}
