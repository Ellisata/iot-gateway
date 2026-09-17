// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"iot-gateway/logger"
)

// 出站队列与断网缓存的容量/节奏参数。
//
// 这几个数值原先在 mqtt / tdengine / influxdb 三个通道里各留了一份逐字相同的
// 副本，集中到此处后队列容量与落盘节奏只有一处定义。
const (
	// outboxSize 每通道有界出站缓冲大小，满时丢弃最旧，保证采集永不阻塞
	outboxSize = 1024
	// spoolChSize 本地缓存写入缓冲大小（与 outbox 同策略，满时丢弃最旧，保证采集永不阻塞）
	spoolChSize = 1024
	// spoolDrainWindow 补发协程单次从本地缓存取回的最大批次数（有界回放窗口）
	spoolDrainWindow = 16
	// spoolDrainBackstop 补发等待兜底间隔：仅作漏 ping 保险。
	// 新批次落盘/重连事件即时唤醒补发，正常空闲时对 SQLite 零无效查询；
	// 此间隔远大于事件间隔，仅兜底极端漏唤醒场景（漏 ping 时最多延迟一个间隔补发）。
	spoolDrainBackstop = 10 * time.Second
	// spoolTrimInterval 本地缓存达到上限后每插入 N 批执行一次批量裁剪，
	// spoolTrimChunk 为单次删除的批次数量：满盘期把逐批 SELECT+DELETE 摊薄 N 倍，
	// 缓解 SQLite 单连接写放大（断连持续期间）。
	spoolTrimInterval = 16
	spoolTrimChunk    = 16
)

// Connectivity 通道连通状态判据：mqtt 由 paho 的连接/断开回调维护，
// tdengine / influxdb 为原子标志，故抽成接口而非统一字段。
// Outbox 据此决定入队是走内存队列还是直接落盘。
type Connectivity interface {
	Connected() bool
}

// OutboxConfig 构造 Outbox 的一次性参数。
type OutboxConfig struct {
	Tag          string       // 日志前缀（通道类型名，如 "mqtt"）
	ID           string       // 通道持久化 ID；断网缓存按 channel_id 隔离
	Name         string       // 通道名（状态快照用）
	DB           *gorm.DB     // 断网缓存持久化；SpoolEnabled 为 false 时忽略
	SpoolEnabled bool         // 配置是否启用断网缓存
	SpoolCap     int          // 缓存批次上限（达到后裁剪最旧）
	Conn         Connectivity // 连通状态判据，必须非 nil
	QueueSize    int          // 出站缓冲大小；≤0 时取默认值 outboxSize
}

// Outbox 推送通道的「非阻塞出站队列 + 断网本地缓存」骨架，并持有该通道的
// 运行状态与计数。
//
// 三个推送通道（mqtt / tdengine / influxdb）的这段逻辑此前各自复制了一份，
// 差异只在日志前缀与个别收尾细节。集中到此处后，队列容量、丢弃策略、落盘、
// 补发唤醒、毒批次判定的语义只有一处实现；各通道只负责「怎么把负载发出去」
// （publish / write / DrainFunc）与各自的连接维护。
//
// 数据流：
//
//	采集线程 ──Enqueue──▶ outbox（有界，满则丢最旧，绝不阻塞采集）
//	                        │
//	                        ├─ 下发成功 ──▶ 下游
//	                        └─ 下发失败 ──▶ spoolCh ──▶ SQLite（按 channel_id 隔离）
//	                                                          │
//	                                       重连/落盘事件 ──▶ DrainLoop 按入队序补发
//
// 生命周期：Start → 通道运行自己的 worker 组消费 Batches() → Close（关 quit）
// → 等 worker 组退出 → Wait（等落盘协程排空在途批次）。见各方法注释。
type Outbox struct {
	tag      string
	id       string
	name     string
	spoolCap int
	conn     Connectivity

	// spool 断网本地缓存（nil 表示未启用，回退为纯内存丢最旧行为）
	spool Spool

	queueSize int

	outbox      chan PushBatch
	spoolCh     chan PushBatch
	spoolDone   chan struct{}
	drainNotify chan struct{}
	quit        chan struct{}

	// workersDone 由 Wait 关闭：此后不会再有 worker 把批次转投 spoolCh。
	workersDone     chan struct{}
	workersDoneOnce sync.Once

	// trimTick 满盘批量裁剪节流计数（仅 spoolWriteLoop 单协程访问，无需加锁）
	trimTick int

	mu           sync.RWMutex
	running      bool
	lastErr      string
	lastPublish  time.Time
	lastSuccess  time.Time
	publishCount atomic.Uint64
	droppedCount atomic.Uint64
}

