// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import "testing"

// TestMigration018SystemConfigSchema 钉住 018 迁移建出的运行期配置覆盖表。
//
// 表**不播种**：没有行 = 使用 default.yaml。这条语义由 runtimecfg 的测试覆盖，
// 这里只验 DDL：列齐全、key 是主键、迁移可重复执行。
func TestMigration018SystemConfigSchema(t *testing.T) {
	db := newTestDB(t)

	assertTableExists(t, db, "system_config", []string{
		"key", "value", "updated_by", "updated_at",
	})

	var cols []struct {
		Name string
		PK   int
	}
	if err := db.Raw("PRAGMA table_info(system_config)").Scan(&cols).Error; err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	pk := make(map[string]int, len(cols))
	for _, c := range cols {
		pk[c.Name] = c.PK
	}
	if pk["key"] == 0 {
		t.Fatalf("key 应是主键，实际 pk=%+v", pk)
	}

	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM system_config").Scan(&n).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 0 {
		t.Fatalf("迁移不应播种任何行（没有行才代表使用 default.yaml），实际 %d 行", n)
	}

	var applied int64
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		"018_system_config.sql").Scan(&applied).Error; err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("018_system_config.sql 未登记到 schema_migrations")
	}

	// 迁移按文件名幂等执行：再跑一次不应失败、也不应重复登记。
	if err := runMigrations(db); err != nil {
		t.Fatalf("重复执行迁移失败: %v", err)
	}
}
