// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

import "encoding/json"

// ==================== 局域网设备扫描发现 VO ====================
//
// 这一层存在的意义是**不把插件契约类型直接暴露到 HTTP 响应里**：
// driver.ProtocolDiscovery / driver.DiscoveredDevice 是插件与网关之间的契约，
// 它随插件版本演进；前端只该看到网关这一层的稳定形状。字段名也更贴合界面语义
// （如 protocolId 是 iot_protocol 主键，插件侧根本没有这个概念）。

// ScanProtocolVO 一个支持局域网扫描的协议。
type ScanProtocolVO struct {
	// Name 协议名（= iot_protocol.name = 提交扫描时的 protocol 字段）。
	Name string `json:"name"`
	// ProtocolID iot_protocol 主键。前端勾选结果建完设备后要跳设备列表，
	// 以及导入时定位协议行都靠它。
	ProtocolID string `json:"protocolId"`
	// Title 扫描页协议下拉里的显示名（插件声明）。
	Title string `json:"title"`
	// Description 该协议扫描行为的说明（插件声明）。
	Description string `json:"description,omitempty"`
	// TitleI18n / DescriptionI18n 可选的按语言覆盖文案（键 zh-cn / en）。
	// 缺省语言由前端回退 Title / Description，旧插件不带这两个字段时行为不变。
	TitleI18n       map[string]string `json:"titleI18n,omitempty"`
	DescriptionI18n map[string]string `json:"descriptionI18n,omitempty"`
	// DefaultPort 插件声明的默认端口，前端用它回填端口输入框。
	DefaultPort int `json:"defaultPort"`
	// TargetMode unicast / broadcast / both，前端据此提示"该协议靠广播发现"。
	TargetMode string `json:"targetMode,omitempty"`
	// ScanFormJSON 扫描选项表单（form-create schema），前端用 <form-create> 渲染。
	ScanFormJSON json.RawMessage `json:"scanFormJson,omitempty"`
}

// ScanSubnetVO 本机一个可扫描的网段。
type ScanSubnetVO struct {
	Iface     string `json:"iface"`
	CIDR      string `json:"cidr"`
	LocalIP   string `json:"localIp"`
	Broadcast string `json:"broadcast"`
	HostCount int    `json:"hostCount"`
}

// ScanDeviceVO 扫到的一台设备。
type ScanDeviceVO struct {
	// Index 在本任务 Devices 数组里的下标，提交创建时回传它定位。
	Index int `json:"index"`

	Host   string `json:"host"`
	Port   int    `json:"port"`
	UnitID int    `json:"unitId,omitempty"`

	Name    string `json:"name,omitempty"`
	Vendor  string `json:"vendor,omitempty"`
	Model   string `json:"model,omitempty"`
	Version string `json:"version,omitempty"`

	// ProtocolJSON 建 device 行时要用的连接配置，**由插件给出**。
	// 前端原样展示（能让运维核对），但提交创建时不必回传 —— 服务端从
	// 自己持有的任务快照里取，不采信客户端。
	ProtocolJSON string `json:"protocolJson,omitempty"`

	Extra []ScanAttributeVO `json:"extra,omitempty"`
}

// ScanAttributeVO 设备的一个附加属性（保序）。
type ScanAttributeVO struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ScanErrorVO 单个目标探测失败的原因。
type ScanErrorVO struct {
	Host   string `json:"host"`
	Reason string `json:"reason"`
}

// ScanJobVO 扫描任务快照。
type ScanJobVO struct {
	JobID    string `json:"jobId"`
	Protocol string `json:"protocol"`
	// State running / done / canceled / failed。
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`

	// RequestedTargets 所选网段展开后的目标总数（不受上限影响）。
	RequestedTargets int `json:"requestedTargets"`
	// PlannedTargets 实际会扫的目标数（上限截断后）。
	PlannedTargets int `json:"plannedTargets"`
	// Truncated 为 true 表示只扫了前 PlannedTargets 个。**前端必须把这件事
	// 显示出来** —— 否则用户会从"没扫到设备"得出"这个网段没有设备"的错误结论。
	Truncated bool `json:"truncated"`

	Total   int `json:"total"`
	Scanned int `json:"scanned"`
	Found   int `json:"found"`

	ElapsedMs int `json:"elapsedMs"`
	// EstimateMaxMs 最坏耗时的上界估算（全部目标走满插件预算）。
	EstimateMaxMs int `json:"estimateMaxMs"`

	// Devices 已发现的设备。**可能为 null** —— 轻量快照（轮询用的
	// withDevices=false，见 dto.ScanJobQueryDTO）不带清单，调用方要保留自己
	// 已经拿到的那一份，据 Found 判断是否漏掉了新设备。非 null 时一定是数组。
	Devices []ScanDeviceVO `json:"devices"`
	// Errors 探测失败明细。与 Devices 同一套 null 语义（一起带、一起不带），
	// 因此**不加 omitempty**：空清单要发成 `[]`，被省掉就与"这一份没带"撞了。
	Errors []ScanErrorVO `json:"errors"`

	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt,omitempty"`
}

// ScanCapabilitiesVO 扫描页初始化所需的全部静态信息，一次请求拿全。
//
// 合成一个响应的理由：协议下拉、网段多选、"功能是否启用"三件事都要在进页面
// 时拿到，分三个接口就三次鉴权 + 三次往返。
type ScanCapabilitiesVO struct {
	// Enabled 总开关（deviceScan.enabled）。为 false 时前端应禁用整个页面。
	Enabled bool `json:"enabled"`
	// Protocols 支持扫描的协议。为空说明没装带扫描能力的插件。
	Protocols []ScanProtocolVO `json:"protocols"`
	// Subnets 本机可扫描的网段。为空说明本机没有可用网段。
	Subnets []ScanSubnetVO `json:"subnets"`
	// MaxTargets 单次扫描的目标数上限，前端可在开始前提示用户"这个网段会被截断"。
	MaxTargets int `json:"maxTargets"`
}
