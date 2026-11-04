// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// TriggerRuleConfigDTO 创建/更新规则共用的配置字段。
//
// ConditionsJSON 由前端以对象形式提交（组间 OR、组内 AND），不是字符串。
// Update 采用**整份覆盖**语义：表单提交什么就保存什么；Status 为空表示保持原状态。
type TriggerRuleConfigDTO struct {
	// Code 业务编码：字母开头，仅字母/数字/下划线/短横线，最长 64；
	// 创建必填，更新时留空 = 保持原编码，传不同值会被拒绝（不可修改）。
	Code        string `json:"code" binding:"max=64"`
	Name        string `json:"name" binding:"required,min=1,max=64"`
	Description string `json:"description" binding:"max=500"`
	// Status 1 启用 / 0 停用。创建时缺省 1；更新时 nil = 不修改。
	Status *int `json:"status" binding:"omitempty,oneof=0 1"`

	ActionDeviceID   string   `json:"actionDeviceId" binding:"required"`
	ActionAddressIDs []string `json:"actionAddressIds" binding:"required,min=1,max=200"`
	TargetChannelID  string   `json:"targetChannelId" binding:"required"`

	ConditionsJSON json.RawMessage `json:"conditionsJson" binding:"required"`

	CooldownMs int `json:"cooldownMs" binding:"omitempty,min=0,max=86400000"`
	TimeoutMs  int `json:"timeoutMs" binding:"omitempty,min=100,max=60000"`
}

// CreateTriggerRuleDTO 创建事件触发规则。
type CreateTriggerRuleDTO struct {
	TriggerRuleConfigDTO
}

// UpdateTriggerRuleDTO 更新事件触发规则（整份覆盖）。
type UpdateTriggerRuleDTO struct {
	ID string `json:"id" binding:"required"`
	TriggerRuleConfigDTO
}

// PageTriggerRuleDTO 规则分页查询。
type PageTriggerRuleDTO struct {
	Page   int    `form:"page" binding:"required,min=1"`
	Size   int    `form:"size" binding:"required,min=1,max=100"`
	Code   string `form:"code"`
	Name   string `form:"name"`
	Status *int   `form:"status" binding:"omitempty,oneof=0 1"`
}

// RecentTriggerDTO 最近触发（仅内存，最多 100 条）查询。
type RecentTriggerDTO struct {
	RuleID string `form:"ruleId"`
	Limit  int    `form:"limit" binding:"omitempty,min=1,max=100"`
}

// PageTriggerRuleRecordDTO 触发历史分页查询。
type PageTriggerRuleRecordDTO struct {
	Page      int    `form:"page" binding:"required,min=1"`
	Size      int    `form:"size" binding:"required,min=1,max=100"`
	RuleID    string `form:"ruleId"`
	RuleCode  string `form:"ruleCode"`
	RuleName  string `form:"ruleName"`
	Status    string `form:"status"`
	StartTime string `form:"startTime"` // 含，格式 YYYY-MM-DD HH:mm:ss
	EndTime   string `form:"endTime"`   // 含，格式 YYYY-MM-DD HH:mm:ss
}
