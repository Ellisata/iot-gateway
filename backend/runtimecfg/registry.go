// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package runtimecfg

import "iot-gateway/configFile"

// ValueType 配置值的类型。增加新类型时在这里与 decode 的 switch 各补一支，
// 并同步前端渲染分支。
type ValueType string

const (
	// ValueTypeBool 布尔开关。
	ValueTypeBool ValueType = "bool"

	// ValueTypeEnum 固定选项的整数。取值范围由 Spec.Options 决定，当前用于
	// 周期类配置（0 = 关闭 / 60 / 300 / 600 秒）。
	ValueTypeEnum ValueType = "enum"
)

// Option 枚举项。Value 是存储与 API 的取值，LabelKey 是前端 i18n key
// （译文由 zh-CN / en 两份 locale 维护，后端不传译文）。
type Option struct {
	Value    int
	LabelKey string
}

// ApplyMode 配置项修改后的生效方式。
type ApplyMode string

const (
	// ApplyHotRead 组件每次用到时读取，修改后立即生效，无需 applier。
	ApplyHotRead ApplyMode = "hot-read"

	// ApplyHotAction 修改后必须启停 / 重配组件，因此要求注册 applier。
	ApplyHotAction ApplyMode = "hot-action"

	// ApplyRestart 只持久化，重启网关后由组件在启动时读取。
	ApplyRestart ApplyMode = "restart"
)

// Hot 报告该模式是否在运行期生效（restart 之外都是）。
func (m ApplyMode) Hot() bool {
	return m == ApplyHotRead || m == ApplyHotAction
}

// Spec 一个可动态管理的配置项。
//
// 显式登记而不是反射 Config：类型、范围、生效方式与文案 key 都是需要人工判断的
// 信息，反射只能拿到字段名。新增开关时在这里补一条，测试会检查每个布尔字段
// 要么已登记、要么在排除表里写明了理由。
type Spec struct {
	Key  Key
	Type ValueType

	// Group / GroupLabelKey 页面分组。前端按 Group 的顺序渲染。
	Group         string
	GroupLabelKey string

	// LabelKey / DescKey 前端 i18n key。后端只传 key，不传译文 ——
	// 译文统一由前端的 zh-CN / en 两份 locale 维护。
	LabelKey string
	DescKey  string

	Mode ApplyMode

	// Base 从启动期配置里取默认值。只能读，不得修改 cfg。
	// 返回值类型必须与 Type 一致：bool 对应 ValueTypeBool，int 对应 ValueTypeEnum。
	Base func(*configFile.Config) any

	// Options 仅 ValueTypeEnum 使用：必须非空、Value 不可重复。
	Options []Option
}

// specs 是**有顺序的**注册表：页面按此顺序分组与排列。
var specs = []Spec{
	{
		Key:           KeyDiscoveryEnabled,
		Type:          ValueTypeBool,
		Group:         "discovery",
		GroupLabelKey: "runtimeConfig.groups.discovery",
		LabelKey:      "runtimeConfig.fields.discoveryEnabled.label",
		DescKey:       "runtimeConfig.fields.discoveryEnabled.hint",
		Mode:          ApplyHotAction,
		Base:          func(c *configFile.Config) any { return c.Discovery.Enabled },
	},
	{
		Key:           KeyDeviceScanEnabled,
		Type:          ValueTypeBool,
		Group:         "deviceScan",
		GroupLabelKey: "runtimeConfig.groups.deviceScan",
		LabelKey:      "runtimeConfig.fields.deviceScanEnabled.label",
		DescKey:       "runtimeConfig.fields.deviceScanEnabled.hint",
		Mode:          ApplyHotRead,
		Base:          func(c *configFile.Config) any { return c.DeviceScan.Enabled },
	},
	{
		Key:           KeyPushChangeOnly,
		Type:          ValueTypeBool,
		Group:         "push",
		GroupLabelKey: "runtimeConfig.groups.push",
		LabelKey:      "runtimeConfig.fields.pushChangeOnly.label",
		DescKey:       "runtimeConfig.fields.pushChangeOnly.hint",
		Mode:          ApplyHotRead,
		Base:          func(c *configFile.Config) any { return c.Push.ChangeOnly },
	},
	{
		Key:           KeyPushKeepaliveIntervalSec,
		Type:          ValueTypeEnum,
		Group:         "push",
		GroupLabelKey: "runtimeConfig.groups.push",
		LabelKey:      "runtimeConfig.fields.pushKeepaliveInterval.label",
		DescKey:       "runtimeConfig.fields.pushKeepaliveInterval.hint",
		Mode:          ApplyHotRead,
		Options: []Option{
			{Value: 0, LabelKey: "runtimeConfig.fields.pushKeepaliveInterval.options.off"},
			{Value: 60, LabelKey: "runtimeConfig.fields.pushKeepaliveInterval.options.min1"},
			{Value: 300, LabelKey: "runtimeConfig.fields.pushKeepaliveInterval.options.min5"},
			{Value: 600, LabelKey: "runtimeConfig.fields.pushKeepaliveInterval.options.min10"},
		},
		Base: func(c *configFile.Config) any { return c.Push.KeepaliveIntervalSec },
	},
	{
		Key:           KeyPluginEnabled,
		Type:          ValueTypeBool,
		Group:         "plugin",
		GroupLabelKey: "runtimeConfig.groups.plugin",
		LabelKey:      "runtimeConfig.fields.pluginEnabled.label",
		DescKey:       "runtimeConfig.fields.pluginEnabled.hint",
		Mode:          ApplyRestart,
		Base:          func(c *configFile.Config) any { return c.Plugin.Enabled },
	},
	{
		Key:           KeyAgentApiEnabled,
		Type:          ValueTypeBool,
		Group:         "agentApi",
		GroupLabelKey: "runtimeConfig.groups.agentApi",
		LabelKey:      "runtimeConfig.fields.agentApiEnabled.label",
		DescKey:       "runtimeConfig.fields.agentApiEnabled.hint",
		// 分组中间件按请求现读开关（middleware.AgentApiSwitchMiddleware），
		// 不需要启停组件。
		Mode: ApplyHotRead,
		Base: func(c *configFile.Config) any { return c.AgentApi.Enabled },
	},
	{
		Key:           KeyControlEnabled,
		Type:          ValueTypeBool,
		Group:         "control",
		GroupLabelKey: "runtimeConfig.groups.control",
		LabelKey:      "runtimeConfig.fields.controlEnabled.label",
		DescKey:       "runtimeConfig.fields.controlEnabled.hint",
		// ControlService 每次请求现读开关：Web / OpenAPI / MQTT 控制主题的
		// 写路径都汇聚到 ControlService.Write，不需要启停组件。关闭后立即
		// 拒绝新的下发，正在执行的那一批不受影响。
		Mode: ApplyHotRead,
		Base: func(c *configFile.Config) any { return c.Control.Enabled },
	},
}

// All 返回注册表的副本，调用方不得依赖修改它来影响运行时。
func All() []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	return out
}

// Lookup 按 key 查注册项。
func Lookup(key Key) (Spec, bool) {
	for _, s := range specs {
		if s.Key == key {
			return s, true
		}
	}
	return Spec{}, false
}
