package v3

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/push"
)

// init 向 push 包注册 influxdb 通道工厂。
// 依赖入口处(main.go)对本包的空导入触发,Engine 的 buildChannels 自动发现。
func init() {
	push.Register("influxdb-v3", func(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
		return newInfluxdbChannel(db, ch)
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
	// connectTimeout 单次连接/健康检查/建库超时
	connectTimeout = 5 * time.Second
	// writeTimeout 单次批量写入超时(批量 ≤5000 行,正常远小于此值)
	writeTimeout = 10 * time.Second
)

// influxdbChannel 单个 InfluxDB 3.x 推送通道运行时,实现 push.Channel。
//
// 架构与 tdengineChannel / mqttChannel 一致:
//   - 采集线程经 push.Engine 非阻塞入队(outbox 满时丢弃最旧);
//   - N 个写入 worker 消费 outbox,将多个批次聚合为单个 Line Protocol HTTP body
//     (按 batchRows 行数与 batchInterval 时间窗口双触发刷出,减少每设备每轮的
//     独立往返);
//   - InfluxDB 不可达时,失败批次(及断连期间直接入队的批次)落入 SQLite 本地缓存
//     (push_outbox,断网缓存),重连后按入队序补发、写成功删除 —— 网络中断期间数据不丢;
//   - 写入走标准库 net/http + POST /api/v3/write_lp(Bearer token,Line Protocol),
//     零新增外部依赖;通道层 reconnect loop 负责连通性探测(GET /health)与
//     best-effort 建库,并在恢复时唤醒补发。
type influxdbChannel struct {
	id   string
	name string
	cfg  *influxdbConfig
	sig  string // 配置签名,用于热加载判断通道配置是否变化

	client    *http.Client
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

	// narrowDrain 补发窄化标记(仅 spoolDrainLoop 单协程访问):某次组写入失败后置位,
	// 使后续补发强制单批一组,逐个批次试写以隔离真正失败的批次,避免坏点连坐整组被毒批次误删;
	// 整窗全部写成功后复位,恢复正常聚合。
	narrowDrain bool

	mu           sync.RWMutex
	running      bool
	lastErr      string
	lastPublish  time.Time
	lastSuccess  time.Time
	publishCount atomic.Uint64
	droppedCount atomic.Uint64
}

// ChannelID 返回通道持久化 ID(实现 push.Channel)
func (ch *influxdbChannel) ChannelID() string { return ch.id }

// ConfigSig 返回配置签名(实现 push.Channel)
func (ch *influxdbChannel) ConfigSig() string { return ch.sig }

// newInfluxdbChannel 根据推送通道配置创建通道运行时(未启动)。
// db(SQLite)用于构建断网本地缓存(spoolEnabled 时)。
func newInfluxdbChannel(db *gorm.DB, ch *po.PushChannel) (*influxdbChannel, error) {
	cfg, err := parseConfig(ch.ConfigJSON)
	if err != nil {
		return nil, err
	}
	ich := &influxdbChannel{
		id:     ch.ID,
		name:   ch.Name,
		cfg:    cfg,
		sig:    ch.ID + "|" + ch.Name + "|" + ch.ConfigJSON,
		client: newHTTPClient(cfg),
	}
	if cfg.spoolEnabled() {
		ich.spool = push.NewSqliteSpool(db, ch.ID)
	}
	return ich, nil
}

// TestConnectivity 同步探测 InfluxDB 连通性（GET /health 带 token，200 视为连通，
// 与 tryConnect 的连通性判定一致），不建库、不启动通道后台 goroutine。
// 用于管理界面保存配置前的连通性校验，可确认端点可达且鉴权通过。
func (ch *influxdbChannel) TestConnectivity() error {
	req, err := http.NewRequest(http.MethodGet, ch.cfg.healthURL(), nil)
	if err != nil {
		return fmt.Errorf("influxdb: build health request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+ch.cfg.Token)

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	resp, err := ch.client.Do(req.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("influxdb: connect failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("influxdb: health check status %d", resp.StatusCode)
	}
	return nil
}

// newHTTPClient 构建 InfluxDB HTTP 客户端。
// 不设全局超时,各请求用独立的 context 超时(connectTimeout / writeTimeout)。
// InsecureSkipVerify 时自建 Transport 跳过 TLS 证书校验(自签证书测试环境)。
// 默认 Transport 的 MaxIdleConnsPerHost=2,并发写入 worker 数超过 2 时连接无法复用,
// 每个请求都新建 TCP/TLS 连接成为吞吐瓶颈,按 batchWorkers 提升空闲连接上限。
func newHTTPClient(cfg *influxdbConfig) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = cfg.batchWorkers()
	if cfg.InsecureSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 用户显式配置
	}
	return &http.Client{Transport: transport}
}

// Start 启动通道的写入 goroutine(立即返回,连接在后台建立)
func (ch *influxdbChannel) Start() {
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
func (ch *influxdbChannel) Stop() {
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

	ch.connected.Store(false)
}

// run 通道主循环:启动 N 个写入 worker + 补发协程 + 重连协程,直至通道停止
func (ch *influxdbChannel) run() {
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

// writeLoop 单个写入 worker:循环消费 outbox,将多个批次聚合为单个 Line Protocol
// body 批量写入,直至通道停止。
//   - 聚合维度:行数达 batchRows 或时间到 batchInterval 双触发刷出,把「每设备每轮
//     一次往返」合并为「攒批一次往返」,降低设备量大时的固定开销;
//   - 与 tdengine 不同,lineBuilder 无「批间冲突」概念(不同采集时间即不同 point),
//     聚合永不拒绝批次;
//   - 写入失败(InfluxDB 不可达/超时等)时整窗转入本地缓存,重连后补发。
func (ch *influxdbChannel) writeLoop() {
	ticker := time.NewTicker(ch.cfg.batchInterval())
	defer ticker.Stop()

	lb := newLineBuilder(ch.cfg.Measurement)
	var pending []push.PushBatch
	flush := func() {
		ch.flushWindow(lb, pending)
		lb = newLineBuilder(ch.cfg.Measurement)
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
					lb.appendBatch(b.CollectedAt, b.Records)
					pending = append(pending, b)
					if lb.points >= ch.cfg.batchRows() {
						flush()
					}
				default:
					ch.flushWindow(lb, pending)
					return
				}
			}
		case b := <-ch.outbox:
			lb.appendBatch(b.CollectedAt, b.Records)
			pending = append(pending, b)
			if lb.points >= ch.cfg.batchRows() {
				flush()
			}
		case <-ticker.C:
			flush() // 未达行数也按时间窗口刷出,约束写入延迟(空窗时 no-op)
		}
	}
}

// flushWindow 将当前聚合窗内的批次写入 InfluxDB。
// 失败且启用本地缓存时,窗内批次全部转入本地缓存待重连补发(不计数,补发成功仍计入
// publishCount);未启用本地缓存时本窗数据被丢弃,计入 droppedCount。
func (ch *influxdbChannel) flushWindow(lb *lineBuilder, pending []push.PushBatch) {
	if lb.empty() {
		return
	}
	if ch.write(lb.String()) {
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
// 恢复连接后执行 best-effort 建库并唤醒补发协程。
func (ch *influxdbChannel) reconnectLoop() {
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

// tryConnect 探测连通性(GET /health,带 token,200 视为连通),成功后 best-effort
// 建库并置 connected 唤醒补发。
func (ch *influxdbChannel) tryConnect() {
	req, err := http.NewRequest(http.MethodGet, ch.cfg.healthURL(), nil)
	if err != nil {
		errMsg := fmt.Sprintf("influxdb: build health request failed: %v", err)
		logger.Error("%s", errMsg)
		ch.setLastErr(errMsg)
		return
	}
	req.Header.Set("Authorization", "Bearer "+ch.cfg.Token)

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	resp, err := ch.client.Do(req.WithContext(ctx))
	if err != nil {
		errMsg := fmt.Sprintf("influxdb: connect failed: %v", err)
		logger.Error("%s", errMsg)
		ch.setLastErr(errMsg)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("influxdb: health check status %d", resp.StatusCode)
		logger.Error("%s", errMsg)
		ch.setLastErr(errMsg)
		return
	}

	ch.ensureDatabase()
	ch.connected.Store(true)
	ch.setLastErr("")
	ch.signalDrain() // 重连后可能积压断连期间落盘的批次,即时唤醒补发
}

// ensureDatabase best-effort 创建目标数据库(POST /api/v3/configure/database)。
// 幂等:409 已存在视为成功;401/403 非 admin 令牌无法建库,视为已预置库放行
// (若库确实缺失,写入会失败并由毒批次机制兜底)。任何失败均不阻塞连接置位。
func (ch *influxdbChannel) ensureDatabase() {
	body := fmt.Sprintf(`{"db":%q}`, ch.cfg.Database)
	req, err := http.NewRequest(http.MethodPost, ch.cfg.configureDatabaseURL(), strings.NewReader(body))
	if err != nil {
		logger.Error("influxdb: build ensure-database request failed: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+ch.cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	resp, err := ch.client.Do(req.WithContext(ctx))
	if err != nil {
		logger.Error("influxdb: ensure database %q failed: %v", ch.cfg.Database, err)
		return
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		logger.Info("influxdb: database %q created", ch.cfg.Database)
	case http.StatusConflict:
		logger.Info("influxdb: database %q already exists", ch.cfg.Database)
	case http.StatusUnauthorized, http.StatusForbidden:
		logger.Warn("influxdb: ensure database %q denied (token may be non-admin), assuming pre-provisioned", ch.cfg.Database)
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		logger.Warn("influxdb: ensure database %q unexpected status %d: %s", ch.cfg.Database, resp.StatusCode, strings.TrimSpace(string(b)))
	}
}

// write 将构建好的单个(可多批聚合)Line Protocol body 写入 InfluxDB。
// 返回是否写入成功(断连/超时/非 2xx 均视为失败),供补发协程决定缓存批次删除还是保留重试。
func (ch *influxdbChannel) write(body string) bool {
	if body == "" {
		return true // 无可写入行,视为成功
	}
	if !ch.connected.Load() {
		return false
	}

	req, err := http.NewRequest(http.MethodPost, ch.cfg.writeURL(), strings.NewReader(body))
	if err != nil {
		errMsg := fmt.Sprintf("influxdb: build write request failed: %v", err)
		logger.Error("%s", errMsg)
		ch.setLastErr(errMsg)
		ch.connected.Store(false)
		return false
	}
	req.Header.Set("Authorization", "Bearer "+ch.cfg.Token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	resp, err := ch.client.Do(req.WithContext(ctx))
	if err != nil {
		// 断连/超时:标记断开,交给 reconnect loop 恢复;丢弃统计由调用方按结果语义处理
		errMsg := fmt.Sprintf("influxdb: write failed: %v", err)
		logger.Error("%s", errMsg)
		ch.setLastErr(errMsg)
		ch.connected.Store(false)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		now := time.Now()
		ch.mu.Lock()
		ch.lastPublish = now
		ch.lastSuccess = now
		ch.mu.Unlock()
		ch.publishCount.Add(1)
		return true
	}

	// 非 2xx:读取响应体作为错误信息(可能含被拒行的详情)。
	// 4xx(除 429 限流)为请求级错误(数据非法/权限/库缺失),连接本身健康:
	// 置 disconnected 会触发重连抖动,且让实时写入整窗转道 spool 后仍被毒批次机制丢弃;
	// 坏数据直接交由补发协程的毒批次窄化机制隔离即可。429 限流与 5xx 才视为服务端过载/故障,
	// 置断开交重连循环退避。
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	errMsg := fmt.Sprintf("influxdb: write status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	logger.Error("%s", errMsg)
	ch.setLastErr(errMsg)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		ch.connected.Store(false)
	}
	return false
}

// Enqueue 非阻塞入队,绝不阻塞采集线程。
// 路由:
//   - 断连且启用本地缓存:直接落盘,避免进内存队列后被写入线程丢弃;
//   - 否则内存 outbox 快路径,满时启用本地缓存则溢写,未启用则丢弃最旧(兼容旧行为)。
func (ch *influxdbChannel) Enqueue(b push.PushBatch) {
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
func (ch *influxdbChannel) spoolEnabled() bool {
	return ch.spool != nil && ch.cfg.spoolEnabled()
}

// spoolEnqueue 非阻塞将批次写入本地缓存写入缓冲(满时丢弃最旧,与 outbox 同策略)
func (ch *influxdbChannel) spoolEnqueue(b push.PushBatch) {
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
// 退出前(quit 关闭)持续排空 spoolCh,并等待写入协程全部退出(done 关闭)—— 写入协程
// 停服前还会把 outbox 中写失败的批次落入 spoolCh,若本协程提前排空返回会把它们留在
// 内存通道中丢失。done 关闭后再做最终排空,保证停服/热更不丢已入队的批次。
func (ch *influxdbChannel) spoolWriteLoop() {
	defer close(ch.spoolDone)

	for {
		select {
		case <-ch.quit:
			ch.drainSpoolCh()
			// 等待 run 的写入协程全部退出(done 关闭)期间持续排空,避免新入队批次堆积
			for {
				select {
				case <-ch.done:
					ch.drainSpoolCh()
					return
				case b := <-ch.spoolCh:
					ch.spoolInsert(b)
					ch.signalDrain()
				}
			}
		case b := <-ch.spoolCh:
			ch.spoolInsert(b)
			ch.signalDrain()
		}
	}
}

// drainSpoolCh 非阻塞排空 spoolCh 中的在途批次(写协程退出前的收尾用)。
func (ch *influxdbChannel) drainSpoolCh() {
	for {
		select {
		case b := <-ch.spoolCh:
			ch.spoolInsert(b)
			ch.signalDrain()
		default:
			return
		}
	}
}

// spoolInsert 将批次写入本地缓存;达到上限后每 N 批批量裁剪最旧以约束磁盘占用。
// Count 为内存计数(无查询),裁剪由原逐批 DeleteOldest(1) 改为批量 DeleteOldest(N),
// 摊薄满盘期的 SQLite 写放大。
func (ch *influxdbChannel) spoolInsert(b push.PushBatch) {
	if ch.spool == nil {
		return
	}
	ch.spoolTrimTick++
	if ch.spoolTrimTick >= spoolTrimInterval {
		ch.spoolTrimTick = 0
		if count, err := ch.spool.Count(); err == nil && count >= int64(ch.cfg.spoolBatchCap()) {
			if err := ch.spool.DeleteOldest(spoolTrimChunk); err != nil {
				logger.Error("influxdb: spool trim oldest failed: %v", err)
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
func (ch *influxdbChannel) spoolDrainLoop() {
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

		ok, failID := ch.drainWindow(pend, ch.narrowDrain)
		if ok {
			ch.poisonBatchID = 0
			ch.narrowDrain = false // 本窗全部写成功,恢复正常聚合
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
		// reconnectLoop 建连成功才能再次进入)仍写失败 ⇒ 连接健康而请求被拒,是数据类错误
		// (毒批次)。跳过(删除 + 记 dropped)而非无限重试,避免队头永久阻塞后续所有补发。
		//
		// 注意:失败组可能混有多批次而坏点不在组头,若直接按组头判毒会连坐误删好数据,
		// 故失败后先置 narrowDrain(单批一组)再等待,重连后逐批试写隔离真正失败的批次。
		if ch.poisonBatchID == failID {
			ch.poisonBatchID = 0
			ch.droppedCount.Add(1)
			logger.Error("influxdb: spool batch %d rejected permanently (data error), drop to unblock drain", failID)
			if err := ch.spool.Delete(failID); err != nil {
				logger.Error("influxdb: spool delete poison batch %d failed: %v", failID, err)
			}
			continue // 保持窄化,继续定位后续可能存在的坏批次
		}
		ch.poisonBatchID = failID
		ch.narrowDrain = true

		if !ch.waitSpoolDrain() {
			return
		}
	}
}

// drainWindow 按入队序将窗口内批次聚合成尽量少的 body 写库,写成功即删除对应缓存行。
//   - narrow 为 true 时强制单批一组:某组写入失败后逐批试写,隔离真正失败的批次,
//     避免坏点连坐整组被上层毒批次判定误删好数据;
//   - 行数达 batchRows 时切分请求,避免单请求过大;
//   - 任一请求写入失败即停止,保留本组及后续待重连重试(断连/超时),避免对故障库空转;
//   - 空 body 批次(理论上不会)不写库但一并删除,避免阻塞后续补发。
//
// 返回是否本窗口全部写入成功;失败时 failID 为首个未写入成功的批次 ID(供上层毒批次判定,
// 成功写出的组已删除,故该 ID 即 spool 队头,重试仍会从它开始)。
func (ch *influxdbChannel) drainWindow(pend []push.SpoolBatch, narrow bool) (ok bool, failID int64) {
	for len(pend) > 0 {
		lb := newLineBuilder(ch.cfg.Measurement)
		var group []push.SpoolBatch

		for i := range pend {
			p := &pend[i]
			if narrow {
				if len(group) > 0 {
					break // 窄化:单批一组,逐个隔离
				}
			} else if lb.points > 0 && lb.points+len(p.Records) > ch.cfg.batchRows() {
				break
			}
			lb.appendBatch(p.CollectedAt, p.Records)
			group = append(group, pend[i])
		}
		if len(group) == 0 {
			return false, pend[0].ID // 防御:lineBuilder 永不拒绝,理论不可达
		}

		if !lb.empty() {
			if !ch.write(lb.String()) {
				return false, group[0].ID
			}
		}
		ids := make([]int64, 0, len(group))
		for i := range group {
			ids = append(ids, group[i].ID)
		}
		if err := ch.spool.DeleteBatch(ids); err != nil {
			logger.Error("influxdb: spool delete batch failed (n=%d): %v", len(group), err)
		}
		pend = pend[len(group):]
	}
	return true, 0
}

// waitSpoolDrain 阻塞等待补发信号:新批次落盘/重连事件即时唤醒,
// 兜底 timer 仅防漏 ping。返回 false 表示通道已停止。
func (ch *influxdbChannel) waitSpoolDrain() bool {
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
func (ch *influxdbChannel) signalDrain() {
	select {
	case ch.drainNotify <- struct{}{}:
	default:
	}
}

// Snapshot 返回通道状态快照(用于状态查询)
func (ch *influxdbChannel) Snapshot() push.ChannelStatusVO {
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
		Type:         "influxdb",
		Running:      ch.running,
		Connected:    ch.connected.Load(),
		Broker:       ch.cfg.baseURL(),
		Topic:        ch.cfg.Database + "/" + ch.cfg.Measurement,
		QueueDepth:   uint64(len(ch.outbox)),
		SpoolDepth:   spoolDepth,
		PublishCount: ch.publishCount.Load(),
		DroppedCount: ch.droppedCount.Load(),
		LastPublish:  push.FormatTime(ch.lastPublish),
		LastSuccess:  push.FormatTime(ch.lastSuccess),
		LastErr:      ch.lastErr,
	}
}

func (ch *influxdbChannel) setLastErr(msg string) {
	ch.mu.Lock()
	ch.lastErr = msg
	ch.mu.Unlock()
}
