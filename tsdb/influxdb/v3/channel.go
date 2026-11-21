// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package influxdb

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ellisata/iot-gateway/tsdb"
	"github.com/Ellisata/iot-gateway/tsdb/delivery"
)

const (
	// reconnectInterval 断连重连尝试间隔(首连在 Start 后立即尝试,失败后按此间隔重试)
	reconnectInterval = 5 * time.Second
	// connectTimeout 单次连接/健康检查/建库超时
	connectTimeout = 5 * time.Second
	// writeTimeout 单次批量写入超时(批量 ≤5000 行,正常远小于此值)
	writeTimeout = 10 * time.Second
)

// Writer 同步写入 InfluxDB 3.x 的客户端。
//
// 只负责「健康检查 + 建库 + Line Protocol 构造 + HTTP 写入」：不排队、不重试、
// 不起后台协程。项目已有自己的队列/断网缓存时直接用它；需要可靠投递时用 Sink。
type Writer struct {
	cfg *Config
	log tsdb.Logger

	// connectMu 串行化建连尝试，避免重连协程与并发写入同时探测。
	connectMu sync.Mutex

	client    *http.Client
	connected atomic.Bool
	// lastStatus 最近一次写入的 HTTP 状态码；网络错误/未连接为 0。
	lastStatus atomic.Int32
}

// NewWriter 创建一个同步 InfluxDB 写入端（尚未连接，首次写入时惰性探测）。
func NewWriter(cfg Config, opts tsdb.Options) *Writer {
	log := opts.Logger
	if log == nil {
		log = tsdb.NopLogger()
	}
	return &Writer{cfg: &cfg, log: log, client: newHTTPClient(&cfg)}
}

// Connected 返回当前连接是否可用。
func (w *Writer) Connected() bool { return w.connected.Load() }

// LastStatus 返回最近一次写入的 HTTP 状态码。
func (w *Writer) LastStatus() int { return int(w.lastStatus.Load()) }

// Close 关闭空闲连接。
func (w *Writer) Close() error {
	w.client.CloseIdleConnections()
	w.connected.Store(false)
	return nil
}

// Write 将若干批次聚合成单个 Line Protocol body 同步写入。
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
			if !agg.Append(b) {
				break
			}
			n++
		}
		if n == 0 {
			return fmt.Errorf("influxdb: batch cannot be aggregated")
		}
		if err := w.WritePayload(ctx, agg.Payload()); err != nil {
			return err
		}
		batches = batches[n:]
	}
	return nil
}

