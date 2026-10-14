// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// ==================== 网关身份 DTO ====================

// UpdateGatewayDTO 编辑网关身份请求。
//
// 刻意只含 name 与 description 两个可编辑项：
//   - id 由首次启动生成，是网关的唯一标识，改了等于换一台设备；
//   - firmware_version 只读，以配置项 gateway.version 为准（启动时自动同步进库），
//     让运维手改会把「版本」变成一句无法核实的话；
//   - status 本轮不开放。
type UpdateGatewayDTO struct {
	Name        string `json:"name" binding:"required,min=1,max=100"`
	Description string `json:"description" binding:"max=500"`
}
