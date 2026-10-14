// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"context"
	"hash/fnv"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"iot-gateway/alarm"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// Options 通知器运行参数。
type Options struct {
	IngressSize    int           // 入口队列容量
	QueueSize      int           // 单个 webhook 的队列容量
	MaxAttempts    int           // 单条事件最大尝试次数（含首次）
	InitialBackoff time.Duration // 首次重试退避
	MaxBackoff     time.Duration // 退避上限
	HTTPTimeout    time.Duration // 单次投递超时
	StopDrain      time.Duration // 停机排空预算
	WatchInterval  time.Duration // 配置巡检间隔
}

// defaultOptions 生产默认参数。
//
// 单条事件最坏耗时 ≈ (MaxAttempts-1) 次退避 + MaxAttempts 次 HTTP 超时
// = 1+2+4s + 4×5s ≈ 27s，之后丢弃并计入 failedCount。
var defaultOptions = Options{
	IngressSize:    1024,
	QueueSize:      256,
	MaxAttempts:    4,
	InitialBackoff: time.Second,
	MaxBackoff:     30 * time.Second,
	HTTPTimeout:    defaultHTTPTimeout,
	StopDrain:      5 * time.Second,
	WatchInterval:  10 * time.Second,
}

// withDefaults 补齐非法/缺省字段。
func (o Options) withDefaults() Options {
	d := defaultOptions
	if o.IngressSize <= 0 {
		o.IngressSize = d.IngressSize
	}
	if o.QueueSize <= 0 {
		o.QueueSize = d.QueueSize
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = d.MaxAttempts
	}
	if o.InitialBackoff <= 0 {
		o.InitialBackoff = d.InitialBackoff
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = d.MaxBackoff
	}
	if o.HTTPTimeout <= 0 {
		o.HTTPTimeout = d.HTTPTimeout
	}
	if o.StopDrain <= 0 {
		o.StopDrain = d.StopDrain
	}
	if o.WatchInterval <= 0 {
		o.WatchInterval = d.WatchInterval
	}
	return o
}

// Dispatcher 报警通知分发器，实现 alarm.NotifySink。
//
// 结构：报警引擎把事件投进入口队列（非阻塞，满则丢），单个 fanout 协程按
// 配置把事件扇出到各 webhook 的独立队列，各 webhook 由自己的 worker 协程
// 串行投递并做指数退避重试。fanout 协程上不做任何 DB IO（SQLite 是单连接），
// 配置读取交给独立的巡检协程。
type Dispatcher struct {
	db     *gorm.DB
	opts   Options
	sender *sender

	// ingress 在构造时分配且永不关闭：Notify 可能来自采集协程，
	// 关闭会导致 send on closed channel panic。
	ingress        chan alarm.Event
	running        atomic.Bool
	ingressDropped atomic.Uint64

	// reloadMu 串行化配置重建（巡检协程与 API 手动刷新可能并发）
	reloadMu sync.Mutex

	mu      sync.RWMutex
	workers map[string]*webhook
	quit    chan struct{}

	watchMu     sync.Mutex
	watchCtx    context.Context
	watchCancel context.CancelFunc
}

// NewDispatcher 创建报警通知分发器（未启动）。
func NewDispatcher(db *gorm.DB) *Dispatcher {
	return newDispatcherWithOptions(db, defaultOptions)
}

// newDispatcherWithOptions 指定参数创建（供测试注入毫秒级退避）。
func newDispatcherWithOptions(db *gorm.DB, opts Options) *Dispatcher {
	opts = opts.withDefaults()
	return &Dispatcher{
		db:      db,
		opts:    opts,
		sender:  newSender(opts.HTTPTimeout),
		ingress: make(chan alarm.Event, opts.IngressSize),
		workers: make(map[string]*webhook),
	}
}

