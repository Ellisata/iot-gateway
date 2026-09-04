// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// ==================== 数据推送通道 DTO ====================

// CreatePushChannelDTO 创建数据推送通道请求
type CreatePushChannelDTO struct {
	Name        string          `json:"name" binding:"required,min=1,max=100"`
	Description string          `json:"description"`
	ConfigJSON  json.RawMessage `json:"configJson" binding:"required"`
}

// UpdatePushChannelDTO 更新数据推送通道请求
type UpdatePushChannelDTO struct {
	ID          string          `json:"id" binding:"required"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	ConfigJSON  json.RawMessage `json:"configJson"`
	Status      *int            `json:"status"`
}

// TestPushChannelDTO 测试推送通道连通性请求
type TestPushChannelDTO struct {
	Name       string          `json:"name" binding:"required"`
	ConfigJSON json.RawMessage `json:"configJson" binding:"required"`
}

// PagePushChannelDTO 数据推送通道分页查询
type PagePushChannelDTO struct {
	Page int    `form:"page" binding:"required,min=1"`
	Size int    `form:"size" binding:"required,min=1,max=100"`
	Name string `form:"name"` // 通道名称（模糊查询）
}
