// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import "testing"

// TestMigration031PushOutboxTopicCode 钉住断网缓存补发所需的主题编码列。
func TestMigration031PushOutboxTopicCode(t *testing.T) {
	db := newTestDB(t)

	assertTableExists(t, db, "push_outbox", []string{
		"device_code", "thing_model_code", "group_code",
	})

	// 旧行默认空串，回放时从 records_json 回退；这里只钉住默认值，避免
	// 升级时因为 NOT NULL 无默认值而失败。
	if err := db.Exec(`INSERT INTO push_outbox (channel_id, device_id, collected_at, records_json)
		VALUES ('c1', 'd1', 't1', '[]')`).Error; err != nil {
		t.Fatalf("insert outbox row: %v", err)
	}
	var codes struct {
		DeviceCode     string `gorm:"column:device_code"`
		ThingModelCode string `gorm:"column:thing_model_code"`
		GroupCode      string `gorm:"column:group_code"`
	}
	if err := db.Raw(`SELECT device_code, thing_model_code, group_code
		FROM push_outbox WHERE channel_id = 'c1'`).Scan(&codes).Error; err != nil {
		t.Fatalf("query codes: %v", err)
	}
	if codes.DeviceCode != "" || codes.ThingModelCode != "" || codes.GroupCode != "" {
		t.Fatalf("defaults = %+v, want empty strings", codes)
	}
}
