// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import "github.com/Ellisata/iot-gateway/tsdb/model"

// CollectedRecord 采集记录。
//
// 类型定义在时序入库组件的 model 包中：采集、推送与时序入库共用同一份
// 中性记录模型，跨包传递零转换。字段语义见 model.Record。
type CollectedRecord = model.Record

// RecordTrigger 事件触发元数据。
type RecordTrigger = model.RecordTrigger
