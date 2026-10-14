// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// OpenApiSecretVO 开放接口密钥响应
type OpenApiSecretVO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
	// IsDefault 是否为启动播种的默认密钥（名称恒为 default）。
	// 前端据此禁用删除按钮并打「默认」标记；它随名称派生，不是库里的列。
	IsDefault bool   `json:"isDefault"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}
