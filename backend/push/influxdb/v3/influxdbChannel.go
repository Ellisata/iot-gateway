// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

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
//
// 队列、断网缓存、补发与状态统计由 push.Outbox 统一提供(见 push/outbox.go),
// 本通道只负责负载构造(lineBuilder)与下发(ch.write)。
type influxdbChannel struct {
	id   string
	name string
	cfg  *influxdbConfig
	sig  string // 配置签名,用于热加载判断通道配置是否变化

	ob *push.Outbox

	client    *http.Client
	connected atomic.Bool

	// done 由 run 关闭:worker 组(写入/补发/重连)全部退出。
	done chan struct{}

	mu      sync.Mutex
	running bool
}

// ChannelID 返回通道持久化 ID(实现 push.Channel)
func (ch *influxdbChannel) ChannelID() string { return ch.id }

// ConfigSig 返回配置签名,用于热加载判断配置是否变化(实现 push.Channel)
func (ch *influxdbChannel) ConfigSig() string { return ch.sig }

// Connected 返回当前连通状态(实现 push.Connectivity,供 Outbox 决定入队路由)
func (ch *influxdbChannel) Connected() bool { return ch.connected.Load() }

// Enqueue 非阻塞入队,绝不阻塞采集线程(实现 push.Channel)。
// 入队路由(直投内存队列 / 落盘 / 丢最旧)由 push.Outbox 统一决定。
func (ch *influxdbChannel) Enqueue(b push.PushBatch) { ch.ob.Enqueue(b) }

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
	ich.ob = push.NewOutbox(push.OutboxConfig{
		Tag:          "influxdb",
		ID:           ch.ID,
		Name:         ch.Name,
		DB:           db,
		SpoolEnabled: cfg.spoolEnabled(),
		SpoolCap:     cfg.spoolBatchCap(),
		Conn:         ich,
	})
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
	ch.running = true
	ch.done = make(chan struct{})
	ch.mu.Unlock()

	ch.ob.Start()
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
	ch.mu.Unlock()

	// 顺序有约束:先触发停止,再等 worker 组退出(停服前还会把 outbox 中写失败的
	// 批次转投缓存),最后才等落盘协程排空 —— 否则在途批次会随进程退出而丢失。
	ch.ob.Close()
	<-ch.done
	ch.ob.Wait()

	ch.connected.Store(false)
}

// run 通道主循环:启动 N 个写入 worker + 补发协程 + 重连协程,直至通道停止
func (ch *influxdbChannel) run() {
	defer close(ch.done)

	// 写入 worker:N 个协程各自攒批,把多个批次聚合成单个 Line Protocol body 批量写入
	// (行数达 batchRows 或时间到 batchInterval 双触发刷出;与 tdengine 不同,lineBuilder
	// 无「批间冲突」概念 —— 不同采集时间即不同 point,聚合永不拒绝批次;
	// 写入失败时整窗转本地缓存待重连补发)。
	w := &push.AggregateWriter{
		New:      func() push.Aggregator { return newLineBuilder(ch.cfg.Measurement) },
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
			Tag:     "influxdb",
			Spool:   ch.ob.Spool(),
			New:     func() push.Aggregator { return newLineBuilder(ch.cfg.Measurement) },
			Write:   ch.write,
			MaxRows: ch.cfg.batchRows(),
		}
		// 写入被拒(4xx)属数据类错误,判毒避免队头永久阻塞;窄化把坏点从组里
		// 隔离出来,避免连坐误删同组好数据。
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch.ob.DrainLoop(d.Drain, push.DrainPolicy{Poison: true, Narrow: true})
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ch.ob.ReconnectLoop(reconnectInterval, ch.tryConnect)
	}()
	wg.Wait()
}

// tryConnect 探测连通性(GET /health,带 token,200 视为连通),成功后 best-effort
// 建库并置 connected 唤醒补发。
func (ch *influxdbChannel) tryConnect() {
	req, err := http.NewRequest(http.MethodGet, ch.cfg.healthURL(), nil)
	if err != nil {
		errMsg := fmt.Sprintf("influxdb: build health request failed: %v", err)
		logger.Error("%s", errMsg)
		ch.ob.SetLastErr(errMsg)
		return
	}
	req.Header.Set("Authorization", "Bearer "+ch.cfg.Token)

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	resp, err := ch.client.Do(req.WithContext(ctx))
	if err != nil {
		errMsg := fmt.Sprintf("influxdb: connect failed: %v", err)
		logger.Error("%s", errMsg)
		ch.ob.SetLastErr(errMsg)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("influxdb: health check status %d", resp.StatusCode)
		logger.Error("%s", errMsg)
		ch.ob.SetLastErr(errMsg)
		return
	}

	ch.ensureDatabase()
	ch.connected.Store(true)
	ch.ob.SetLastErr("")
	ch.ob.Notify() // 重连后可能积压断连期间落盘的批次,即时唤醒补发
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
		ch.ob.SetLastErr(errMsg)
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
		ch.ob.SetLastErr(errMsg)
		ch.connected.Store(false)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		ch.ob.MarkPublished(1)
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
	ch.ob.SetLastErr(errMsg)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		ch.connected.Store(false)
	}
	return false
}

// Snapshot 返回通道状态快照(用于状态查询)
func (ch *influxdbChannel) Snapshot() push.ChannelStatusVO {
	vo := push.ChannelStatusVO{
		Type:   "influxdb",
		Broker: ch.cfg.baseURL(),
		Topic:  ch.cfg.Database + "/" + ch.cfg.Measurement,
	}
	ch.ob.FillStatus(&vo)
	return vo
}

// ==================== push.Aggregator 适配 ====================
// lineBuilder 实现 push.Aggregator,使补发走 push.GroupedDrainer 的
// 同一套分组/切分/删除算法(与 tdengine 的 insertBuilder 共享)。

// Append 追加一个批次。lineBuilder 无「批间冲突」概念(不同采集时间即不同 point),
// 故恒返回 true,永不由聚合侧切断负载。
func (b *lineBuilder) Append(p push.PushBatch) bool {
	b.appendBatch(p.CollectedAt, p.Records)
	return true
}

// Rows 当前负载已聚合的 point 行数。
func (b *lineBuilder) Rows() int { return b.points }

// Empty 当前负载是否为空。
func (b *lineBuilder) Empty() bool { return b.empty() }

// Payload 输出 Line Protocol 负载。
func (b *lineBuilder) Payload() string { return b.String() }
