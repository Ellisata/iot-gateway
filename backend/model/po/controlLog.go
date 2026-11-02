// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"gorm.io/gorm"

	"iot-gateway/utils"
)

// ControlLog 一条控制下发审计记录（每个点位一行）。
//
// 控制是高风险动作：谁、从哪来、对哪台设备的哪个点、请求写什么、实际写成什么、
// 是否回读确认，都必须能事后回答。它与采集数据分开存：采集数据不落本地库，
// 而审计记录必须留在本地。
type ControlLog struct {
	ID string `gorm:"primaryKey;column:id" json:"id"`

	// RequestID 调用方提供的幂等键。为空表示不做幂等去重。
	RequestID string `gorm:"column:request_id;not null;default:''" json:"requestId"`
	// Source 调用来源：web / openapi / mqtt / agent。
	Source string `gorm:"column:source;not null;default:''" json:"source"`
	// Operator 操作者（JWT 用户名；开放接口可为密钥名或空）。
	Operator string `gorm:"column:operator;not null;default:''" json:"operator"`

	DeviceID     string `gorm:"column:device_id;not null" json:"deviceId"`
	DeviceName   string `gorm:"column:device_name;not null;default:''" json:"deviceName"`
	ProtocolName string `gorm:"column:protocol_name;not null;default:''" json:"protocolName"`

	AddressID    string `gorm:"column:address_id;not null" json:"addressId"`
	AddressName  string `gorm:"column:address_name;not null;default:''" json:"addressName"`
	AddressLabel string `gorm:"column:address_label;not null;default:''" json:"addressLabel"`
	DataType     string `gorm:"column:data_type;not null;default:''" json:"dataType"`
	RwPermission string `gorm:"column:rw_permission;not null;default:''" json:"rwPermission"`

	// RequestValue 请求下发的原始字符串。
	RequestValue string `gorm:"column:request_value;not null;default:''" json:"requestValue"`
	// ResultValue 驱动实际写入的值（按数据类型格式化）。
	ResultValue string `gorm:"column:result_value;not null;default:''" json:"resultValue"`
	// ReadbackValue 回读值；未回读时为空。
	ReadbackValue string `gorm:"column:readback_value;not null;default:''" json:"readbackValue"`
	// Verified 是否回读并确认与写入值一致。
	Verified int `gorm:"column:verified;not null;default:0" json:"verified"`

	// Code driver.WriteCodeXxx；0 表示成功。
	Code    int    `gorm:"column:code;not null;default:0" json:"code"`
	Message string `gorm:"column:message;not null;default:''" json:"message"`

	CreatedAt string `gorm:"column:created_at" json:"createdAt"`
}

// TableName 表名
func (ControlLog) TableName() string { return "control_log" }

// BeforeCreate GORM 回调：插入前自动生成 UUID v7
func (c *ControlLog) BeforeCreate(_ *gorm.DB) error {
	if c.ID == "" {
		c.ID = utils.GenerateUUIDV7()
	}
	return nil
}
