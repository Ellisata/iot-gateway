// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import "testing"

// TestMigration026MqttControlTopic 钉住 MQTT 下行控制主题列。
//
// 生产只跑 migrations/*.sql（没有 AutoMigrate），列名写错时订阅页保存会
// 在运行期报 "no such column"，而单测全绿。
func TestMigration026MqttControlTopic(t *testing.T) {
	db := newTestDB(t)

	var cols []struct {
		Name string
	}
	if err := db.Raw("PRAGMA table_info(mqtt_subscription)").Scan(&cols).Error; err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	found := false
	for _, c := range cols {
		if c.Name == "control_topic" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("mqtt_subscription 缺少 control_topic 列，实际列: %+v", cols)
	}

	var applied int64
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		"026_mqtt_subscription_control_topic.sql").Scan(&applied).Error; err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("026_mqtt_subscription_control_topic.sql 未登记到 schema_migrations")
	}

	if err := runMigrations(db); err != nil {
		t.Fatalf("重复执行迁移失败: %v", err)
	}
}
