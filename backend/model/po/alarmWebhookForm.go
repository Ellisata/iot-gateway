// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"iot-gateway/utils"

	"gorm.io/gorm"
)

// AlarmWebhookForm 报警通知 Webhook 表单持久化对象。
//
// 与 PushChannelForm 同构：表单结构由数据驱动，前端按 name 拉取后动态渲染配置页，
// 后端不感知具体字段。名称按 webhook 类型取值（dingtalk/wecom/feishu/custom）。
type AlarmWebhookForm struct {
	ID        string `gorm:"primaryKey;column:id" json:"id"`
	Name      string `gorm:"column:name;uniqueIndex;not null" json:"name"`
	FormJSON  string `gorm:"column:form_json;not null" json:"formJson"`
	CreatedAt string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (AlarmWebhookForm) TableName() string {
	return "alarm_webhook_form"
}

func (a *AlarmWebhookForm) BeforeCreate(_ *gorm.DB) error {
	if a.ID == "" {
		a.ID = utils.GenerateUUIDV7()
	}
	return nil
}
