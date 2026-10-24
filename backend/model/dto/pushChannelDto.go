// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// ==================== 数据推送通道 DTO ====================

// CreatePushChannelDTO 创建数据推送通道请求
type CreatePushChannelDTO struct {
	// Type 通道类型（mqtt/http/tdengine-v3...）。旧客户端不传时回落为 Name。
	Type        string          `json:"type" binding:"max=50"`
	Name        string          `json:"name" binding:"required,min=1,max=100"`
	Description string          `json:"description"`
	ConfigJSON  json.RawMessage `json:"configJson" binding:"required"`
}

// UpdatePushChannelDTO 更新数据推送通道请求
type UpdatePushChannelDTO struct {
	ID          string          `json:"id" binding:"required"`
	Type        string          `json:"type" binding:"max=50"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	ConfigJSON  json.RawMessage `json:"configJson"`
	Status      *int            `json:"status"`
}

// TestPushChannelDTO 测试推送通道连通性请求
type TestPushChannelDTO struct {
	// Type 通道类型。旧客户端把类型放在 Name 里，服务端兼容回落。
	Type       string          `json:"type" binding:"max=50"`
	Name       string          `json:"name" binding:"max=100"`
	ConfigJSON json.RawMessage `json:"configJson" binding:"required"`
}

// PreviewPushChannelDTO 预览出站载荷请求
type PreviewPushChannelDTO struct {
	Type       string          `json:"type" binding:"required,max=50"`
	ConfigJSON json.RawMessage `json:"configJson" binding:"required"`
}

// TestSendPushChannelDTO 测试发送请求（会真的向配置的下游发请求）
type TestSendPushChannelDTO struct {
	Type       string          `json:"type" binding:"required,max=50"`
	ConfigJSON json.RawMessage `json:"configJson" binding:"required"`
}

// PreviewPushChannelMappingDTO 只预览出站编码，不需要通道类型与连接配置。
// Format 为空时按 mapping-json 处理（兼容旧前端）；script 格式同样把脚本配置
// 放在 Mapping 里。
type PreviewPushChannelMappingDTO struct {
	Format  string          `json:"format" binding:"max=30"`
	Mapping json.RawMessage `json:"mapping" binding:"required"`
}

// PagePushChannelDTO 数据推送通道分页查询
type PagePushChannelDTO struct {
	Page int    `form:"page" binding:"required,min=1"`
	Size int    `form:"size" binding:"required,min=1,max=100"`
	Name string `form:"name"` // 实例名（模糊查询）
	Type string `form:"type"` // 通道类型过滤（可选）
}
