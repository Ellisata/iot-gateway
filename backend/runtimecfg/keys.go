// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package runtimecfg 管理「default.yaml 作默认值、页面 / API 覆盖」的运行期配置开关。
//
// 分层：
//   - configFile：启动期加载 default.yaml（嵌入 + 磁盘 + APP_ENV），结果只读；
//   - runtimecfg：在只读默认值之上叠加 system_config 表里的覆盖值，
//     并负责校验、持久化与热应用；
//   - 各组件：需要动态生效的值从 runtimecfg.Reader 读，其余仍在构造时读 configFile。
//
// **优先级**：system_config 行 > configFile（default.yaml 及环境配置）。
// 表里没有行 = 使用默认值；「恢复默认」就是删除该行。
//
// 新增开关的四步清单见 specs/runtime-config.md。本包只依赖 configFile、
// model/po、logger 与 gorm，不得反向依赖任何业务组件。
package runtimecfg

// Key 运行期配置项标识。取值是 default.yaml 的点分路径，**发布后不得改名** ——
// 它同时是 system_config.key 与对外 API 的标识。
type Key string

const (
	// KeyDiscoveryEnabled 局域网自动发现（UDP 广播）总开关。
	KeyDiscoveryEnabled Key = "discovery.enabled"

	// KeyDeviceScanEnabled 局域网 PLC 扫描总开关。
	KeyDeviceScanEnabled Key = "deviceScan.enabled"

	// KeyPushChangeOnly 变化才推送开关。
	KeyPushChangeOnly Key = "push.changeOnly"

	// KeyPluginEnabled 外部协议插件总开关（仅重启生效）。
	KeyPluginEnabled Key = "plugin.enabled"
)
