// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tdengine

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	// 空导入:注册 database/sql 驱动名 "taosWS"(纯 Go WebSocket 驱动,免 CGO)。
	// sql.Open("taosWS", dsn) 依赖本导入触发的 init 注册。
	_ "github.com/taosdata/driver-go/v3/taosWS"

	"github.com/Ellisata/iot-gateway/tsdb"
	"github.com/Ellisata/iot-gateway/tsdb/delivery"
)

const (
	// reconnectInterval 断连重连尝试间隔(首连在 Start 后立即尝试,失败后按此间隔重试)
	reconnectInterval = 5 * time.Second
	// connectTimeout 单次连接/建库建表/校验超时
	connectTimeout = 5 * time.Second
	// writeTimeout 单次批量写入超时(批量 ≤5000 行,正常远小于此值)
	writeTimeout = 10 * time.Second
	// connMaxLifetime 连接最大存活时间:定期刷新底层 WebSocket,避免长连接残旧
	connMaxLifetime = 10 * time.Minute
)

// drainPolicy 补发失败的处理策略。
//
// TDengine 的补发窗口按 batchRows 聚合成一条多子表 INSERT,某组写失败时 failID
// 是组头,而坏数据可能不在组头 —— 只按组头判毒会每轮删掉一个好批次,直到坏批
// 浮到队头,连坐丢数据。Narrow 在首次失败后强制逐批试写,先把真正失败的批次
// 隔离出来再判毒,与 influxdb 通道保持一致。
var drainPolicy = delivery.DrainPolicy{Poison: true, Narrow: true}

// Writer 同步写入 TDengine 的客户端。
//
// 它只负责「连接 + 建库建表 + SQL 构造 + 写入」：不排队、不重试、不起后台
// 协程。项目已有自己的队列/断网缓存时直接用它；需要可靠投递时用 Sink
// （Writer + delivery.Outbox）。
type Writer struct {
	cfg *Config
	log tsdb.Logger

	// connectMu 串行化建连尝试，避免重连协程与并发写入同时打开多条连接。
	connectMu sync.Mutex

	mu        sync.RWMutex
	db        *sql.DB
	connected atomic.Bool
}

// NewWriter 创建一个同步 TDengine 写入端（尚未连接，首次写入时惰性建连）。
func NewWriter(cfg Config, opts tsdb.Options) *Writer {
	log := opts.Logger
	if log == nil {
		log = tsdb.NopLogger()
	}
	return &Writer{cfg: &cfg, log: log}
}

// Connected 返回当前连接是否可用。
func (w *Writer) Connected() bool { return w.connected.Load() }

// Close 关闭底层连接。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.db != nil {
		err := w.db.Close()
		w.db = nil
		w.connected.Store(false)
		return err
	}
	w.connected.Store(false)
	return nil
}

// Write 将若干批次聚合后同步写入 TDengine。
//
// 行数达到配置的 batchRows 时切分负载；同一子表在非空负载中再次出现时先刷出
// 当前负载，保证跨采集轮次的数据不会被批内去重丢弃。ctx 未带 deadline 时
// 使用 writeTimeout 兜底。
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
			return fmt.Errorf("tdengine: batch cannot be aggregated")
		}
		if err := w.WritePayload(ctx, agg.Payload()); err != nil {
			return err
		}
		batches = batches[n:]
	}
	return nil
}

// WritePayload 写入一条已构造好的 INSERT 语句（可为多子表聚合）。
func (w *Writer) WritePayload(ctx context.Context, stmt string) error {
	if stmt == "" {
		return nil
	}
	db, err := w.ensure(ctx)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		w.connected.Store(false)
		return fmt.Errorf("tdengine: exec failed: %w", err)
	}
	return nil
}

// probeTCP 在交给驱动之前先做一次带超时的 TCP 可达性检查。
//
// taosWS 驱动的 Connect/Ping 忽略 ctx（驱动源码里 `_ = ctx`），connectTimeout
// 对真正的建连根本不生效：地址不可达时会一直等到操作系统 TCP 超时（Windows
// 约 21s），随后 database/sql 的内部重试看到已过期的 ctx，返回的却是误导性的
// "context deadline exceeded"，把真正的网络错误盖掉了。预检只确认端口可达，
// 握手/鉴权/建库建表仍由驱动完成 —— 它不替代 Ping。
func (w *Writer) probeTCP(ctx context.Context) error {
	addr := net.JoinHostPort(w.cfg.Host, w.cfg.port())

	// 驱动建连走的是 gorilla Dialer,其 Proxy 为 http.ProxyFromEnvironment:
	// 配了 HTTP(S)_PROXY 时真正连接由代理转发,直连预检会把「代理可达、目标
	// 不可直连」的正常部署误判为故障。这种情况跳过预检,交回驱动判断。
	// 注意 gorilla 在查代理前已把 ws/wss 改写成 http/https,这里必须同样改写,
	// 否则 ProxyFromEnvironment 遇到 ws 直接返回 nil,守卫形同虚设。
	proxyScheme := "http"
	if w.cfg.UseSSL {
		proxyScheme = "https"
	}
	if endpointURL, err := url.Parse(proxyScheme + "://" + addr); err == nil {
		if proxy, perr := http.ProxyFromEnvironment(&http.Request{URL: endpointURL}); perr == nil && proxy != nil {
			return nil
		}
	}

	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	return conn.Close()
}

