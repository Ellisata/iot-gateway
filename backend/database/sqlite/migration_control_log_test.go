// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import "testing"

// TestMigration025ControlLogSchema 钉住控制下发审计表的 DDL。
//
// 生产只跑 migrations/*.sql（没有 AutoMigrate），迁移写错时其余单测仍会全绿，
// 直到运行时第一次写审计才报错 —— 而那时现场已经下发过一次控制指令。
func TestMigration025ControlLogSchema(t *testing.T) {
	db := newTestDB(t)

	assertTableExists(t, db, "control_log", []string{
		"id", "request_id", "source", "operator",
		"device_id", "device_name", "protocol_name",
		"address_id", "address_name", "address_label", "data_type", "rw_permission",
		"request_value", "result_value", "readback_value", "verified",
		"code", "message", "created_at",
	})

	var applied int64
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		"025_control_log.sql").Scan(&applied).Error; err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("025_control_log.sql 未登记到 schema_migrations")
	}

	// 迁移按文件名幂等执行：再跑一次不应失败。
	if err := runMigrations(db); err != nil {
		t.Fatalf("重复执行迁移失败: %v", err)
	}
}
