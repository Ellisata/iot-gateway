// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestMigration030DeviceCollectorGroupCodeSchema 钉住 030 迁移在真实 SQL 路径上
// 建出的列、部分唯一索引与存量回填行为。
func TestMigration030DeviceCollectorGroupCodeSchema(t *testing.T) {
	db := newTestDB(t)

	for _, table := range []string{"device", "collector_group"} {
		assertTableExists(t, db, table, []string{"code"})
	}

	for _, idx := range []string{
		"idx_device_code",
		"idx_collector_group_device_code",
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

	// 设备编码全局唯一；空串不参与唯一约束（历史/测试库直接写入的行）。
	if err := db.Exec(`INSERT INTO device (id, code, name, protocol_id, protocol_json)
		VALUES ('d1', 'plc-1', '设备1', 'p1', '{}')`).Error; err != nil {
		t.Fatalf("insert device: %v", err)
	}
	if err := db.Exec(`INSERT INTO device (id, code, name, protocol_id, protocol_json)
		VALUES ('d2', 'plc-1', '设备2', 'p1', '{}')`).Error; err == nil {
		t.Error("重复设备编码应当失败")
	}
	for _, stmt := range []string{
		`INSERT INTO device (id, code, name, protocol_id, protocol_json)
			VALUES ('d3', '', '空编码1', 'p1', '{}')`,
		`INSERT INTO device (id, code, name, protocol_id, protocol_json)
			VALUES ('d4', '', '空编码2', 'p1', '{}')`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Errorf("空编码应可共存，实际失败：%v", err)
		}
	}

	// 采集组编码设备内唯一，跨设备可复用；空串同样不参与唯一约束。
	if err := db.Exec(`INSERT INTO collector_group (id, device_id, code, name, interval_ms)
		VALUES ('g1', 'd1', 'fast', 'fast', 500)`).Error; err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := db.Exec(`INSERT INTO collector_group (id, device_id, code, name, interval_ms)
		VALUES ('g2', 'd1', 'fast', 'fast-2', 500)`).Error; err == nil {
		t.Error("同一设备内重复组编码应当失败")
	}
	if err := db.Exec(`INSERT INTO collector_group (id, device_id, code, name, interval_ms)
		VALUES ('g3', 'd2', 'fast', 'fast', 500)`).Error; err != nil {
		t.Errorf("不同设备可复用组编码，实际失败：%v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO collector_group (id, device_id, code, name, interval_ms)
			VALUES ('g4', 'd1', '', '空编码1', 500)`,
		`INSERT INTO collector_group (id, device_id, code, name, interval_ms)
			VALUES ('g5', 'd1', '', '空编码2', 500)`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Errorf("空组编码应可共存，实际失败：%v", err)
		}
	}
}

// TestMigration030BackfillsExistingRows 验证升级存量库时编码被回填成非空且唯一，
// 而不是留给上层一条无法使用的空编码。
func TestMigration030BackfillsExistingRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })

	// 旧结构：只有 030 之前存在的列。
	for _, stmt := range []string{
		`CREATE TABLE device (id TEXT PRIMARY KEY)`,
		`CREATE TABLE collector_group (id TEXT PRIMARY KEY, device_id TEXT NOT NULL)`,
		`INSERT INTO device (id) VALUES ('dev-1'), ('dev-2')`,
		`INSERT INTO collector_group (id, device_id) VALUES ('g-1', 'dev-1'), ('g-2', 'dev-1')`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("seed old schema: %v", err)
		}
	}
	if err := bootstrapSchemaMigrations(db); err != nil {
		t.Fatalf("bootstrap schema_migrations: %v", err)
	}
	if err := applyMigration(db, "030_device_collector_group_code.sql"); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	var deviceCodes []string
	if err := db.Raw("SELECT code FROM device ORDER BY id").Scan(&deviceCodes).Error; err != nil {
		t.Fatalf("query device codes: %v", err)
	}
	if len(deviceCodes) != 2 || deviceCodes[0] == "" || deviceCodes[1] == "" || deviceCodes[0] == deviceCodes[1] {
		t.Fatalf("设备编码回填 = %v，期望非空且唯一", deviceCodes)
	}

	var groupCodes []string
	if err := db.Raw("SELECT code FROM collector_group ORDER BY id").Scan(&groupCodes).Error; err != nil {
		t.Fatalf("query group codes: %v", err)
	}
	if len(groupCodes) != 2 || groupCodes[0] == "" || groupCodes[1] == "" || groupCodes[0] == groupCodes[1] {
		t.Fatalf("采集组编码回填 = %v，期望非空且唯一", groupCodes)
	}
}
