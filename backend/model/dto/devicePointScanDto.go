// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// ==================== 点位范围扫描 DTO ====================
//
// 与 deviceScanDto.go 是同一个页面上的两件事，所以归在同一个 controller 下，
// 但请求形状完全不同：设备扫描面向**网段**（目标由网关规划），点位扫描面向
// **一台已存在的设备**（目标由 deviceId 指定）。

// PointCapabilityQueryDTO 查询某个协议的点位扫描能力（设备地址页进页面时问一次）。
type PointCapabilityQueryDTO struct {
	// ProtocolName 协议名（= iot_protocol.name = 设备地址页路由上的 protocolName）。
	//
	// 它只决定"该显示哪个协议的能力与表单"，不决定网关去连哪台设备 ——
	// 那由 DiscoverPoints 的 deviceId 在服务端定。因此这里采信客户端是安全的。
	ProtocolName string `form:"protocolName" binding:"required"`
}

// DiscoverPointsDTO 发起一次点位范围扫描。
type DiscoverPointsDTO struct {
	// DeviceID 要扫描的设备。**只收 id，不收连接配置** —— protocolJson 由服务端
	// 从 device 行读取后透传给插件（照 CreateDevicesFromScanDTO 的先例，不采信
	// 客户端的连接配置：它决定网关去连哪台设备）。
	DeviceID string `json:"deviceId" binding:"required"`

	// Options 扫描选项，形状由该协议的 pointFormJson 定义，原样透传给插件。
	// 与 StartDeviceScanDTO.Options 同样是 RawMessage：不同协议的扫描参数差别很大
	// （Modbus 是功能区+范围，将来 OPC UA 可能是节点前缀），服务端不做结构解析。
	Options json.RawMessage `json:"options"`
}

// CreateAddressesFromScanDTO 把勾选的点位批量建成 device_address 行。
type CreateAddressesFromScanDTO struct {
	// DeviceID 这批点位挂在哪台设备下。
	DeviceID string `json:"deviceId" binding:"required"`

	// Items 要创建的点位。
	Items []ScanAddressItemDTO `json:"items" binding:"required,min=1"`
}

// ScanAddressItemDTO 一条待创建的点位。
//
// 与 ScanDeviceItemDTO 的**关键差异：这里采信客户端的全部字段**。
// 设备扫描那边要防的是"客户端篡改决定网关去连哪台设备的连接配置"，
// 所以 protocolJson 由服务端从快照里取；而点位的名称/标签/类型本来就是用户
// 在界面上逐行编辑的产物，服务端只做校验与去重，没有"权威来源"可言。
// 唯一的服务端权威是 DeviceID —— 它决定协议，从而决定类型名怎么映射。
type ScanAddressItemDTO struct {
	// Name **设备地址写法**（如 %MW100）。前端把它渲染成只读列 ——
	// 名字非法（该协议解析不出）的后果是这个点位永远没有值（只报「地址无效」，
	// 要改配置才恢复），所以它是从扫描结果原样带过来的。
	// 注意**服务端这一层不校验写法**（驱动在插件里，主二进制不含协议解析器），
	// 校验只发生在驱动的读取路径上。
	Name string `json:"name" binding:"required"`

	// Label 中文标签。留空时服务端兜底为 Name（见 CreateAddresses 的说明）。
	Label string `json:"label"`

	// CommonDataType 通用数据类型名（如 "Word"），由服务端映射成协议内部类型。
	CommonDataType string `json:"commonDataType" binding:"required"`

	// RwPermission 读写权限：R / W / RW。
	RwPermission string `json:"rwPermission"`

	// ScanFrequency 扫描频率（毫秒）。<= 0 表示未指定，按默认 1000 处理
	// （与单条创建、Excel 导入走的是同一套规则）。
	ScanFrequency int `json:"scanFrequency"`

	// Description 描述。
	Description string `json:"description"`
}
