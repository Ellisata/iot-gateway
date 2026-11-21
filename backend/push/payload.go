// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import "github.com/Ellisata/iot-gateway/tsdb"

// PushBatch 一批推送数据（按 (设备, 采集组) 分组）。
//
// 类型定义在时序入库组件中（tsdb.Batch）：推送通道与时序入库共用同一份
// 批次模型，Enqueue 跨包传递零转换。字段语义见 tsdb.Batch。
type PushBatch = tsdb.Batch
