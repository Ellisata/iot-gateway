// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// ==================== 设备 DTO ====================

// CreateDeviceDTO 创建设备请求
type CreateDeviceDTO struct {
	Name         string          `json:"name" binding:"required,min=1,max=100"`
	ProtocolID   string          `json:"protocolId" binding:"required"`
	ProtocolJSON json.RawMessage `json:"protocolJson" binding:"required"`
	Description  string          `json:"description"`
}

// UpdateDeviceDTO 更新设备请求
type UpdateDeviceDTO struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	ProtocolID   string          `json:"protocolId"`
	ProtocolJSON json.RawMessage `json:"protocolJson"`
	Description  string          `json:"description"`
	Status       *int            `json:"status"`
}

// TestDeviceConnectionDTO 测试设备连接请求
// 不同协议的 protocolJson 字段各不相同，故以原始 JSON 直接传递，不做固定结构解析
type TestDeviceConnectionDTO struct {
	ProtocolName string          `json:"protocolName" binding:"required"`
	ProtocolJSON json.RawMessage `json:"protocolJson" binding:"required"`
}

// BatchDeleteDeviceDTO 批量删除设备请求
//
// 没有 BatchDeleteDeviceAddressDTO 那样的 deviceId 范围字段：设备地址挂在某台
// 设备下，所以那条约束是在防「拿 A 设备的 id 去删 B 设备的点位」；设备之间没有
// 从属关系，可删集合就是全部设备，勾选什么删什么。
//
// 上限 200 与设备地址批量删除取同一个数：再多的勾选本来也不是正常操作，
// 挡住的是误传超大列表把一次请求变成一条巨长的 IN。
type BatchDeleteDeviceDTO struct {
	IDs []string `json:"ids" binding:"required,min=1,max=200"`
}

// ==================== 设备地址 DTO ====================

// DeviceAddressThresholdDTO 点位报警条件配置。
//
// 为什么是嵌套对象而不是平铺字段：更新需要三态 —— 未传（不动配置）、传了就整份覆盖、
// 传空算子（等于关掉条件）。平铺字段下「未传」与「传空」都解码成零值，一次不带条件的
// 部分更新就会静默清掉运维刚配的条件。嵌套对象让"整块省不省略"成为一个明确的信号。
type DeviceAddressThresholdDTO struct {
	Enabled *int     `json:"enabled"` // 1 启用 0 关闭；省略 = 0
	Op      *string  `json:"op"`      // 比较符 range|gt|ge|lt|le|eq|ne|in；可用集合按数据类型取
	Values  []string `json:"values"`  // 操作数：range 为 [min,max]；单比较为 1 个；in 为集合
}

// CreateDeviceAddressDTO 创建设备地址请求
// commonDataType 为通用数据类型名，data_type 由服务端结合协议映射得出。
// dataType 字段保留用于兼容旧请求（未传 commonDataType 时视为通用类型名）。
type CreateDeviceAddressDTO struct {
	DeviceID       string                     `json:"deviceId" binding:"required"`
	Name           string                     `json:"name" binding:"required,min=1,max=100"`
	Label          string                     `json:"label" binding:"required,min=1,max=100"` // 地址 name 的中文说明（如温度、电流）
	CommonDataType string                     `json:"commonDataType"`
	DataType       string                     `json:"dataType"` // 兼容旧请求，服务端映射后覆盖
	RwPermission   string                     `json:"rwPermission" binding:"required,oneof=R W RW"`
	ScanFrequency  int                        `json:"scanFrequency"`
	Description    string                     `json:"description"`
	Threshold      *DeviceAddressThresholdDTO `json:"threshold"`
}

// UpdateDeviceAddressDTO 更新设备地址请求
//
// Threshold 整个对象**可以省略**：不传 = 完全不动阈值配置（部分更新安全）；
// 传了就整份覆盖。见 DeviceAddressThresholdDTO 的说明。
type UpdateDeviceAddressDTO struct {
	ID             string                     `json:"id" binding:"required"`
	Name           string                     `json:"name"`
	Label          string                     `json:"label" binding:"required,min=1,max=100"` // 地址 name 的中文说明（如温度、电流）
	CommonDataType string                     `json:"commonDataType"`
	DataType       string                     `json:"dataType"` // 兼容旧请求，服务端映射后覆盖
	RwPermission   string                     `json:"rwPermission"`
	ScanFrequency  int                        `json:"scanFrequency"`
	Description    string                     `json:"description"`
	Status         *int                       `json:"status"`
	Threshold      *DeviceAddressThresholdDTO `json:"threshold"`
}

// BatchDeleteDeviceAddressDTO 批量删除设备地址请求
//
// deviceId 是删除范围的前置约束：这些 id 必须属于这台设备才会被删。
// 不带它的话，一个手工构造的请求可以用当前页看到的 id 去删另一台设备的点位 ——
// 而删点位是静默生效的（采集任务下一轮热加载就少了一个点），事后很难发现。
type BatchDeleteDeviceAddressDTO struct {
	DeviceID string   `json:"deviceId" binding:"required"`
	IDs      []string `json:"ids" binding:"required,min=1,max=200"`
}

// ==================== 通用分页 DTO ====================

// PageDeviceDTO 设备分页查询（名称模糊检索）
type PageDeviceDTO struct {
	Page int    `form:"page" binding:"required,min=1"`
	Size int    `form:"size" binding:"required,min=1,max=100"`
	Name string `form:"name"` // 设备名称（模糊查询）
}

// PageDeviceAddressDTO 设备地址分页查询（按设备ID + 名称模糊检索）
type PageDeviceAddressDTO struct {
	Page     int    `form:"page" binding:"required,min=1"`
	Size     int    `form:"size" binding:"required,min=1,max=100"`
	DeviceID string `form:"deviceId" binding:"required"` // 所属设备ID
	Name     string `form:"name"`                        // 地址名称（模糊查询）
}
