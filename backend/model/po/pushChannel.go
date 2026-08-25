package po

import (
	"iot-gateway/utils"

	"gorm.io/gorm"
)

// PushChannel 数据推送通道持久化对象
type PushChannel struct {
	ID          string `gorm:"primaryKey;column:id" json:"id"`
	Name        string `gorm:"column:name;uniqueIndex;not null" json:"name"` // 通道类型：mqtt/opcua/netty
	Description string `gorm:"column:description" json:"description"`
	ConfigJSON  string `gorm:"column:config_json;not null" json:"configJson"`
	Status      int    `gorm:"column:status;default:1" json:"status"`
	CreatedAt   string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt   string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (PushChannel) TableName() string {
	return "push_channel"
}

func (p *PushChannel) BeforeCreate(_ *gorm.DB) error {
	if p.ID == "" {
		p.ID = utils.GenerateUUIDV7()
	}
	return nil
}