// NewOutbox 构造通道的出站队列与断网缓存。
// SpoolEnabled 时会立即绑定 db 做一次既有积压的 seed 计数（push_outbox 跨重启持久化）。
func NewOutbox(cfg OutboxConfig) *Outbox {
	size := cfg.QueueSize
	if size <= 0 {
		size = outboxSize
	}
	o := &Outbox{
		tag:       cfg.Tag,
		id:        cfg.ID,
		name:      cfg.Name,
		spoolCap:  cfg.SpoolCap,
		conn:      cfg.Conn,
		queueSize: size,
	}
	if cfg.SpoolEnabled {
		o.spool = NewSqliteSpool(cfg.DB, cfg.ID)
	}
	return o
}

// ID 返回通道持久化 ID。
func (o *Outbox) ID() string { return o.id }

// Name 返回通道名。
func (o *Outbox) Name() string { return o.name }

// Spool 返回断网本地缓存，未启用时为 nil。
// 补发策略（DrainFunc）据此删除已成功下发的批次。
func (o *Outbox) Spool() Spool { return o.spool }

// SpoolEnabled 断网本地缓存是否启用。
func (o *Outbox) SpoolEnabled() bool { return o.spool != nil }

// Quit 返回停止信号：关闭后各 worker 与落盘协程应尽快退出。
func (o *Outbox) Quit() <-chan struct{} { return o.quit }

// Batches 返回出站队列的读端，供通道的 worker 组消费。
func (o *Outbox) Batches() <-chan PushBatch { return o.outbox }

// Start 创建运行时通道并启动断网缓存的落盘协程。可重复调用（已启动则 no-op）。
//
// 调用方（通道）随后应运行自己的 worker 组消费 Batches()，并在退出后按
// Close → 等 worker 退出 → Wait 的顺序收尾。
func (o *Outbox) Start() {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.running {
		return
	}
	o.running = true
	o.quit = make(chan struct{})
	o.workersDone = make(chan struct{})
	o.workersDoneOnce = sync.Once{}
	o.outbox = make(chan PushBatch, o.queueSize)

	// 本地缓存写协程立即启动：断连期间的批次随时可落盘
	if o.spoolEnabled() {
		o.spoolCh = make(chan PushBatch, spoolChSize)
		o.spoolDone = make(chan struct{})
		o.drainNotify = make(chan struct{}, 1)
		go o.spoolWriteLoop()
	}
}

// Close 触发停止：关闭 quit，各 worker 与落盘协程由此退出。幂等。
// 不等待任何协程 —— 调用方需等自己的 worker 组退出后再调用 Wait。
func (o *Outbox) Close() {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.running {
		return
	}
	o.running = false
	close(o.quit)
}

// Wait 等待落盘协程把在途批次全部落盘后退出，返回后不会再有未落盘的批次。
//
// 调用前提：本通道的 worker 组必须已全部退出。worker 停服前会把 outbox 中
// 写失败的批次转投缓存，提前调用会把它们留在内存通道中丢失。故调用方应先
// Close，再等自己的 worker 组退出，最后才调用 Wait。
func (o *Outbox) Wait() {
	o.mu.RLock()
	started := o.quit != nil
	spoolDone := o.spoolDone
	o.mu.RUnlock()

	if !started {
		return // 从未 Start，没有落盘协程可等
	}
	o.workersDoneOnce.Do(func() { close(o.workersDone) })
	if spoolDone != nil {
		<-spoolDone
	}
}

