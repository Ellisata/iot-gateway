// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"iot-gateway/utils"

	"gorm.io/gorm"
)

// PushChannelForm 数据推送通道表单持久化对象
type PushChannelForm struct {
	ID        string `gorm:"primaryKey;column:id" json:"id"`
	Name      string `gorm:"column:name;uniqueIndex;not null" json:"name"`
	FormJSON  string `gorm:"column:form_json;not null" json:"formJson"`
	CreatedAt string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (PushChannelForm) TableName() string {
	return "push_channel_form"
}

func (p *PushChannelForm) BeforeCreate(_ *gorm.DB) error {
	if p.ID == "" {
		p.ID = utils.GenerateUUIDV7()
	}
	return nil
}
