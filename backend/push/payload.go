// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import "iot-gateway/collector"

// PushBatch 一批推送数据（按 (设备, 采集组) 分组）。
// 携带结构化采集记录与统一采集时间，由各通道按自身线上格式序列化。
// 字段导出：作为 Channel.Enqueue 的入参跨包传递。
//
// 组身份是批次级冗余（同一批次内的记录共享同一个组），供通道做 topic 选择、
// group-json 帧头与断网补发；未分组批次 GroupID/GroupName 为空、Interval 为 0。
type PushBatch struct {
	DeviceID        string
	GroupID         string
	GroupName       string
	GroupIntervalMs int
	CollectedAt     string
	Records         []collector.CollectedRecord
}