// TestConnectivity 同步验证连接与建库建表（与 tryConnect 一致），成功后关闭连接。
func (w *Writer) TestConnectivity() error {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if err := w.probeTCP(ctx); err != nil {
		return fmt.Errorf("tdengine: connect failed: %w", err)
	}

	db, err := sql.Open("taosWS", w.cfg.dsn())
	if err != nil {
		return fmt.Errorf("tdengine: open: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("tdengine: connect failed: %w", err)
	}
	if err := w.ensureSchema(ctx, db); err != nil {
		return fmt.Errorf("tdengine: ensure schema failed: %w", err)
	}
	return nil
}

// newAggregator 为每个新负载创建 SQL 构造器，供 Sink 的攒批/补发复用。
func (w *Writer) newAggregator() delivery.Aggregator {
	return newInsertBuilder(w.cfg.safeDB, w.cfg.safeStable)
}

// ensure 返回可用连接；未连接时惰性建连一次。
func (w *Writer) ensure(ctx context.Context) (*sql.DB, error) {
	if w.connected.Load() {
		if db := w.currentDB(); db != nil {
			return db, nil
		}
	}
	if err := w.tryConnect(ctx); err != nil {
		return nil, err
	}
	db := w.currentDB()
	if db == nil {
		return nil, fmt.Errorf("tdengine: not connected")
	}
	return db, nil
}

func (w *Writer) currentDB() *sql.DB {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.db
}

// tryConnect 建立连接 + 建库建表(均幂等),成功则置 connected。
func (w *Writer) tryConnect(ctx context.Context) error {
	w.connectMu.Lock()
	defer w.connectMu.Unlock()
	if w.connected.Load() && w.currentDB() != nil {
		return nil // 并发调用：已由另一条路径建好连接
	}

	db, err := sql.Open("taosWS", w.cfg.dsn())
	if err != nil {
		return fmt.Errorf("tdengine: open: %w", err)
	}
	db.SetMaxOpenConns(w.cfg.batchWorkers())
	db.SetMaxIdleConns(w.cfg.batchWorkers())
	db.SetConnMaxLifetime(connMaxLifetime)

	pingCtx := ctx
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		pingCtx, cancel = context.WithTimeout(ctx, connectTimeout)
		defer cancel()
	}
	if err := w.probeTCP(pingCtx); err != nil {
		db.Close()
		return fmt.Errorf("tdengine: connect failed: %w", err)
	}
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return fmt.Errorf("tdengine: connect failed: %w", err)
	}
	if err := w.ensureSchema(pingCtx, db); err != nil {
		db.Close()
		return fmt.Errorf("tdengine: ensure schema failed: %w", err)
	}

	w.mu.Lock()
	if w.db != nil {
		w.db.Close()
	}
	w.db = db
	w.mu.Unlock()

	w.connected.Store(true)
	return nil
}

// ensureSchema 建库建表(IF NOT EXISTS,幂等),仅建成功才视为连接就绪。
// 建完再做一次**表结构漂移探测**(见 warnSchemaDrift)。
func (w *Writer) ensureSchema(ctx context.Context, db *sql.DB) error {
	for _, stmt := range w.cfg.ensureSchemaSQL() {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt, err)
		}
	}
	w.warnSchemaDrift(ctx, db)
	return nil
}

