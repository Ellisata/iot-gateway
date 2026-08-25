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
	// outboxSize 每通道有界出站缓冲大小,满时丢弃最旧,保证采集永不阻塞
	outboxSize = 1024
	// spoolChSize 本地缓存写入缓冲大小(与 outbox 同策略)
	spoolChSize = 1024
	// spoolDrainWindow 补发协程单次从本地缓存取回的最大批次数(有界回放窗口)
	spoolDrainWindow = 16
	// spoolDrainBackstop 补发等待兜底间隔(仅防漏 ping,见 mqtt 通道同名单词注释)
	spoolDrainBackstop = 10 * time.Second
	// spoolTrimInterval 本地缓存达到上限后每插入 N 批执行一次批量裁剪,
	// spoolTrimChunk 为单次删除的批次数量:满盘期把逐批 SELECT+DELETE 摊薄 N 倍,
	// 缓解 SQLite 单连接写放大(断连持续期间)。
	spoolTrimInterval = 16
	spoolTrimChunk    = 16

	// reconnectInterval 断连重连尝试间隔(首连在 Start 后立即尝试,失败后按此间隔重试)
	reconnectInterval = 5 * time.Second
	// connectTimeout 单次连接/建库建表/校验超时
	connectTimeout = 5 * time.Second
	// writeTimeout 单次批量写入超时(批量 ≤500 条,正常远小于此值)
	writeTimeout = 10 * time.Second
	// connMaxLifetime 连接最大存活时间:定期刷新底层 WebSocket,避免长连接残旧
	connMaxLifetime = 10 * time.Minute
)

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
type tdengineChannel struct {
	id   string
	name string
	cfg  *tdengineConfig
	sig  string // 配置签名,用于热加载判断通道配置是否变化

	db        *sql.DB
	connected atomic.Bool

	// spool 断网本地缓存(nil 表示未启用,回退为纯内存丢最旧行为)
	spool         push.Spool
	spoolCh       chan push.PushBatch // 本地缓存写入缓冲(写协程消费)
	spoolDone     chan struct{}       // 写协程退出信号
	drainNotify   chan struct{}       // 补发唤醒信号(cap=1 合流)
	spoolTrimTick int                 // 满盘批量裁剪节流计数(仅 spoolWriteLoop 单协程访问)

	outbox chan push.PushBatch

	quit chan struct{}
	done chan struct{}

	// poisonBatchID 上一轮补发失败且尚未二次确认的批次 ID(仅 spoolDrainLoop 单协程访问)。
	// 同一队头批次在连接恢复后仍写失败 ⇒ 数据类错误(毒批次),跳过避免队头永久阻塞后续补发。
	poisonBatchID int64

	mu           sync.RWMutex
	running      bool
	lastErr      string
	lastPublish  time.Time
	lastSuccess  time.Time
	publishCount atomic.Uint64
	droppedCount atomic.Uint64
}

// ChannelID 返回通道持久化 ID(实现 push.Channel)
func (ch *tdengineChannel) ChannelID() string { return ch.id }

// ConfigSig 返回配置签名(实现 push.Channel)
func (ch *tdengineChannel) ConfigSig() string { return ch.sig }

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
	if cfg.spoolEnabled() {
		tch.spool = push.NewSqliteSpool(db, ch.ID)
	}
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
	ch.quit = make(chan struct{})
	ch.done = make(chan struct{})
	ch.outbox = make(chan push.PushBatch, outboxSize)
	ch.running = true
	ch.mu.Unlock()

	// 本地缓存写协程立即启动:断连期间的批次随时可落盘
	if ch.spool != nil {
		ch.spoolCh = make(chan push.PushBatch, spoolChSize)
		ch.spoolDone = make(chan struct{})
		ch.drainNotify = make(chan struct{}, 1)
		go ch.spoolWriteLoop()
	}

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
	close(ch.quit)
	ch.mu.Unlock()

	<-ch.done
	// 等待写协程排空在途批次
	if ch.spool != nil && ch.spoolDone != nil {
		<-ch.spoolDone
	}

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

	var wg sync.WaitGroup
	for i := 0; i < ch.cfg.batchWorkers(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch.writeLoop()
		}()
	}
	if ch.spoolEnabled() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch.spoolDrainLoop()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ch.reconnectLoop()
	}()
	wg.Wait()
}

