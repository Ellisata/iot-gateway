// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package iotdb

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apache/iotdb-client-go/v2/client"

	"github.com/Ellisata/iot-gateway/tsdb"
	"github.com/Ellisata/iot-gateway/tsdb/delivery"
)

const (
	// reconnectInterval 断连重连尝试间隔(首连在 Start 后立即尝试,失败后按此间隔重试)
	reconnectInterval = 5 * time.Second
	// writeTimeout 单次写入的兜底超时;原生客户端的写调用不接受 context,
	// 这里只用于在取会话前/后检查取消,无法中断已在进行的 RPC。
	writeTimeout = 10 * time.Second
)

// drainPolicy 补发失败的处理策略。服务端明确拒绝(SQL/类型/权限)属数据类错误,
// 判毒避免队头永久阻塞;窄化把坏点从聚合组里隔离出来,避免连坐误删好批次。
var drainPolicy = delivery.DrainPolicy{Poison: true, Narrow: true}

// session 是 Writer 依赖的最小会话能力。
//
// 用窄接口而不是直接持有 client.Session,是为了在单元测试里注入假会话;
// *client.Session 天然满足本接口,生产路径只做一层薄包装。
type session interface {
	ExecuteNonQueryStatement(sql string) error
	InsertRecords(deviceIDs []string, measurements [][]string, dataTypes [][]client.TSDataType, values [][]any, timestamps []int64) error
	Close() error
}

// sessionPool 会话池的最小契约(同样为测试可替换)。
type sessionPool interface {
	Get() (session, error)
	Put(session)
	Close()
}

// iotdbSessionPool 包装官方 client.SessionPool。
//
// GetSession 返回 Session 结构体值(其方法是指针接收者),这里取地址包装成
// *client.Session 以满足窄接口;PutBack 再把值放回池中。
type iotdbSessionPool struct {
	pool      *client.SessionPool
	closeOnce sync.Once
}

func (p *iotdbSessionPool) Get() (session, error) {
	s, err := p.pool.GetSession()
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (p *iotdbSessionPool) Put(s session) {
	if cs, ok := s.(*client.Session); ok {
		p.pool.PutBack(*cs)
	}
}

func (p *iotdbSessionPool) Close() {
	p.closeOnce.Do(p.pool.Close)
}

// Writer 同步写入 IoTDB 2.x(树模型)的客户端。
//
// 只负责「连接 + 建库 + 行映射 + 写入」:不排队、不重试、不起后台协程。
// 项目已有自己的队列/断网缓存时直接用它;需要可靠投递时用 Sink。
type Writer struct {
	cfg *Config
	log tsdb.Logger

	// connectMu 串行化建池/建库尝试,避免重连协程与并发写入重复建库
	connectMu sync.Mutex

	// useMu 保证 Close 关池时没有在途的 GetSession/InsertRecords。
	// 客户端会话池关闭后再发送会 panic(send on closed channel),不是返回错误;
	// Sink 路径本来就在 worker 全部退出后才 Close,这里给直接把 Writer 当库用、
	// 可能并发 Close 的调用方兜底。
	useMu sync.RWMutex

	mu        sync.RWMutex
	pool      sessionPool
	connected atomic.Bool

	// newPool 生产路径固定为 cfg.newPool;测试可注入假池
	newPool func(*Config) sessionPool
}

// NewWriter 创建一个同步 IoTDB 写入端(尚未连接,首次写入时惰性建连)。
func NewWriter(cfg Config, opts tsdb.Options) *Writer {
	log := opts.Logger
	if log == nil {
		log = tsdb.NopLogger()
	}
	return &Writer{
		cfg:     &cfg,
		log:     log,
		newPool: func(c *Config) sessionPool { return c.newPool() },
	}
}

// Connected 返回当前连接是否可用。
func (w *Writer) Connected() bool { return w.connected.Load() }

// Close 关闭底层会话池。会话池只创建一次、关闭一次;重连不重建池
// (客户端 Session 自带重连,重建/提前关闭池会让并发 GetSession 触发 panic)。
func (w *Writer) Close() error {
	w.useMu.Lock()
	defer w.useMu.Unlock()

	w.mu.Lock()
	p := w.pool
	w.pool = nil
	w.mu.Unlock()

	w.connected.Store(false)
	if p != nil {
		p.Close()
	}
	return nil
}

// Write 将若干批次聚合后同步写入 IoTDB。
//
// ctx 仅用于在负载构造阶段检查取消:原生客户端的 InsertRecords 不接受 context,
// 已经发出的 RPC 无法被 deadline 中断(超时语义见 Config.PoolConfig 的注释)。
func (w *Writer) Write(ctx context.Context, batches []tsdb.Batch) error {
	if len(batches) == 0 {
		return nil
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, writeTimeout)
		defer cancel()
	}

	for len(batches) > 0 {
		agg := w.newAggregator()
		n := 0
		for n < len(batches) {
			b := batches[n]
			if agg.Rows() > 0 && agg.Rows()+len(b.Records) > w.cfg.batchRows() {
				break
			}
			agg.Append(b)
			n++
		}
		if n == 0 {
			return fmt.Errorf("iotdb: batch cannot be aggregated")
		}
		if err := w.WritePayload(ctx, agg.Payload()); err != nil {
			return err
		}
		batches = batches[n:]
	}
	return nil
}

