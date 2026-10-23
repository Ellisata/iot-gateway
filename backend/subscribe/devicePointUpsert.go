// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"iot-gateway/model/po"
)

var devicePointConflictColumns = []clause.Column{
	{Name: "device_id"},
	{Name: "device_address_id"},
}

// UpsertDevicePoints 订阅报文自动上报路径的 upsert。
//
// 手工维护（source = manual）的行是运维对元信息的最终确认，自动上报既不能改
// 它的名称/标签/类型，也不能把 updated_at 冲掉；其余行保持原有语义：
// 非空新值覆盖、空值不覆盖已有非空值（兼容未配置名称字段的历史订阅）。
//
// 表达式里用不带表名的 source / updated_at：ON CONFLICT DO UPDATE 中
// 未限定的列名指向已存在的行，excluded.* 才是本次要插入的值。
func UpsertDevicePoints(db *gorm.DB, rows []po.MqttSubscriptionDevicePoint) error {
	if len(rows) == 0 {
		return nil
	}
	return db.Clauses(clause.OnConflict{
		Columns: devicePointConflictColumns,
		DoUpdates: clause.Assignments(map[string]interface{}{
			"device_name":          gorm.Expr(autoKeepExpr("excluded.device_name", "device_name")),
			"device_address_label": gorm.Expr(autoKeepExpr("excluded.device_address_label", "device_address_label")),
			"kind":                 gorm.Expr(autoKeepExpr("excluded.kind", "kind")),
			"updated_at": gorm.Expr(
				"CASE WHEN source = '" + po.MqttSubscriptionDevicePointSourceManual +
					"' THEN updated_at ELSE excluded.updated_at END"),
		}),
	}).Create(&rows).Error
}

// UpsertManualDevicePoints 手工新增 / 编辑 / Excel 导入路径的 upsert。
//
// 提交值无条件覆盖，并把 source 置为 manual；created_at 不在更新集内，
// 已存在的行保留首次出现时间。调用方负责校验与去重。
func UpsertManualDevicePoints(db *gorm.DB, rows []po.MqttSubscriptionDevicePoint) error {
	if len(rows) == 0 {
		return nil
	}
	return db.Clauses(clause.OnConflict{
		Columns: devicePointConflictColumns,
		DoUpdates: clause.Assignments(map[string]interface{}{
			"device_name":          gorm.Expr("excluded.device_name"),
			"device_address_label": gorm.Expr("excluded.device_address_label"),
			"kind":                 gorm.Expr("excluded.kind"),
			"source":               gorm.Expr("excluded.source"),
			"updated_at":           gorm.Expr("excluded.updated_at"),
		}),
	}).Create(&rows).Error
}

// autoKeepExpr 自动上报的元信息更新表达式：manual 行保持原值，
// 其余行非空新值覆盖、空值保留旧值。
func autoKeepExpr(incoming, existing string) string {
	return "CASE WHEN source = '" + po.MqttSubscriptionDevicePointSourceManual + "' THEN " + existing +
		" WHEN " + incoming + " <> '' THEN " + incoming + " ELSE " + existing + " END"
}
