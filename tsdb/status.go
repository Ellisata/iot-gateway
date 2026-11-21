// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tsdb

import "time"

// Status 时序入库端的运行状态快照。
//
// 字段语义与网关推送通道的状态响应保持一致：Broker/Topic 是后端视角的
// 目标描述（如 TDengine 端点/库名、InfluxDB 地址/库+measurement）。
type Status struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Running      bool   `json:"running"`
	Connected    bool   `json:"connected"`
	Broker       string `json:"broker"`
	Topic        string `json:"topic"`
	QueueDepth   uint64 `json:"queueDepth"`
	SpoolDepth   uint64 `json:"spoolDepth"`   // 断网本地缓存待补发批次
	PublishCount uint64 `json:"publishCount"` // 下发次数（写入批次）
	RowsCount    uint64 `json:"rowsCount"`    // 成功写出的记录行数
	DroppedCount uint64 `json:"droppedCount"`
	LastPublish  string `json:"lastPublishTime"`
	LastSuccess  string `json:"lastSuccessTime"`
	LastErr      string `json:"error"`
	// Extra 后端类型特有的观测项（仅展示用途）。
	Extra map[string]any `json:"extra,omitempty"`
}

// FormatTime 格式化时间为字符串，供各后端在 Status 中复用。
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
