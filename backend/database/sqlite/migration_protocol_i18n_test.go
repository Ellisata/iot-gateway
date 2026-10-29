// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"
)

type protocolFormFixture struct {
	Tag       string            `json:"tag"`
	Protocols map[string]string `json:"protocols"`
}

func loadProtocolFormFixture(t *testing.T) protocolFormFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/protocol_forms_v0.1.0.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture protocolFormFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(fixture.Protocols) != 15 {
		t.Fatalf("fixture has %d protocol forms, want 15", len(fixture.Protocols))
	}
	return fixture
}

func insertProtocolRow(t *testing.T, db *gorm.DB, id, name, form string) {
	t.Helper()
	if err := db.Exec(`INSERT INTO iot_protocol (id, name, description, form_json, sort, created_at, updated_at)
		VALUES (?, ?, '', ?, 100, '2026-01-01 00:00:00', '2026-01-01 00:00:00')`, id, name, form).Error; err != nil {
		t.Fatalf("insert protocol %s: %v", name, err)
	}
}

func readProtocolForm(t *testing.T, db *gorm.DB, name string) string {
	t.Helper()
	var form string
	if err := db.Table("iot_protocol").Select("form_json").Where("name = ?", name).Scan(&form).Error; err != nil {
		t.Fatalf("read protocol %s: %v", name, err)
	}
	return form
}

// TestMigration024LocalizesProtocolForms 覆盖真实迁移器路径：024 把 v0.1.0 的中文
// 协议表单升级成 {{$t.key}} + options.language，同时
//   - 兼容 CRLF 存进去的旧行；
//   - 不碰运维定制过的行；
//   - 重放不会覆盖已经升级/再次改过的行。
func TestMigration024LocalizesProtocolForms(t *testing.T) {
	db := newTestDB(t)
	fixture := loadProtocolFormFixture(t)

	i := 0
	for protocol, form := range fixture.Protocols {
		// 第一条模拟 Windows 侧写进 CRLF 的库。
		if i == 0 {
			form = strings.ReplaceAll(form, "\n", "\r\n")
		}
		insertProtocolRow(t, db, "p-"+protocol, protocol, form)
		i++
	}
	const customName = "Custom.Protocol"
	const customForm = `{"rule":[{"type":"input","field":"x","title":"自定义"}]}`
	insertProtocolRow(t, db, "p-custom", customName, customForm)

	// 重新执行 024（newTestDB 已经跑过一次，此时没有任何协议行）。
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version = '024_seed_protocol_form_i18n.sql'`).Error; err != nil {
		t.Fatalf("reset migration record: %v", err)
	}
	if err := applyMigration(db, "024_seed_protocol_form_i18n.sql"); err != nil {
		t.Fatalf("apply 024: %v", err)
	}

	for protocol := range fixture.Protocols {
		form := readProtocolForm(t, db, protocol)
		if !strings.Contains(form, `"language"`) {
			t.Errorf("%s 没有 language 表", protocol)
			continue
		}
		var parsed struct {
			Rule    []map[string]any `json:"rule"`
			Options struct {
				Language map[string]map[string]string `json:"language"`
			} `json:"options"`
		}
		if err := json.Unmarshal([]byte(form), &parsed); err != nil {
			t.Errorf("%s 不是合法 JSON: %v", protocol, err)
			continue
		}
		if len(parsed.Options.Language["zh-cn"]) == 0 || len(parsed.Options.Language["en"]) == 0 {
			t.Errorf("%s 缺少 zh-cn/en 译文", protocol)
		}
		for _, rule := range parsed.Rule {
			title, _ := rule["title"].(string)
			if strings.Contains(title, "{{$t.") {
				key := strings.TrimSuffix(strings.TrimPrefix(title, "{{$t."), "}}")
				if parsed.Options.Language["en"][key] == "" {
					t.Errorf("%s 的 title %q 没有英文译文", protocol, title)
				}
			}
		}
	}

	if got := readProtocolForm(t, db, customName); got != customForm {
		t.Errorf("定制行被改了: %q", got)
	}

	// 重放：已升级的行与再次被改过的行都不应被覆盖。
	changedForm := readProtocolForm(t, db, "ModBus.TCP")
	modified := strings.Replace(changedForm, `"language"`, `"language_kept"`, 1)
	if err := db.Exec(`UPDATE iot_protocol SET form_json = ? WHERE name = ?`, modified, "ModBus.TCP").Error; err != nil {
		t.Fatalf("modify row: %v", err)
	}
	if err := db.Exec(`DELETE FROM schema_migrations WHERE version = '024_seed_protocol_form_i18n.sql'`).Error; err != nil {
		t.Fatalf("reset migration record: %v", err)
	}
	if err := applyMigration(db, "024_seed_protocol_form_i18n.sql"); err != nil {
		t.Fatalf("re-apply 024: %v", err)
	}
	if got := readProtocolForm(t, db, "ModBus.TCP"); got != modified {
		t.Errorf("重放覆盖了已改过的行")
	}
	if got := readProtocolForm(t, db, customName); got != customForm {
		t.Errorf("重放覆盖了定制行")
	}
}

// TestMigration024StatementCount 迁移器按 ASCII 分号朴素切分：文件里多一个分号
// 就会多出一条语句（或把一条语句切坏）。这里用真实 splitSQL 钉住语句数。
func TestMigration024StatementCount(t *testing.T) {
	raw, err := migrationsFS.ReadFile("migrations/024_seed_protocol_form_i18n.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	stmts := splitSQL(string(raw))
	if len(stmts) != 15 {
		t.Fatalf("024 split into %d statements, want 15", len(stmts))
	}
}
