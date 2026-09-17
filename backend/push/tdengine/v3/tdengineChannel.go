// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package v3

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	// 空导入:注册 database/sql 驱动名 "taosWS"(纯 Go WebSocket 驱动,免 CGO)。
	// sql.Open("taosWS", dsn) 依赖本导入触发的 init 注册。
	_ "github.com/taosdata/driver-go/v3/taosWS"
	"gorm.io/gorm"

	"iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/push"
)

// init 向 push 包注册 tdengine 通道工厂。
// 依赖入口处(main.go)对本包的空导入触发,Engine 的 buildChannels 自动发现。
func init() {
	push.Register("tdengine-v3", func(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
		return newTdengineChannel(db, ch)
	})
}

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
// 与 influxdb 通道的差异只在 Narrow:TDengine 的补发窗口按 batchRows 聚合成一条
// 多子表 INSERT,某组写失败时 failID 是组头,而坏数据可能不在组头 —— 按组头判毒
// 会连坐删掉组内的好批次。打开 Narrow 可先逐批试写定位真正失败的批次再判毒。
// 此项保持与本次重构前一致(false),需要时改此一处即可。
var drainPolicy = push.DrainPolicy{Poison: true, Narrow: false}

// tdengineChannel 单个 TDengine 3.x 推送通道运行时,实现 push.Channel。
//
// 架构与 mqttChannel 一致:
//   - 采集线程经 push.Engine 非阻塞入队(outbox 满时丢弃最旧);
//   - N 个写入 worker 消费 outbox,将多个批次聚合为单条多子表 INSERT 执行
//     (按 batchRows 行数与 batchInterval 时间窗口双触发刷出,减少每设备每轮的
//     独立往返;聚合窗内子表冲突时提前刷出,不跨轮去重);
//   - TDengine 不可达时,失败批次(及断连期间直接入队的批次)落入 SQLite 本地缓存
//     (push_outbox,断网缓存),重连后按入队序补发、写成功删除 —— 网络中断期间数据不丢;
//   - 底层 taosWS 驱动自带 autoReconnect;通道层 reconnect loop 负责连通性探测与
//     建库建表(幂等),并在恢复时唤醒补发。
//
// 连接策略:DSN 不携带数据库,所有表名使用 {db}.{table} 全限定写法,规避 database/sql
// 连接池中 USE 只对单条连接生效的问题(见 tdengineConfig.dsn)。
//
// 队列、断网缓存、补发与状态统计由 push.Outbox 统一提供(见 push/outbox.go),
// 本通道只负责负载构造(insertBuilder)与下发(ch.write)。
type tdengineChannel struct {
	id   string
	name string
	cfg  *tdengineConfig
	sig  string // 配置签名,用于热加载判断通道配置是否变化

	ob *push.Outbox

	db        *sql.DB
	connected atomic.Bool

	// done 由 run 关闭:worker 组(写入/补发/重连)全部退出。
	done chan struct{}

	mu      sync.RWMutex
	running bool
}

// ChannelID 返回通道持久化 ID(实现 push.Channel)
func (ch *tdengineChannel) ChannelID() string { return ch.id }

// ConfigSig 返回配置签名(实现 push.Channel)
func (ch *tdengineChannel) ConfigSig() string { return ch.sig }

// Connected 返回当前连通状态(实现 push.Connectivity,供 Outbox 决定入队路由)
func (ch *tdengineChannel) Connected() bool { return ch.connected.Load() }

// Enqueue 非阻塞入队,绝不阻塞采集线程(实现 push.Channel)。
// 入队路由(直投内存队列 / 落盘 / 丢最旧)由 push.Outbox 统一决定。
func (ch *tdengineChannel) Enqueue(b push.PushBatch) { ch.ob.Enqueue(b) }

