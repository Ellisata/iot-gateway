// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// GatewayVO 网关响应
type GatewayVO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	IP              string `json:"ip"`
	Port            int    `json:"port"`
	Protocol        string `json:"protocol"`
	FirmwareVersion string `json:"firmwareVersion"`
	Description     string `json:"description"`
	Status          int    `json:"status"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

// DeviceVO 设备响应
type DeviceVO struct {
	ID              string `json:"id"`
	IotGatewayID    string `json:"iotGatewayId"`
	Name            string `json:"name"`
	ProtocolID      string `json:"protocolId"`
	ProtocolName    string `json:"protocolName"`
	ProtocolJSON    string `json:"protocolJson"`
	Description     string `json:"description"`
	Status          int    `json:"status"`
	Online          bool   `json:"online"`          // 当前是否在线（无 active 断联报警）
	LastSuccessTime string `json:"lastSuccessTime"` // 最近一次成功采集时间（内存态，"" 表示尚无成功记录）
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

// DeviceOverviewVO 设备在线情况统计响应
type DeviceOverviewVO struct {
	Total       int     `json:"total"`       // 全部设备数（启用 + 禁用）
	Enabled     int     `json:"enabled"`     // 启用设备数（status=1）
	Disabled    int     `json:"disabled"`    // 禁用设备数（status != 1）
	Collected   int     `json:"collected"`   // 启用且已接入采集调度、有活跃地址的设备数
	Online      int     `json:"online"`      // 采集正常（无 active 断联报警）
	Offline     int     `json:"offline"`     // 已判定离线
	UnCollected int     `json:"unCollected"` // 启用但未接入采集（协议不支持/无驱动/无活跃地址）
	OnlineRate  float64 `json:"onlineRate"`  // 在线率 = online / collected（保留 2 位小数）
}

// DeviceAddressVO 设备地址响应
type DeviceAddressVO struct {
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
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}
