package po

import (
	"gorm.io/gorm"

	"iot-gateway/utils"
)

// DeviceAddress 物联网设备地址位持久化对象
type DeviceAddress struct {
	ID             string `gorm:"primaryKey;column:id" json:"id"`
	DeviceID       string `gorm:"column:device_id;not null" json:"deviceId"`
	Name           string `gorm:"column:name;uniqueIndex;not null" json:"name"`
	Label          string `gorm:"column:label;not null" json:"label"`                     // 地址 name 的中文说明（如温度、电流）
	CommonDataType string `gorm:"column:common_data_type;not null" json:"commonDataType"` // 通用数据类型名（配置界面展示）
	DataType       string `gorm:"column:data_type;not null" json:"dataType"`              // 对应协议的内部数据类型名（驱动解码用）
	RwPermission   string `gorm:"column:rw_permission;not null" json:"rwPermission"`
	ScanFrequency  int    `gorm:"column:scan_frequency;default:1000" json:"scanFrequency"`
	Description    string `gorm:"column:description" json:"description"`
	Status         int    `gorm:"column:status;default:1" json:"status"`
	CreatedAt      string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt      string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (DeviceAddress) TableName() string {
	return "device_address"
}

// BeforeCreate GORM 回调：插入前自动生成 UUID v7
func (d *DeviceAddress) BeforeCreate(_ *gorm.DB) error {
	if d.ID == "" {
		d.ID = utils.GenerateUUIDV7()
	}
	return nil
}
