// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

// CollectedRecord 采集记录
type CollectedRecord struct {
	DeviceID           string `json:"deviceID"`
	DeviceName         string `json:"deviceName"`
	DeviceAddressID    string `json:"deviceAddressID"`
	DeviceAddressName  string `json:"deviceAddressName"`
	DeviceAddressLabel string `json:"deviceAddressLabel"` // 地址 name 的中文说明（如温度、电流）
	Value              string `json:"value"`
	DataType           string `json:"dataType"`
	// Kind 数据类型类别（driver.KindXxx），由驱动从已解析类型填充。
	// 携带 json tag 以保证断网缓存（push/spool）序列化回放后不丢失。
	// MQTT 通道自行构建 pushPoint，不受本字段影响。
	Kind string `json:"kind"`
	// Protocol 协议名称（iot_protocol.name，如 "ModBus.TCP"），由 collector 填充，
	// 供下游区分协议命名空间（dataType 为协议内部类型名）。
	Protocol string `json:"protocol"`
	// Quality 采集质量码（driver.QualityXxx：类别在高 2 位、成因在低 6 位）。
	// 带 json tag 以保证断网缓存（push/spool）序列化回放后不丢失。
	Quality int `json:"quality"`
	// CollectedAt 采集时间（毫秒精度本地时间，如 "2026-10-19 17:05:30.123"）。
	//
	// 本地点位采集留空：推送引擎按批次落地时间统一补齐（见 push.Engine.pushRecords）。
	// 外部 MQTT 订阅可在映射里配置 collectedAt，携带报文自带的时间；
	// 时序通道逐条优先用它，留空/无法解析时才回落到批次时间。
	CollectedAt string `json:"collectedAt,omitempty"`
}
