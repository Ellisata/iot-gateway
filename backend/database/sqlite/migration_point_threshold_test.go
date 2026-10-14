// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"testing"

	"gorm.io/gorm"
)

// TestMigration015PointThresholdSchema 钉住 015 迁移在真实迁移路径上建出的结构。
//
// 为什么必须单独测它：其余所有测试都走 GORM 的 AutoMigrate（从 PO 标签现推表结构），
// 而**生产只跑 migrations/*.sql**（没有 AutoMigrate）。于是迁移文件写错时，
// 单测全绿、启动即挂 —— 这条用例是唯一能替生产挡住它的地方。
//
// 它同时守着迁移器的那个坑：splitSQL 按 ASCII 分号朴素切分且不剥离注释，
// 注释里出现一个分号就会把语句劈成两半（失败位置离真正的原因很远）。
func TestMigration015PointThresholdSchema(t *testing.T) {
	db := newTestDB(t)

	// 1) 三个新列必须存在，且都有 NOT NULL + 默认值（开关 0、算子与操作数空串）
	wantCols := map[string]struct {
		notNull bool
		dflt    string
	}{
		"threshold_enabled": {notNull: true, dflt: "0"},
		// PRAGMA 报的是**带引号的 SQL 字面量**，所以空串的默认值就是两个单引号
		"threshold_op":    {notNull: true, dflt: "''"},
		"threshold_value": {notNull: true, dflt: "''"},
	}
	// 列名必须显式给出：GORM 的默认命名是 snake_case，NotNull 会被映射成 not_null，
	// 而 PRAGMA 返回的是 notnull（无下划线）——不写 tag 的话该字段恒为 0，
	// 断言会以一个与迁移无关的理由失败。
	type columnInfo struct {
		Name    string  `gorm:"column:name"`
		Type    string  `gorm:"column:type"`
		NotNull int     `gorm:"column:notnull"`
		Dflt    *string `gorm:"column:dflt_value"`
	}
	var cols []columnInfo
	if err := db.Raw("PRAGMA table_info(device_address)").Scan(&cols).Error; err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	got := make(map[string]columnInfo, len(cols))
	for _, c := range cols {
		got[c.Name] = c
	}
	for name, want := range wantCols {
		c, ok := got[name]
		if !ok {
			t.Fatalf("device_address 缺少列 %s", name)
		}
		if (c.NotNull == 1) != want.notNull {
			t.Fatalf("%s 的 NOT NULL 不符：实际 %d", name, c.NotNull)
		}
		if want.dflt != "" {
			got := "<nil>"
			if c.Dflt != nil {
				got = *c.Dflt
			}
			if got != want.dflt {
				t.Fatalf("%s 的默认值应为 %s，实际 %s", name, want.dflt, got)
			}
		}
	}

	// 2) 越限记录表与三个索引
	assertTableExists(t, db, "point_threshold_record", []string{
		"id", "device_id", "device_name", "address_id", "address_name",
		"value", "kind", "bound", "threshold_op", "threshold_value",
		"occurred_at", "created_at",
	})
	for _, idx := range []string{
		"idx_point_threshold_record_device",
		"idx_point_threshold_record_address",
		"idx_point_threshold_record_time",
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

	// 3) 迁移被记入 schema_migrations（版本名就是文件名）
	var applied int64
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		"015_point_threshold.sql").Scan(&applied).Error; err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("015_point_threshold.sql 未被记入 schema_migrations，实际 %d 条", applied)
	}

	// 4) 再跑一次是 no-op（幂等），不得因 ALTER TABLE 重复执行而报错
	if err := runMigrations(db); err != nil {
		t.Fatalf("重复执行迁移应幂等，实际报错：%v", err)
	}
}

// assertTableExists 断言表存在且包含全部给定列。
func assertTableExists(t *testing.T, db *gorm.DB, table string, cols []string) {
	t.Helper()

	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).
		Scan(&n).Error; err != nil {
		t.Fatalf("query table %s: %v", table, err)
	}
	if n != 1 {
		t.Fatalf("表 %s 不存在", table)
	}

	var existing []struct{ Name string }
	if err := db.Raw("PRAGMA table_info(" + table + ")").Scan(&existing).Error; err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
	}
	have := make(map[string]bool, len(existing))
	for _, c := range existing {
		have[c.Name] = true
	}
	for _, c := range cols {
		if !have[c] {
			t.Fatalf("表 %s 缺少列 %s", table, c)
		}
	}
}
