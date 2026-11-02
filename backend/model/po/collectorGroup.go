// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"gorm.io/gorm"

	"iot-gateway/utils"
)

// CollectorGroup 采集组：设备下的点位分组，组持有采集周期。
//
// 采集/上报/订阅都以组为单位：组内点位共享 interval_ms 与一次轮询批次，
// 推送侧按 (device_id, group_id) 分帧，通道可按组 ID 订阅。
// 点位未绑定组（device_address.group_id = ''）时沿用 scan_frequency 旧行为。
type CollectorGroup struct {
	ID       string `gorm:"primaryKey;column:id" json:"id"`
	DeviceID string `gorm:"column:device_id;not null;uniqueIndex:idx_collector_group_device_name" json:"deviceId"`
	Name     string `gorm:"column:name;not null;uniqueIndex:idx_collector_group_device_name" json:"name"`
	// IntervalMs 采集周期（毫秒）。对绑定到本组的点位权威，
	// 此时 device_address.scan_frequency 不生效。
	IntervalMs  int    `gorm:"column:interval_ms;not null;default:1000" json:"intervalMs"`
	Description string `gorm:"column:description;not null;default:''" json:"description"`
	// Status 1 启用 / 0 停用。停用后绑定到本组的点位不再采集与推送。
	Status    int    `gorm:"column:status;default:1" json:"status"`
	CreatedAt string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (CollectorGroup) TableName() string {
	return "collector_group"
}

// BeforeCreate GORM 回调：插入前自动生成 UUID v7
func (g *CollectorGroup) BeforeCreate(_ *gorm.DB) error {
	if g.ID == "" {
		g.ID = utils.GenerateUUIDV7()
	}
	return nil
}
