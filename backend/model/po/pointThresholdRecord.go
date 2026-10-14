// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"gorm.io/gorm"

	"iot-gateway/utils"
)

// PointThresholdRecord 点位越限记录持久化对象。
//
// 网关刻意不在本地保存采集数据（历史趋势由外部时序库承担），本表是窄范围例外：
// 只记越限样本、只为排障与对账，且带自动保留清理（见 alarm.ThresholdEngine.cleanup）。
// 它与 alarm 表是两件事：alarm 是去抖后的状态机（连续 N 轮才报），本表按**原始越限**
// 逐条留证（同一连续越限段内节流）——因此 1~2 轮的尖峰只进本表、不进报警。
type PointThresholdRecord struct {
	ID          string `gorm:"primaryKey;column:id" json:"id"`
	DeviceID    string `gorm:"column:device_id;not null" json:"deviceId"`
	DeviceName  string `gorm:"column:device_name;not null" json:"deviceName"`
	AddressID   string `gorm:"column:address_id;not null" json:"addressId"`
	AddressName string `gorm:"column:address_name;not null" json:"addressName"`
	// Value 驱动的原始字符串（未重新格式化），使记录与报警文案、实时页逐字节一致。
	Value string `gorm:"column:value;not null" json:"value"`
	Kind  string `gorm:"column:kind;not null;default:''" json:"kind"`
	// Bound 命中的是条件里的哪一部分：range 为 high / low，其余算子即算子名本身。
	Bound string `gorm:"column:bound;not null" json:"bound"`
	// ThresholdOp / ThresholdValue 是写入时的**快照**，运维改配置后历史记录仍可自证。
	// ThresholdValue 为操作数的 JSON 数组字符串。
	ThresholdOp    string `gorm:"column:threshold_op;not null;default:''" json:"thresholdOp"`
	ThresholdValue string `gorm:"column:threshold_value;not null;default:''" json:"thresholdValue"`
	OccurredAt     string `gorm:"column:occurred_at;not null" json:"occurredAt"`
	CreatedAt      string `gorm:"column:created_at" json:"createdAt"`
}

// TableName 表名
func (PointThresholdRecord) TableName() string {
	return "point_threshold_record"
}

// BeforeCreate GORM 回调：插入前自动生成 UUID v7
func (p *PointThresholdRecord) BeforeCreate(_ *gorm.DB) error {
	if p.ID == "" {
		p.ID = utils.GenerateUUIDV7()
	}
	return nil
}
