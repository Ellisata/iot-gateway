// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"gorm.io/gorm"

	"iot-gateway/utils"
)

// Device 物联网设备持久化对象
type Device struct {
	ID string `gorm:"primaryKey;column:id" json:"id"`
	// Code 设备编码：面向 MQTT 主题、导入导出与外部引用的稳定标识。
	// 创建后不可修改（服务层校验），全局唯一；唯一性由迁移里的部分唯一索引
	// 兜底（排除空串，兼容旧数据/测试库直接写入的行）。
	Code         string `gorm:"column:code;not null;default:''" json:"code"`
	Name         string `gorm:"column:name;uniqueIndex;not null" json:"name"`
	ProtocolID   string `gorm:"column:protocol_id;not null" json:"protocolId"`
	ProtocolJSON string `gorm:"column:protocol_json;not null" json:"protocolJson"`
	// ModelID 绑定的物模型 ID（thing_model.id）。空串 = 未绑定。
	ModelID     string `gorm:"column:model_id;not null;default:'';index:idx_device_model" json:"modelId"`
	Description string `gorm:"column:description" json:"description"`
	Status      int    `gorm:"column:status;default:1" json:"status"`
	CreatedAt   string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt   string `gorm:"column:updated_at" json:"updatedAt"`

	// ScanIdentity 扫描来源身份签名（`协议|主机|端口|站号`，见
	// service.scanIdentityOf），用来回答"设备表里是不是已经有一台设备连在
	// 这个地址上了"。
	//
	// **扫描结果一键建设备**、**手工新建**、**按协议模板的 Excel 导入**以及
	// **修改连接配置**时都会写它；通用模板导入（无连接参数列）以及串口协议
	//（没有 host）留空。
	// 写入侧只认官方协议表单统一的 host / port / unitId / dstNode 键名，
	// 不从任意插件自定义表单里猜地址 —— 猜错的签名会让扫描判重给出相反的结论。
	// 它回答的是"这台设备当前连在哪个地址"，是判重标记而不是通用设备地址列，
	// 别拿它当地址用。
	//
	// 连接配置被真正修改时（见 DeviceService.UpdateDevice），签名会按新配置重算；
	// 新配置里认不出 host 时写空串（空签名不参与判重）。
	//
	// 不参与 JSON 序列化：它是网关内部的判重标记，对 API 调用方没有意义。
	ScanIdentity string `gorm:"column:scan_identity;not null;default:''" json:"-"`
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
