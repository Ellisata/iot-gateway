// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"gorm.io/gorm"

	"iot-gateway/utils"
)

// ThingModelProperty 物模型属性持久化对象。
//
// 属性是协议无关的语义测点声明：identifier 是下游映射的稳定键，kind 对应
// driver.KindXxx 六类之一。具体寄存器地址与协议内部类型在 device_address 上，
// 本表不保存、也不复制它们。
type ThingModelProperty struct {
	ID      string `gorm:"primaryKey;column:id" json:"id"`
	ModelID string `gorm:"column:model_id;not null;index:idx_thing_model_property_model_sort,priority:1" json:"modelId"`
	// Identifier 属性标识符（如 temperature）。模型内唯一，创建后不可修改。
	Identifier string `gorm:"column:identifier;not null;uniqueIndex:idx_thing_model_property_model_identifier,priority:2" json:"identifier"`
	// Name 属性显示名（如“温度”）。
	Name string `gorm:"column:name;not null" json:"name"`
	// Kind 数据类型类别：bool / int / uint / float / string / time。
	Kind string `gorm:"column:kind;not null" json:"kind"`
	// AccessMode 读写模式：R / W / RW（与 device_address.rw_permission 同枚举）。
	AccessMode string `gorm:"column:access_mode;not null;default:'R'" json:"accessMode"`
	// Unit 工程单位（如 ℃、%、A），仅展示，不做量纲换算。
	Unit        string `gorm:"column:unit;not null;default:''" json:"unit"`
	Description string `gorm:"column:description;not null;default:''" json:"description"`
	// SortOrder 展示顺序，越小越靠前。
	SortOrder int `gorm:"column:sort_order;not null;default:0;index:idx_thing_model_property_model_sort,priority:2" json:"sortOrder"`
	// Status 1 启用 / 0 停用。停用不解除已有点位绑定。
	Status    int    `gorm:"column:status;default:1" json:"status"`
	CreatedAt string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (ThingModelProperty) TableName() string {
	return "thing_model_property"
}

// BeforeCreate GORM 回调：插入前自动生成 UUID v7
func (t *ThingModelProperty) BeforeCreate(_ *gorm.DB) error {
	if t.ID == "" {
		t.ID = utils.GenerateUUIDV7()
	}
	return nil
}
