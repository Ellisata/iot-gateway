// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

import "encoding/json"

// TriggerRuleAddressVO 规则引用的点位（条件或动作）的展示投影。
//
// Kind 由服务层按「协议 + 内部数据类型」算出；前端据此决定可用算子，
// 不需要再按协议逐个查类型表。
type TriggerRuleAddressVO struct {
	ID           string `json:"id"`
	DeviceID     string `json:"deviceId"`
	DeviceName   string `json:"deviceName"`
	ProtocolName string `json:"protocolName"`
	Name         string `json:"name"`
	Label        string `json:"label"`
	DataType     string `json:"dataType"`
	Kind         string `json:"kind"`
	RwPermission string `json:"rwPermission"`
}

// TriggerRuleVO 事件触发规则响应。
type TriggerRuleVO struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      int    `json:"status"`

	ActionDeviceID   string                 `json:"actionDeviceId"`
	ActionDeviceName string                 `json:"actionDeviceName"`
	ActionAddressIDs []string               `json:"actionAddressIds"`
	ActionAddresses  []TriggerRuleAddressVO `json:"actionAddresses"`

	TargetChannelID   string `json:"targetChannelId"`
	TargetChannelName string `json:"targetChannelName"`

	ConditionsJSON     json.RawMessage        `json:"conditionsJson"`
	ConditionAddresses []TriggerRuleAddressVO `json:"conditionAddresses"`
	ConditionCount     int                    `json:"conditionCount"`

	CooldownMs int    `json:"cooldownMs"`
	TimeoutMs  int    `json:"timeoutMs"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// TriggerRecentRecordVO 最近触发读到的动作点位。
type TriggerRecentRecordVO struct {
	AddressID   string `json:"addressId"`
	AddressName string `json:"addressName"`
	Value       string `json:"value"`
	Kind        string `json:"kind"`
	Quality     int    `json:"quality"`
}

// TriggerRecentVO 最近一次已结束的触发（仅内存，重启即清空）。
//
// success 只表示已进入通道 outbox 队列，不代表已送达下游；真正的投递结果看
// 通道监控 GET /push/recentMessages。
type TriggerRecentVO struct {
	Seq              uint64                  `json:"seq"`
	TriggerID        string                  `json:"triggerId"`
	RuleID           string                  `json:"ruleId"`
	RuleCode         string                  `json:"ruleCode"`
	RuleName         string                  `json:"ruleName"`
	FiredAt          string                  `json:"firedAt"`
	Status           string                  `json:"status"`
	DurationMs       int                     `json:"durationMs"`
	Error            string                  `json:"error"`
	ActionDeviceID   string                  `json:"actionDeviceId"`
	ActionDeviceName string                  `json:"actionDeviceName"`
	ChannelID        string                  `json:"channelId"`
	ChannelName      string                  `json:"channelName"`
	Records          []TriggerRecentRecordVO `json:"records"`
}

// TriggerRuleRecordVO 触发历史响应。
type TriggerRuleRecordVO struct {
	ID               string `json:"id"`
	TriggerID        string `json:"triggerId"`
	RuleID           string `json:"ruleId"`
	RuleCode         string `json:"ruleCode"`
	RuleName         string `json:"ruleName"`
	FiredAt          string `json:"firedAt"`
	Status           string `json:"status"`
	ConditionJSON    string `json:"conditionJson"`
	ActionResultJSON string `json:"actionResultJson"`
	DurationMs       int    `json:"durationMs"`
	Error            string `json:"error"`
	CreatedAt        string `json:"createdAt"`
}