// writeLoop 单个写入 worker:循环消费 outbox,将多个批次聚合为单条多子表 INSERT
// 批量写入,直至通道停止。
//   - 聚合维度:行数达 batchRows 或时间到 batchInterval 双触发刷出,把「每设备每轮
//     一次往返」合并为「攒批一次往返」,降低设备量大时的固定开销;
//   - 聚合窗内子表冲突(跨采集轮次同一设备+点位)时提前刷出,不跨轮去重丢数据;
//   - 写入失败(TDengine 不可达/超时等)时整窗转入本地缓存,重连后补发。
func (ch *tdengineChannel) writeLoop() {
	ticker := time.NewTicker(ch.cfg.batchInterval())
	defer ticker.Stop()

	ib := newInsertBuilder(ch.cfg.safeDB, ch.cfg.safeStable)
	var pending []push.PushBatch
	flush := func() {
		ch.flushWindow(ib, pending)
		ib = newInsertBuilder(ch.cfg.safeDB, ch.cfg.safeStable)
		pending = pending[:0]
	}

	for {
		select {
		case <-ch.quit:
			// 停服前排空 outbox 在途批次:继续消费直至队列空(写失败转本地缓存),
			// 避免优雅停机/热更时缓存队列中尚未写入的批次静默丢失。
			for {
				select {
				case b := <-ch.outbox:
					for !ib.appendBatch(b.CollectedAt, b.Records) {
						flush()
					}
					pending = append(pending, b)
					if ib.rows >= ch.cfg.batchRows() {
						flush()
					}
				default:
					ch.flushWindow(ib, pending)
					return
				}
			}
		case b := <-ch.outbox:
			// 子表冲突则先刷出本窗再追加,避免跨轮同地址被批内去重丢弃
			for !ib.appendBatch(b.CollectedAt, b.Records) {
				flush()
			}
			pending = append(pending, b)
			if ib.rows >= ch.cfg.batchRows() {
				flush()
			}
		case <-ticker.C:
			flush() // 未达行数也按时间窗口刷出,约束写入延迟(空窗时 no-op)
		}
	}
}

// flushWindow 将当前聚合窗内的批次写入 TDengine。
// 失败且启用本地缓存时,窗内批次全部转入本地缓存待重连补发(不计数,补发成功仍计入
// publishCount);未启用本地缓存时本窗数据被丢弃,计入 droppedCount。
func (ch *tdengineChannel) flushWindow(ib *insertBuilder, pending []push.PushBatch) {
	if ib.empty() {
		return
	}
	if ch.write(ib.String()) {
		return
	}
	if ch.spoolEnabled() {
		for _, b := range pending {
			ch.spoolEnqueue(b)
		}
		return
	}
	ch.droppedCount.Add(uint64(len(pending)))
}

// reconnectLoop 连通性维护:首连立即尝试,之后断连时按 reconnectInterval 重试。
// 恢复连接后执行建库建表(幂等)并唤醒补发协程。
func (ch *tdengineChannel) reconnectLoop() {
	if !ch.connected.Load() {
		ch.tryConnect()
	}
	ticker := time.NewTicker(reconnectInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ch.quit:
			return
		case <-ticker.C:
			if !ch.connected.Load() {
				ch.tryConnect()
			}
		}
	}
}

// tryConnect 建立连接 + 建库建表(均幂等),成功则置 connected 并唤醒补发。
func (ch *tdengineChannel) tryConnect() {
	db, err := sql.Open("taosWS", ch.cfg.dsn())
	if err != nil {
		errMsg := fmt.Sprintf("tdengine: open: %v", err)
		logger.Error("%s", errMsg)
		ch.setLastErr(errMsg)
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
		ch.setLastErr(errMsg)
		return
	}
	if err := ch.ensureSchema(db); err != nil {
		db.Close()
		errMsg := fmt.Sprintf("tdengine: ensure schema failed: %v", err)
		logger.Error("%s", errMsg)
		ch.setLastErr(errMsg)
		return
	}

	ch.mu.Lock()
	if ch.db != nil {
		ch.db.Close()
	}
	ch.db = db
	ch.mu.Unlock()

	ch.connected.Store(true)
	ch.setLastErr("")
	ch.signalDrain() // 重连后可能积压断连期间落盘的批次,即时唤醒补发
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
		ch.setLastErr(errMsg)
		ch.connected.Store(false)
		return false
	}

	now := time.Now()
	ch.mu.Lock()
	ch.lastPublish = now
	ch.lastSuccess = now
	ch.mu.Unlock()
	ch.publishCount.Add(1)
	return true
}

