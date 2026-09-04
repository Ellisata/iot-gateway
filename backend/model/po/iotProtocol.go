// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"iot-gateway/utils"

	"gorm.io/gorm"
)

// IotProtocol 物联网协议持久化对象
type IotProtocol struct {
	ID          string `gorm:"primaryKey;column:id" json:"id"`
	Name        string `gorm:"column:name;uniqueIndex;not null" json:"name"`
	Description string `gorm:"column:description" json:"description"`
	FormJSON    string `gorm:"column:form_json" json:"formJson"`
	Sort        int    `gorm:"column:sort;default:1" json:"sort"`
	Status      int    `gorm:"column:status;default:1" json:"status"`
	CreatedAt   string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt   string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (IotProtocol) TableName() string {
	return "iot_protocol"
}

func (p *IotProtocol) BeforeCreate(_ *gorm.DB) error {
	if p.ID == "" {
		p.ID = utils.GenerateUUIDV7()
	}
	return nil
}
