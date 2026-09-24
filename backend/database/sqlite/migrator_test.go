// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newMigrationTestDB 打开临时库并执行完整迁移链。
//
// 迁移器按 ASCII 分号朴素切分 SQL（见 migrator.go 的 splitSQL），因此
// 迁移文件里任何字符串字面量中混入分号都会在此处直接报错——这正是本测试的价值。
func newMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "migrate.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

func TestMigrationsApplyCleanlyAndIdempotently(t *testing.T) {
	db := newMigrationTestDB(t)

	for _, table := range []string{"alarm", "alarm_webhook", "alarm_webhook_form", "push_channel"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected table %q to exist after migrations", table)
		}
	}

	// 再次执行：全部已记录在 schema_migrations，应当直接跳过而不报错
	if err := runMigrations(db); err != nil {
		t.Fatalf("re-running migrations should be a no-op, got: %v", err)
	}
}

// form_json 是纯文本列，SQL 层无法校验其结构，写坏了只能等前端发现，
// 因此在这里把结构断言补上。
func TestAlarmWebhookFormSeedIsValid(t *testing.T) {
	db := newMigrationTestDB(t)

	type formRow struct {
		Name     string `gorm:"column:name"`
		FormJSON string `gorm:"column:form_json"`
	}
	var rows []formRow
	if err := db.Table("alarm_webhook_form").Select("name, form_json").Find(&rows).Error; err != nil {
		t.Fatalf("query forms: %v", err)
	}

	// 类型 -> 表单必须包含的字段
	wantFields := map[string][]string{
		"dingtalk": {"url", "secret", "msgType", "atAll", "atList"},
		"wecom":    {"url", "msgType"},
		"feishu":   {"url", "secret", "msgType", "atAll", "atList"},
		"custom":   {"url", "secret"},
	}

	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		required, ok := wantFields[r.Name]
		if !ok {
			t.Fatalf("unexpected alarm_webhook_form row %q", r.Name)
		}
		seen[r.Name] = true

		var doc struct {
			Rule []struct {
				Field    string `json:"field"`
				Title    string `json:"title"`
				Required bool   `json:"$required"`
			} `json:"rule"`
			Options json.RawMessage `json:"options"`
		}
		if err := json.Unmarshal([]byte(r.FormJSON), &doc); err != nil {
			t.Fatalf("form %q is not valid JSON: %v", r.Name, err)
		}
		if len(doc.Options) == 0 || string(doc.Options) == "null" {
			t.Fatalf("form %q is missing the options block", r.Name)
		}

		fields := make(map[string]bool, len(doc.Rule))
		urlRequired := false
		for _, rule := range doc.Rule {
			if rule.Field == "" || rule.Title == "" {
				t.Fatalf("form %q has a rule with empty field/title: %+v", r.Name, rule)
			}
			fields[rule.Field] = true
			if rule.Field == "url" && rule.Required {
				urlRequired = true
			}
		}
		for _, f := range required {
			if !fields[f] {
				t.Fatalf("form %q is missing field %q", r.Name, f)
			}
		}
		// URL 是唯一的必填项：没有它这条配置根本不可用
		if !urlRequired {
			t.Fatalf("form %q should mark the url field as required", r.Name)
		}
	}

	for name := range wantFields {
		if !seen[name] {
			t.Fatalf("no seeded form row for webhook type %q", name)
		}
	}
}