// Enqueue 非阻塞入队,绝不阻塞采集线程。
// 路由:
//   - 断连且启用本地缓存:直接落盘,避免进内存队列后被写入线程丢弃;
//   - 否则内存 outbox 快路径,满时启用本地缓存则溢写,未启用则丢弃最旧(兼容旧行为)。
func (ch *tdengineChannel) Enqueue(b push.PushBatch) {
	// 断连且启用本地缓存:直接落盘
	if ch.spoolEnabled() && !ch.connected.Load() {
		ch.spoolEnqueue(b)
		return
	}

	// 内存快路径
	select {
	case ch.outbox <- b:
		return
	default:
	}

	// 内存队列满:启用本地缓存则溢写,否则丢弃最旧
	if ch.spoolEnabled() {
		ch.spoolEnqueue(b)
		return
	}

	// 丢弃最旧、保留最新,被丢弃的批次计入 droppedCount
	select {
	case <-ch.outbox:
		ch.droppedCount.Add(1)
	default:
	}

	select {
	case ch.outbox <- b:
	default:
		ch.droppedCount.Add(1) // 仍满,丢弃本条
	}
}

// spoolEnabled 本地缓存是否实际启用(配置启用且已构建 spool)
func (ch *tdengineChannel) spoolEnabled() bool {
	return ch.spool != nil && ch.cfg.spoolEnabled()
}

// spoolEnqueue 非阻塞将批次写入本地缓存写入缓冲(满时丢弃最旧,与 outbox 同策略)
func (ch *tdengineChannel) spoolEnqueue(b push.PushBatch) {
	select {
	case ch.spoolCh <- b:
		return
	default:
	}

	select {
	case <-ch.spoolCh:
		ch.droppedCount.Add(1)
	default:
	}

	select {
	case ch.spoolCh <- b:
	default:
		ch.droppedCount.Add(1) // 仍满,丢弃本条
	}
}

// spoolWriteLoop 本地缓存写协程:消费 spoolCh 串行写入 SQLite。
// 退出前(quit 关闭)排空在途批次,保证停服/热更不丢已入队的批次。
func (ch *tdengineChannel) spoolWriteLoop() {
	defer close(ch.spoolDone)

	for {
		select {
		case <-ch.quit:
			for {
				select {
				case b := <-ch.spoolCh:
					ch.spoolInsert(b)
					ch.signalDrain()
				default:
					return
				}
			}
		case b := <-ch.spoolCh:
			ch.spoolInsert(b)
			ch.signalDrain()
		}
	}
}

// spoolInsert 将批次写入本地缓存;达到上限后每 N 批批量裁剪最旧以约束磁盘占用。
// Count 为内存计数(无查询),裁剪由原逐批 DeleteOldest(1) 改为批量 DeleteOldest(N),
// 摊薄满盘期的 SQLite 写放大。
func (ch *tdengineChannel) spoolInsert(b push.PushBatch) {
	if ch.spool == nil {
		return
	}
	ch.spoolTrimTick++
	if ch.spoolTrimTick >= spoolTrimInterval {
		ch.spoolTrimTick = 0
		if count, err := ch.spool.Count(); err == nil && count >= int64(ch.cfg.spoolBatchCap()) {
			if err := ch.spool.DeleteOldest(spoolTrimChunk); err != nil {
				logger.Error("tdengine: spool trim oldest failed: %v", err)
			}
		}
	}
	if err := ch.spool.Insert(b); err != nil {
		errMsg := fmt.Sprintf("spool insert: %v", err)
		logger.Error("%s", errMsg)
		ch.setLastErr(errMsg)
	}
}

// spoolDrainLoop 断网缓存补发协程:连接可用时按入队序取回未写成功的批次,
// 写入成功即删除;失败(断连/超时)保留待重连重试。
// 空缓存时阻塞等待事件(新批次落盘/重连)唤醒,无固定轮询。
func (ch *tdengineChannel) spoolDrainLoop() {
	for {
		if !ch.connected.Load() {
			if !ch.waitSpoolDrain() {
				return
			}
			continue
		}

		pend, err := ch.spool.FetchOldest(spoolDrainWindow)
		if err != nil {
			if !ch.waitSpoolDrain() {
				return
			}
			continue
		}
		if len(pend) == 0 {
			if !ch.waitSpoolDrain() {
				return
			}
			continue
		}

		ok, failID := ch.drainWindow(pend)
		if ok {
			ch.poisonBatchID = 0
			// 本窗全部写入成功且恰好取满(可能仍有积压)则立即续取,不 sleep;
			// 已到尾部则回等待,避免对空缓存空转
			if len(pend) == spoolDrainWindow {
				continue
			}
			if !ch.waitSpoolDrain() {
				return
			}
			continue
		}

		// 写入失败。同一队头批次若在连接恢复后(drainLoop 仅在 connected 时运行,失败后须等
		// reconnectLoop 建连成功才能再次进入)仍写失败 ⇒ 连接健康而语句被拒,是数据类错误
		// (毒批次)。跳过(删除 + 记 dropped)而非无限重试,避免队头永久阻塞后续所有补发。
		if ch.poisonBatchID == failID {
			ch.poisonBatchID = 0
			ch.droppedCount.Add(1)
			logger.Error("tdengine: spool batch %d rejected permanently (data error), drop to unblock drain", failID)
			if err := ch.spool.Delete(failID); err != nil {
				logger.Error("tdengine: spool delete poison batch %d failed: %v", failID, err)
			}
			continue
		}
		ch.poisonBatchID = failID

		if !ch.waitSpoolDrain() {
			return
		}
	}
}

