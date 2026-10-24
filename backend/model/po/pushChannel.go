// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"iot-gateway/utils"

	"gorm.io/gorm"
)

// PushChannel 数据推送通道持久化对象
type PushChannel struct {
	ID string `gorm:"primaryKey;column:id" json:"id"`
	// Type 通道类型（mqtt/http/tdengine-v3...），决定用哪个工厂构建。
	Type string `gorm:"column:type;not null;default:'';uniqueIndex:idx_push_channel_type_name" json:"type"`
	// Name 通道实例名（同一类型可建多条）。
	Name        string `gorm:"column:name;not null;uniqueIndex:idx_push_channel_type_name" json:"name"`
	Description string `gorm:"column:description" json:"description"`
	ConfigJSON  string `gorm:"column:config_json;not null" json:"configJson"`
	Status      int    `gorm:"column:status;default:1" json:"status"`
	CreatedAt   string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt   string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (PushChannel) TableName() string {
	return "push_channel"
}

func (p *PushChannel) BeforeCreate(_ *gorm.DB) error {
	if p.ID == "" {
		p.ID = utils.GenerateUUIDV7()
	}
	// 兼容旧测试/旧客户端：只给 Name 不给 Type 时，历史语义是「Name 即类型」。
	if p.Type == "" {
		p.Type = p.Name
	}
	return nil
}
