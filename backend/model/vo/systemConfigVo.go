// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// SystemConfigVO 运行开关页的完整数据（按分组组织）。
type SystemConfigVO struct {
	Groups []SystemConfigGroupVO `json:"groups"`
}

// SystemConfigGroupVO 一组开关。LabelKey 由前端翻译。
type SystemConfigGroupVO struct {
	Key      string                `json:"key"`
	LabelKey string                `json:"labelKey"`
	Fields   []SystemConfigFieldVO `json:"fields"`
}

// SystemConfigFieldVO 单个开关的当前状态。
type SystemConfigFieldVO struct {
	Key  string `json:"key"`
	Type string `json:"type"`

	// Value 生效值；DefaultValue 是 default.yaml 里的值。
	// 动态类型由 Type 决定：bool 对应 "bool"，int 对应 "enum"。
	Value        any `json:"value"`
	DefaultValue any `json:"defaultValue"`

	// Options 仅 "enum" 类型有值，前端据此渲染下拉框。
	Options []SystemConfigOptionVO `json:"options,omitempty"`

	// Source "default" 表示无覆盖行，"override" 表示页面 / API 改过。
	Source string `json:"source"`
	// Hot 是否运行期生效；false 表示需要重启。
	Hot bool `json:"hot"`
	// PendingRestart 已保存但与本次启动读取的值不同（仅 restart 项会为 true）。
	PendingRestart bool `json:"pendingRestart"`

	UpdatedBy string `json:"updatedBy"`
	UpdatedAt string `json:"updatedAt"`

	// LabelKey / DescKey 前端 i18n key。
	LabelKey string `json:"labelKey"`
	DescKey  string `json:"descKey"`
}

// SystemConfigOptionVO 枚举项：Value 为存储值，LabelKey 由前端翻译。
type SystemConfigOptionVO struct {
	Value    int    `json:"value"`
	LabelKey string `json:"labelKey"`
}

// SystemConfigUpdateVO 修改 / 重置后的结果。
type SystemConfigUpdateVO struct {
	Key string `json:"key"`
	// Value 动态类型由配置项的 Type 决定（bool / int）。
	Value           any  `json:"value"`
	RequiresRestart bool `json:"requiresRestart"`
}
