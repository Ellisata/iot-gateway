// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tsdb

import "context"

// Writer 一个同步时序数据库写入端。
//
// 只负责写入本身：不排队、不重试、不起后台协程。项目已有自己的队列/断网
// 缓存时直接用它；需要可靠投递时用 Sink（Writer + delivery）。
type Writer interface {
	Write(ctx context.Context, batches []Batch) error
	TestConnectivity() error
	Close() error
}

// Sink 一个时序数据库写入端。
//
// 实现必须保证 Enqueue 永不阻塞调用方（采集线程）；队列满时由内部策略
// （启用 SpoolStore 则溢写落盘，否则丢弃最旧）决定。
type Sink interface {
	// ID 返回持久化标识（断网缓存按它隔离）。
	ID() string
	// Start 启动后台写入协程，立即返回；连接在后台建立。
	Start()
	// Stop 停止并排空：先停 worker，再等落盘协程把在途批次落盘。
	Stop()
	// Enqueue 非阻塞入队一批数据。
	Enqueue(Batch)
	// Status 返回当前运行状态快照。
	Status() Status
	// TestConnectivity 同步探测连通性后关闭，不启动后台协程。
	TestConnectivity() error
}

// RecentProvider 可选接口：后端可提供最近写入记录（仅内存）。
// 观测能力不该成为每个后端的强制义务，与 Sink 分开定义。
type RecentProvider interface {
	RecentMessages(limit int) []RecentPushMessage
}

// Options 构造一个时序入库端的一次性参数。
type Options struct {
	// ID 持久化标识；断网缓存按它隔离，必填。
	ID string
	// Name 展示名（状态快照用）。
	Name string
	// Store 断网本地缓存；nil 表示关闭断网缓存（退回纯内存丢最旧行为）。
	Store SpoolStore
	// Logger 组件日志；nil 时静默。
	Logger Logger
}
