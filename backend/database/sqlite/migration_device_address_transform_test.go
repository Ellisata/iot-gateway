// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import "testing"

// TestMigration036DeviceAddressTransformSchema 钉住 036 迁移在真实迁移路径上
// 建出的列。理由同 015：生产只跑 migrations/*.sql，不跑 AutoMigrate。
func TestMigration036DeviceAddressTransformSchema(t *testing.T) {
	db := newTestDB(t)

	// PRAGMA 返回的默认值是带引号的 SQL 字面量，空串就是两个单引号。
	type columnInfo struct {
		Name    string  `gorm:"column:name"`
		NotNull int     `gorm:"column:notnull"`
		Dflt    *string `gorm:"column:dflt_value"`
	}
	var cols []columnInfo
	if err := db.Raw("PRAGMA table_info(device_address)").Scan(&cols).Error; err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	var found *columnInfo
	for i := range cols {
		if cols[i].Name == "transform_json" {
			found = &cols[i]
			break
		}
	}
	if found == nil {
		t.Fatal("device_address 缺少列 transform_json")
	}
	if found.NotNull != 1 {
		t.Fatalf("transform_json 应为 NOT NULL，实际 notnull=%d", found.NotNull)
	}
	if found.Dflt == nil || *found.Dflt != "''" {
		got := "<nil>"
		if found.Dflt != nil {
			got = *found.Dflt
		}
		t.Fatalf("transform_json 默认值应为空串，实际 %s", got)
	}

	// 迁移被记入 schema_migrations（版本名就是文件名）
	var applied int64
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		"036_device_address_transform.sql").Scan(&applied).Error; err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("036_device_address_transform.sql 未被记入 schema_migrations，实际 %d 条", applied)
	}

	// 再跑一次是 no-op（幂等）
	if err := runMigrations(db); err != nil {
		t.Fatalf("重复执行迁移应幂等，实际报错：%v", err)
	}
}