// Notify 实现 alarm.NotifySink：非阻塞投递，绝不反压报警状态机。
//
// 该实现只做一次 select/default 入队，是 NotifySink 契约的唯一生产实现，
// 契约见 alarm.NotifySink 文档。
func (d *Dispatcher) Notify(ev alarm.Event) {
	if !d.running.Load() {
		// 未启动/已停止：不无谓入队，直接计数丢弃
		d.ingressDropped.Add(1)
		return
	}
	select {
	case d.ingress <- ev:
	default:
		d.ingressDropped.Add(1)
	}
}

// Start 加载配置并启动分发（可重复调用；Stop 后可再次启动）。
func (d *Dispatcher) Start() error {
	d.mu.Lock()
	if d.running.Load() {
		d.mu.Unlock()
		return nil
	}
	d.quit = make(chan struct{})
	d.running.Store(true)
	quit := d.quit
	d.mu.Unlock()

	d.reload()
	go d.fanoutLoop(quit)
	d.startWatcher(quit)

	logger.Info("notify: alarm webhook dispatcher started")
	return nil
}

// Stop 停止分发并等待在途投递排空（有界），幂等。
func (d *Dispatcher) Stop() {
	d.mu.Lock()
	if !d.running.Load() {
		d.mu.Unlock()
		return
	}
	d.running.Store(false)
	d.stopWatcher()

	quit := d.quit
	workers := make([]*webhook, 0, len(d.workers))
	for _, w := range d.workers {
		workers = append(workers, w)
	}
	// 清空实例表：再次 Start 时 reload 会重建全新实例（旧实例的 quit 已关闭）
	d.workers = make(map[string]*webhook)
	d.mu.Unlock()

	if n := len(d.ingress); n > 0 {
		d.ingressDropped.Add(uint64(n))
		logger.Warn("notify: 停机时入口队列仍有 %d 条报警未分发，已丢弃", n)
	}
	close(quit)

	deadline := time.Now().Add(d.opts.StopDrain)
	for _, w := range workers {
		w.stop(deadline)
	}
	logger.Info("notify: alarm webhook dispatcher stopped")
}

// Refresh 手动热刷新配置（增删改配置后调用，尽力而为）。
// 未启动时直接返回：配置已落库，Start 时会加载。
func (d *Dispatcher) Refresh() error {
	if !d.running.Load() {
		return nil
	}
	d.reload()
	return nil
}

// GetStatus 返回运行健康快照。
func (d *Dispatcher) GetStatus() DispatcherStatus {
	d.mu.RLock()
	workers := make([]*webhook, 0, len(d.workers))
	for _, w := range d.workers {
		workers = append(workers, w)
	}
	d.mu.RUnlock()

	statuses := make([]WebhookStatusVO, 0, len(workers))
	for _, w := range workers {
		statuses = append(statuses, w.snapshot())
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })

	return DispatcherStatus{
		Running:        d.running.Load(),
		IngressDepth:   len(d.ingress),
		IngressDropped: d.ingressDropped.Load(),
		Webhooks:       statuses,
	}
}

// SendOnce 按给定配置同步投递一次报警事件，不做重试，供「测试发送」接口使用。
// 返回平台原始错误（加签失败、关键字不匹配等），便于操作者定位。
func SendOnce(cfg *po.AlarmWebhook, ev alarm.Event) error {
	formatter, err := NewFormatter(cfg)
	if err != nil {
		return err
	}
	req, err := formatter.Format(ev)
	if err != nil {
		return err
	}
	return newSender(0).send(req)
}

// fanoutLoop 入口队列的单消费者：把事件扇出到各 webhook 队列。
func (d *Dispatcher) fanoutLoop(quit <-chan struct{}) {
	for {
		select {
		case <-quit:
			return
		case ev := <-d.ingress:
			d.fanout(ev)
		}
	}
}

// fanout 向所有运行中的 webhook 非阻塞投递（与 push.Engine.PushRecords 同构）。
func (d *Dispatcher) fanout(ev alarm.Event) {
	d.mu.RLock()
	workers := make([]*webhook, 0, len(d.workers))
	for _, w := range d.workers {
		workers = append(workers, w)
	}
	d.mu.RUnlock()

	for _, w := range workers {
		w.enqueue(ev)
	}
}

