// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// ==================== 协议插件 DTO ====================

// UninstallPluginDTO 卸载插件请求。
//
// File 是插件目录下的**文件名**（basename），不是任意路径：卸载能力只对
// 管理端开放，但也没有理由把「删任意文件」的入口留在请求体里。
type UninstallPluginDTO struct {
	File string `json:"file" binding:"required,min=1,max=255"`
}
