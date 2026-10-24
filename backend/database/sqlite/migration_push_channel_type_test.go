// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"encoding/json"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestMigration020AddsTypeAndKeepsLegacyRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := bootstrapSchemaMigrations(db); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// 迁移前的旧表：name 唯一，没有 type 列。
	if err := db.Exec(`CREATE TABLE push_channel (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		description TEXT,
		config_json TEXT NOT NULL,
		status INTEGER NOT NULL DEFAULT 1,
		created_at TEXT,
		updated_at TEXT
	)`).Error; err != nil {
		t.Fatalf("create old table: %v", err)
	}
	if err := db.Exec(`INSERT INTO push_channel
		(id, name, description, config_json, status, created_at, updated_at)
		VALUES ('c1', 'mqtt', 'old', '{"broker":"h"}', 1, '2026-01-01 00:00:00', '2026-01-01 00:00:00')`).Error; err != nil {
		t.Fatalf("insert old row: %v", err)
	}

	if err := applyMigration(db, "020_rebuild_push_channel_with_type.sql"); err != nil {
		t.Fatalf("apply 020: %v", err)
	}

	var row struct {
		ID         string
		Name       string
		Type       string
		ConfigJSON string `gorm:"column:config_json"`
	}
	if err := db.Table("push_channel").Select("id, name, type, config_json").Where("id = ?", "c1").Scan(&row).Error; err != nil {
		t.Fatalf("select row: %v", err)
	}
	if row.Name != "mqtt" || row.Type != "mqtt" {
		t.Fatalf("legacy row = %+v, want name/type mqtt", row)
	}
	if row.ConfigJSON != `{"broker":"h"}` {
		t.Errorf("config_json = %q", row.ConfigJSON)
	}

	// (type, name) 唯一：同类型同名拒绝，不同类型同名允许。
	if err := db.Exec(`INSERT INTO push_channel (id, name, type, config_json, status)
		VALUES ('c2', 'mqtt', 'mqtt', '{}', 1)`).Error; err == nil {
		t.Error("duplicate (type,name) should fail")
	}
	if err := db.Exec(`INSERT INTO push_channel (id, name, type, config_json, status)
		VALUES ('c3', 'mqtt', 'http', '{}', 1)`).Error; err != nil {
		t.Errorf("same name with different type should be allowed: %v", err)
	}
}

func TestMigration021SeedsHTTPFormWithoutClobbering(t *testing.T) {
	db := newTestDB(t)

	var formJSON string
	if err := db.Table("push_channel_form").Select("form_json").Where("name = ?", "http").Scan(&formJSON).Error; err != nil {
		t.Fatalf("select http form: %v", err)
	}
	if formJSON == "" {
		t.Fatal("http form not seeded")
	}
	var form struct {
		Rule []struct {
			Field string `json:"field"`
		} `json:"rule"`
	}
	if err := json.Unmarshal([]byte(formJSON), &form); err != nil {
		t.Fatalf("form_json parse: %v", err)
	}
	fields := make(map[string]bool)
	for _, r := range form.Rule {
		fields[r.Field] = true
	}
	for _, want := range []string{"url", "method", "authType", "timeoutSeconds", "spoolDisabled"} {
		if !fields[want] {
			t.Errorf("http form missing field %q", want)
		}
	}

	// 重放迁移不得覆盖表单设计器的运行时定制。
	if err := db.Exec(`UPDATE push_channel_form SET form_json = 'sentinel' WHERE name = 'http'`).Error; err != nil {
		t.Fatalf("set sentinel: %v", err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version = '021_add_http_push_channel_form.sql'`).Error; err != nil {
		t.Fatalf("delete migration row: %v", err)
	}
	if err := applyMigration(db, "021_add_http_push_channel_form.sql"); err != nil {
		t.Fatalf("replay 021: %v", err)
	}
	var after string
	if err := db.Table("push_channel_form").Select("form_json").Where("name = ?", "http").Scan(&after).Error; err != nil {
		t.Fatalf("select after replay: %v", err)
	}
	if after != "sentinel" {
		t.Errorf("replay overwrote customized form: %q", after)
	}
}

func TestMigration022SeedsOpcUAFormWithoutClobbering(t *testing.T) {
	db := newTestDB(t)

	var formJSON string
	if err := db.Table("push_channel_form").Select("form_json").Where("name = ?", "opcua").Scan(&formJSON).Error; err != nil {
		t.Fatalf("select opcua form: %v", err)
	}
	if formJSON == "" {
		t.Fatal("opcua form not seeded")
	}
	var form struct {
		Rule []struct {
			Field string `json:"field"`
		} `json:"rule"`
	}
	if err := json.Unmarshal([]byte(formJSON), &form); err != nil {
		t.Fatalf("form_json parse: %v", err)
	}
	fields := make(map[string]bool)
	for _, r := range form.Rule {
		fields[r.Field] = true
	}
	for _, want := range []string{"listenHost", "listenPort", "securityPolicy", "nodeIdStyle", "deviceIds", "advertiseHosts"} {
		if !fields[want] {
			t.Errorf("opcua form missing field %q", want)
		}
	}

	// 重放迁移不得覆盖表单设计器的运行时定制。
	if err := db.Exec(`UPDATE push_channel_form SET form_json = 'sentinel' WHERE name = 'opcua'`).Error; err != nil {
		t.Fatalf("set sentinel: %v", err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version = '022_add_opcua_push_channel_form.sql'`).Error; err != nil {
		t.Fatalf("delete migration row: %v", err)
	}
	if err := applyMigration(db, "022_add_opcua_push_channel_form.sql"); err != nil {
		t.Fatalf("replay 022: %v", err)
	}
	var after string
	if err := db.Table("push_channel_form").Select("form_json").Where("name = ?", "opcua").Scan(&after).Error; err != nil {
		t.Fatalf("select after replay: %v", err)
	}
	if after != "sentinel" {
		t.Errorf("replay overwrote customized form: %q", after)
	}
}
