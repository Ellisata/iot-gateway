// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import "testing"

// TestMigration019MqttSubscriptionParserSchema 钉住 019 迁移新增的解析器列。
//
// builtin 沿用 mapping_json（历史数据零迁移），parser_type 的默认值保证
// 老行加载时仍按内置映射运行。
func TestMigration019MqttSubscriptionParserSchema(t *testing.T) {
	db := newTestDB(t)

	assertTableExists(t, db, "mqtt_subscription", []string{
		"id", "name", "broker", "topic", "mapping_json",
		"parser_type", "parser_config", "sample_topic",
	})

	var cols []struct {
		Name       string
		NotNull    int    `gorm:"column:notnull"`
		DefaultVal string `gorm:"column:dflt_value"`
	}
	if err := db.Raw("PRAGMA table_info(mqtt_subscription)").Scan(&cols).Error; err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	found := false
	for _, c := range cols {
		if c.Name != "parser_type" {
			continue
		}
		found = true
		if c.NotNull != 1 {
			t.Errorf("parser_type.NotNull = %d, want 1", c.NotNull)
		}
		if c.DefaultVal != "'builtin'" {
			t.Errorf("parser_type default = %q, want 'builtin'", c.DefaultVal)
		}
	}
	if !found {
		t.Fatal("parser_type 列不存在")
	}

	// 不显式写 parser_type 时按 builtin 落库（历史行/旧客户端路径）。
	if err := db.Exec(`INSERT INTO mqtt_subscription
		(id, name, broker, topic, mapping_json)
		VALUES ('s1', 'legacy', '127.0.0.1', 't', '{}')`).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	var parserType string
	if err := db.Raw("SELECT parser_type FROM mqtt_subscription WHERE id = 's1'").Scan(&parserType).Error; err != nil {
		t.Fatalf("select parser_type: %v", err)
	}
	if parserType != "builtin" {
		t.Errorf("parser_type = %q, want builtin", parserType)
	}

	// 脚本配置与样例主题可独立落库。
	if err := db.Exec(`INSERT INTO mqtt_subscription
		(id, name, broker, topic, mapping_json, parser_type, parser_config, sample_topic)
		VALUES ('s2', 'scripted', '127.0.0.1', 't', '{}', 'script', '{"script":"function parse(){}"}', 'factory/line1/telemetry')`).Error; err != nil {
		t.Fatalf("insert script row: %v", err)
	}
	var cfg, sampleTopic string
	if err := db.Raw("SELECT parser_config, sample_topic FROM mqtt_subscription WHERE id = 's2'").
		Row().Scan(&cfg, &sampleTopic); err != nil {
		t.Fatalf("select script row: %v", err)
	}
	if cfg == "" || sampleTopic != "factory/line1/telemetry" {
		t.Errorf("parser_config=%q sample_topic=%q", cfg, sampleTopic)
	}
}
