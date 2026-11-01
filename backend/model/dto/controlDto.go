// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// ControlItemDTO 单点写入。
//
// 只接受**点位 id**，不接受原始地址：调用方不能借控制接口绕过设备地址表
// 往任意寄存器写值。服务端会校验该点位属于请求里的设备、处于启用状态、
// rw_permission 含 W。
type ControlItemDTO struct {
	DeviceAddressID string `json:"deviceAddressId" binding:"required"`
	// Value 字符串形式的目标值。编码由插件按点位的数据类型完成
	//（不在网关侧解析，避免 JSON float64 丢失 int64/uint64 精度）。
	Value string `json:"value"`
}

// ControlDTO 控制下发请求。
type ControlDTO struct {
	DeviceID string `json:"deviceId" binding:"required"`
	// RequestID 幂等键。相同 requestId 的重复请求返回首次结果，不重复下发。
	// 由前端/开放接口/MQTT 调用方生成；为空则不做去重。
	RequestID string `json:"requestId"`
	// Verify 写后回读校验。只对有读权限（R/RW）的点位生效。
	Verify bool `json:"verify"`
	// Items 本次下发的点位，上限 50：再多的批量写通常意味着误操作，
	// 且一条 HTTP 请求里混入多个设备的控制动作不应该被允许。
	Items []ControlItemDTO `json:"items" binding:"required,min=1,max=50,dive"`
}

// ControlCapabilityDTO 查询某台设备是否支持控制下发。
type ControlCapabilityDTO struct {
	DeviceID string `form:"deviceId" binding:"required"`
}
