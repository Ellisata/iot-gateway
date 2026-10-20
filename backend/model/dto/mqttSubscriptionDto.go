// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// CreateMqttSubscriptionDTO 新增 MQTT 订阅请求
type CreateMqttSubscriptionDTO struct {
	Name        string          `json:"name"        binding:"required,min=1,max=100"`
	Description string          `json:"description"`
	Broker      string          `json:"broker"      binding:"required,min=1,max=200"`
	Port        string          `json:"port"`
	ClientID    string          `json:"clientId"`
	Username    string          `json:"username"`
	Password    string          `json:"password"`
	Topic       string          `json:"topic"       binding:"required,min=1,max=500"`
	QoS         int             `json:"qos"`
	MappingJSON json.RawMessage `json:"mappingJson" binding:"required"`
	SampleJSON  json.RawMessage `json:"sampleJson"`
	Sort        int             `json:"sort"`
	Status      *int            `json:"status"`
}

// UpdateMqttSubscriptionDTO 编辑 MQTT 订阅请求
type UpdateMqttSubscriptionDTO struct {
	ID          string          `json:"id"          binding:"required"`
	Name        string          `json:"name"        binding:"required,min=1,max=100"`
	Description string          `json:"description"`
	Broker      string          `json:"broker"      binding:"required,min=1,max=200"`
	Port        string          `json:"port"`
	ClientID    string          `json:"clientId"`
	Username    string          `json:"username"`
	Password    string          `json:"password"`
	Topic       string          `json:"topic"       binding:"required,min=1,max=500"`
	QoS         int             `json:"qos"`
	MappingJSON json.RawMessage `json:"mappingJson"`
	SampleJSON  json.RawMessage `json:"sampleJson"`
	Sort        int             `json:"sort"`
	Status      *int            `json:"status"`
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
	Page              int    `form:"page"              binding:"required,min=1"`
	Size              int    `form:"size"              binding:"required,min=1,max=100"`
	DeviceID          string `form:"deviceId"`
	DeviceName        string `form:"deviceName"`
	DeviceAddressID   string `form:"deviceAddressId"`
	DeviceAddressName string `form:"deviceAddressName"`
	Protocol          string `form:"protocol"`
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

// PreviewMqttSubscriptionDTO 映射规则预览请求（不落库）
type PreviewMqttSubscriptionDTO struct {
	MappingJSON json.RawMessage `json:"mappingJson" binding:"required"`
	SampleJSON  json.RawMessage `json:"sampleJson"  binding:"required"`
}
