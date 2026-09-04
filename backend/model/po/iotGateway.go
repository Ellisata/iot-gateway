// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

// IotGateway 物联网网关持久化对象
type IotGateway struct {
	ID              string `gorm:"primaryKey;column:id" json:"id"`
	Name            string `gorm:"column:name;uniqueIndex;not null" json:"name"`
	IP              string `gorm:"column:ip;not null" json:"ip"`
	Port            int    `gorm:"column:port;not null" json:"port"`
	Protocol        string `gorm:"column:protocol;not null" json:"protocol"`
	FirmwareVersion string `gorm:"column:firmware_version" json:"firmwareVersion"`
	Description     string `gorm:"column:description" json:"description"`
	Status          int    `gorm:"column:status;default:1" json:"status"`
	CreatedAt       string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt       string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (IotGateway) TableName() string {
	return "iot_gateway"
}
