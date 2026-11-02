// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// CreateCollectorGroupDTO 创建采集组请求。
type CreateCollectorGroupDTO struct {
	DeviceID    string `json:"deviceId" binding:"required"`
	Name        string `json:"name" binding:"required,min=1,max=64"`
	IntervalMs  int    `json:"intervalMs" binding:"required,min=100,max=600000"`
	Description string `json:"description" binding:"max=500"`
}

// UpdateCollectorGroupDTO 更新采集组请求。
//
// 更新是部分更新：Name 空 = 不改；IntervalMs <= 0 = 不改；Status 为 nil = 不改。
// DeviceID 不在 DTO 中 —— 组一旦创建就不允许换设备，服务层使用库中的 device_id。
type UpdateCollectorGroupDTO struct {
	ID          string `json:"id" binding:"required"`
	Name        string `json:"name" binding:"max=64"`
	IntervalMs  int    `json:"intervalMs" binding:"omitempty,min=100,max=600000"`
	Description string `json:"description" binding:"max=500"`
	Status      *int   `json:"status" binding:"omitempty,oneof=0 1"`
}

// PageCollectorGroupDTO 采集组分页查询（设备维度）。
type PageCollectorGroupDTO struct {
	Page     int    `form:"page" binding:"required,min=1"`
	Size     int    `form:"size" binding:"required,min=1,max=100"`
	DeviceID string `form:"deviceId" binding:"required"` // 所属设备ID
	Name     string `form:"name"`                        // 组名模糊查询
}

// ListCollectorGroupsDTO 采集组下拉列表。
//
// DeviceID 为空时返回全部设备的组（推送通道的订阅选择器需要跨设备选择），
// 非空时只返回该设备的组（点位表单使用）。
type ListCollectorGroupsDTO struct {
	DeviceID string `form:"deviceId"`
}

// BatchAssignCollectorGroupDTO 批量把点位分配/解绑到采集组。
//
// GroupID 为空串 = 解绑（点位回到 scan_frequency 旧行为）。
// deviceId 是归属前置约束：这些 ids 必须属于这台设备才会被更新。
type BatchAssignCollectorGroupDTO struct {
	DeviceID string   `json:"deviceId" binding:"required"`
	IDs      []string `json:"ids" binding:"required,min=1,max=200"`
	GroupID  string   `json:"groupId"`
}
