// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"encoding/json"
	"testing"
)

func TestMigration037SeedsIoTDBTreeForm(t *testing.T) {
	db := newTestDB(t)
	var applied int64
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		"037_add_iotdb_tree_push_channel_form.sql").Scan(&applied).Error; err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("037 未登记到 schema_migrations，实际 %d 条", applied)
	}

	var raw string
	if err := db.Table("push_channel_form").Select("form_json").
		Where("name = ?", "iotdb-tree").Scan(&raw).Error; err != nil {
		t.Fatalf("query IoTDB form: %v", err)
	}
	if raw == "" {
		t.Fatal("IoTDB 表单不存在")
	}
	var form struct {
		Rule []struct {
			Field string `json:"field"`
			Type  string `json:"type"`
		} `json:"rule"`
		Options struct {
			Language map[string]map[string]string `json:"language"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(raw), &form); err != nil {
		t.Fatalf("unmarshal form: %v", err)
	}
	fields := make(map[string]string, len(form.Rule))
	for _, rule := range form.Rule {
		fields[rule.Field] = rule.Type
	}
	for _, want := range []string{"host", "port", "username", "password", "database", "timeZone", "batchWorkers", "batchRows", "batchIntervalMs", "spoolDisabled", "spoolMaxBatches"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("IoTDB 表单缺少字段 %s", want)
		}
	}
	for _, lang := range []string{"zh-cn", "en"} {
		if len(form.Options.Language[lang]) == 0 {
			t.Errorf("IoTDB 表单缺少 %s 文案", lang)
		}
	}

	content, err := migrationsFS.ReadFile("migrations/037_add_iotdb_tree_push_channel_form.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, stmt := range splitSQL(string(content)) {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("re-run statement: %v", err)
		}
	}
	var count int64
	if err := db.Table("push_channel_form").Where("name = ?", "iotdb-tree").Count(&count).Error; err != nil {
		t.Fatalf("count IoTDB forms: %v", err)
	}
	if count != 1 {
		t.Fatalf("IoTDB 表单行数 = %d, want 1", count)
	}
}
