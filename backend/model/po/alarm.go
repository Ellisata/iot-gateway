// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"gorm.io/gorm"

	"iot-gateway/utils"
)

// Alarm 报警持久化对象（目标为设备 / 推送通道 / 采集点位）。
// 同一目标同时最多一条 status='active' 的报警记录；恢复时置 cleared
// 并写一条 recover 历史记录。
type Alarm struct {
	ID         string `gorm:"primaryKey;column:id" json:"id"`
	TargetID   string `gorm:"column:target_id;not null" json:"targetId"`                    // device.id / push_channel.id / device_address.id
	TargetName string `gorm:"column:target_name;not null" json:"targetName"`                // 名称快照，删除后仍可追溯
	TargetType string `gorm:"column:target_type;not null;default:device" json:"targetType"` // device | channel | point
	AlarmType  string `gorm:"column:alarm_type;not null" json:"alarmType"`                  // offline | quality | recover
	// DeviceID 点位报警所属的设备 ID（device.id）。
	// **仅 target_type='point' 与 'point_threshold' 的行填写**，含两者写下的 recover
	// 历史行；device / channel 行恒为空串 —— 它们的 target_id 就是自己，不需要这一列。
	// 点位报警的 target_id 是点位自身，看不回设备，而"删除/停用设备时清掉其点位的
	// 活跃报警"必须在设备行（乃至点位行）已被删除之后仍能反查。
	DeviceID       string `gorm:"column:device_id;not null;default:''" json:"deviceId"`
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
