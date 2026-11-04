// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"gorm.io/gorm"

	"iot-gateway/utils"
)

// TriggerRuleRecord 一次触发动作的历史记录。
//
// RuleCode / RuleName / ConditionJSON / ActionResultJSON 都是**触发时刻的快照**：
// 规则后来改名、改条件或被删除，历史仍能自证当时发生了什么。
type TriggerRuleRecord struct {
	ID        string `gorm:"primaryKey;column:id" json:"id"`
	TriggerID string `gorm:"column:trigger_id;not null" json:"triggerId"`
	RuleID    string `gorm:"column:rule_id;not null" json:"ruleId"`
	RuleName  string `gorm:"column:rule_name;not null" json:"ruleName"`
	RuleCode  string `gorm:"column:rule_code;not null;default:''" json:"ruleCode"`
	FiredAt   string `gorm:"column:fired_at;not null" json:"firedAt"`
	// Status 见 triggerrule.Status*：success/offline/read_failed/push_failed/dropped。
	Status           string `gorm:"column:status;not null" json:"status"`
	ConditionJSON    string `gorm:"column:condition_json;not null;default:''" json:"conditionJson"`
	ActionResultJSON string `gorm:"column:action_result_json;not null;default:''" json:"actionResultJson"`
	DurationMs       int    `gorm:"column:duration_ms;not null;default:0" json:"durationMs"`
	Error            string `gorm:"column:error;not null;default:''" json:"error"`
	CreatedAt        string `gorm:"column:created_at" json:"createdAt"`
}

// TableName 表名
func (TriggerRuleRecord) TableName() string {
	return "trigger_rule_record"
}

// BeforeCreate 插入前生成 UUID v7。
func (r *TriggerRuleRecord) BeforeCreate(_ *gorm.DB) error {
	if r.ID == "" {
		r.ID = utils.GenerateUUIDV7()
	}
	return nil
}
