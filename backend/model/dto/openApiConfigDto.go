// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// ==================== 配置管理只读接口 ====================

// OpenApiConfigPageDTO 配置域通用分页查询。
//
// Size 上限 100；设备配置对象较大，使用 OpenApiDeviceConfigPageDTO（上限 20）。
type OpenApiConfigPageDTO struct {
	Page    int    `form:"page" binding:"required,min=1"`
	Size    int    `form:"size" binding:"required,min=1,max=100"`
	Keyword string `form:"keyword"`
	Status  *int   `form:"status"`
}

// OpenApiDeviceConfigPageDTO 设备配置分页查询。设备对象含点位与采集组，页大小收紧到 20。
type OpenApiDeviceConfigPageDTO struct {
	Page    int    `form:"page" binding:"required,min=1"`
	Size    int    `form:"size" binding:"required,min=1,max=20"`
	Keyword string `form:"keyword"`
	Status  *int   `form:"status"`
}

// OpenApiPushChannelPageDTO 数据推送通道分页查询。
type OpenApiPushChannelPageDTO struct {
	Page   int    `form:"page" binding:"required,min=1"`
	Size   int    `form:"size" binding:"required,min=1,max=100"`
	Name   string `form:"name"`
	Type   string `form:"type"`
	Status *int   `form:"status"`
}

// OpenApiAlarmWebhookPageDTO 告警 Webhook 分页查询。
type OpenApiAlarmWebhookPageDTO struct {
	Page   int    `form:"page" binding:"required,min=1"`
	Size   int    `form:"size" binding:"required,min=1,max=100"`
	Name   string `form:"name"`
	Type   string `form:"type"`
	Status *int   `form:"status"`
}

// ==================== 数据推送通道批量写 ====================

// OpenApiPushChannelBatchUpsertDTO 推送通道批量 upsert。
//
// 自然键是 (type, name)。逐条结果、部分成功；顶层响应恒为成功信封。
type OpenApiPushChannelBatchUpsertDTO struct {
	Items []OpenApiPushChannelUpsertDTO `json:"items" binding:"required,min=1,max=100"`
}

// OpenApiPushChannelUpsertDTO 单个推送通道。
//
// Config 是通道配置对象（不是字符串）。更新时省略表示保持原配置；
// 给出时按路径把 "***" 替换为现有密钥，未出现的密钥字段保持原值。
type OpenApiPushChannelUpsertDTO struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description *string         `json:"description"`
	Config      json.RawMessage `json:"config"`
	Status      *int            `json:"status"`
}

// OpenApiPushChannelKeyDTO 推送通道自然键。
type OpenApiPushChannelKeyDTO struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// OpenApiPushChannelBatchDeleteDTO 推送通道批量删除。
type OpenApiPushChannelBatchDeleteDTO struct {
	Items []OpenApiPushChannelKeyDTO `json:"items" binding:"required,min=1,max=100"`
}

// ==================== 告警 Webhook 批量写 ====================

// OpenApiAlarmWebhookBatchUpsertDTO 告警 Webhook 批量 upsert（自然键 name）。
type OpenApiAlarmWebhookBatchUpsertDTO struct {
	Items []OpenApiAlarmWebhookUpsertDTO `json:"items" binding:"required,min=1,max=100"`
}

// OpenApiAlarmWebhookUpsertDTO 单个告警 Webhook。
//
// Secret 为 "***" 或省略时保留现有密钥；显式空串表示清空。
// AtList 直接使用字符串数组，避免界面逗号分隔格式进入开放接口。
type OpenApiAlarmWebhookUpsertDTO struct {
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Description *string   `json:"description"`
	URL         *string   `json:"url"`
	Secret      *string   `json:"secret"`
	MsgType     *string   `json:"msgType"`
	AtAll       *bool     `json:"atAll"`
	AtList      *[]string `json:"atList"`
	Status      *int      `json:"status"`
}

