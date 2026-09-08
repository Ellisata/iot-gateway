// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// OpenApiDeviceNamesDTO 开放接口批量查询设备名称请求（JSON Body 传设备ID数组）
type OpenApiDeviceNamesDTO struct {
	IDs []string `json:"ids" binding:"required,min=1"`
}

// OpenApiAddressLabelsDTO 开放接口批量查询点位标签请求（JSON Body 传设备ID数组 + 点位ID数组，
// 设备ID作归属校验：不属于这些设备的点位视为不存在）
type OpenApiAddressLabelsDTO struct {
	DeviceIDs  []string `json:"deviceIds" binding:"required,min=1"`
	AddressIDs []string `json:"addressIds" binding:"required,min=1"`
}