// reload 重建 webhook 实例表：配置未变的保留原实例（队列与计数不丢），
// 新增/变更的启动新实例，被删除/停用的停止旧实例。
func (d *Dispatcher) reload() {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()

	rows, err := d.loadEnabled()
	if err != nil {
		logger.Error("notify: 加载报警 Webhook 配置失败: %v", err)
		return
	}

	d.mu.RLock()
	old := d.workers
	d.mu.RUnlock()

	newMap := make(map[string]*webhook, len(rows))
	var toStop []*webhook
	for i := range rows {
		cfg := &rows[i]
		w, err := newWebhook(d.sender, d.opts, cfg)
		if err != nil {
			// 配置非法：跳过该条而非中断全量加载，其余 webhook 照常工作
			logger.Error("notify: webhook %s(%s) 配置非法，已跳过: %v", cfg.Name, cfg.Type, err)
			continue
		}
		if prev, ok := old[w.id]; ok && prev.cfgSig == w.cfgSig {
			newMap[w.id] = prev // 配置未变化，保留运行中的实例
			continue
		}
		w.start()
		newMap[w.id] = w
		if prev, ok := old[w.id]; ok {
			toStop = append(toStop, prev)
		}
	}
	for id, prev := range old {
		if _, ok := newMap[id]; !ok {
			toStop = append(toStop, prev) // 被删除或停用
		}
	}

	d.mu.Lock()
	d.workers = newMap
	d.mu.Unlock()

	// 解锁后再停旧实例：stop 会阻塞等待排空，占着锁会让 Notify/fanout 卡住
	deadline := time.Now().Add(d.opts.StopDrain)
	for _, prev := range toStop {
		prev.stop(deadline)
	}
}

// loadEnabled 加载启用中的 webhook 配置。
func (d *Dispatcher) loadEnabled() ([]po.AlarmWebhook, error) {
	var rows []po.AlarmWebhook
	if err := d.db.Where("status = 1").Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// startWatcher 启动配置巡检协程（10s 比对校验和）。
func (d *Dispatcher) startWatcher(quit <-chan struct{}) {
	d.watchMu.Lock()
	defer d.watchMu.Unlock()
	if d.watchCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.watchCtx, d.watchCancel = ctx, cancel
	go d.watchLoop(ctx, quit)
}

// stopWatcher 停止配置巡检协程。
func (d *Dispatcher) stopWatcher() {
	d.watchMu.Lock()
	defer d.watchMu.Unlock()
	if d.watchCancel != nil {
		d.watchCancel()
		d.watchCancel = nil
		d.watchCtx = nil
	}
}

// watchLoop 定期比对配置校验和，变化时热刷新。
func (d *Dispatcher) watchLoop(ctx context.Context, quit <-chan struct{}) {
	interval := d.opts.WatchInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastChecksum := d.loadChecksum()
	logger.Info("notify: config watcher started (interval=%s)", interval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-quit:
			return
		case <-ticker.C:
			cur := d.loadChecksum()
			if cur == 0 {
				// 读取失败（返回 0）：不比对，避免误触发全量重建
				continue
			}
			if cur != lastChecksum {
				logger.Info("notify: 报警 Webhook 配置变化 (checksum %d->%d)，热加载", lastChecksum, cur)
				d.reload()
				lastChecksum = cur
			}
		}
	}
}

// loadChecksum 计算启用中 webhook 配置的校验和（失败返回 0）。
func (d *Dispatcher) loadChecksum() uint64 {
	rows, err := d.loadEnabled()
	if err != nil {
		logger.Error("notify: 计算配置校验和失败: %v", err)
		return 0
	}
	h := fnv.New64a()
	for i := range rows {
		io.WriteString(h, configSig(&rows[i]))
		io.WriteString(h, "\n")
	}
	return h.Sum64()
}