// ReconnectLoop 连通性维护协程：首连立即尝试，之后仅在断开时按 interval 重试，
// 直至 Outbox 停止。
//
// 放在 Outbox 上是因为它只需要「停止信号 + 连通状态」这两样，而两者都在这里；
// 真正的建连（含幂等的建库建表）由通道的 tryConnect 负责 —— 它成功时必须置位
// 连通状态并调用 Notify 唤醒补发，否则断连期间落盘的批次会一直等到兜底 timer。
func (o *Outbox) ReconnectLoop(interval time.Duration, tryConnect func()) {
	if !o.isConnected() {
		tryConnect()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-o.quit:
			return
		case <-ticker.C:
			if !o.isConnected() {
				tryConnect()
			}
		}
	}
}

// Enqueue 非阻塞入队，绝不阻塞采集线程。
// 路由：
//   - 断连且启用本地缓存：直接落盘，避免进内存队列后被 worker 丢弃；
//   - 否则内存 outbox 快路径，满时启用本地缓存则溢写，未启用则丢弃最旧（兼容旧行为）。
func (o *Outbox) Enqueue(b PushBatch) {
	// 断连且启用本地缓存：直接落盘
	if o.spoolEnabled() && !o.isConnected() {
		o.SpoolEnqueue(b)
		return
	}

	// 内存快路径
	select {
	case o.outbox <- b:
		return
	default:
	}

	// 内存队列满：启用本地缓存则溢写，否则丢弃最旧
	if o.spoolEnabled() {
		o.SpoolEnqueue(b)
		return
	}

	// 丢弃最旧、保留最新，被丢弃的批次计入 droppedCount
	select {
	case <-o.outbox:
		o.droppedCount.Add(1)
	default:
	}

	select {
	case o.outbox <- b:
	default:
		o.droppedCount.Add(1) // 仍满，丢弃本条
	}
}

// Notify 非阻塞通知补发协程有新批次可补发（cap=1 自动合流，无信号时 no-op）。
// 重连回调与新批次落盘都经此唤醒补发，避免定时轮询。
func (o *Outbox) Notify() {
	select {
	case o.drainNotify <- struct{}{}:
	default:
	}
}

// WaitDrain 阻塞等待补发信号：新批次落盘/重连事件即时唤醒，
// 兜底 timer 仅防漏 ping。返回 false 表示通道已停止。
func (o *Outbox) WaitDrain() bool {
	backstop := time.NewTimer(spoolDrainBackstop)
	defer backstop.Stop()

	select {
	case <-o.quit:
		return false
	case <-o.drainNotify:
		return true
	case <-backstop.C:
		return true
	}
}

// MarkPublished 记录 n 次成功下发：累加成功计数并刷新最近发布/成功时间。
func (o *Outbox) MarkPublished(n uint64) {
	now := time.Now()
	o.mu.Lock()
	o.lastPublish = now
	o.lastSuccess = now
	o.mu.Unlock()
	o.publishCount.Add(n)
}

// Drop 累加被丢弃的批次数（队列满、缓存满、毒批次、未启用缓存时的下发失败）。
func (o *Outbox) Drop(n uint64) { o.droppedCount.Add(n) }

// Dropped 返回累计丢弃批次数的当前值。
func (o *Outbox) Dropped() uint64 { return o.droppedCount.Load() }

// SetLastErr 记录最近一次错误。
func (o *Outbox) SetLastErr(msg string) {
	o.mu.Lock()
	o.lastErr = msg
	o.mu.Unlock()
}

