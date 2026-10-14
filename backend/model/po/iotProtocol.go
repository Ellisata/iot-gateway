// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"iot-gateway/utils"

	"gorm.io/gorm"
)

// IotProtocol 物联网协议持久化对象
//
// 表里还留着 status 列（迁移 004 建的），但 Go 侧刻意不再映射它：协议行全部由插件
// 供给、供给时恒为启用，而协议行的消费方（collector 与设备表单的协议选择器）都不看
// status，所以「禁用协议」这个能力从未生效过。只写不读的字段留着，只会让人以为它有用。
// 真要停采，禁用设备或移走插件文件都已覆盖。删列需重建 SQLite 表，不值得。
type IotProtocol struct {
	ID          string `gorm:"primaryKey;column:id" json:"id"`
	Name        string `gorm:"column:name;uniqueIndex;not null" json:"name"`
	Description string `gorm:"column:description" json:"description"`
	FormJSON    string `gorm:"column:form_json" json:"formJson"`
	Sort        int    `gorm:"column:sort;default:1" json:"sort"`
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
