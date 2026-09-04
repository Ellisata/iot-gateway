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

// ==================== 设备地址 DTO ====================

// CreateDeviceAddressDTO 创建设备地址请求
// commonDataType 为通用数据类型名，data_type 由服务端结合协议映射得出。
// dataType 字段保留用于兼容旧请求（未传 commonDataType 时视为通用类型名）。
type CreateDeviceAddressDTO struct {
	DeviceID       string `json:"deviceId" binding:"required"`
	Name           string `json:"name" binding:"required,min=1,max=100"`
	Label          string `json:"label" binding:"required,min=1,max=100"` // 地址 name 的中文说明（如温度、电流）
	CommonDataType string `json:"commonDataType"`
	DataType       string `json:"dataType"` // 兼容旧请求，服务端映射后覆盖
	RwPermission   string `json:"rwPermission" binding:"required,oneof=R W RW"`
	ScanFrequency  int    `json:"scanFrequency"`
	Description    string `json:"description"`
}

// UpdateDeviceAddressDTO 更新设备地址请求
type UpdateDeviceAddressDTO struct {
	ID             string `json:"id" binding:"required"`
	Name           string `json:"name"`
	Label          string `json:"label" binding:"required,min=1,max=100"` // 地址 name 的中文说明（如温度、电流）
	CommonDataType string `json:"commonDataType"`
	DataType       string `json:"dataType"` // 兼容旧请求，服务端映射后覆盖
	RwPermission   string `json:"rwPermission"`
	ScanFrequency  int    `json:"scanFrequency"`
	Description    string `json:"description"`
	Status         *int   `json:"status"`
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
