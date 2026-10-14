// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// PluginInfoVO 网关侧的插件环境信息。
//
// 供管理端在**选择文件之前**展示：插件必须匹配本网关的平台与命名规则，
// 先让用户看见比传完再逐个报错要好得多。
type PluginInfoVO struct {
	// OS / Arch 网关运行平台（如 windows / amd64）。插件的构建目标必须与之一致。
	OS   string `json:"os"`
	Arch string `json:"arch"`

	// Dir 解析后的插件目录**绝对路径**（相对网关可执行文件所在目录解析）。
	Dir string `json:"dir"`

	// Glob 插件文件名匹配规则（plugin.glob，默认 iot-driver-*）。
	// 不匹配该规则的文件即使传进目录也不会被加载。
	Glob string `json:"glob"`

	// FileExt 本平台要求的文件名后缀（Windows 为 ".exe"，其余为空串）。
	FileExt string `json:"fileExt"`

	// Enabled 插件总开关（plugin.enabled）。为 false 时网关不加载任何插件。
	Enabled bool `json:"enabled"`
}

// PluginImportErrorVO 单个插件文件的导入失败明细。
type PluginImportErrorVO struct {
	Name string `json:"name"`
	// Reason 面向运维的失败原因，如「该文件是 linux/ELF 格式，本网关运行于 windows/amd64」。
	Reason string `json:"reason"`
}

// PluginImportResultVO 批量插件导入结果汇总。
//
// 与 ExcelImportResultVO 同形（total/success/failed/errors），
// 因为对前端而言这是同一类交互：批量上传 + 逐项成败明细。
type PluginImportResultVO struct {
	Total   int                   `json:"total"`
	Success int                   `json:"success"`
	Failed  int                   `json:"failed"`
	Errors  []PluginImportErrorVO `json:"errors"`

	// Dir 插件落盘目录，便于失败时用户直接去核对。
	Dir string `json:"dir"`

	// Pending 本次写入的文件数。它们要等下一轮目录扫描才会被加载。
	Pending int `json:"pending"`

	// Hint 提示文案：说明加载时机与「同名文件覆盖 = 热升级」的语义。
	Hint string `json:"hint"`

	// Warnings 逐项的非致命提示（例如内容未变而跳过写入）。
	Warnings []string `json:"warnings"`
}