// drainWindow 按入队序将窗口内批次聚合成尽量少的 INSERT 写库,写成功即删除对应缓存行。
//   - 子表冲突(跨轮次同地址)或行数达 batchRows 时切分语句,冲突批次留给下一语句,
//     不跨语句去重丢数据;
//   - 任一语句写入失败即停止,保留本组及后续待重连重试(断连/超时),避免对故障库空转;
//   - 空记录批次(理论上不会)不写库但一并删除,避免阻塞后续补发。
//
// 返回是否本窗口全部写入成功;失败时 failID 为首个未写入成功的批次 ID(供上层毒批次判定,
// 成功写出的组已删除,故该 ID 即 spool 队头,重试仍会从它开始)。
func (ch *tdengineChannel) drainWindow(pend []push.SpoolBatch) (ok bool, failID int64) {
	for len(pend) > 0 {
		ib := newInsertBuilder(ch.cfg.safeDB, ch.cfg.safeStable)
		var group []push.SpoolBatch

		for i := range pend {
			p := &pend[i]
			if ib.rows > 0 && ib.rows+len(p.Records) > ch.cfg.batchRows() {
				break
			}
			if !ib.appendBatch(p.CollectedAt, p.Records) {
				break // 子表冲突:本语句到此为止,冲突批次留给下一语句
			}
			group = append(group, pend[i])
		}
		if len(group) == 0 {
			return false, pend[0].ID // 防御:首个批次即无法追加,交由上层等待重试
		}

		if !ib.empty() {
			if !ch.write(ib.String()) {
				return false, group[0].ID
			}
		}
		ids := make([]int64, 0, len(group))
		for i := range group {
			ids = append(ids, group[i].ID)
		}
		if err := ch.spool.DeleteBatch(ids); err != nil {
			logger.Error("tdengine: spool delete batch failed (n=%d): %v", len(group), err)
		}
		pend = pend[len(group):]
	}
	return true, 0
}

// waitSpoolDrain 阻塞等待补发信号:新批次落盘/重连事件即时唤醒,
// 兜底 timer 仅防漏 ping。返回 false 表示通道已停止。
func (ch *tdengineChannel) waitSpoolDrain() bool {
	backstop := time.NewTimer(spoolDrainBackstop)
	defer backstop.Stop()

	select {
	case <-ch.quit:
		return false
	case <-ch.drainNotify:
		return true
	case <-backstop.C:
		return true
	}
}

// signalDrain 非阻塞通知补发协程有新批次可补发(cap=1 自动合流,无信号时 no-op)。
func (ch *tdengineChannel) signalDrain() {
	select {
	case ch.drainNotify <- struct{}{}:
	default:
	}
}

// Snapshot 返回通道状态快照(用于状态查询)
func (ch *tdengineChannel) Snapshot() push.ChannelStatusVO {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	spoolDepth := uint64(0)
	if ch.spoolEnabled() {
		if n, err := ch.spool.Count(); err == nil {
			spoolDepth = uint64(n)
		}
	}

	return push.ChannelStatusVO{
		ID:           ch.id,
		Name:         ch.name,
		Type:         "tdengine",
		Running:      ch.running,
		Connected:    ch.connected.Load(),
		Broker:       ch.cfg.endpoint(),
		Topic:        ch.cfg.Database,
		QueueDepth:   uint64(len(ch.outbox)),
		SpoolDepth:   spoolDepth,
		PublishCount: ch.publishCount.Load(),
		DroppedCount: ch.droppedCount.Load(),
		LastPublish:  push.FormatTime(ch.lastPublish),
		LastSuccess:  push.FormatTime(ch.lastSuccess),
		LastErr:      ch.lastErr,
	}
}

func (ch *tdengineChannel) setLastErr(msg string) {
	ch.mu.Lock()
	ch.lastErr = msg
	ch.mu.Unlock()
}
