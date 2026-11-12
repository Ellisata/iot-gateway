// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

import (
	"encoding/json"

	"iot-gateway/notify"
	"iot-gateway/push"
)

// 配置批量写动作。
const (
	OpenApiConfigActionCreated = "created"
	OpenApiConfigActionUpdated = "updated"
	OpenApiConfigActionDeleted = "deleted"
	OpenApiConfigActionFailed  = "failed"
)

// OpenApiConfigCapabilitiesVO 网关配置同步能力。
//
// 管理平台先调该接口：types 决定某个配置域是否可读写、单批上限多少；
// hashVersion 与本地不一致时禁止做漂移判断。
type OpenApiConfigCapabilitiesVO struct {
	CfgSyncVersion int                                      `json:"cfgSyncVersion"`
	OpenApiVersion string                                   `json:"openApiVersion"`
	GatewayVersion string                                   `json:"gatewayVersion"`
	HashVersion    int                                      `json:"hashVersion"`
	HashAlgorithm  string                                   `json:"hashAlgorithm"`
	Types          map[string]OpenApiConfigTypeCapabilityVO `json:"types"`
}

// OpenApiConfigTypeCapabilityVO 单个配置域的能力。
type OpenApiConfigTypeCapabilityVO struct {
	Read              bool `json:"read"`
	Upsert            bool `json:"upsert"`
	Delete            bool `json:"delete"`
	Update            bool `json:"update"`
	Reset             bool `json:"reset"`
	MaxBatch          int  `json:"maxBatch"`
	ReplaceProperties bool `json:"replaceProperties"`
	ReplacePoints     bool `json:"replacePoints"`
}

// OpenApiConfigItemVO 一条配置的期望态投影。
//
// Payload 的字段与批量 upsert 请求体逐字对应，正是管理平台应保存的期望态；
// 密钥字段已脱敏为 "***"（空值仍为空串）。ContentHash 由网关按
// sha256-canonical-json-v1 计算，管理平台用同一算法对 Payload 计算即可比对。
type OpenApiConfigItemVO struct {
	TargetKey   string          `json:"targetKey"`
	Payload     json.RawMessage `json:"payload"`
	ContentHash string          `json:"contentHash"`
}

// OpenApiRuntimeConfigVO 运行开关的期望态 + 元数据。
type OpenApiRuntimeConfigVO struct {
	TargetKey      string                   `json:"targetKey"`
	Key            string                   `json:"key"`
	Type           string                   `json:"type"`
	Value          any                      `json:"value"`
	DefaultValue   any                      `json:"defaultValue"`
	Options        []OpenApiRuntimeOptionVO `json:"options,omitempty"`
	Source         string                   `json:"source"`
	Hot            bool                     `json:"hot"`
	PendingRestart bool                     `json:"pendingRestart"`
	LabelKey       string                   `json:"labelKey"`
	DescKey        string                   `json:"descKey"`
	ContentHash    string                   `json:"contentHash"`
}

// OpenApiRuntimeOptionVO 枚举开关的选项。
type OpenApiRuntimeOptionVO struct {
	Value    int    `json:"value"`
	LabelKey string `json:"labelKey"`
}

// OpenApiFormVO 动态表单 schema（推送通道 / 告警 Webhook）。
type OpenApiFormVO struct {
	Name     string          `json:"name"`
	FormJSON json.RawMessage `json:"formJson"`
}

// OpenApiProtocolVO 协议及其点位数据类型（供设备模板编辑）。
type OpenApiProtocolVO struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	FormJSON      json.RawMessage   `json:"formJson"`
	CommonTypes   []string          `json:"commonTypes"`
	ExtendedTypes []string          `json:"extendedTypes"`
	KindByType    map[string]string `json:"kindByType"`
}

// OpenApiPushChannelTypeVO 推送通道类型元数据。
type OpenApiPushChannelTypeVO = push.ChannelTypeMeta

// OpenApiAlarmWebhookTypeVO 告警 Webhook 类型元数据。
type OpenApiAlarmWebhookTypeVO = notify.TypeMeta

// OpenApiConfigBatchUpsertVO 配置批量 upsert 响应（逐条结果、部分成功）。
type OpenApiConfigBatchUpsertVO struct {
	Total   int                         `json:"total"`
	Success int                         `json:"success"`
	Failed  int                         `json:"failed"`
	Created int                         `json:"created"`
	Updated int                         `json:"updated"`
	Items   []OpenApiConfigUpsertItemVO `json:"items"`
}

// OpenApiConfigUpsertItemVO 单条配置 upsert 结果。
type OpenApiConfigUpsertItemVO struct {
	TargetKey    string `json:"targetKey"`
	Action       string `json:"action"`
	ContentHash  string `json:"contentHash,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// OpenApiConfigBatchDeleteVO 配置批量删除响应。
type OpenApiConfigBatchDeleteVO struct {
	Requested int                         `json:"requested"`
	Deleted   int                         `json:"deleted"`
	Failed    int                         `json:"failed"`
	Items     []OpenApiConfigDeleteItemVO `json:"items"`
}

// OpenApiConfigDeleteItemVO 单条删除结果。
type OpenApiConfigDeleteItemVO struct {
	TargetKey    string `json:"targetKey"`
	Action       string `json:"action"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// OpenApiSystemConfigBatchUpdateVO 运行开关批量修改响应。
type OpenApiSystemConfigBatchUpdateVO struct {
	Total           int                               `json:"total"`
	Success         int                               `json:"success"`
	Failed          int                               `json:"failed"`
	RequiresRestart []string                          `json:"requiresRestart"`
	Items           []OpenApiSystemConfigUpdateItemVO `json:"items"`
}

// OpenApiSystemConfigUpdateItemVO 单个开关修改结果。
type OpenApiSystemConfigUpdateItemVO struct {
	Key             string `json:"key"`
	Value           any    `json:"value"`
	RequiresRestart bool   `json:"requiresRestart"`
	ErrorCode       string `json:"errorCode,omitempty"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
}
