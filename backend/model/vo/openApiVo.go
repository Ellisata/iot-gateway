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

// OpenApiDeviceNameVO 开放接口设备名称响应（按ID批量查询）。
// 采用对象而非 map 结构，便于后续按兼容性承诺增量扩展字段。
type OpenApiDeviceNameVO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OpenApiAddressLabelVO 开放接口点位标签响应（按设备ID+点位ID批量查询）
type OpenApiAddressLabelVO struct {
	ID         string `json:"id"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"` // 所属设备名称（设备已删除时为空串）
	Label      string `json:"label"`      // 地址 name 的中文说明（如温度、电流）
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
	// 时间戳与设备 VO 对齐，供外部系统判断"这个点位是不是新配的/刚改过"。
	// 格式与网关其余时间字段一致（yyyy-MM-dd HH:mm:ss）。
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// OpenApiSyncDeviceVO 开放接口「设备 + 点位」分批同步响应（一台设备及其全部点位）。
//
// 匿名内嵌 OpenApiDeviceVO 而非复制字段：encoding/json 会把匿名嵌入结构体的字段
// 提升到本层，故 JSON 形态与 4.1 的设备对象**逐字段一致** —— 外部系统可复用同一套
// 解析代码，将来 4.1 增删字段这里也自动跟上，不会漂移出两个"差不多"的设备对象。
//
// Addresses 恒为非 nil 切片（无点位时是 []，不是 null）：设备不会因为"没有点位"
// 而从同步流里消失 —— 这正是本接口用嵌套形态而非扁平分段的理由。
type OpenApiSyncDeviceVO struct {
	OpenApiDeviceVO
	Addresses []OpenApiDeviceAddressVO `json:"addresses"`
}
