// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"

	"iot-gateway/driver"
	"iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/workerPool"
)

// Engine 采集引擎，管理全局唯一的轮询任务
type Engine struct {
	db        *gorm.DB
	pool      *workerPool.WorkerPool
	sink      RecordSink
	stateSink DeviceStateSink // 设备在线状态接收者（断联报警），可为 nil

	mu      sync.RWMutex
	task    *gatewayTask
	running bool

	// 自动热加载
	watchCtx      context.Context
	watchCancel   context.CancelFunc
	watchOnce     sync.Once
	watchInterval time.Duration // 配置巡检间隔，默认 10s；测试可缩短以验证 watchLoop 定时执行
}

// NewEngine 创建采集引擎
// sink 用于接收采集数据（如推送引擎），可为 nil；
// stateSink 用于接收设备在线状态（如断联报警引擎），可为 nil。
func NewEngine(db *gorm.DB, pool *workerPool.WorkerPool, sink RecordSink, stateSink DeviceStateSink) *Engine {
	return &Engine{
		db:            db,
		pool:          pool,
		sink:          sink,
		stateSink:     stateSink,
		watchInterval: 10 * time.Second,
	}
}

// Start 启动采集引擎，开始轮询所有设备
func (e *Engine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running {
		return nil
	}

	// 启动 WorkerPool（如果未启动）
	if !e.pool.IsRunning() {
		e.pool.Start()
	}

	// 创建并启动唯一的采集任务（自动加载所有活跃设备）
	task, err := newGatewayTask(e.db, e.sink, e.pool, e.stateSink)
	if err != nil {
		return fmt.Errorf("collector engine: create task failed: %w", err)
	}
	e.task = task
	task.Start()

	e.running = true

	// 启动后台自动热加载 watcher
	e.startWatcherLocked()

	logger.Info("collector engine started")
	return nil
}

// Stop 停止采集引擎
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return
	}

	// 停止自动热加载 watcher
	e.stopWatcherLocked()

	if e.task != nil {
		e.task.Stop()
		e.task = nil
	}

	e.pool.Stop()
	e.running = false
	logger.Info("collector engine stopped")
}

// Refresh 热刷新采集配置。
// 顺序：建新任务（驱动只创建不连接）→ 停旧任务 → 启动新任务（首轮懒连接）。
func (e *Engine) Refresh() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return fmt.Errorf("collector engine: not running")
	}

	// 1. 基于最新 DB 数据构建新任务
	newTask, err := newGatewayTask(e.db, e.sink, e.pool, e.stateSink)
	if err != nil {
		return fmt.Errorf("collector engine: refresh create task failed: %w", err)
	}

	// 2. 先停旧任务（排空在途轮询、关闭旧驱动），再启动新任务
	if e.task != nil {
		e.task.Stop()
	}

	// 3. 启动新任务（首轮轮询按需连接）
	newTask.Start()

	// 4. 原子置换
	e.task = newTask

	logger.Info("collector: hot refresh completed")
	return nil
}

// StartCollection 启动采集（兼容旧 API）
// 行为与 Start 完全一致，保留该方法仅为旧调用方（HTTP /collection/start、RestartCollection）提供入口。
func (e *Engine) StartCollection() error {
	return e.Start()
}

// StopCollection 停止采集（暂停语义）
// 与 Stop 的区别：保留 worker pool（不调 pool.Stop），便于 StartCollection 廉价恢复采集。
// 守卫统一为 e.running（与 Start/Stop/Refresh 一致；e.task 与 e.running 始终同步）。
func (e *Engine) StopCollection() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return fmt.Errorf("collector engine: not running")
	}

	// 与 Stop 语义一致：同时停止后台热加载 watcher，
	// 否则 watcher 持续巡检，配置变化时 Refresh 报「not running」且每轮重复报错。
	e.stopWatcherLocked()

	if e.task != nil {
		e.task.Stop()
		e.task = nil
	}
	e.running = false

	logger.Info("collector engine: collection stopped")
	return nil
}

// GetStatus 返回采集状态（协议无关）
func (e *Engine) GetStatus() []GatewayStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.task == nil {
		return nil
	}

	running, lastPoll, lastSuccess, errCount, deviceCount := e.task.Status()
	return []GatewayStatus{
		{
			Running:         running,
			LastPollTime:    formatTime(lastPoll),
			LastSuccessTime: formatTime(lastSuccess),
			ErrorCount:      errCount,
			DeviceCount:     deviceCount,
			AddressCount:    e.task.TotalAddressCount(),
		},
	}
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

