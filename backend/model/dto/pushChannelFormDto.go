// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// ==================== 数据推送通道表单 DTO ====================

// CreatePushChannelFormDTO 创建数据推送通道表单请求
type CreatePushChannelFormDTO struct {
	Name     string          `json:"name" binding:"required,min=1,max=100"`
	FormJSON json.RawMessage `json:"formJson" binding:"required"`
}

// UpdatePushChannelFormDTO 更新数据推送通道表单请求
type UpdatePushChannelFormDTO struct {
	ID       string          `json:"id" binding:"required"`
	Name     string          `json:"name"`
	FormJSON json.RawMessage `json:"formJson"`
}

// GetPushChannelFormByNameDTO 根据名称查询数据推送通道表单请求
type GetPushChannelFormByNameDTO struct {
	Name string `form:"name" binding:"required,min=1,max=100"`
}
