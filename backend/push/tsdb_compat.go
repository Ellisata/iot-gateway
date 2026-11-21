// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway/tsdb"
	"github.com/Ellisata/iot-gateway/tsdb/delivery"
	tsdbsqlite "github.com/Ellisata/iot-gateway/tsdb/spool/sqlite"

	"iot-gateway/logger"
)

// 本文件把网关既有的推送通道骨架 API 转发到独立的时序入库组件。
//
// 队列、断网缓存、补发、攒批写入与最近写入观测只有一份实现（tsdb/delivery）；
// push 一侧保留原有类型名与构造入口，mqtt / webhook / kafka / opcua 等通道
// 无需感知组件拆分。时序入库通道（TDengine / InfluxDB）直接实现 tsdb.Sink，
// 再由各自的适配器包装成 push.Channel。

type (
	// Outbox 非阻塞出站队列 + 断网本地缓存骨架。
	Outbox = delivery.Outbox
	// Connectivity 通道连通状态判据。
	Connectivity = delivery.Connectivity
	// Spool 断网本地缓存接口。
	Spool = tsdb.SpoolStore
	// SpoolBatch 从断网缓存取回的一批待补发数据。
	SpoolBatch = tsdb.StoredBatch
	// AggregateWriter 攒批下发写协程。
	AggregateWriter = delivery.AggregateWriter
	// Aggregator 负载聚合器契约。
	Aggregator = delivery.Aggregator
	// GroupedDrainer 聚合写回式补发策略。
	GroupedDrainer = delivery.GroupedDrainer
	// DrainPolicy 补发失败处理策略。
	DrainPolicy = delivery.DrainPolicy
	// DrainFunc 补发策略函数。
	DrainFunc = delivery.DrainFunc
	// RecentRing 最近写入环形缓冲。
	RecentRing = delivery.RecentRing
	// RecentPushMessage 最近写入快照。
	RecentPushMessage = tsdb.RecentPushMessage
	// WriteObservation 一次聚合写入的观测结果。
	WriteObservation = tsdb.WriteObservation
	// RecentMessagesProvider 可选观测接口。
	RecentMessagesProvider = tsdb.RecentProvider
	// SqliteSpool 基于 SQLite 的断网缓存实现。
	SqliteSpool = tsdbsqlite.Spool
)

// RecentPushRingSize 每个通道在内存中保留的最近推送条数。
const RecentPushRingSize = delivery.RecentPushRingSize

// NewRecentRing 创建指定容量的环形缓冲。
func NewRecentRing(size int) *RecentRing { return delivery.NewRecentRing(size) }

// PreviewPayload 生成载荷预览并返回原始字节数。
func PreviewPayload(payload []byte) (string, int, bool) {
	return delivery.PreviewPayload(payload)
}

// PreviewPayloadString 同 PreviewPayload，输入为字符串。
func PreviewPayloadString(payload string) (string, int, bool) {
	return delivery.PreviewPayloadString(payload)
}

// NewSqliteSpool 创建指定通道的 SQLite 断网缓存（复用网关配置库连接）。
func NewSqliteSpool(db *gorm.DB, channelID string) *SqliteSpool {
	return tsdbsqlite.NewSpool(db, channelID, tsdbsqlite.WithLogger(ComponentLogger()))
}

// OutboxConfig 构造 Outbox 的一次性参数（网关形态：直接注入配置库连接）。
type OutboxConfig struct {
	Tag          string       // 日志前缀（通道类型名，如 "mqtt"）
	ID           string       // 通道持久化 ID；断网缓存按 channel_id 隔离
	Name         string       // 通道名（状态快照用）
	DB           *gorm.DB     // 断网缓存持久化；SpoolEnabled 为 false 时忽略
	SpoolEnabled bool         // 配置是否启用断网缓存
	SpoolCap     int          // 缓存批次上限（达到后裁剪最旧）
	Conn         Connectivity // 连通状态判据，必须非 nil
	QueueSize    int          // 出站缓冲大小；≤0 时取默认值
}

// NewOutbox 构造通道的出站队列与断网缓存。
func NewOutbox(cfg OutboxConfig) *Outbox {
	var store tsdb.SpoolStore
	if cfg.SpoolEnabled && cfg.DB != nil {
		store = NewSqliteSpool(cfg.DB, cfg.ID)
	}
	return delivery.NewOutbox(delivery.OutboxConfig{
		Tag:       cfg.Tag,
		ID:        cfg.ID,
		Name:      cfg.Name,
		Store:     store,
		SpoolCap:  cfg.SpoolCap,
		Conn:      cfg.Conn,
		QueueSize: cfg.QueueSize,
		Logger:    ComponentLogger(),
	})
}

// componentLogger 把组件日志转发到网关的全局日志系统。
type componentLogger struct{}

func (componentLogger) Debugf(format string, args ...any) { logger.Debug(format, args...) }
func (componentLogger) Infof(format string, args ...any)  { logger.Info(format, args...) }
func (componentLogger) Warnf(format string, args ...any)  { logger.Warn(format, args...) }
func (componentLogger) Errorf(format string, args ...any) { logger.Error(format, args...) }

// ComponentLogger 返回转发到网关日志系统的组件日志器。
// 供时序通道适配器注入 tsdb.Options。
func ComponentLogger() tsdb.Logger { return componentLogger{} }
