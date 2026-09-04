// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// OpenApiDeviceVO 开放接口设备响应（裁剪版）。
// 相比 DeviceVO 剔除了 protocolJson（PLC 连接配置，敏感）与
// iotGatewayId、protocolId 等内部管理字段。
type OpenApiDeviceVO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ProtocolName    string `json:"protocolName"`
	Description     string `json:"description"`
	Status          int    `json:"status"`
	Online          bool   `json:"online"`          // 当前是否在线（无 active 断联报警）
	LastSuccessTime string `json:"lastSuccessTime"` // 最近一次成功采集时间（"" 表示尚无成功记录）
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

// OpenApiDeviceAddressVO 开放接口设备地址响应（只读点位定义）
type OpenApiDeviceAddressVO struct {
	ID             string `json:"id"`
	DeviceID       string `json:"deviceId"`
	Name           string `json:"name"`
	Label          string `json:"label"` // 地址 name 的中文说明（如温度、电流）
	CommonDataType string `json:"commonDataType"`
	DataType       string `json:"dataType"`
	RwPermission   string `json:"rwPermission"`
	ScanFrequency  int    `json:"scanFrequency"`
	Description    string `json:"description"`
	Status         int    `json:"status"`
}
