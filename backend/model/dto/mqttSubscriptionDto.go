// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// CreateMqttSubscriptionDTO 新增 MQTT 订阅请求
//
// MappingJSON 与 ParserConfig 互斥：builtin 用 mappingJson，其余解析方式用
// parserConfig。binding 不强制 mappingJson，由服务层按 parserType 校验。
type CreateMqttSubscriptionDTO struct {
	Name         string          `json:"name"         binding:"required,min=1,max=100"`
	Description  string          `json:"description"`
	Broker       string          `json:"broker"       binding:"required,min=1,max=200"`
	Port         string          `json:"port"`
	ClientID     string          `json:"clientId"`
	Username     string          `json:"username"`
	Password     string          `json:"password"`
	Topic        string          `json:"topic"        binding:"required,min=1,max=500"`
	QoS          int             `json:"qos"`
	ParserType   string          `json:"parserType"`
	ParserConfig json.RawMessage `json:"parserConfig"`
	MappingJSON  json.RawMessage `json:"mappingJson"`
	SampleJSON   json.RawMessage `json:"sampleJson"`
	SampleTopic  string          `json:"sampleTopic"`
	Sort         int             `json:"sort"`
	Status       *int            `json:"status"`
}

// UpdateMqttSubscriptionDTO 编辑 MQTT 订阅请求
//
// parserConfig/mappingJson 为空表示沿用原值（列表页只改启停时不必回传整份配置）。
type UpdateMqttSubscriptionDTO struct {
	ID           string          `json:"id"           binding:"required"`
	Name         string          `json:"name"         binding:"required,min=1,max=100"`
	Description  string          `json:"description"`
	Broker       string          `json:"broker"       binding:"required,min=1,max=200"`
	Port         string          `json:"port"`
	ClientID     string          `json:"clientId"`
	Username     string          `json:"username"`
	Password     string          `json:"password"`
	Topic        string          `json:"topic"        binding:"required,min=1,max=500"`
	QoS          int             `json:"qos"`
	ParserType   string          `json:"parserType"`
	ParserConfig json.RawMessage `json:"parserConfig"`
	MappingJSON  json.RawMessage `json:"mappingJson"`
	SampleJSON   json.RawMessage `json:"sampleJson"`
	SampleTopic  string          `json:"sampleTopic"`
	Sort         int             `json:"sort"`
	Status       *int            `json:"status"`
}

// PageMqttSubscriptionDTO MQTT 订阅分页查询
type PageMqttSubscriptionDTO struct {
	Page   int    `form:"page"   binding:"required,min=1"`
	Size   int    `form:"size"   binding:"required,min=1,max=100"`
	Name   string `form:"name"`
	Status *int   `form:"status"`
}

// PageMqttSubscriptionDevicePointDTO 订阅设备/点位信息分页查询。
//
// 数据来自订阅报文转换后的元信息索引（mqtt_subscription_device_point），
// 不含采样值；名称/ID 均按模糊匹配。
type PageMqttSubscriptionDevicePointDTO struct {
	Page            int    `form:"page"              binding:"required,min=1"`
	Size            int    `form:"size"              binding:"required,min=1,max=100"`
	DeviceID        string `form:"deviceId"`
	DeviceName      string `form:"deviceName"`
	DeviceAddressID string `form:"deviceAddressId"`
	// Source 维护来源：manual 手工维护 / auto 订阅报文自动收录；空 = 全部。
	Source string `form:"source" binding:"omitempty,oneof=manual auto"`
}

// CreateMqttSubscriptionDevicePointDTO 手工新增订阅设备/点位。
//
// deviceId + deviceAddressId 是自然键（与时序库身份口径一致），创建后不可修改。
type CreateMqttSubscriptionDevicePointDTO struct {
	DeviceID           string `json:"deviceId"           binding:"required,min=1,max=128"`
	DeviceAddressID    string `json:"deviceAddressId"    binding:"required,min=1,max=128"`
	DeviceName         string `json:"deviceName"         binding:"max=100"`
	DeviceAddressLabel string `json:"deviceAddressLabel" binding:"max=100"`
	Kind               string `json:"kind"`
}

// UpdateMqttSubscriptionDevicePointDTO 编辑订阅设备/点位。
//
// deviceId / deviceAddressId 只用于定位，不是可修改字段；元信息按提交值覆盖，
// 空串表示清空。
type UpdateMqttSubscriptionDevicePointDTO struct {
	DeviceID           string `json:"deviceId"           binding:"required,min=1,max=128"`
	DeviceAddressID    string `json:"deviceAddressId"    binding:"required,min=1,max=128"`
	DeviceName         string `json:"deviceName"         binding:"max=100"`
	DeviceAddressLabel string `json:"deviceAddressLabel" binding:"max=100"`
	Kind               string `json:"kind"`
}

// DeleteMqttSubscriptionDevicePointDTO 删除订阅设备/点位。
//
// 用查询参数而不是路径参数：自然键是两个可能含特殊字符的字符串，
// 放进 URL 路径需要额外转义，查询参数没有这个问题。
type DeleteMqttSubscriptionDevicePointDTO struct {
	DeviceID        string `json:"deviceId"        form:"deviceId"        binding:"required,min=1,max=128"`
	DeviceAddressID string `json:"deviceAddressId" form:"deviceAddressId" binding:"required,min=1,max=128"`
}

// BatchDeleteMqttSubscriptionDevicePointDTO 批量删除订阅设备/点位请求。
//
// 每项是自然键对（该表主键就是 device_id + device_address_id）。上限 200
// 与设备 / 设备地址批量删除一致：再多的勾选不是正常操作，挡住的是误传
// 超大列表把一次请求变成一长串删除条件。
type BatchDeleteMqttSubscriptionDevicePointDTO struct {
	Items []DeleteMqttSubscriptionDevicePointDTO `json:"items" binding:"required,min=1,max=200,dive"`
}

// RestoreMqttSubscriptionDevicePointAutoDTO 把手工艺维护的行交还给自动收录。
//
// 只带自然键：操作不改任何元信息，只改维护方式。
type RestoreMqttSubscriptionDevicePointAutoDTO struct {
	DeviceID        string `json:"deviceId"        binding:"required,min=1,max=128"`
	DeviceAddressID string `json:"deviceAddressId" binding:"required,min=1,max=128"`
}

// TestMqttSubscriptionDTO 订阅连通性测试请求（不落库）
type TestMqttSubscriptionDTO struct {
	Broker   string `json:"broker"   binding:"required,min=1,max=200"`
	Port     string `json:"port"`
	ClientID string `json:"clientId"`
	Username string `json:"username"`
	Password string `json:"password"`
	Topic    string `json:"topic"    binding:"required,min=1,max=500"`
}

// PreviewMqttSubscriptionDTO 解析预览请求（不落库）。
//
// parserType 为空按 builtin；builtin 读 mappingJson，其余读 parserConfig。
type PreviewMqttSubscriptionDTO struct {
	ParserType   string          `json:"parserType"`
	ParserConfig json.RawMessage `json:"parserConfig"`
	MappingJSON  json.RawMessage `json:"mappingJson"`
	SampleJSON   json.RawMessage `json:"sampleJson"  binding:"required"`
	SampleTopic  string          `json:"sampleTopic"`
}
