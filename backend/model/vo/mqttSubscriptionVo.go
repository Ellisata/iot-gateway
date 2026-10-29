// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

import (
	"encoding/json"

	"iot-gateway/collector"
)

// MqttSubscriptionVO MQTT 订阅响应
type MqttSubscriptionVO struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Broker       string          `json:"broker"`
	Port         string          `json:"port"`
	ClientID     string          `json:"clientId"`
	Username     string          `json:"username"`
	Password     string          `json:"password"`
	Topic        string          `json:"topic"`
	QoS          int             `json:"qos"`
	ParserType   string          `json:"parserType"`
	ParserConfig json.RawMessage `json:"parserConfig"`
	MappingJSON  json.RawMessage `json:"mappingJson"`
	SampleJSON   json.RawMessage `json:"sampleJson"`
	SampleTopic  string          `json:"sampleTopic"`
	Sort         int             `json:"sort"`
	Status       int             `json:"status"`
	CreatedAt    string          `json:"createdAt"`
	UpdatedAt    string          `json:"updatedAt"`
}

// MqttSubscriptionStatusVO MQTT 订阅运行状态
type MqttSubscriptionStatusVO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Broker      string `json:"broker"`
	Topic       string `json:"topic"`
	Running     bool   `json:"running"`
	Connected   bool   `json:"connected"`
	Messages    uint64 `json:"messages"` // 收到的报文数
	Records     uint64 `json:"records"`  // 成功转换并投递的记录数
	Errors      uint64 `json:"errors"`   // 转换/投递失败次数
	Skipped     uint64 `json:"skipped"`  // 报文内被跳过的记录数
	Dropped     uint64 `json:"dropped"`  // 队列满时丢弃的报文数
	LastMessage string `json:"lastMessageTime"`
	Error       string `json:"error"`
}

// MqttSubscriptionRecentMessagesVO 某个订阅最近收到的报文（新→旧）。
//
// Active 表示该订阅当前是否在管理器里。启用了但尚未加载/正在热加载时用它区分，
// 比返回「订阅不存在」更准确；前端据此显示「订阅当前未运行」。
type MqttSubscriptionRecentMessagesVO struct {
	Active   bool                              `json:"active"`
	Messages []MqttSubscriptionRecentMessageVO `json:"messages"`
}

// MqttSubscriptionRecentMessageVO 一条最近收到的订阅报文。
//
// Payload 是预览文本（最多 2 KiB），不是完整载荷；Size 是原始字节数。
// ReceivedAt 的秒级精度与 Status 一致，Seq 用于区分同一秒内到达的报文。
type MqttSubscriptionRecentMessageVO struct {
	Seq        uint64 `json:"seq"`
	Topic      string `json:"topic"`
	Payload    string `json:"payload"`
	Size       int    `json:"size"`
	Truncated  bool   `json:"truncated"`
	Oversized  bool   `json:"oversized"`
	ReceivedAt string `json:"receivedAt"`
	QoS        int    `json:"qos"`
	Retained   bool   `json:"retained"`
}

// MqttSubscriptionDevicePointVO 订阅设备/点位信息。
//
// 一行 = 订阅报文里出现过的一个点位及其设备元信息（自然键 deviceId + deviceAddressId）。
type MqttSubscriptionDevicePointVO struct {
	DeviceID           string `json:"deviceId"`
	DeviceAddressID    string `json:"deviceAddressId"`
	DeviceName         string `json:"deviceName"`
	DeviceAddressLabel string `json:"deviceAddressLabel"`
	Kind               string `json:"kind"`
	// Source auto=订阅报文自动发现，manual=手工新增/编辑/导入。
	Source    string `json:"source"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// BatchDeleteMqttSubscriptionDevicePointResultVO 批量删除订阅设备/点位的结果。
//
// Deleted 是实际删除的行数，可能小于 Requested：勾选后又被别人删掉、或该行
// 在这期间变回自动收录（source=auto 不在删除集里）都是正常情况，
// 前端据此提示"少删了几条"，而不是把它当成整批失败。
type BatchDeleteMqttSubscriptionDevicePointResultVO struct {
	Requested int   `json:"requested"`
	Deleted   int64 `json:"deleted"`
}

// MqttSubscriptionPreviewVO 映射预览结果。
//
// 转换失败不是业务错误：配置写错是编辑过程中的正常反馈，前端直接展示在预览面板里。
type MqttSubscriptionPreviewVO struct {
	Count   int                         `json:"count"`
	Records []collector.CollectedRecord `json:"records"`
	Skipped int                         `json:"skipped"`
	Error   string                      `json:"error"`
}