// warnSchemaDrift 探测已部署实例的超级表结构与本版是否一致:
//   - 还留着 quality_cause 列(本版已删,成因并进 quality);
//   - 缺少 thing_model_code / property_code 两个 tag(本版新增)。
//
// 两种情况的共同后果是写入侧的位置式 INSERT 列数/tag 数对不上,TDengine 会拒收
// 该语句;而 SQL 报错在 delivery 的补发路径里会被判为**毒批次**(删除并记 dropped),
// 所以忘记迁移的症状不是响亮停摆,而是**静默、永久的数据丢失**。
// 建表语句是 CREATE STABLE IF NOT EXISTS,表已存在就整个跳过 —— 新版**不会**替
// 运维删列/加 tag。一条启动期就能看见的日志是这条路上唯一廉价的护栏。
//
// 探测本身**绝不阻断建连**:查询失败只记 WARN —— 与 ensureSchemaSQL "只建不改、
// 绝不让通道起不来"是同一条理由(它每轮重连都会跑)。
func (w *Writer) warnSchemaDrift(ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx, "DESCRIBE "+w.cfg.safeDB+"."+w.cfg.safeStable)
	if err != nil {
		w.log.Warnf("tdengine: 表结构探测失败(不影响采集与写入): %v", err)
		return
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		w.log.Warnf("tdengine: 表结构探测读取列名失败(不影响采集与写入): %v", err)
		return
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	var table [][]any
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			w.log.Warnf("tdengine: 表结构探测读取行失败(不影响采集与写入): %v", err)
			return
		}
		row := make([]any, len(vals))
		copy(row, vals)
		table = append(table, row)
	}

	if hasQualityCauseColumn(cols, table) {
		w.log.Errorf("tdengine: 超级表 %s.%s 仍带着 quality_cause 列,而本版已不再写入它 —— "+
			"位置式 INSERT 会因列数不符被拒,且该错误被判为毒批次后**静默丢弃数据**。"+
			"请立即执行一次: ALTER STABLE %s.%s DROP COLUMN quality_cause;",
			w.cfg.safeDB, w.cfg.safeStable, w.cfg.safeDB, w.cfg.safeStable)
	}
	if missing := missingSchemaTags(cols, table); len(missing) > 0 {
		w.log.Errorf("tdengine: 超级表 %s.%s 缺少 tag %v —— 写入侧的 TAGS(...) 会因 tag 数不符被拒,"+
			"该错误被判为毒批次后**静默丢弃数据**。"+
			"请立即执行: ALTER STABLE %s.%s ADD TAG thing_model_code NCHAR(64); "+
			"ALTER STABLE %s.%s ADD TAG property_code NCHAR(64); "+
			"(已有子表的新 tag 为 NULL,新写入不会回填,需要时按子表 ALTER TABLE ... SET TAG ...)",
			w.cfg.safeDB, w.cfg.safeStable, missing,
			w.cfg.safeDB, w.cfg.safeStable, w.cfg.safeDB, w.cfg.safeStable)
	}
}

// schemaCodeTags 本版新增、写入侧 TAGS(...) 必需的 tag 名。
// 前两个(device_id / device_address_id)是历史 tag,任何既有超级表都有,不在此列。
var schemaCodeTags = []string{"thing_model_code", "property_code"}

// hasQualityCauseColumn 报告 DESCRIBE 的结果里有没有 quality_cause 这个列(字段)。
//
// **按列名找 Field 那一列,不认下标**:DESCRIBE 的列形状因 TDengine 版本而异
// (3.x 是 Field/Type/Length/Note,更早的版本列名与列数都不同),认下标就会在
// 某个版本上读到别的列、把探测变成一句恒真的空话或恒假的哑话。
// 认不出形状(没有 Field 列)时返回 false 而不是报错 —— 这只是启动期的一句提示,
// 不是判据,拿不准就放过。
func hasQualityCauseColumn(cols []string, table [][]any) bool {
	names := describeFieldNames(cols, table)
	if names == nil {
		return false
	}
	_, ok := names["quality_cause"]
	return ok
}

// missingSchemaTags 返回本版必需、但 DESCRIBE 结果里找不到的 tag 名(按 schemaCodeTags 顺序)。
// 认不出 DESCRIBE 形状时返回 nil(拿不准就放过,理由同 hasQualityCauseColumn)。
func missingSchemaTags(cols []string, table [][]any) []string {
	names := describeFieldNames(cols, table)
	if names == nil {
		return nil
	}
	var missing []string
	for _, tag := range schemaCodeTags {
		if _, ok := names[tag]; !ok {
			missing = append(missing, tag)
		}
	}
	return missing
}

// describeFieldNames 把 DESCRIBE 结果的 Field 列收集成小写名字集合。
// 返回 nil 表示认不出形状(没有 Field 列)。
func describeFieldNames(cols []string, table [][]any) map[string]struct{} {
	fieldIdx := -1
	for i, c := range cols {
		if strings.EqualFold(c, "field") {
			fieldIdx = i
			break
		}
	}
	if fieldIdx < 0 {
		return nil
	}
	names := make(map[string]struct{}, len(table))
	for _, row := range table {
		if fieldIdx >= len(row) {
			continue
		}
		if name, ok := row[fieldIdx].(string); ok {
			names[strings.ToLower(name)] = struct{}{}
		}
	}
	return names
}

// ==================== Sink：Writer + 断网缓存/补发 ====================

