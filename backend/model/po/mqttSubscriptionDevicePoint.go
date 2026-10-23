// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

// 订阅设备/点位行的维护来源。
const (
	// MqttSubscriptionDevicePointSourceAuto 由订阅报文自动 upsert。
	MqttSubscriptionDevicePointSourceAuto = "auto"
	// MqttSubscriptionDevicePointSourceManual 手工新增 / 编辑 / Excel 导入；
	// 自动上报不得再覆盖该行的元信息。
	MqttSubscriptionDevicePointSourceManual = "manual"
)

// MqttSubscriptionDevicePoint MQTT 订阅设备/点位信息索引。
//
// 一行 = 订阅报文里出现过的一个点位，连同它的设备与点位元信息。
// 自然键是 (device_id, device_address_id)：与 TDengine 子表 / InfluxDB tag 的
// 身份口径一致，同一个点位被多条订阅上报时也只有一行。
//
// 本表只做元信息索引，不存 value / quality 等采样值（那些只进时序库）。
type MqttSubscriptionDevicePoint struct {
	DeviceID           string `gorm:"column:device_id;primaryKey" json:"deviceId"`
	DeviceAddressID    string `gorm:"column:device_address_id;primaryKey" json:"deviceAddressId"`
	DeviceName         string `gorm:"column:device_name;not null;default:''" json:"deviceName"`
	DeviceAddressLabel string `gorm:"column:device_address_label;not null;default:''" json:"deviceAddressLabel"`
	Kind               string `gorm:"column:kind;not null;default:''" json:"kind"`
	// Source 维护来源：auto（订阅报文自动上报）/ manual（手工维护）。
	Source    string `gorm:"column:source;not null;default:'auto'" json:"source"`
	CreatedAt string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (MqttSubscriptionDevicePoint) TableName() string {
	return "mqtt_subscription_device_point"
}
