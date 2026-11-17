// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

// ConfigTables 是 config 范围备份/恢复的白名单：只包含“重建一套网关所需的
// 配置、用户、密钥与身份”，不含运行与历史数据。
//
// 新增配置表时必须同时更新这里与 runtimeTables，否则 tables_test.go 的
// 分类完整性测试会失败 —— 这是刻意的：漏掉一张表会让 config 包悄悄丢配置。
var ConfigTables = []string{
	"sys_user",
	"iot_gateway",
	"iot_protocol",
	"device",
	"device_address",
	"collector_group",
	"thing_model",
	"thing_model_property",
	"push_channel",
	"push_channel_form",
	"alarm_webhook",
	"alarm_webhook_form",
	"mqtt_subscription",
	"mqtt_subscription_device_point",
	"trigger_rule",
	"system_config",
	"open_api_secret",
}

// runtimeTables 是 config 范围**刻意排除**的表：报警、控制审计、触发记录、
// 越限留证与断网缓存。它们要么体量随时间增长（push_outbox），要么属于历史
// 证据（alarm / control_log），恢复配置时不应被旧的备份覆盖或清空。
var runtimeTables = []string{
	"push_outbox",
	"alarm",
	"control_log",
	"trigger_rule_record",
	"point_threshold_record",
}

// schemaMigrationsTable 由迁移器维护，config 快照不复制它：临时库已经按
// 当前二进制应用了全部迁移，复制旧值反而会让 manifest 的 schema 描述失真。
const schemaMigrationsTable = "schema_migrations"

var configTableSet = toSet(ConfigTables)
var runtimeTableSet = toSet(runtimeTables)

func toSet(items []string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		out[item] = struct{}{}
	}
	return out
}

// IsConfigTable 判断表是否属于配置白名单。
func IsConfigTable(table string) bool {
	_, ok := configTableSet[table]
	return ok
}

// IsKnownTable 判断表是否已被分类（配置或运行）。
func IsKnownTable(table string) bool {
	if table == schemaMigrationsTable {
		return true
	}
	if _, ok := configTableSet[table]; ok {
		return true
	}
	_, ok := runtimeTableSet[table]
	return ok
}