// WritePayload 写入一条已构造好的 JSON 负载(可为多设备多行)。
func (w *Writer) WritePayload(ctx context.Context, payload string) error {
	if payload == "" {
		return nil
	}
	deviceIDs, measurements, dataTypes, values, timestamps, err := decodePayload(payload)
	if err != nil {
		return err
	}
	if len(deviceIDs) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// 与 Close 互斥:关闭会话池前必须先等在途的 Get/InsertRecords 结束。
	w.useMu.RLock()
	defer w.useMu.RUnlock()

	p, err := w.ensure()
	if err != nil {
		return err
	}
	sess, err := p.Get()
	if err != nil {
		w.connected.Store(false)
		return fmt.Errorf("iotdb: get session failed: %w", err)
	}
	err = sess.InsertRecords(deviceIDs, measurements, dataTypes, values, timestamps)
	p.Put(sess)
	if err != nil {
		w.connected.Store(false)
		return fmt.Errorf("iotdb: insert failed: %w", err)
	}
	return nil
}

// TestConnectivity 同步验证连接与建库,成功后关闭临时会话池。
func (w *Writer) TestConnectivity() error {
	p := w.newPool(w.cfg)
	defer p.Close()

	sess, err := p.Get()
	if err != nil {
		return fmt.Errorf("iotdb: connect failed: %w", err)
	}
	err = w.ensureDatabase(sess)
	p.Put(sess)
	if err != nil {
		return fmt.Errorf("iotdb: ensure database failed: %w", err)
	}
	return nil
}

// ensureDatabase 建库,库已存在(状态码 501)视为成功。
//
// 树模型的 CREATE DATABASE 没有 IF NOT EXISTS,只能先执行再用状态码判幂等。
func (w *Writer) ensureDatabase(sess session) error {
	err := sess.ExecuteNonQueryStatement(w.cfg.createDatabaseSQL())
	if err == nil || isDatabaseExistsErr(err) {
		return nil
	}
	return err
}

// isDatabaseExistsErr 识别「数据库已存在」(IoTDB 树模型重复 CREATE DATABASE 的状态码 501)。
// 客户端把非成功状态封装为 *client.ExecutionError,按状态码判断而非匹配错误文案。
func isDatabaseExistsErr(err error) bool {
	var execErr *client.ExecutionError
	return errors.As(err, &execErr) && execErr.Code == client.DatabaseAlreadyExists
}

// newAggregator 为每个新负载创建聚合器,供 Sink 的攒批/补发复用。
func (w *Writer) newAggregator() delivery.Aggregator {
	return newRowBuilder(w.cfg.databasePath())
}

// ensure 返回可用会话池;未连接时惰性建连一次。
func (w *Writer) ensure() (sessionPool, error) {
	if w.connected.Load() {
		if p := w.currentPool(); p != nil {
			return p, nil
		}
	}
	if err := w.tryConnect(); err != nil {
		return nil, err
	}
	p := w.currentPool()
	if p == nil {
		return nil, fmt.Errorf("iotdb: not connected")
	}
	return p, nil
}

