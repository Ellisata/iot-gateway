// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"gorm.io/gorm"

	"iot-gateway/utils"
)

// TriggerRule 事件触发采集规则。
//
// 条件与动作点位都以 JSON 列存储：
//   - ActionAddressIDs 是 string[]（动作点位 ID），动作点位必须属于 ActionDeviceID；
//   - ConditionsJSON 是 DNF 结构（组间 OR、组内 AND），见 triggerrule.Conditions。
//
// 名称与编码全局唯一（迁移里的唯一索引兜底），服务层先查再落库。
// 编码面向下游业务系统，创建后不可修改；名称仅用于展示。
type TriggerRule struct {
	ID          string `gorm:"primaryKey;column:id" json:"id"`
	Code        string `gorm:"column:code;not null;default:''" json:"code"`
	Name        string `gorm:"column:name;not null" json:"name"`
	Description string `gorm:"column:description;not null;default:''" json:"description"`
	Status      int    `gorm:"column:status;not null;default:1" json:"status"`

	ActionDeviceID   string `gorm:"column:action_device_id;not null" json:"actionDeviceId"`
	ActionAddressIDs string `gorm:"column:action_address_ids;not null;default:'[]'" json:"actionAddressIds"`
	TargetChannelID  string `gorm:"column:target_channel_id;not null" json:"targetChannelId"`

	ConditionsJSON string `gorm:"column:conditions_json;not null;default:'{\"groups\":[]}'" json:"conditionsJson"`
	CooldownMs     int    `gorm:"column:cooldown_ms;not null;default:0" json:"cooldownMs"`
	TimeoutMs      int    `gorm:"column:timeout_ms;not null;default:5000" json:"timeoutMs"`

	CreatedAt string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (TriggerRule) TableName() string {
	return "trigger_rule"
}

// BeforeCreate 插入前生成 UUID v7。
func (r *TriggerRule) BeforeCreate(_ *gorm.DB) error {
	if r.ID == "" {
		r.ID = utils.GenerateUUIDV7()
	}
	return nil
}
