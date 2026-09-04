// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"gorm.io/gorm"

	"iot-gateway/utils"
)

// Device 物联网设备持久化对象
type Device struct {
	ID           string `gorm:"primaryKey;column:id" json:"id"`
	Name         string `gorm:"column:name;uniqueIndex;not null" json:"name"`
	ProtocolID   string `gorm:"column:protocol_id;not null" json:"protocolId"`
	ProtocolJSON string `gorm:"column:protocol_json;not null" json:"protocolJson"`
	Description  string `gorm:"column:description" json:"description"`
	Status       int    `gorm:"column:status;default:1" json:"status"`
	CreatedAt    string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt    string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (Device) TableName() string {
	return "device"
}

// BeforeCreate GORM 回调：插入前自动生成 UUID v7
func (d *Device) BeforeCreate(_ *gorm.DB) error {
	if d.ID == "" {
		d.ID = utils.GenerateUUIDV7()
	}
	return nil
}
