// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// maxRecordsPerBatch 单个 PushBatch 的最大记录数上限。
// 超出时按设备分片为多个小批次：约束 MQTT 单帧载荷（每点约 150B，
// 500 点 ≈ 75KB，兼容 EMQX/AWS 等主流 broker 的载荷上限）与 outbox 内存
// （防止整台设备百万级点位打包成超大批次入队）。
// 后续可提升为配置项（configFile.Config）。
const maxRecordsPerBatch = 500

// Engine 数据推送引擎，管理所有活跃推送通道。
//
// 实现 collector.RecordSink：采集引擎每轮采集后将数据交给 PushRecords，
// 本引擎按设备分组后分发给各推送通道（非阻塞入队，序列化由各通道自行负责）。
// 通过后台 watcher 监控 push_channel 表变化，热加载通道配置。
type Engine struct {
	db *gorm.DB

	mu       sync.RWMutex
	channels map[string]Channel
	running  bool

	// 自动热加载
	watchCtx    context.Context
	watchCancel context.CancelFunc
	watchOnce   sync.Once
}

// NewEngine 创建数据推送引擎
func NewEngine(db *gorm.DB) *Engine {
	return &Engine{
		db:       db,
		channels: make(map[string]Channel),
	}
}

// Start 启动推送引擎：加载活跃通道并开始监控配置变化
func (e *Engine) Start() error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return nil
	}
	e.running = true
	e.mu.Unlock()

	// 初始加载活跃通道
	e.refresh()

	// 启动后台自动热加载 watcher
	e.mu.Lock()
	e.startWatcherLocked()
	e.mu.Unlock()

	logger.Info("push engine started")
	return nil
}

// Stop 停止推送引擎，关闭所有通道
func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}
	e.running = false
	e.stopWatcherLocked()

	channels := make([]Channel, 0, len(e.channels))
	for _, ch := range e.channels {
		channels = append(channels, ch)
	}
	e.channels = make(map[string]Channel)
	e.mu.Unlock()

	for _, ch := range channels {
		ch.Stop()
	}
	logger.Info("push engine stopped")
}

// Refresh 热刷新推送配置（新建/修改/删除通道无需重启）
func (e *Engine) Refresh() error {
	e.mu.RLock()
	if !e.running {
		e.mu.RUnlock()
		return fmt.Errorf("push engine: not running")
	}
	e.mu.RUnlock()

	e.refresh()
	return nil
}

// PushRecords 实现 collector.RecordSink：按设备分组后分发给各通道。
// 非阻塞：通道 outbox 满时丢弃最旧，绝不阻塞采集线程。
func (e *Engine) PushRecords(records []collector.CollectedRecord) {
	if len(records) == 0 {
		return
	}

	// 毫秒精度:TDengine 主键含时间戳(亚秒级轮次同秒会覆盖),秒级格式会静默丢数据
	now := time.Now().Format("2006-01-02 15:04:05.000")

	// 按设备分组（当前采集流程本就是一设备一批，此处防御性分组）
	groups := make(map[string][]collector.CollectedRecord)
	deviceIDs := make([]string, 0, len(records))
	for _, r := range records {
		if _, ok := groups[r.DeviceID]; !ok {
			deviceIDs = append(deviceIDs, r.DeviceID)
		}
		groups[r.DeviceID] = append(groups[r.DeviceID], r)
	}

	// 快照当前活跃通道
	e.mu.RLock()
	chans := make([]Channel, 0, len(e.channels))
	for _, ch := range e.channels {
		chans = append(chans, ch)
	}
	e.mu.RUnlock()
	if len(chans) == 0 {
		return
	}

	// 按设备分组后分片入队：每台设备拆成 ≤maxRecordsPerBatch 的小批次，
	// 避免整台设备所有点位打包成一个超大批次（MQTT 单帧载荷与 outbox 内存均不可控）。
	for _, devID := range deviceIDs {
		for _, b := range splitBatches(devID, now, groups[devID]) {
			for _, ch := range chans {
				ch.Enqueue(b)
			}
		}
	}
}

// splitBatches 将同一设备的记录切片为 ≤maxRecordsPerBatch 的小批次。
// 对底层数组零拷贝切片，各批次共享同一采集时间戳。
func splitBatches(deviceID, collectedAt string, records []collector.CollectedRecord) []PushBatch {
	if len(records) <= maxRecordsPerBatch {
		return []PushBatch{{DeviceID: deviceID, CollectedAt: collectedAt, Records: records}}
	}
	batches := make([]PushBatch, 0, (len(records)+maxRecordsPerBatch-1)/maxRecordsPerBatch)
	for i := 0; i < len(records); i += maxRecordsPerBatch {
		end := i + maxRecordsPerBatch
		if end > len(records) {
			end = len(records)
		}
		batches = append(batches, PushBatch{
			DeviceID:    deviceID,
			CollectedAt: collectedAt,
			Records:     records[i:end],
		})
	}
	return batches
}

