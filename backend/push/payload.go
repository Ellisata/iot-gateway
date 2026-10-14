// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import "iot-gateway/collector"

// PushBatch 一批推送数据（按设备分组）。
// 携带结构化采集记录与统一采集时间，由各通道按自身线上格式序列化。
// 字段导出：作为 Channel.Enqueue 的入参跨包传递。
type PushBatch struct {
	DeviceID    string
	CollectedAt string
	Records     []collector.CollectedRecord
}
