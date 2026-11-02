// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"gorm.io/gorm"

	"iot-gateway/utils"
)

// ThingModel 设备物模型持久化对象。
//
// 物模型是**协议无关的语义模板**：它只声明“这类设备有哪些属性”，不保存寄存器
// 地址、协议内部类型、采集频率或报警阈值 —— 那些仍属于 device / device_address。
// 同一模型可被使用不同协议的设备复用，因此本表刻意没有 protocol_id 列。
//
// v1 只做属性（properties）。事件由现有报警状态机承担，服务需要驱动具备统一的
// 写/调用能力，而当前 SDK 的 Driver 只有 Read，故暂不建表。
type ThingModel struct {
	ID string `gorm:"primaryKey;column:id" json:"id"`
	// Code 模型标识（如 th_sensor），创建后不可修改：导入导出与外部引用靠它。
	Code string `gorm:"column:code;uniqueIndex;not null" json:"code"`
	// Name 显示名（如“温湿度传感器”）。
	Name string `gorm:"column:name;uniqueIndex;not null" json:"name"`
	// Category 分类 code（固定枚举，见 constants.ThingModelCategory*）。
	// 只存 code，展示名由前端 i18n 提供，避免英文界面出现中文分类名。
	Category string `gorm:"column:category;not null;default:''" json:"category"`
	// Version 仅作展示。v1 不做多版本绑定：设备只认 model_id，不携带版本快照。
	Version     string `gorm:"column:version;not null;default:'1.0.0'" json:"version"`
	Description string `gorm:"column:description;not null;default:''" json:"description"`
	// Status 1 启用 / 0 停用。停用只影响“新建绑定”，不影响已绑定设备的采集。
	Status    int    `gorm:"column:status;default:1" json:"status"`
	CreatedAt string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (ThingModel) TableName() string {
	return "thing_model"
}

// BeforeCreate GORM 回调：插入前自动生成 UUID v7
func (t *ThingModel) BeforeCreate(_ *gorm.DB) error {
	if t.ID == "" {
		t.ID = utils.GenerateUUIDV7()
	}
	return nil
}