// GetStatus 返回所有通道的运行状态
func (e *Engine) GetStatus() []ChannelStatusVO {
	e.mu.RLock()
	defer e.mu.RUnlock()

	statuses := make([]ChannelStatusVO, 0, len(e.channels))
	for _, ch := range e.channels {
		statuses = append(statuses, ch.Snapshot())
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
	return statuses
}

// refresh 重建通道列表并原子替换，未变化的通道保留运行实例
func (e *Engine) refresh() {
	newChans, err := e.buildChannels()
	if err != nil {
		logger.Error("push: refresh build channels failed: %v", err)
		return
	}

	e.mu.Lock()
	var toStop []Channel
	newMap := make(map[string]Channel, len(newChans))
	for _, nc := range newChans {
		if old, ok := e.channels[nc.ChannelID()]; ok && old.ConfigSig() == nc.ConfigSig() {
			newMap[nc.ChannelID()] = old // 配置未变化，保留运行中的实例
			continue
		}
		nc.Start() // 新通道或配置变化：启动新实例
		newMap[nc.ChannelID()] = nc
		if old, ok := e.channels[nc.ChannelID()]; ok {
			toStop = append(toStop, old)
		}
	}
	for id, old := range e.channels {
		if _, ok := newMap[id]; !ok {
			toStop = append(toStop, old) // 被删除或停用
		}
	}
	e.channels = newMap
	e.mu.Unlock()

	// 解锁后再停止旧通道：Stop 会阻塞等待发布排空，不能占用锁（否则 PushRecords 会卡住）
	for _, old := range toStop {
		old.Stop()
	}
}

// buildChannels 从数据库加载活跃通道，按类型注册表构建，
// 跳过配置非法或类型不支持的通道
func (e *Engine) buildChannels() ([]Channel, error) {
	var rows []po.PushChannel
	if err := e.db.Where("status = 1").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("push: load push channels failed: %w", err)
	}

	chans := make([]Channel, 0, len(rows))
	for i := range rows {
		ch := &rows[i]
		factory, ok := channelFactories[strings.ToLower(ch.Name)]
		if !ok {
			logger.Warn("push: channel type %s not supported yet, skip", ch.Name)
			continue
		}
		c, err := factory(e.db, ch)
		if err != nil {
			logger.Error("push: channel %s config invalid, skip: %v", ch.Name, err)
			continue
		}
		chans = append(chans, c)
	}
	return chans, nil
}

// startWatcherLocked 启动后台自动热加载监视器（调用方需持有 mu 写锁）
func (e *Engine) startWatcherLocked() {
	e.watchOnce.Do(func() {
		e.watchCtx, e.watchCancel = context.WithCancel(context.Background())
		go e.watchLoop(e.watchCtx)
	})
}

// stopWatcherLocked 停止后台自动热加载监视器（调用方需持有 mu 写锁）
func (e *Engine) stopWatcherLocked() {
	if e.watchCancel != nil {
		e.watchCancel()
		e.watchCancel = nil
		e.watchCtx = nil
		e.watchOnce = sync.Once{}
	}
}

// watchLoop 定期比对 push_channel 配置，发生变化时热刷新
func (e *Engine) watchLoop(ctx context.Context) {
	const checkInterval = 10 * time.Second

	lastChecksum := e.loadChecksum()

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	logger.Info("push: config watcher started (interval=%s)", checkInterval)

	for {
		select {
		case <-ctx.Done():
			logger.Info("push: config watcher stopped")
			return
		case <-ticker.C:
			cur := e.loadChecksum()
			if cur != lastChecksum {
				logger.Info("push: push channel config changed (checksum %d->%d), hot reloading", lastChecksum, cur)
				e.refresh()
				lastChecksum = cur
			}
		}
	}
}

// loadChecksum 计算活跃通道配置的校验和（id/name/config_json）
// 新增、修改、删除、启停均会使校验和变化
func (e *Engine) loadChecksum() uint64 {
	type row struct {
		ID         string
		Name       string
		ConfigJSON string
	}
	var rows []row
	if err := e.db.Model(&po.PushChannel{}).
		Select("id, name, config_json").
		Where("status = 1").
		Order("id").
		Find(&rows).Error; err != nil {
		logger.Error("push: load checksum failed: %v", err)
		return 0
	}

	h := fnv.New64a()
	for _, r := range rows {
		io.WriteString(h, r.ID)
		io.WriteString(h, "|")
		io.WriteString(h, r.Name)
		io.WriteString(h, "|")
		io.WriteString(h, r.ConfigJSON)
		io.WriteString(h, "\n")
	}
	return h.Sum64()
}
