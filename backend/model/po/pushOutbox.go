// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

// PushOutbox 推送断网本地缓存持久化对象。
// 采集批次在推送通道断连/内存队列满时写入本表,重连后按 id(入队序)补发,发布成功后删除。
type PushOutbox struct {
	ID          int64  `gorm:"primaryKey;column:id;autoIncrement" json:"id"`
	ChannelID   string `gorm:"column:channel_id;index" json:"channelId"`
	DeviceID    string `gorm:"column:device_id" json:"deviceId"`
	CollectedAt string `gorm:"column:collected_at" json:"collectedAt"`
	RecordsJSON string `gorm:"column:records_json" json:"recordsJson"`
	CreatedAt   string `gorm:"column:created_at" json:"createdAt"`
}

// TableName 表名
func (PushOutbox) TableName() string {
	return "push_outbox"
}
