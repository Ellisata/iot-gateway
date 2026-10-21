// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

// SystemConfig 运行期配置覆盖（单行一个 key）。
//
// 表里**没有行**表示「使用 default.yaml 的默认值」，因此本表不播种。
// 只有通过「运行开关」页面 / API 显式修改过的项才会留下行；「恢复默认」
// 就是删除对应的行。语义与优先级见 specs/runtime-config.md。
//
// key 是 default.yaml 的点分路径（如 "push.changeOnly"），不是 Go 字段名 ——
// 它同时是对外 API 的标识，发布后不再改名。
type SystemConfig struct {
	Key       string `gorm:"primaryKey;column:key" json:"key"`
	Value     string `gorm:"column:value;not null" json:"value"` // JSON 标量，当前是 true / false
	UpdatedBy string `gorm:"column:updated_by;not null;default:''" json:"updatedBy"`
	UpdatedAt string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (SystemConfig) TableName() string {
	return "system_config"
}
