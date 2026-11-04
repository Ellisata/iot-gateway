// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import "testing"

// TestMigration033TriggerRuleSchema 钉住事件触发规则迁移在真实 SQL 路径上建出的结构。
func TestMigration033TriggerRuleSchema(t *testing.T) {
	db := newTestDB(t)

	assertTableExists(t, db, "trigger_rule", []string{
		"id", "code", "name", "description", "status", "action_device_id", "action_address_ids",
		"target_channel_id", "conditions_json", "cooldown_ms", "timeout_ms",
		"created_at", "updated_at",
	})
	assertTableExists(t, db, "trigger_rule_record", []string{
		"id", "trigger_id", "rule_id", "rule_name", "rule_code", "fired_at", "status",
		"condition_json", "action_result_json", "duration_ms", "error", "created_at",
	})

	for _, idx := range []string{
		"idx_trigger_rule_name",
		"idx_trigger_rule_code",
		"idx_trigger_rule_status",
		"idx_trigger_rule_record_trigger",
		"idx_trigger_rule_record_rule_time",
		"idx_trigger_rule_record_fired_at",
	} {
		var n int64
		if err := db.Raw(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", idx).Scan(&n).Error; err != nil {
			t.Fatalf("query index %s: %v", idx, err)
		}
		if n != 1 {
			t.Fatalf("缺少索引 %s", idx)
		}
	}

	// 规则名称与编码全局唯一；记录 trigger_id 唯一。
	if err := db.Exec(`INSERT INTO trigger_rule
		(id, code, name, action_device_id, target_channel_id)
		VALUES ('r1', 'rule-a-code', 'rule-a', 'd1', 'c1')`).Error; err != nil {
		t.Fatalf("insert rule: %v", err)
	}
	if err := db.Exec(`INSERT INTO trigger_rule
		(id, code, name, action_device_id, target_channel_id)
		VALUES ('r2', 'rule-b-code', 'rule-a', 'd1', 'c1')`).Error; err == nil {
		t.Error("重复规则名应当失败")
	}
	if err := db.Exec(`INSERT INTO trigger_rule
		(id, code, name, action_device_id, target_channel_id)
		VALUES ('r3', 'rule-a-code', 'rule-b', 'd1', 'c1')`).Error; err == nil {
		t.Error("重复规则编码应当失败")
	}

	if err := db.Exec(`INSERT INTO trigger_rule_record
		(id, trigger_id, rule_id, rule_name, rule_code, fired_at, status)
		VALUES ('x1', 't1', 'r1', 'rule-a', 'rule-a-code', '2026-11-03 10:00:00', 'success')`).Error; err != nil {
		t.Fatalf("insert record: %v", err)
	}
	if err := db.Exec(`INSERT INTO trigger_rule_record
		(id, trigger_id, rule_id, rule_name, rule_code, fired_at, status)
		VALUES ('x2', 't1', 'r1', 'rule-a', 'rule-a-code', '2026-11-03 10:00:01', 'success')`).Error; err == nil {
		t.Error("重复 trigger_id 应当失败")
	}

	var applied int64
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		"033_trigger_rule.sql").Scan(&applied).Error; err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("033_trigger_rule.sql 未被记入 schema_migrations，实际 %d 条", applied)
	}
	applied = 0
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		"034_trigger_rule_code.sql").Scan(&applied).Error; err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("034_trigger_rule_code.sql 未被记入 schema_migrations，实际 %d 条", applied)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("重复执行迁移应幂等，实际报错：%v", err)
	}
}