// FillStatus 用队列与缓存的实时状态填充状态快照的公共字段
// （ID/Name/Running/Connected/队列深度/缓存积压/计数/时间/错误）。
// 通道负责先补齐自身类型相关的字段（Type/Broker/Topic）。
func (o *Outbox) FillStatus(vo *ChannelStatusVO) {
	// 连通状态在锁外取：conn 的实现（如 mqtt 的 paho 回调状态）持有自己的锁，
	// 避免与 o.mu 形成嵌套锁序。
	connected := o.isConnected()

	o.mu.RLock()
	defer o.mu.RUnlock()

	spoolDepth := uint64(0)
	if o.spoolEnabled() {
		if n, err := o.spool.Count(); err == nil {
			spoolDepth = uint64(n)
		}
	}

	vo.ID = o.id
	vo.Name = o.name
	vo.Running = o.running
	vo.Connected = connected
	vo.QueueDepth = uint64(len(o.outbox))
	vo.SpoolDepth = spoolDepth
	vo.PublishCount = o.publishCount.Load()
	vo.DroppedCount = o.droppedCount.Load()
	vo.LastPublish = FormatTime(o.lastPublish)
	vo.LastSuccess = FormatTime(o.lastSuccess)
	vo.LastErr = o.lastErr
}

// spoolEnabled 断网本地缓存是否实际可用（构造时按配置决定，运行期不变）。
func (o *Outbox) spoolEnabled() bool { return o.spool != nil }

// isConnected 查询通道连通状态（conn 为 nil 的裸构造视为未连接）。
func (o *Outbox) isConnected() bool {
	return o.conn != nil && o.conn.Connected()
}

// SpoolEnqueue 非阻塞把批次转投本地缓存写入缓冲，缓冲满时丢弃最旧
// （同 outbox 策略），保证采集线程永不阻塞。未启用缓存时 no-op。
// 供通道在下发失败、或内存队列满且已启用缓存时转投。
func (o *Outbox) SpoolEnqueue(b PushBatch) {
	if !o.spoolEnabled() {
		return
	}
	select {
	case o.spoolCh <- b:
		return
	default:
	}

	select {
	case <-o.spoolCh:
		o.droppedCount.Add(1)
	default:
	}

	select {
	case o.spoolCh <- b:
	default:
		o.droppedCount.Add(1) // 仍满，丢弃本条
	}
}

// spoolWriteLoop 本地缓存写协程：消费 spoolCh 串行写入 SQLite。
//
// 退出前（quit 关闭）先排空在途批次，再等 Wait 标记 worker 组已退出 ——
// worker 停服前会把 outbox 中写失败的批次转投 spoolCh，提前返回会把它们
// 留在内存通道中丢失。
func (o *Outbox) spoolWriteLoop() {
	defer close(o.spoolDone)

	for {
		select {
		case <-o.quit:
			o.drainSpoolCh()
			for {
				select {
				case <-o.workersDone:
					o.drainSpoolCh()
					return
				case b := <-o.spoolCh:
					o.insert(b)
				}
			}
		case b := <-o.spoolCh:
			o.insert(b)
		}
	}
}

// drainSpoolCh 非阻塞排空 spoolCh 中的在途批次（写协程退出前的收尾用）。
func (o *Outbox) drainSpoolCh() {
	for {
		select {
		case b := <-o.spoolCh:
			o.insert(b)
		default:
			return
		}
	}
}

// insert 将批次写入本地缓存，并 ping 补发协程（事件驱动补发，无需轮询）。
// 达到上限后每 N 批批量裁剪最旧以约束磁盘占用：Count 为内存计数（无查询），
// 裁剪由原逐批 DeleteOldest(1) 改为批量 DeleteOldest(N)，
// 摊薄满盘期的 SQLite 写放大。
func (o *Outbox) insert(b PushBatch) {
	defer o.Notify()

	if o.spool == nil {
		return
	}
	o.trimTick++
	if o.trimTick >= spoolTrimInterval {
		o.trimTick = 0
		if count, err := o.spool.Count(); err == nil && count >= int64(o.spoolCap) {
			if err := o.spool.DeleteOldest(spoolTrimChunk); err != nil {
				logger.Error("%s: spool trim oldest failed: %v", o.tag, err)
			}
		}
	}
	if err := o.spool.Insert(b); err != nil {
		errMsg := fmt.Sprintf("spool insert: %v", err)
		logger.Error("%s", errMsg)
		o.SetLastErr(errMsg)
	}
}
