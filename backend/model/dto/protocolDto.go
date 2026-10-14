// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// ==================== 协议 DTO ====================

// UpdateProtocolDTO 更新协议请求。
//
// 没有 CreateProtocolDTO：协议只能由插件供给，手工建的行没有驱动与之对应。
// Name 仍在此保留，但仅用于**校验它没被改动**（协议名是与插件驱动的唯一纽带，
// 见 ProtocolService.UpdateProtocol）；服务端不会把它写回库里。
type UpdateProtocolDTO struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	FormJSON    json.RawMessage `json:"formJson"`
	Sort        *int            `json:"sort"`
}

// UpdateProtocolFormByNameDTO 根据协议名称更新表单 JSON
type UpdateProtocolFormByNameDTO struct {
	Name     string          `json:"name" binding:"required,min=1,max=100"`
	FormJSON json.RawMessage `json:"formJson" binding:"required"`
}

// UpdateProtocolFormByIdDTO 根据协议ID更新表单 JSON
type UpdateProtocolFormByIdDTO struct {
	ID       string          `json:"id" binding:"required"`
	FormJSON json.RawMessage `json:"formJson" binding:"required"`
}

// PageProtocolDTO 协议分页查询
type PageProtocolDTO struct {
	Page int    `form:"page" binding:"required,min=1"`
	Size int    `form:"size" binding:"required,min=1,max=100"`
	Name string `form:"name"` // 协议名称（模糊查询）
}
