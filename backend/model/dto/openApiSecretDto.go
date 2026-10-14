// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// ==================== 开放接口密钥 DTO ====================

// CreateOpenApiSecretDTO 创建开放接口密钥请求
type CreateOpenApiSecretDTO struct {
	Name string `json:"name" binding:"required,min=1,max=100"`
}

// UpdateOpenApiSecretNameDTO 编辑开放接口密钥名称请求
type UpdateOpenApiSecretNameDTO struct {
	ID   string `json:"id" binding:"required"`
	Name string `json:"name" binding:"required,min=1,max=100"`
}
