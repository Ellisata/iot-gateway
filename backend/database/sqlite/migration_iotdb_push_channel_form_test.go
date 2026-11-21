// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"encoding/json"
	"testing"
)

// TestMigration037SeedsIotdbForm 钉住 IoTDB 动态表单：字段齐全、双语可用、
// 重复执行幂等（表单设计器里的运行时定制不被覆盖）。
func TestMigration037SeedsIotdbForm(t *testing.T) {
	db := newTestDB(t)

	var applied int64
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		"037_add_iotdb_push_channel_form.sql").Scan(&applied).Error; err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("037 未登记到 schema_migrations，实际 %d 条", applied)
	}

	var raw string
	if err := db.Table("push_channel_form").Select("form_json").
		Where("name = ?", "iotdb-v2").Scan(&raw).Error; err != nil {
		t.Fatalf("query iotdb form: %v", err)
	}
	if raw == "" {
		t.Fatal("iotdb-v2 表单不存在")
	}

	var form struct {
		Rule []struct {
			Field   string            `json:"field"`
			Type    string            `json:"type"`
			Value   any               `json:"value"`
			Display *bool             `json:"display"`
			Props   map[string]any    `json:"props"`
			Control []json.RawMessage `json:"control"`
		} `json:"rule"`
		Options struct {
			Language map[string]map[string]string `json:"language"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(raw), &form); err != nil {
		t.Fatalf("unmarshal form: %v", err)
	}

	fields := make(map[string]int, len(form.Rule))
	for i, rule := range form.Rule {
		fields[rule.Field] = i
	}
	for _, want := range []string{
		"host", "port", "username", "password", "database", "nodes",
		"rpcCompression", "tlsEnabled", "tlsCAFile", "tlsCertFile", "tlsKeyFile",
		"insecureSkipVerify", "spoolDisabled", "spoolMaxBatches",
	} {
		if _, ok := fields[want]; !ok {
			t.Errorf("iotdb-v2 表单缺少字段 %s", want)
		}
	}

	// 证书字段由「启用 TLS」开关联动，默认关闭时全部隐藏。
	swIdx, ok := fields["tlsEnabled"]
	if !ok {
		t.Fatal("iotdb-v2 表单缺少 tlsEnabled 开关")
	}
	sw := form.Rule[swIdx]
	if sw.Type != "switch" {
		t.Errorf("tlsEnabled type = %q, want switch", sw.Type)
	}
	if sw.Value != false {
		t.Errorf("tlsEnabled 默认值 = %v, want false", sw.Value)
	}
	if len(sw.Control) != 1 {
		t.Fatalf("tlsEnabled control 条数 = %d, want 1", len(sw.Control))
	}
	// 密码框允许切换明文显示（Element Plus el-input 的 show-password）。
	pwdIdx, ok := fields["password"]
	if !ok {
		t.Fatal("iotdb-v2 表单缺少 password 字段")
	}
	if got, _ := form.Rule[pwdIdx].Props["type"].(string); got != "password" {
		t.Errorf("password props.type = %q, want password", got)
	}
	if show, _ := form.Rule[pwdIdx].Props["showPassword"].(bool); !show {
		t.Error("password 应支持切换明文显示（props.showPassword=true）")
	}

	for _, field := range []string{"tlsCAFile", "tlsCertFile", "tlsKeyFile", "insecureSkipVerify"} {
		i, ok := fields[field]
		if !ok {
			continue
		}
		if form.Rule[i].Display == nil || *form.Rule[i].Display {
			t.Errorf("%s 应默认隐藏（display=false）", field)
		}
	}

	for _, lang := range []string{"zh-cn", "en"} {
		if len(form.Options.Language[lang]) == 0 {
			t.Errorf("iotdb-v2 表单缺少 %s 文案", lang)
		}
		if form.Options.Language[lang]["tlsEnabled_title"] == "" {
			t.Errorf("iotdb-v2 表单缺少 %s 的 tlsEnabled_title", lang)
		}
		if form.Options.Language[lang]["tlsEnabled_info"] == "" {
			t.Errorf("iotdb-v2 表单缺少 %s 的 tlsEnabled_info", lang)
		}
	}

	// 重复执行迁移文件里的语句：INSERT OR IGNORE 不应产生第二行。
	content, err := migrationsFS.ReadFile("migrations/037_add_iotdb_push_channel_form.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, stmt := range splitSQL(string(content)) {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("re-run statement: %v", err)
		}
	}

	var count int64
	if err := db.Table("push_channel_form").Where("name = ?", "iotdb-v2").Count(&count).Error; err != nil {
		t.Fatalf("count iotdb forms: %v", err)
	}
	if count != 1 {
		t.Fatalf("iotdb-v2 表单行数 = %d, want 1", count)
	}
}
