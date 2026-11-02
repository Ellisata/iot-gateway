// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// ControlItemResultVO 单点控制结果。
type ControlItemResultVO struct {
	AddressID    string `json:"addressId"`
	AddressName  string `json:"addressName"`
	AddressLabel string `json:"addressLabel"`
	DataType     string `json:"dataType"`
	RwPermission string `json:"rwPermission"`

	// Value 请求下发的原始字符串。
	Value string `json:"value"`
	// ResultValue 驱动实际写入的值（格式化后）。
	ResultValue string `json:"resultValue,omitempty"`
	// ReadbackValue 回读值；verify=false 或点位只有 W 权限时为空。
	ReadbackValue string `json:"readbackValue,omitempty"`
	// Verified 是否回读并确认一致。仅在有回读值时可能为 true。
	Verified bool `json:"verified"`
	// OK 写操作是否被设备接受。
	OK bool `json:"ok"`
	// Code driver.WriteCodeXxx。
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

// ControlResultVO 一次控制下发的整体结果。
type ControlResultVO struct {
	RequestID  string `json:"requestId"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	// ProtocolName 协议名（含传输方式，如 ModBus.TCP）。
	ProtocolName string `json:"protocolName"`
	// Duplicate 该 requestId 已处理过，本次直接返回历史结果，未重复下发。
	Duplicate bool `json:"duplicate"`

	Results []ControlItemResultVO `json:"results"`
}

// ControlCapabilityVO 控制能力查询结果。
type ControlCapabilityVO struct {
	DeviceID     string `json:"deviceId"`
	DeviceName   string `json:"deviceName"`
	ProtocolName string `json:"protocolName"`
	// Enabled 控制总开关（control.enabled）是否打开。
	Enabled bool `json:"enabled"`
	// Writable 协议插件是否声明了 Writable。
	Writable bool `json:"writable"`
	// Reason 不可写的可读原因；可写时为空。
	Reason string `json:"reason,omitempty"`
}
