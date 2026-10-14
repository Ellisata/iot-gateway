// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// GatewayVO 网关自身身份响应。
//
// 单行表：整个网关只有一条身份记录，见 migrations/012_iot_gateway.sql。
// 原先定义在 deviceVo.go 里，随网关身份模块独立成文件。
type GatewayVO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Port            int    `json:"port"`
	FirmwareVersion string `json:"firmwareVersion"`
	Description     string `json:"description"`
	Status          int    `json:"status"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}