// watchLoop 定期比对 DB 采集配置，发生变化时自动 Refresh
// 同时监控 device 和 device_address 两张表，任意变化均触发热加载
func (e *Engine) watchLoop(ctx context.Context) {
	interval := e.watchInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}

	// 内容级 checksum：活跃设备与活跃地址逐行哈希，任意变更都能感知
	lastChecksum := e.loadConfigChecksum()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger.Info("collector: config watcher started (interval=%s)", interval)

	for {
		select {
		case <-ctx.Done():
			logger.Info("collector: config watcher stopped")
			return
		case <-ticker.C:
			cur := e.loadConfigChecksum()
			if cur != lastChecksum {
				logger.Info("collector: config changed (checksum %d->%d), hot reloading", lastChecksum, cur)
				if err := e.Refresh(); err != nil {
					logger.Error("collector: hot reload failed: %v", err)
					continue
				}
				lastChecksum = cur
			}
		}
	}
}

// loadConfigChecksum 计算当前采集配置的内容级校验和。
// 对活跃设备与活跃地址逐行做 FNV-1a 哈希（而非「设备数×偏移+地址数」拼凑），
// 新增、删除、修改、启停、协议/连接参数改动均能使校验和变化，
// 也不会出现「先停用后启用回到旧值」而检测不到的盲区。
func (e *Engine) loadConfigChecksum() uint64 {
	h := fnv.New64a()

	var devices []po.Device
	if err := e.db.Model(&po.Device{}).Where("status = 1").Order("id").Find(&devices).Error; err != nil {
		logger.Error("collector: load config checksum failed: %v", err)
		return h.Sum64()
	}
	for _, d := range devices {
		io.WriteString(h, d.ID)
		io.WriteString(h, "|")
		io.WriteString(h, d.Name)
		io.WriteString(h, "|")
		io.WriteString(h, d.ProtocolID)
		io.WriteString(h, "|")
		io.WriteString(h, d.ProtocolJSON)
		io.WriteString(h, "\n")
	}

	var addrs []po.DeviceAddress
	if err := e.db.Model(&po.DeviceAddress{}).Where("status = 1").Order("id").Find(&addrs).Error; err != nil {
		logger.Error("collector: load config checksum failed: %v", err)
		return h.Sum64()
	}
	for _, a := range addrs {
		io.WriteString(h, a.ID)
		io.WriteString(h, "|")
		io.WriteString(h, a.DeviceID)
		io.WriteString(h, "|")
		io.WriteString(h, a.Name)
		io.WriteString(h, "|")
		io.WriteString(h, a.DataType)
		io.WriteString(h, "|")
		io.WriteString(h, a.RwPermission)
		io.WriteString(h, "|")
		io.WriteString(h, strconv.Itoa(a.ScanFrequency))
		io.WriteString(h, "\n")
	}

	return h.Sum64()
}

// GetDriver 返回指定设备的活跃驱动实例。
// 采集引擎未运行或设备不在采集列表中时返回 nil。
// 供设备连接测试等场景复用已建立的连接，避免重复打开串口。
func (e *Engine) GetDriver(deviceID string) driver.Driver {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.task == nil {
		return nil
	}
	return e.task.GetDriver(deviceID)
}

// ActiveDeviceIDs 返回当前参与采集调度的设备 ID 列表（引擎未运行或空任务时返回 nil）。
// 供在线统计使用：collected 集合 = 有协议驱动且至少一个活跃地址的启用设备。
func (e *Engine) ActiveDeviceIDs() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.task == nil {
		return nil
	}
	return e.task.activeDeviceIDs()
}

// DeviceLastSuccessTime 返回指定设备最近一次成功采集的时间（格式化字符串）。
// 引擎未运行、设备未参与采集或尚无成功记录时返回 ""。
// 内存态：网关重启/热刷新后短暂为空，直到下一轮成功采集。
func (e *Engine) DeviceLastSuccessTime(deviceID string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.task == nil {
		return ""
	}
	return formatTime(e.task.deviceLastSuccess(deviceID))
}

// IsRunning 返回引擎是否在运行
func (e *Engine) IsRunning() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.running && e.task != nil
}

// GatewayStatus 采集状态
type GatewayStatus struct {
	Running         bool   `json:"running"`
	LastPollTime    string `json:"lastPollTime"`
	LastSuccessTime string `json:"lastSuccessTime"`
	ErrorCount      int    `json:"errorCount"`
	DeviceCount     int    `json:"deviceCount"`
	AddressCount    int    `json:"addressCount"`
}

// formatTime 格式化时间为字符串
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
