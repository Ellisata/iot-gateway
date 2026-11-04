// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import "time"

// ChannelStatusVO 推送通道运行状态响应
type ChannelStatusVO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Running      bool   `json:"running"`
	Connected    bool   `json:"connected"`
	Broker       string `json:"broker"`
	Topic        string `json:"topic"`
	QueueDepth   uint64 `json:"queueDepth"`
	SpoolDepth   uint64 `json:"spoolDepth"`   // 断网本地缓存待补发批次
	PublishCount uint64 `json:"publishCount"` // 下发次数（时序库=写入批次，OPC UA=地址空间更新批次）
	RowsCount    uint64 `json:"rowsCount"`    // 成功写出的记录行数（时序库=写入行，OPC UA=更新点位）
	DroppedCount uint64 `json:"droppedCount"`
	LastPublish  string `json:"lastPublishTime"`
	LastSuccess  string `json:"lastSuccessTime"`
	LastErr      string `json:"error"`
	// Extra 通道类型特有的观测项（仅展示用途）。键是稳定标识，文案由前端翻译；
	// 没有类型特有指标的通道留空。例如 OPC UA 的点位覆盖度与订阅数。
	Extra map[string]any `json:"extra,omitempty"`
}

// FormatTime 格式化时间为字符串，供各通道子包在 Snapshot 中复用
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
