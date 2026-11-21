// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package tsdb 提供可复用的时序数据库入库组件。
//
// 根包只定义契约：中性模型（Record/Batch）、同步 Writer、聚合后可靠的 Sink、
// 状态与观测数据结构。具体实现按层拆开：
//
//   - tsdb/tdengine/v3、tsdb/influxdb/v3：时序库后端（连接、建库建表、SQL / Line Protocol
//     与同步 Writer）。路径里的 v3 指上游服务端的 API 版本，不是 Go 模块大版本；
//   - tsdb/delivery（可选）：非阻塞队列、断网缓存、补发与最近写入观测；
//   - tsdb/spool/sqlite、tsdb/spool/postgres（可选）：断网缓存的 SpoolStore 实现。
//     它们是缓存存储，不是时序库后端，与 tdengine/influxdb 分属两个层次。
//
// 只需要同步写入的项目用 Writer，不引入 delivery：
//
//	w := tdengine.NewWriter(cfg, tsdb.Options{})
//	err := w.Write(ctx, []tsdb.Batch{...})
//
// 需要断网不丢的项目用 Sink（Writer + delivery）：
//
//	store, _ := sqlite.Open("data/spool.db")
//	sink := tdengine.New(cfg, tsdb.Options{ID: "proj-a", Store: store})
//	sink.Start()
//	sink.Enqueue(tsdb.Batch{...})
//	sink.Stop()
package tsdb

import "github.com/Ellisata/iot-gateway/tsdb/model"

// Record 一条待入库的采集记录（与 model.Record 同一类型）。
type Record = model.Record

// RecordTrigger 事件触发元数据（与 model.RecordTrigger 同一类型）。
type RecordTrigger = model.RecordTrigger

// Batch 一批待入库数据（与 model.Batch 同一类型）。
type Batch = model.Batch
