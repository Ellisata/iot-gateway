// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import "testing"

// TestMigration028CollectorGroupSchema 钉住采集组迁移在真实 SQL 路径上建出的结构。
func TestMigration028CollectorGroupSchema(t *testing.T) {
	db := newTestDB(t)

	assertTableExists(t, db, "collector_group", []string{
		"id", "device_id", "name", "interval_ms", "description", "status",
		"created_at", "updated_at",
	})

	for _, idx := range []string{
		"idx_collector_group_device_name",
		"idx_collector_group_device_status",
		"idx_device_address_group",
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

	// device_address.group_id 与 push_outbox 的组列：NOT NULL 且默认空/0，存量行无需回填。
	for _, c := range []struct{ table, col string }{
		{"device_address", "group_id"},
		{"push_outbox", "group_id"},
		{"push_outbox", "group_name"},
		{"push_outbox", "group_interval_ms"},
	} {
		type columnInfo struct {
			Name    string  `gorm:"column:name"`
			NotNull int     `gorm:"column:notnull"`
			Dflt    *string `gorm:"column:dflt_value"`
		}
		var cols []columnInfo
		if err := db.Raw("PRAGMA table_info(" + c.table + ")").Scan(&cols).Error; err != nil {
			t.Fatalf("pragma table_info(%s): %v", c.table, err)
		}
		found := false
		for _, info := range cols {
			if info.Name != c.col {
				continue
			}
			found = true
			if info.NotNull != 1 {
				t.Fatalf("%s.%s 应为 NOT NULL", c.table, c.col)
			}
		}
		if !found {
			t.Fatalf("%s 缺少列 %s", c.table, c.col)
		}
	}

	// (device_id, name) 唯一。
	if err := db.Exec(`INSERT INTO collector_group (id, device_id, name, interval_ms)
		VALUES ('g1', 'd1', 'fast', 500)`).Error; err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := db.Exec(`INSERT INTO collector_group (id, device_id, name, interval_ms)
		VALUES ('g2', 'd1', 'fast', 1000)`).Error; err == nil {
		t.Error("同一设备内重复组名应当失败")
	}
	if err := db.Exec(`INSERT INTO collector_group (id, device_id, name, interval_ms)
		VALUES ('g3', 'd2', 'fast', 500)`).Error; err != nil {
		t.Errorf("不同设备可复用组名，实际失败：%v", err)
	}

	// 迁移被记入 schema_migrations，且重复执行幂等。
	for _, version := range []string{"028_collector_group.sql", "029_push_outbox_group.sql"} {
		var applied int64
		if err := db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).
			Scan(&applied).Error; err != nil {
			t.Fatalf("query schema_migrations: %v", err)
		}
		if applied != 1 {
			t.Fatalf("%s 未被记入 schema_migrations，实际 %d 条", version, applied)
		}
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("重复执行迁移应幂等，实际报错：%v", err)
	}
}
