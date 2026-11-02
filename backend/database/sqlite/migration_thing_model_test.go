// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"testing"

	"gorm.io/gorm"
)

// TestMigration025ThingModelSchema 钉住 025 迁移在真实迁移路径上建出的结构。
//
// 生产只跑 migrations/*.sql（没有 AutoMigrate），所以这里必须直接验证 SQL
// 建出的表、列、索引与唯一约束，而不是依赖 PO 标签。
func TestMigration025ThingModelSchema(t *testing.T) {
	db := newTestDB(t)

	assertTableExists(t, db, "thing_model", []string{
		"id", "code", "name", "category", "version", "description", "status",
		"created_at", "updated_at",
	})
	assertTableExists(t, db, "thing_model_property", []string{
		"id", "model_id", "identifier", "name", "kind", "access_mode", "unit",
		"description", "sort_order", "status", "created_at", "updated_at",
	})

	// 设备/点位的绑定列：NOT NULL 且默认空串，存量行无需迁移。
	for table, col := range map[string]string{
		"device":         "model_id",
		"device_address": "property_id",
	} {
		type columnInfo struct {
			Name    string  `gorm:"column:name"`
			NotNull int     `gorm:"column:notnull"`
			Dflt    *string `gorm:"column:dflt_value"`
		}
		var cols []columnInfo
		if err := db.Raw("PRAGMA table_info(" + table + ")").Scan(&cols).Error; err != nil {
			t.Fatalf("pragma table_info(%s): %v", table, err)
		}
		found := false
		for _, c := range cols {
			if c.Name != col {
				continue
			}
			found = true
			if c.NotNull != 1 {
				t.Fatalf("%s.%s 应为 NOT NULL", table, col)
			}
			if c.Dflt == nil || *c.Dflt != "''" {
				t.Fatalf("%s.%s 默认值应为空串，实际 %v", table, col, c.Dflt)
			}
		}
		if !found {
			t.Fatalf("%s 缺少列 %s", table, col)
		}
	}

	for _, idx := range []string{
		"idx_thing_model_property_model_identifier",
		"idx_thing_model_property_model_sort",
		"idx_device_model",
		"idx_device_address_device_property",
		"idx_device_address_property",
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

	seedThingModelRows(t, db)

	// 属性标识符在模型内唯一，跨模型可同名。
	if err := db.Exec(`INSERT INTO thing_model_property (id, model_id, identifier, name, kind)
		VALUES ('p2', 'm1', 'temperature', '温度副本', 'float')`).Error; err == nil {
		t.Error("同一模型内重复 identifier 应当失败")
	}
	if err := db.Exec(`INSERT INTO thing_model_property (id, model_id, identifier, name, kind)
		VALUES ('p3', 'm2', 'temperature', '温度', 'float')`).Error; err != nil {
		t.Errorf("不同模型可复用 identifier，实际失败：%v", err)
	}

	// 同一设备内一个属性只能绑一个点位；空串（未绑定）不受唯一约束影响。
	for _, stmt := range []string{
		`INSERT INTO device_address (id, device_id, name, label, common_data_type, data_type, rw_permission)
			VALUES ('a1', 'd1', 'D0', '未绑定1', 'Float', 'float32', 'R')`,
		`INSERT INTO device_address (id, device_id, name, label, common_data_type, data_type, rw_permission)
			VALUES ('a2', 'd1', 'D1', '未绑定2', 'Float', 'float32', 'R')`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("同设备多个未绑定点位应允许：%v", err)
		}
	}
	if err := db.Exec(`UPDATE device_address SET property_id = 'p1' WHERE id = 'a1'`).Error; err != nil {
		t.Fatalf("绑定属性失败：%v", err)
	}
	if err := db.Exec(`UPDATE device_address SET property_id = 'p1' WHERE id = 'a2'`).Error; err == nil {
		t.Error("同一设备重复绑定同一属性应当失败")
	}
	if err := db.Exec(`INSERT INTO device (id, name, protocol_id, protocol_json) VALUES ('d2', '设备2', 'p', '{}')`).Error; err != nil {
		t.Fatalf("insert device d2: %v", err)
	}
	if err := db.Exec(`INSERT INTO device_address (id, device_id, name, label, common_data_type, data_type, rw_permission, property_id)
		VALUES ('a3', 'd2', 'D0', '跨设备绑定', 'Float', 'float32', 'R', 'p1')`).Error; err != nil {
		t.Errorf("不同设备可绑定同一属性，实际失败：%v", err)
	}

	// 迁移被记入 schema_migrations，且重复执行幂等。
	var applied int64
	if err := db.Raw("SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		"027_thing_model.sql").Scan(&applied).Error; err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("027_thing_model.sql 未被记入 schema_migrations，实际 %d 条", applied)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("重复执行迁移应幂等，实际报错：%v", err)
	}
}

// seedThingModelRows 造两条模型、一条属性、一台设备，供约束用例使用。
func seedThingModelRows(t *testing.T, db *gorm.DB) {
	t.Helper()

	for _, stmt := range []string{
		`INSERT INTO thing_model (id, code, name) VALUES ('m1', 'th_sensor', '温湿度传感器')`,
		`INSERT INTO thing_model (id, code, name) VALUES ('m2', 'th_meter', '电表')`,
		`INSERT INTO thing_model_property (id, model_id, identifier, name, kind)
			VALUES ('p1', 'm1', 'temperature', '温度', 'float')`,
		`INSERT INTO device (id, name, protocol_id, protocol_json) VALUES ('d1', '设备1', 'p', '{}')`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("seed %s: %v", stmt, err)
		}
	}
}
