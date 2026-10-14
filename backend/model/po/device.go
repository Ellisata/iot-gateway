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

	// ScanIdentity 扫描来源身份签名（`协议|主机|端口|站号`，见
	// service.scanIdentityOf），用来回答"设备表里是不是已经有一台设备连在
	// 这个地址上了"。
	//
	// **只有"扫描结果一键建设备"会写它**，手工新建与 Excel 导入一律留空 ——
	// 网关侧要从 protocol_json 里认出地址，得先有一套跨协议的键名约定
	//（那些表单是插件自己声明的，键名不统一），代价远大于收益。因此这份信息的
	// 语义是"这行是扫描建出来的、且建出来之后没被改过配置"，不是一个通用的
	// 设备地址列 —— 别拿它当地址用。
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