// newTdengineChannel 根据推送通道配置创建通道运行时(未启动)。
// db(SQLite)用于构建断网本地缓存(spoolEnabled 时)。
func newTdengineChannel(db *gorm.DB, ch *po.PushChannel) (*tdengineChannel, error) {
	cfg, err := parseConfig(ch.ConfigJSON)
	if err != nil {
		return nil, err
	}
	tch := &tdengineChannel{
		id:   ch.ID,
		name: ch.Name,
		cfg:  cfg,
		sig:  ch.ID + "|" + ch.Name + "|" + ch.ConfigJSON,
	}
	tch.ob = push.NewOutbox(push.OutboxConfig{
		Tag:          "tdengine",
		ID:           ch.ID,
		Name:         ch.Name,
		DB:           db,
		SpoolEnabled: cfg.spoolEnabled(),
		SpoolCap:     cfg.spoolBatchCap(),
		Conn:         tch,
	})
	return tch, nil
}

// TestConnectivity 同步验证连接与建库建表（与通道启动逻辑 tryConnect 一致），
// 成功后关闭连接，不启动通道后台 goroutine。
// 用于管理界面保存配置前的连通性校验，可确认端点可达、鉴权通过且具备建库建表权限。
func (ch *tdengineChannel) TestConnectivity() error {
	db, err := sql.Open("taosWS", ch.cfg.dsn())
	if err != nil {
		return fmt.Errorf("tdengine: open: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("tdengine: connect failed: %w", err)
	}
	if err := ch.ensureSchema(db); err != nil {
		return fmt.Errorf("tdengine: ensure schema failed: %w", err)
	}
	return nil
}

// Start 启动通道的写入 goroutine(立即返回,连接在后台建立)
func (ch *tdengineChannel) Start() {
	ch.mu.Lock()
	if ch.running {
		ch.mu.Unlock()
		return
	}
	ch.running = true
	ch.done = make(chan struct{})
	ch.mu.Unlock()

	ch.ob.Start()
	go ch.run()
}

// Stop 停止通道,等待写入 goroutine 退出
func (ch *tdengineChannel) Stop() {
	ch.mu.Lock()
	if !ch.running {
		ch.mu.Unlock()
		return
	}
	ch.running = false
	ch.mu.Unlock()

	// 顺序有约束:先触发停止,再等 worker 组退出(停服前还会把 outbox 中写失败的
	// 批次转投缓存),最后才等落盘协程排空 —— 否则在途批次会随进程退出而丢失。
	ch.ob.Close()
	<-ch.done
	ch.ob.Wait()

	ch.mu.Lock()
	if ch.db != nil {
		ch.db.Close()
		ch.db = nil
	}
	ch.mu.Unlock()
	ch.connected.Store(false)
}

// run 通道主循环:启动 N 个写入 worker + 补发协程 + 重连协程,直至通道停止
func (ch *tdengineChannel) run() {
	defer close(ch.done)

	// 写入 worker:N 个协程各自攒批,把多个批次聚合为单条多子表 INSERT 批量写入
	// (行数达 batchRows 或时间到 batchInterval 双触发刷出;聚合窗内子表冲突时提前
	// 刷出,不跨轮去重;写入失败时整窗转本地缓存待重连补发)。
	w := &push.AggregateWriter{
		New:      func() push.Aggregator { return newInsertBuilder(ch.cfg.safeDB, ch.cfg.safeStable) },
		Write:    ch.write,
		MaxRows:  ch.cfg.batchRows(),
		Interval: ch.cfg.batchInterval(),
	}
	var wg sync.WaitGroup
	for i := 0; i < ch.cfg.batchWorkers(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Run(ch.ob)
		}()
	}
	if ch.ob.SpoolEnabled() {
		d := &push.GroupedDrainer{
			Tag:     "tdengine",
			Spool:   ch.ob.Spool(),
			New:     func() push.Aggregator { return newInsertBuilder(ch.cfg.safeDB, ch.cfg.safeStable) },
			Write:   ch.write,
			MaxRows: ch.cfg.batchRows(),
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch.ob.DrainLoop(d.Drain, drainPolicy)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ch.ob.ReconnectLoop(reconnectInterval, ch.tryConnect)
	}()
	wg.Wait()
}

// tryConnect 建立连接 + 建库建表(均幂等),成功则置 connected 并唤醒补发。
func (ch *tdengineChannel) tryConnect() {
	db, err := sql.Open("taosWS", ch.cfg.dsn())
	if err != nil {
		errMsg := fmt.Sprintf("tdengine: open: %v", err)
		logger.Error("%s", errMsg)
		ch.ob.SetLastErr(errMsg)
		return
	}
	db.SetMaxOpenConns(ch.cfg.batchWorkers())
	db.SetMaxIdleConns(ch.cfg.batchWorkers())
	db.SetConnMaxLifetime(connMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		errMsg := fmt.Sprintf("tdengine: connect failed: %v", err)
		logger.Error("%s", errMsg)
		ch.ob.SetLastErr(errMsg)
		return
	}
	if err := ch.ensureSchema(db); err != nil {
		db.Close()
		errMsg := fmt.Sprintf("tdengine: ensure schema failed: %v", err)
		logger.Error("%s", errMsg)
		ch.ob.SetLastErr(errMsg)
		return
	}

	ch.mu.Lock()
	if ch.db != nil {
		ch.db.Close()
	}
	ch.db = db
	ch.mu.Unlock()

	ch.connected.Store(true)
	ch.ob.SetLastErr("")
	ch.ob.Notify() // 重连后可能积压断连期间落盘的批次,即时唤醒补发
}

// ensureSchema 建库建表(IF NOT EXISTS,幂等),仅建成功才视为连接就绪
func (ch *tdengineChannel) ensureSchema(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	for _, stmt := range ch.cfg.ensureSchemaSQL() {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt, err)
		}
	}
	return nil
}