// WritePayload 将已构造好的 Line Protocol body 写入 InfluxDB。
func (w *Writer) WritePayload(ctx context.Context, body string) error {
	if body == "" {
		return nil
	}
	// 每次写入先清空上次状态码：未连接/构建请求失败/网络错误都应显示为 0，
	// 不能让上一次的 2xx/4xx 留在观测记录里。
	w.lastStatus.Store(0)
	if err := w.ensure(ctx); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, w.cfg.writeURL(), strings.NewReader(body))
	if err != nil {
		w.connected.Store(false)
		return fmt.Errorf("influxdb: build write request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.Token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := w.client.Do(req.WithContext(ctx))
	if err != nil {
		w.connected.Store(false)
		return fmt.Errorf("influxdb: write failed: %w", err)
	}
	defer resp.Body.Close()
	w.lastStatus.Store(int32(resp.StatusCode))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// 非 2xx:读取响应体作为错误信息(可能含被拒行的详情)。
	// 4xx(除 429 限流)为请求级错误(数据非法/权限/库缺失),连接本身健康:
	// 置 disconnected 会触发重连抖动,且让实时写入整窗转道 spool 后仍被毒批次机制丢弃;
	// 坏数据直接交由补发协程的毒批次窄化机制隔离即可。429 限流与 5xx 才视为服务端过载/故障,
	// 置断开交重连循环退避。
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		w.connected.Store(false)
	}
	return fmt.Errorf("influxdb: write status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

// probeTCP 在 HTTP 健康检查前先做一次带超时的 TCP 可达性检查。
//
// net/http 的 DialContext 本身服从 ctx，不会像 taosWS 那样拖到系统 TCP 超时；
// 但 ctx 先到期时 url.Error 只保留 "context deadline exceeded"，真正的
// "dial tcp ...: i/o timeout" 被盖掉，用户看不出是地址/端口不可达。预检让
// 不可达目标在 5s 内返回带地址的错误；握手与鉴权仍由 GET /health 完成。
func (w *Writer) probeTCP(ctx context.Context) error {
	// Transport 走 http.ProxyFromEnvironment：配置代理时真正连接由代理转发，
	// 直连预检会把「代理可达、目标不可直连」的正常部署误判为故障，跳过。
	if u, err := url.Parse(w.cfg.baseURL()); err == nil {
		if proxy, perr := http.ProxyFromEnvironment(&http.Request{URL: u}); perr == nil && proxy != nil {
			return nil
		}
	}

	addr := net.JoinHostPort(w.cfg.Host, w.cfg.port())
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	return conn.Close()
}

// TestConnectivity 同步探测连通性（GET /health 带 token），不建库、不启动后台协程。
func (w *Writer) TestConnectivity() error {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if err := w.probeTCP(ctx); err != nil {
		return fmt.Errorf("influxdb: connect failed: %w", err)
	}
	return w.health(ctx)
}

// newAggregator 为每个新负载创建 Line Protocol 构造器。
func (w *Writer) newAggregator() delivery.Aggregator {
	return newLineBuilder(w.cfg.Measurement)
}

// ensure 返回可用连接；未连接时惰性探测一次。
func (w *Writer) ensure(ctx context.Context) error {
	if w.connected.Load() {
		return nil
	}
	return w.tryConnect(ctx)
}

// tryConnect 探测连通性（GET /health，200 视为连通），成功后 best-effort 建库。
func (w *Writer) tryConnect(ctx context.Context) error {
	w.connectMu.Lock()
	defer w.connectMu.Unlock()
	if w.connected.Load() {
		return nil // 并发调用：已由另一条路径探测成功
	}

	healthCtx := ctx
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		healthCtx, cancel = context.WithTimeout(ctx, connectTimeout)
		defer cancel()
	}
	if err := w.probeTCP(healthCtx); err != nil {
		return fmt.Errorf("influxdb: connect failed: %w", err)
	}
	if err := w.health(healthCtx); err != nil {
		return err
	}
	w.ensureDatabase(healthCtx)
	w.connected.Store(true)
	return nil
}

// health 执行 GET /health，200 视为连通。
func (w *Writer) health(ctx context.Context) error {
	req, err := http.NewRequest(http.MethodGet, w.cfg.healthURL(), nil)
	if err != nil {
		return fmt.Errorf("influxdb: build health request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.Token)

	resp, err := w.client.Do(req.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("influxdb: connect failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("influxdb: health check status %d", resp.StatusCode)
	}
	return nil
}

// ensureDatabase best-effort 创建目标数据库(POST /api/v3/configure/database)。
// 幂等:409 已存在视为成功;401/403 非 admin 令牌无法建库,视为已预置库放行
// (若库确实缺失,写入会失败并由毒批次机制兜底)。任何失败均不阻塞连接置位。
func (w *Writer) ensureDatabase(ctx context.Context) {
	body := fmt.Sprintf(`{"db":%q}`, w.cfg.Database)
	req, err := http.NewRequest(http.MethodPost, w.cfg.configureDatabaseURL(), strings.NewReader(body))
	if err != nil {
		w.log.Errorf("influxdb: build ensure-database request failed: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	dbCtx := ctx
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		dbCtx, cancel = context.WithTimeout(ctx, connectTimeout)
		defer cancel()
	}
	resp, err := w.client.Do(req.WithContext(dbCtx))
	if err != nil {
		w.log.Errorf("influxdb: ensure database %q failed: %v", w.cfg.Database, err)
		return
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		w.log.Infof("influxdb: database %q created", w.cfg.Database)
	case http.StatusConflict:
		w.log.Infof("influxdb: database %q already exists", w.cfg.Database)
	case http.StatusUnauthorized, http.StatusForbidden:
		w.log.Warnf("influxdb: ensure database %q denied (token may be non-admin), assuming pre-provisioned", w.cfg.Database)
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		w.log.Warnf("influxdb: ensure database %q unexpected status %d: %s", w.cfg.Database, resp.StatusCode, strings.TrimSpace(string(b)))
	}
}

// newHTTPClient 构建 InfluxDB HTTP 客户端。
// 不设全局超时,各请求用独立的 context 超时(connectTimeout / writeTimeout)。
// InsecureSkipVerify 时自建 Transport 跳过 TLS 证书校验(自签证书测试环境)。
// 默认 Transport 的 MaxIdleConnsPerHost=2,并发写入 worker 数超过 2 时连接无法复用,
// 每个请求都新建 TCP/TLS 连接成为吞吐瓶颈,按 batchWorkers 提升空闲连接上限。
func newHTTPClient(cfg *Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = cfg.batchWorkers()
	if cfg.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 用户显式配置
	}
	return &http.Client{Transport: transport}
}

// ==================== Sink：Writer + 断网缓存/补发 ====================

// influxdbSink 是带可靠投递的 InfluxDB 入库端，实现 tsdb.Sink。
type influxdbSink struct {
	id          string
	name        string
	channelType string
	log         tsdb.Logger
	writer      *Writer

	ob     *delivery.Outbox
	recent *delivery.RecentRing
	// rowsCount 成功写出的累计记录行数（publishCount 是刷盘批次次数）。
	rowsCount atomic.Uint64

	done chan struct{}

	mu      sync.Mutex
	running bool
}

// New 创建带断网缓存/补发的 InfluxDB 入库端（未启动）。
// opts.Store 为 nil 时不启用断网缓存；配置里 spoolDisabled=true 时即使提供也忽略。
func New(cfg Config, opts tsdb.Options) tsdb.Sink {
	log := opts.Logger
	if log == nil {
		log = tsdb.NopLogger()
	}
	s := &influxdbSink{
		id:          opts.ID,
		name:        opts.Name,
		channelType: "influxdb-v3",
		log:         log,
		writer:      NewWriter(cfg, opts),
		recent:      delivery.NewRecentRing(delivery.RecentPushRingSize),
	}
	var store tsdb.SpoolStore
	if cfg.SpoolEnabled() {
		store = opts.Store
	}
	s.ob = delivery.NewOutbox(delivery.OutboxConfig{
		Tag:      "influxdb",
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
func (s *influxdbSink) ID() string { return s.id }

// Connected 返回当前连通状态(实现 delivery.Connectivity)。
func (s *influxdbSink) Connected() bool { return s.writer.Connected() }

// Enqueue 非阻塞入队,绝不阻塞调用方(实现 tsdb.Sink)。
func (s *influxdbSink) Enqueue(b tsdb.Batch) { s.ob.Enqueue(b) }

// TestConnectivity 同步探测连通性。
func (s *influxdbSink) TestConnectivity() error { return s.writer.TestConnectivity() }

// Start 启动写入 goroutine(立即返回,连接在后台建立)。
func (s *influxdbSink) Start() {
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

// Stop 停止并等待 worker 组退出、缓存排空、空闲连接关闭。
func (s *influxdbSink) Stop() {
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
func (s *influxdbSink) run() {
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
			Tag:     "influxdb",
			Spool:   s.ob.Spool(),
			New:     s.writer.newAggregator,
			Write:   s.write,
			MaxRows: s.writer.cfg.batchRows(),
			Observe: s.observeWrite,
			Log:     s.log,
		}
		// 写入被拒(4xx)属数据类错误,判毒避免队头永久阻塞;窄化把坏点从组里
		// 隔离出来,避免连坐误删同组好数据。
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.ob.DrainLoop(d.Drain, delivery.DrainPolicy{Poison: true, Narrow: true})
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.ob.ReconnectLoop(reconnectInterval, s.tryConnect)
	}()
	wg.Wait()
}

// tryConnect 探测连通性，成功后唤醒补发。
func (s *influxdbSink) tryConnect() {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if err := s.writer.tryConnect(ctx); err != nil {
		s.ob.SetLastErr(err.Error())
		s.log.Errorf("%v", err)
		return
	}
	s.ob.SetLastErr("")
	s.ob.Notify() // 重连后可能积压断连期间落盘的批次,即时唤醒补发
}

// write 下发一次聚合负载，返回是否成功。
func (s *influxdbSink) write(body string) bool {
	if body == "" {
		return true // 无可写入行,视为成功
	}
	if !s.writer.Connected() {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if err := s.writer.WritePayload(ctx, body); err != nil {
		errMsg := err.Error()
		s.log.Errorf("%s", errMsg)
		s.ob.SetLastErr(errMsg)
		return false
	}

	s.ob.MarkPublished(1)
	return true
}

// Status 返回入库端状态快照。
func (s *influxdbSink) Status() tsdb.Status {
	vo := tsdb.Status{
		Type:   s.channelType,
		Broker: s.writer.cfg.baseURL(),
		Topic:  s.writer.cfg.Database + "/" + s.writer.cfg.Measurement,
	}
	s.ob.FillStatus(&vo)
	vo.RowsCount = s.rowsCount.Load()
	return vo
}

// observeWrite 记录一次写入（实时刷盘或补发分组），供管理端监控查看。
func (s *influxdbSink) observeWrite(o tsdb.WriteObservation) {
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
		Target:     s.writer.cfg.Database + "/" + s.writer.cfg.Measurement,
		Records:    o.Rows,
		StatusCode: s.writer.LastStatus(),
		DurationMs: o.Duration.Milliseconds(),
		Error:      errMsg,
		FromDrain:  o.FromDrain,
	}, o.Payload)
}

// RecentMessages 实现 tsdb.RecentProvider：最近写入（新→旧）。
func (s *influxdbSink) RecentMessages(limit int) []tsdb.RecentPushMessage {
	if s.recent == nil {
		return []tsdb.RecentPushMessage{}
	}
	return s.recent.Snapshot(limit)
}

// ==================== delivery.Aggregator 适配 ====================

// Append 追加一个批次。lineBuilder 无「批间冲突」概念(不同采集时间即不同 point),
// 故恒返回 true,永不由聚合侧切断负载。
func (b *lineBuilder) Append(p tsdb.Batch) bool {
	b.appendBatch(p.CollectedAt, p.Records)
	return true
}

// Rows 当前负载已聚合的 point 行数。
func (b *lineBuilder) Rows() int { return b.points }

// Empty 当前负载是否为空。
func (b *lineBuilder) Empty() bool { return b.empty() }

// Payload 输出 Line Protocol 负载。
func (b *lineBuilder) Payload() string { return b.String() }

// 编译期保证同步 Writer 实现 tsdb.Writer 契约。
var _ tsdb.Writer = (*Writer)(nil)
