// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// CreateUserDTO 创建用户请求
type CreateUserDTO struct {
	Username string `json:"username"     binding:"required,min=2,max=50"`
	Password string `json:"password" binding:"required,min=6"`
}

// UpdateUserDTO 更新用户请求
type UpdateUserDTO struct {
	Name   string `json:"name"   binding:"min=2,max=50"`
	Email  string `json:"email"  binding:"email"`
	Avatar string `json:"avatar"`
}

// ChangePasswordDTO 修改密码请求（三个字段均经 RSA 加密传输，同 LoginDTO）
type ChangePasswordDTO struct {
	OldPassword     string `json:"oldPassword"     binding:"required"`
	NewPassword     string `json:"newPassword"     binding:"required,min=6"`
	ConfirmPassword string `json:"confirmPassword" binding:"required"`
}

// LoginDTO 登录请求
type LoginDTO struct {
	Username string `json:"username"    binding:"required"`
	Password string `json:"password" binding:"required,min=6"`
}

// PageDTO 分页请求
type PageDTO struct {
	Page int `json:"page" form:"page" binding:"min=1"`
	Size int `json:"size" form:"size" binding:"min=1,max=100"`
}