// write 将构建好的单条(可为多子表聚合)INSERT 语句写入 TDengine。
// 返回是否写入成功(断连/超时/错误均视为失败),供补发协程决定缓存批次删除还是保留重试。
func (ch *tdengineChannel) write(stmt string) bool {
	if stmt == "" {
		return true // 无可写入行,视为成功
	}
	if !ch.connected.Load() {
		return false
	}

	ch.mu.RLock()
	db := ch.db
	ch.mu.RUnlock()
	if db == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		// 断连/错误:标记断开,交给 reconnect loop 恢复;丢弃统计由调用方按结果语义处理
		errMsg := fmt.Sprintf("tdengine: exec failed: %v", err)
		logger.Error("%s", errMsg)
		ch.ob.SetLastErr(errMsg)
		ch.connected.Store(false)
		return false
	}

	ch.ob.MarkPublished(1)
	return true
}

// Snapshot 返回通道状态快照(用于状态查询)
func (ch *tdengineChannel) Snapshot() push.ChannelStatusVO {
	vo := push.ChannelStatusVO{
		Type:   "tdengine",
		Broker: ch.cfg.endpoint(),
		Topic:  ch.cfg.Database,
	}
	ch.ob.FillStatus(&vo)
	return vo
}

// ==================== push.Aggregator 适配 ====================
// insertBuilder 实现 push.Aggregator,使补发走 push.GroupedDrainer 的
// 同一套分组/切分/删除算法(与 influxdb 的 lineBuilder 共享)。

// Append 追加一个批次,返回是否与当前语句兼容
// (子表冲突时返回 false,由 GroupedDrainer 先刷出本窗再追加)。
func (b *insertBuilder) Append(p push.PushBatch) bool {
	return b.appendBatch(p.CollectedAt, p.Records)
}

// Rows 当前语句已聚合的记录行数。
func (b *insertBuilder) Rows() int { return b.rows }

// Empty 当前语句是否为空。
func (b *insertBuilder) Empty() bool { return b.empty() }

// Payload 输出 INSERT 语句。
func (b *insertBuilder) Payload() string { return b.String() }