// OpenApiAlarmWebhookBatchDeleteDTO 告警 Webhook 批量删除（按 name）。
type OpenApiAlarmWebhookBatchDeleteDTO struct {
	Names []string `json:"names" binding:"required,min=1,max=100"`
}

// ==================== 运行开关批量写 ====================

// OpenApiSystemConfigBatchUpdateDTO 运行开关批量修改。
type OpenApiSystemConfigBatchUpdateDTO struct {
	Items []OpenApiSystemConfigUpdateDTO `json:"items" binding:"required,min=1,max=50"`
}

// OpenApiSystemConfigUpdateDTO 单个开关。Value 是 JSON 标量（bool / 枚举整数）。
type OpenApiSystemConfigUpdateDTO struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// OpenApiSystemConfigBatchResetDTO 运行开关批量恢复默认。
type OpenApiSystemConfigBatchResetDTO struct {
	Keys []string `json:"keys" binding:"required,min=1,max=50"`
}

// ==================== 设备与点位批量写 ====================

// OpenApiDeviceConfigBatchUpsertDTO 设备配置批量 upsert。
//
// 设备按自然键匹配：Code 非空用 Code，否则用 Name。点位/采集组在设备内按 Name
// 匹配。每台设备一个事务，跨设备部分成功。
type OpenApiDeviceConfigBatchUpsertDTO struct {
	Items []OpenApiDeviceConfigUpsertDTO `json:"items" binding:"required,min=1,max=20"`
}

// OpenApiDeviceConfigUpsertDTO 单台设备及其采集组、点位。
//
// Mode: merge（默认，只 upsert）| replace（删除本次未出现的点位与采集组）。
// 管理平台统一使用 replace，否则期望态无法收敛。
type OpenApiDeviceConfigUpsertDTO struct {
	Mode         string                           `json:"mode"`
	Code         string                           `json:"code"`
	Name         string                           `json:"name"`
	ProtocolName string                           `json:"protocolName"`
	ProtocolJSON json.RawMessage                  `json:"protocolJson"`
	ModelCode    string                           `json:"modelCode"`
	Description  *string                          `json:"description"`
	Status       *int                             `json:"status"`
	Groups       []OpenApiCollectorGroupUpsertDTO `json:"groups"`
	Points       []OpenApiDevicePointUpsertDTO    `json:"points"`
}

// OpenApiCollectorGroupUpsertDTO 采集组。设备内按 Name 匹配；Code 非空时设备内唯一。
type OpenApiCollectorGroupUpsertDTO struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	IntervalMs  *int    `json:"intervalMs"`
	Description *string `json:"description"`
	Status      *int    `json:"status"`
}

// OpenApiDevicePointUpsertDTO 点位。设备内按 Name（设备地址写法）匹配。
//
// PropertyIdentifier / GroupName 使用自然键，禁止传网关内部 ID。
type OpenApiDevicePointUpsertDTO struct {
	Name               string               `json:"name"`
	Label              string               `json:"label"`
	CommonDataType     string               `json:"commonDataType"`
	RwPermission       string               `json:"rwPermission"`
	ScanFrequency      *int                 `json:"scanFrequency"`
	Description        *string              `json:"description"`
	Status             *int                 `json:"status"`
	PropertyIdentifier *string              `json:"propertyIdentifier"`
	GroupName          *string              `json:"groupName"`
	Threshold          *OpenApiThresholdDTO `json:"threshold"`
	Transform          json.RawMessage      `json:"transform"`
}

// OpenApiThresholdDTO 报警条件。
type OpenApiThresholdDTO struct {
	Enabled bool     `json:"enabled"`
	Op      string   `json:"op"`
	Values  []string `json:"values"`
}

// OpenApiDeviceConfigKeyDTO 设备自然键。
type OpenApiDeviceConfigKeyDTO struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// OpenApiDeviceConfigBatchDeleteDTO 设备批量删除。
type OpenApiDeviceConfigBatchDeleteDTO struct {
	Items []OpenApiDeviceConfigKeyDTO `json:"items" binding:"required,min=1,max=20"`
}
