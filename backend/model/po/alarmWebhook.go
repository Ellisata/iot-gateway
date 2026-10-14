// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"iot-gateway/utils"

	"gorm.io/gorm"
)

// AlarmWebhook 报警通知 Webhook 持久化对象（钉钉 / 企业微信 / 飞书 / 通用自定义）。
//
// URL 本身即为凭证（钉钉 access_token、企微 key、飞书 hook uuid），
// 故 API 层原样返回以支持前端回显编辑，但 Secret 字段绝不回显。
type AlarmWebhook struct {
	ID          string `gorm:"primaryKey;column:id" json:"id"`
	Name        string `gorm:"column:name;uniqueIndex;not null" json:"name"` // 展示名，如「生产群-钉钉」
	Type        string `gorm:"column:type;not null" json:"type"`             // dingtalk | wecom | feishu | custom
	Description string `gorm:"column:description" json:"description"`
	URL         string `gorm:"column:url;not null" json:"url"`                           // 完整 webhook 地址
	Secret      string `gorm:"column:secret" json:"secret"`                              // 加签密钥；custom 时作为 Bearer Token
	MsgType     string `gorm:"column:msg_type;not null;default:markdown" json:"msgType"` // markdown | text | card（按类型可选）
	AtAll       int    `gorm:"column:at_all;not null;default:0" json:"atAll"`            // 是否 @所有人（企微不支持）
	AtList      string `gorm:"column:at_list" json:"atList"`                             // JSON 数组：钉钉=手机号，飞书=open_id
	Status      int    `gorm:"column:status;default:1" json:"status"`                    // 1 启用 0 停用
	CreatedAt   string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt   string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (AlarmWebhook) TableName() string {
	return "alarm_webhook"
}

// BeforeCreate GORM 回调：插入前自动生成 UUID v7
func (a *AlarmWebhook) BeforeCreate(_ *gorm.DB) error {
	if a.ID == "" {
		a.ID = utils.GenerateUUIDV7()
	}
	return nil
}