// tdengineSink 是带可靠投递的 TDengine 入库端，实现 tsdb.Sink。
//
// 队列、断网缓存、攒批写入与补发由 delivery 提供；本类型只负责把负载交给
// Writer，并把写入结果映射为通道状态与最近写入观测。
type tdengineSink struct {
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

	mu      sync.RWMutex
	running bool
}

// New 创建带断网缓存/补发的 TDengine 入库端（未启动）。
// opts.Store 为 nil 时不启用断网缓存；配置里 spoolDisabled=true 时即使提供也忽略。
func New(cfg Config, opts tsdb.Options) tsdb.Sink {
	log := opts.Logger
	if log == nil {
		log = tsdb.NopLogger()
	}
	s := &tdengineSink{
		id:          opts.ID,
		name:        opts.Name,
		channelType: "tdengine-v3",
		log:         log,
		writer:      NewWriter(cfg, opts),
		recent:      delivery.NewRecentRing(delivery.RecentPushRingSize),
	}
	var store tsdb.SpoolStore
	if cfg.SpoolEnabled() {
		store = opts.Store
	}
	s.ob = delivery.NewOutbox(delivery.OutboxConfig{
		Tag:      "tdengine",
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
func (s *tdengineSink) ID() string { return s.id }

// Connected 返回当前连通状态(实现 delivery.Connectivity)。
func (s *tdengineSink) Connected() bool { return s.writer.Connected() }

// Enqueue 非阻塞入队,绝不阻塞调用方(实现 tsdb.Sink)。
func (s *tdengineSink) Enqueue(b tsdb.Batch) { s.ob.Enqueue(b) }

// TestConnectivity 同步验证连接与建库建表。
func (s *tdengineSink) TestConnectivity() error { return s.writer.TestConnectivity() }

// Start 启动写入 goroutine(立即返回,连接在后台建立)。
func (s *tdengineSink) Start() {
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

// Stop 停止并等待 worker 组退出、缓存排空、连接关闭。
func (s *tdengineSink) Stop() {
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
func (s *tdengineSink) run() {
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
			Tag:     "tdengine",
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

// tryConnect 建立连接 + 建库建表，成功则唤醒补发。
func (s *tdengineSink) tryConnect() {
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

// write 下发一次聚合负载，返回是否成功（供攒批/补发判定缓存与毒批次）。
func (s *tdengineSink) write(stmt string) bool {
	if stmt == "" {
		return true // 无可写入行,视为成功
	}
	if !s.writer.Connected() {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if err := s.writer.WritePayload(ctx, stmt); err != nil {
		// 断连/错误:标记断开,交给 reconnect loop 恢复;丢弃统计由调用方按结果语义处理
		errMsg := err.Error()
		s.log.Errorf("%s", errMsg)
		s.ob.SetLastErr(errMsg)
		return false
	}

	s.ob.MarkPublished(1)
	return true
}

// Status 返回入库端状态快照。
func (s *tdengineSink) Status() tsdb.Status {
	vo := tsdb.Status{
		Type:   s.channelType,
		Broker: s.writer.cfg.endpoint(),
		Topic:  s.writer.cfg.Database,
	}
	s.ob.FillStatus(&vo)
	vo.RowsCount = s.rowsCount.Load()
	return vo
}

// observeWrite 记录一次写入（实时刷盘或补发分组），供管理端监控查看。
func (s *tdengineSink) observeWrite(o tsdb.WriteObservation) {
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
		Target:     s.writer.cfg.Database,
		Records:    o.Rows,
		DurationMs: o.Duration.Milliseconds(),
		Error:      errMsg,
		FromDrain:  o.FromDrain,
	}, o.Payload)
}

// RecentMessages 实现 tsdb.RecentProvider：最近写入（新→旧）。
func (s *tdengineSink) RecentMessages(limit int) []tsdb.RecentPushMessage {
	if s.recent == nil {
		return []tsdb.RecentPushMessage{}
	}
	return s.recent.Snapshot(limit)
}

// ==================== delivery.Aggregator 适配 ====================
// insertBuilder 实现 delivery.Aggregator,使攒批与补发走同一套分组/切分算法
// (与 influxdb 的 lineBuilder 共享)。

// Append 追加一个批次,返回是否与当前语句兼容
// (子表冲突时返回 false,由 GroupedDrainer 先刷出本窗再追加)。
func (b *insertBuilder) Append(p tsdb.Batch) bool {
	return b.appendBatch(p.CollectedAt, p.Records)
}

// Rows 当前语句已聚合的记录行数。
func (b *insertBuilder) Rows() int { return b.rows }

// Empty 当前语句是否为空。
func (b *insertBuilder) Empty() bool { return b.empty() }

// Payload 输出 INSERT 语句。
func (b *insertBuilder) Payload() string { return b.String() }

// 编译期保证同步 Writer 实现 tsdb.Writer 契约。
var _ tsdb.Writer = (*Writer)(nil)
