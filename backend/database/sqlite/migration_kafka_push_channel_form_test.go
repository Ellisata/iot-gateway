// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"encoding/json"
	"testing"
)

// TestMigration035SeedsKafkaForm 钉住 Kafka 动态表单：字段齐全、双语可用、
// 重复执行幂等（表单设计器里的运行时定制不被覆盖）。
func TestMigration035SeedsKafkaForm(t *testing.T) {
	db := newTestDB(t)

	var applied int64
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		"035_add_kafka_push_channel_form.sql").Scan(&applied).Error; err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("035 未登记到 schema_migrations，实际 %d 条", applied)
	}

	var raw string
	if err := db.Table("push_channel_form").Select("form_json").
		Where("name = ?", "kafka").Scan(&raw).Error; err != nil {
		t.Fatalf("query kafka form: %v", err)
	}
	if raw == "" {
		t.Fatal("kafka 表单不存在")
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
	for _, want := range []string{
		"brokers", "topic", "clientId", "acks", "allowAutoTopicCreation",
		"authType", "saslUsername", "saslPassword", "tlsEnabled",
		"insecureSkipVerify", "timeoutSeconds", "workers",
		"spoolDisabled", "spoolMaxBatches",
	} {
		if _, ok := fields[want]; !ok {
			t.Errorf("kafka 表单缺少字段 %s", want)
		}
	}

	for _, lang := range []string{"zh-cn", "en"} {
		if len(form.Options.Language[lang]) == 0 {
			t.Errorf("kafka 表单缺少 %s 文案", lang)
		}
	}

	// 重复执行迁移文件里的语句：INSERT OR IGNORE 不应产生第二行。
	content, err := migrationsFS.ReadFile("migrations/035_add_kafka_push_channel_form.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, stmt := range splitSQL(string(content)) {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("re-run statement: %v", err)
		}
	}

	var count int64
	if err := db.Table("push_channel_form").Where("name = ?", "kafka").Count(&count).Error; err != nil {
		t.Fatalf("count kafka forms: %v", err)
	}
	if count != 1 {
		t.Fatalf("kafka 表单行数 = %d, want 1", count)
	}
}
