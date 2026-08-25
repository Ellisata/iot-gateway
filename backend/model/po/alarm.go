package po

import (
	"gorm.io/gorm"

	"iot-gateway/utils"
)

// Alarm 断联报警持久化对象（目标为设备或推送通道）。
// 同一目标同时最多一条 status='active' 的 offline 记录，代表该目标当前离线；
// 恢复时置 cleared 并写一条 recover 历史记录。
type Alarm struct {
	ID             string `gorm:"primaryKey;column:id" json:"id"`
	TargetID       string `gorm:"column:target_id;not null" json:"targetId"`                    // device.id 或 push_channel.id
	TargetName     string `gorm:"column:target_name;not null" json:"targetName"`                // 名称快照，删除后仍可追溯
	TargetType     string `gorm:"column:target_type;not null;default:device" json:"targetType"` // device | channel
	AlarmType      string `gorm:"column:alarm_type;not null" json:"alarmType"`                  // offline | recover
	Level          string `gorm:"column:level;not null;default:warning" json:"level"`
	Content        string `gorm:"column:content;not null" json:"content"`
	Status         string `gorm:"column:status;not null;default:active" json:"status"` // active | cleared
	FirstOccurTime string `gorm:"column:first_occur_time;not null" json:"firstOccurTime"`
	LastOccurTime  string `gorm:"column:last_occur_time;not null" json:"lastOccurTime"`
	ClearTime      string `gorm:"column:clear_time" json:"clearTime"`
	CreatedAt      string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt      string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (Alarm) TableName() string {
	return "alarm"
}

// BeforeCreate GORM 回调：插入前自动生成 UUID v7
func (a *Alarm) BeforeCreate(_ *gorm.DB) error {
	if a.ID == "" {
		a.ID = utils.GenerateUUIDV7()
	}
	return nil
}