func (w *Writer) currentPool() sessionPool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.pool
}

// tryConnect 取得一条会话并执行建库(幂等),成功才置 connected。
// 会话池只创建一次;失败时保留池,交给重连循环复用/重建会话。
func (w *Writer) tryConnect() error {
	w.connectMu.Lock()
	defer w.connectMu.Unlock()
	if w.connected.Load() && w.currentPool() != nil {
		return nil // 并发调用:已由另一条路径建好
	}

	w.mu.Lock()
	p := w.pool
	if p == nil {
		p = w.newPool(w.cfg)
		w.pool = p
	}
	w.mu.Unlock()

	sess, err := p.Get()
	if err != nil {
		return fmt.Errorf("iotdb: connect failed: %w", err)
	}
	err = w.ensureDatabase(sess)
	p.Put(sess)
	if err != nil {
		return fmt.Errorf("iotdb: ensure database failed: %w", err)
	}
	w.connected.Store(true)
	return nil
}

// ==================== Sink:Writer + 断网缓存/补发 ====================

// iotdbSink 是带可靠投递的 IoTDB 入库端,实现 tsdb.Sink。
type iotdbSink struct {
	id          string
	name        string
	channelType string
	log         tsdb.Logger
	writer      *Writer

	ob     *delivery.Outbox
	recent *delivery.RecentRing
	// rowsCount 成功写出的累计记录行数(publishCount 是刷盘批次次数)。
	rowsCount atomic.Uint64

	done chan struct{}

	mu      sync.RWMutex
	running bool
}

// New 创建带断网缓存/补发的 IoTDB 入库端(未启动)。
// opts.Store 为 nil 时不启用断网缓存;配置里 spoolDisabled=true 时即使提供也忽略。
func New(cfg Config, opts tsdb.Options) tsdb.Sink {
	log := opts.Logger
	if log == nil {
		log = tsdb.NopLogger()
	}
	s := &iotdbSink{
		id:          opts.ID,
		name:        opts.Name,
		channelType: "iotdb-v2",
		log:         log,
		writer:      NewWriter(cfg, opts),
		recent:      delivery.NewRecentRing(delivery.RecentPushRingSize),
	}
	var store tsdb.SpoolStore
	if cfg.SpoolEnabled() {
		store = opts.Store
	}
	s.ob = delivery.NewOutbox(delivery.OutboxConfig{
		Tag:      "iotdb",
		ID:       opts.ID,
		Name:     opts.Name,
		Store:    store,
		SpoolCap: cfg.SpoolBatchCap(),
		Conn:     s,
		Logger:   log,
	})
	return s
}

// ID 返回持久化标识(实现 tsdb.Sink)。
func (s *iotdbSink) ID() string { return s.id }

// Connected 返回当前连通状态(实现 delivery.Connectivity)。
func (s *iotdbSink) Connected() bool { return s.writer.Connected() }

// Enqueue 非阻塞入队,绝不阻塞调用方(实现 tsdb.Sink)。
func (s *iotdbSink) Enqueue(b tsdb.Batch) { s.ob.Enqueue(b) }

// TestConnectivity 同步验证连接与建库。
func (s *iotdbSink) TestConnectivity() error { return s.writer.TestConnectivity() }

// Start 启动写入 goroutine(立即返回,连接在后台建立)。
func (s *iotdbSink) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.done = make(chan struct{})
	s.mu.Unlock()

	s.ob.Start()
	go s.run()
}

// Stop 停止并等待 worker 组退出、缓存排空、会话池关闭。
func (s *iotdbSink) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	// 顺序有约束:先触发停止,再等 worker 组退出(停服前还会把 outbox 中写失败的
	// 批次转投缓存),最后才等落盘协程排空 —— 否则在途批次会随进程退出而丢失。
	s.ob.Close()
	<-s.done
	s.ob.Wait()
	_ = s.writer.Close()
}

