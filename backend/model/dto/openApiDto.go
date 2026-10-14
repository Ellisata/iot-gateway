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

// OpenApiSyncDevicePointsDTO 开放接口「设备 + 点位」游标分批同步请求。
//
// 用游标而非 page/size：同步是长任务，翻页途中若有人增删设备/点位，
// OFFSET 会整体位移，导致漏读或重读某些行且不报任何错。设备 id 是 UUIDv7
// （长度固定的 32 位 hex，字典序即时间序），故 after = 上一页最后一条设备 id 即可稳定续读。
//
// After 为空表示从头开始；Size 沿用其它分页接口的 1~100。
type OpenApiSyncDevicePointsDTO struct {
	After string `form:"after"` // 游标：上一页响应里的 nextAfter；首次不传
	Size  int    `form:"size" binding:"required,min=1,max=100"`
}
