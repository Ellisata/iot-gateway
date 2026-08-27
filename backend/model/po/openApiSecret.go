package po

import (
	"iot-gateway/utils"

	"gorm.io/gorm"
)

// OpenApiSecret 开放接口密钥持久化对象
type OpenApiSecret struct {
	ID        string `gorm:"primaryKey;column:id" json:"id"`
	Name      string `gorm:"column:name;uniqueIndex;not null" json:"name"` // 密钥名称
	Key       string `gorm:"column:key;not null" json:"key"`               // 密钥内容
	CreatedAt string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (OpenApiSecret) TableName() string {
	return "open_api_secret"
}

func (o *OpenApiSecret) BeforeCreate(_ *gorm.DB) error {
	if o.ID == "" {
		o.ID = utils.GenerateUUIDV7()
	}
	return nil
}