// run 启动 N 个写入 worker + 补发协程 + 重连协程,直至入库端停止。
func (s *iotdbSink) run() {
	defer close(s.done)

	w := &delivery.AggregateWriter{
		New:      s.writer.newAggregator,
		Write:    s.write,
		MaxRows:  s.writer.cfg.batchRows(),
		Interval: s.writer.cfg.batchInterval(),
		Observe:  s.observeWrite,
	}
	var wg sync.WaitGroup
	for i := 0; i < s.writer.cfg.batchWorkers(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Run(s.ob)
		}()
	}
	if s.ob.SpoolEnabled() {
		d := &delivery.GroupedDrainer{
			Tag:     "iotdb",
			Spool:   s.ob.Spool(),
			New:     s.writer.newAggregator,
			Write:   s.write,
			MaxRows: s.writer.cfg.batchRows(),
			Observe: s.observeWrite,
			Log:     s.log,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.ob.DrainLoop(d.Drain, drainPolicy)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.ob.ReconnectLoop(reconnectInterval, s.tryConnect)
	}()
	wg.Wait()
}

// tryConnect 建立连接 + 建库,成功则唤醒补发。
func (s *iotdbSink) tryConnect() {
	if err := s.writer.tryConnect(); err != nil {
		s.ob.SetLastErr(err.Error())
		s.log.Errorf("%v", err)
		return
	}
	s.ob.SetLastErr("")
	s.ob.Notify() // 重连后可能积压断连期间落盘的批次,即时唤醒补发
}

// write 下发一次聚合负载,返回是否成功(供攒批/补发判定缓存与毒批次)。
func (s *iotdbSink) write(payload string) bool {
	if payload == "" {
		return true // 无可写入行,视为成功
	}
	if !s.writer.Connected() {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if err := s.writer.WritePayload(ctx, payload); err != nil {
		errMsg := err.Error()
		s.log.Errorf("%s", errMsg)
		s.ob.SetLastErr(errMsg)
		return false
	}

	s.ob.MarkPublished(1)
	return true
}

// Status 返回入库端状态快照。
func (s *iotdbSink) Status() tsdb.Status {
	vo := tsdb.Status{
		Type:   s.channelType,
		Broker: s.writer.cfg.endpoint(),
		Topic:  s.writer.cfg.databasePath(),
	}
	s.ob.FillStatus(&vo)
	vo.RowsCount = s.rowsCount.Load()
	return vo
}

// observeWrite 记录一次写入(实时刷盘或补发分组),供管理端监控查看。
func (s *iotdbSink) observeWrite(o tsdb.WriteObservation) {
	if s.recent == nil {
		return
	}
	errMsg := ""
	if o.OK {
		s.rowsCount.Add(uint64(o.Rows))
	} else {
		errMsg = s.ob.LastErr()
		if errMsg == "" {
			errMsg = "write failed"
		}
	}
	s.recent.AddString(tsdb.RecentPushMessage{
		At:         time.Now(),
		Target:     s.writer.cfg.databasePath(),
		Records:    o.Rows,
		DurationMs: o.Duration.Milliseconds(),
		Error:      errMsg,
		FromDrain:  o.FromDrain,
	}, o.Payload)
}

// RecentMessages 实现 tsdb.RecentProvider:最近写入(新→旧)。
func (s *iotdbSink) RecentMessages(limit int) []tsdb.RecentPushMessage {
	if s.recent == nil {
		return []tsdb.RecentPushMessage{}
	}
	return s.recent.Snapshot(limit)
}

// ==================== delivery.Aggregator 适配 ====================

// Append 追加一个批次。IoTDB 的 insertRecords 没有语句级子表唯一约束,
// 同设备不同时间戳可出现在同一负载,故恒接受。
func (b *rowBuilder) Append(p tsdb.Batch) bool {
	b.appendBatch(p.CollectedAt, p.Records)
	return true
}

// Rows 当前负载已聚合的记录行数。
func (b *rowBuilder) Rows() int { return len(b.rows) }

// Empty 当前负载是否为空。
func (b *rowBuilder) Empty() bool { return b.empty() }

// Payload 输出 JSON 负载。
func (b *rowBuilder) Payload() string { return b.String() }

// 编译期保证同步 Writer 实现 tsdb.Writer 契约。
var _ tsdb.Writer = (*Writer)(nil)
