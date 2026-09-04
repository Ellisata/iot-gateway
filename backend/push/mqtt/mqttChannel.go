// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mqtt

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"gorm.io/gorm"

	"iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/push"
	"iot-gateway/utils"
)

// init 向 push 包注册 mqtt 通道工厂。
// 依赖入口处（main.go）对本包的空导入触发，Engine 的 buildChannels 自动发现。
func init() {
	push.Register("mqtt", func(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
		return newMqttChannel(db, ch)
	})
}

const (
	// outboxSize 每通道有界出站缓冲大小，满时丢弃最旧，保证采集永不阻塞
	outboxSize = 1024
	// publishTimeout 单次发布等待完成的最长时间（防止断连期间 QoS1 token 悬挂）
	publishTimeout = 3 * time.Second
	// testConnectTimeout 连通性测试等待上限（略大于 buildClientOptions 的 5s 连接超时，
	// 保证连接尝试先出确定结果再判超时，避免误报）
	testConnectTimeout = 6 * time.Second
	// defaultPublishWorkers 并发发布 worker 数默认值（配置缺省/非法时回落）
	defaultPublishWorkers = 4
	// maxPublishWorkers 发布 worker 数上限，防止配置异常导致 goroutine 数量失控
	maxPublishWorkers = 64

	// defaultSpoolMaxBatches 断网本地缓存最大批次上限默认值；
	// 达到后裁剪最旧批次以约束磁盘占用。
	defaultSpoolMaxBatches = 100000
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

	// clientIDBaseCap 基础前缀截断长度：拼接随机后缀后整体控制在 23 字节内，
	// 兼容 MQTT 3.1 常见的 clientId 长度上限（超长会被 broker 拒连）。
	clientIDBaseCap = 12
	// clientIDSuffixLen 运行时追加的随机后缀长度（hex 字符数）。
	// 实际 clientId = 配置值前缀 + 随机后缀，保证本实例/本通道唯一，
	// 多实例或残留进程不再因同 clientId 被 broker 会话接管踢断。
	clientIDSuffixLen = 10
)

// mqttChannel 单个 MQTT 推送通道的运行时，实现 push.Channel。
//
// 每个通道独立 outbox（有界缓冲）+ N 个发布 worker：
//   - 采集线程经 push.Engine 非阻塞入队，outbox 满时丢弃最旧；
//   - N 个发布 worker（默认 4，可配置）仅在连接成功后开始消费 outbox，
//     共享同一连接并发 Publish，paho 内部流水线写帧（不等 ACK），
//     吞吐 ≈ N × 单 worker 串行发布速率，broker 慢/跨广域时不再轻易丢最旧；
//   - 断连/内存队列满的批次落入 SQLite 本地缓存（push_outbox，断网缓存），
//     重连后按入队序补发、发布成功删除 —— 网络中断期间采集数据不丢失；
//   - 重连由 paho 的 AutoReconnect / ConnectRetry 负责。
//
// 排序保证：批内记录顺序保持（单次 marshal + 单次 Publish）；跨批可能乱序，
// 但每条点位携带 collectedAt（毫秒级时间戳），下游按时间兜底排序。
type mqttChannel struct {
	id   string
	name string
	cfg  *mqttConfig
	sig  string // 配置签名，用于热加载判断通道配置是否变化

	// clientID 实际使用的 clientId：配置值前缀 + 通道级随机后缀（见 resolveClientID）。
	// 新建通道/重启进程即换新值，从根上避免多实例同 clientId 被 broker 会话接管踢断。
	clientID string

	// spool 断网本地缓存（nil 表示未启用，回退为纯内存丢最旧行为）
	spool         push.Spool
	spoolCh       chan push.PushBatch // 本地缓存写入缓冲（写协程消费）
	spoolDone     chan struct{}       // 写协程退出信号
	drainNotify   chan struct{}       // 补发唤醒信号（新批次落盘/重连时 ping，cap=1 合流）
	spoolTrimTick int                 // 满盘批量裁剪节流计数（仅 spoolWriteLoop 单协程访问）

	outbox chan push.PushBatch

	quit chan struct{}
	done chan struct{}

	mu           sync.RWMutex
	running      bool
	connected    bool
	lastErr      string
	lastPublish  time.Time
	lastSuccess  time.Time
	publishCount atomic.Uint64
	droppedCount atomic.Uint64
}

// ChannelID 返回通道持久化 ID（实现 push.Channel）
func (ch *mqttChannel) ChannelID() string { return ch.id }

// ConfigSig 返回配置签名，用于热加载判断配置是否变化（实现 push.Channel）
func (ch *mqttChannel) ConfigSig() string { return ch.sig }

// newMqttChannel 根据推送通道配置创建通道运行时（未启动）。
// db 用于构建断网本地缓存（spoolEnabled 时）。
func newMqttChannel(db *gorm.DB, ch *po.PushChannel) (*mqttChannel, error) {
	cfg, err := parseConfig(ch.ConfigJSON)
	if err != nil {
		return nil, err
	}
	mch := &mqttChannel{
		id:       ch.ID,
		name:     ch.Name,
		cfg:      cfg,
		sig:      ch.ID + "|" + ch.Name + "|" + ch.ConfigJSON,
		clientID: resolveClientID(cfg.ClientID),
	}
	if cfg.spoolEnabled() {
		mch.spool = push.NewSqliteSpool(db, ch.ID)
	}
	return mch, nil
}

// TestConnectivity 同步建立一次连接验证 broker 可达与鉴权，成功后立即断开。
// 测试模式关闭自动重连/重试（否则连接失败会无限重试挂住测试），
// 不启动通道后台 goroutine，用于管理界面保存配置前的连通性校验。
func (ch *mqttChannel) TestConnectivity() error {
	opts := ch.buildClientOptions()
	opts.SetAutoReconnect(false)
	opts.SetConnectRetry(false)

	client := mqtt.NewClient(opts)
	token := client.Connect()
	defer client.Disconnect(0)
	if !token.WaitTimeout(testConnectTimeout) {
		return fmt.Errorf("mqtt: connect timed out after %s", testConnectTimeout)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt: connect failed: %w", err)
	}
	return nil
}

// Start 启动通道的发布 goroutine
func (ch *mqttChannel) Start() {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.running {
		return
	}
	ch.quit = make(chan struct{})
	ch.done = make(chan struct{})
	ch.outbox = make(chan push.PushBatch, outboxSize)
	ch.running = true

	// 本地缓存写协程立即启动：断连期间的批次随时可落盘
	if ch.spool != nil {
		ch.spoolCh = make(chan push.PushBatch, spoolChSize)
		ch.spoolDone = make(chan struct{})
		ch.drainNotify = make(chan struct{}, 1)
		go ch.spoolWriteLoop()
	}

	go ch.run()
}

// Stop 停止通道，等待发布 goroutine 退出
func (ch *mqttChannel) Stop() {
	ch.mu.Lock()
	if !ch.running {
		ch.mu.Unlock()
		return
	}
	ch.running = false
	close(ch.quit)
	ch.mu.Unlock()

	<-ch.done
	// 等待写协程排空在途批次（quit 后写协程会先把 spoolCh 中已入队的批次落盘再退出）
	if ch.spool != nil && ch.spoolDone != nil {
		<-ch.spoolDone
	}
}

// run 通道主循环：等待首次连接成功后，按配置起 N 个发布 worker 与补发协程并发消费
func (ch *mqttChannel) run() {
	defer close(ch.done)

	client := mqtt.NewClient(ch.buildClientOptions())

	token := client.Connect()
	if !ch.waitFirstConnect(token) {
		// 停止或连接中止：直接退出
		client.Disconnect(0)
		return
	}

	var wg sync.WaitGroup
	for i := 0; i < ch.cfg.publishWorkers(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch.publishLoop(client)
		}()
	}
	// 断网缓存的补发协程：连接成功后按入队序回放未发完的批次
	if ch.spoolEnabled() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch.spoolDrainLoop(client)
		}()
	}
	// 所有 worker 退出（quit 关闭）后断开连接
	wg.Wait()
	client.Disconnect(250)
}

// publishLoop 单个发布 worker：循环消费 outbox，直至通道停止。
// 发布失败（断连/超时等）时将该批次转入本地缓存，重连后补发，避免数据丢失。
func (ch *mqttChannel) publishLoop(client mqtt.Client) {
	for {
		select {
		case <-ch.quit:
			return
		case b := <-ch.outbox:
			if !ch.publish(client, b) && ch.spoolEnabled() {
				ch.spoolEnqueue(b)
			}
		}
	}
}

// waitFirstConnect 阻塞等待首次连接成功，同时监听停止信号。
//
// paho 开启 ConnectRetry 后，Connect 返回的 token 仅在连接成功或 Disconnect
// 后被终止时完成，因此不能裸 Wait()，需轮询以保证停止时能及时退出。
// 返回 false 表示已停止/连接被中止。
func (ch *mqttChannel) waitFirstConnect(token mqtt.Token) bool {
	for {
		select {
		case <-ch.quit:
			return false
		case <-time.After(200 * time.Millisecond):
			if token.WaitTimeout(50 * time.Millisecond) {
				if token.Error() != nil {
					// 连接被中止（如 Stop 触发 Disconnect）
					errMsg := token.Error().Error()
					logger.Error("%s", errMsg)
					ch.setConnected(false)
					ch.setLastErr(errMsg)
					return false
				}
				ch.setConnected(true)
				ch.setLastErr("")
				return true
			}
		}
	}
}

// publish 向配置的所有主题发布一批数据。
// 返回是否全部主题发布成功（断连/超时/错误均视为失败），
// 供补发协程决定该批次是删除还是保留重试。
func (ch *mqttChannel) publish(client mqtt.Client, b push.PushBatch) bool {
	if client == nil || !client.IsConnected() {
		ch.droppedCount.Add(1)
		return false
	}

	topics := buildTopics(ch.cfg.Topic, b.DeviceID)
	if len(topics) == 0 {
		ch.droppedCount.Add(1)
		return false
	}

	payload, err := marshalBatch(b.Records, b.CollectedAt)
	if err != nil {
		errMsg := fmt.Sprintf("marshal batch: %v", err)
		logger.Error("%s", errMsg)
		ch.setLastErr(errMsg)
		ch.droppedCount.Add(1)
		return false
	}

	now := time.Now()
	ok := true
	for _, topic := range topics {
		tok := client.Publish(topic, ch.cfg.QoS, false, payload)
		if !tok.WaitTimeout(publishTimeout) {
			errMsg := fmt.Sprintf("publish %s timed out", topic)
			logger.Error("%s", errMsg)
			ch.setLastErr(errMsg)
			ch.droppedCount.Add(1)
			ok = false
			continue
		}
		if err := tok.Error(); err != nil {
			errMsg := fmt.Sprintf("publish %s: %v", topic, err)
			logger.Error("%s", errMsg)
			ch.setLastErr(errMsg)
			ch.droppedCount.Add(1)
			ok = false
			continue
		}
		ch.mu.Lock()
		ch.lastPublish = now
		ch.lastSuccess = now
		ch.mu.Unlock()
		ch.publishCount.Add(1)
	}
	return ok
}

// Enqueue 非阻塞入队，绝不阻塞采集线程。
// 路由：
//   - 断连且启用本地缓存：直接落盘，避免进内存队列后被发布线程丢弃；
//   - 否则内存 outbox 快路径，满时启用本地缓存则溢写，未启用则丢弃最旧（兼容旧行为）。
func (ch *mqttChannel) Enqueue(b push.PushBatch) {
	// 断连且启用本地缓存：直接落盘
	if ch.spoolEnabled() && !ch.isConnected() {
		ch.spoolEnqueue(b)
		return
	}

	// 内存快路径
	select {
	case ch.outbox <- b:
		return
	default:
	}

	// 内存队列满：启用本地缓存则溢写，否则丢弃最旧
	if ch.spoolEnabled() {
		ch.spoolEnqueue(b)
		return
	}

	// 丢弃最旧、保留最新，被丢弃的批次计入 droppedCount
	select {
	case <-ch.outbox:
		ch.droppedCount.Add(1)
	default:
	}

	select {
	case ch.outbox <- b:
	default:
		ch.droppedCount.Add(1) // 仍满，丢弃本条
	}
}

// spoolEnabled 本地缓存是否实际启用（配置启用且已构建 spool）。
// nil spool（未启用）时 Enqueue 回退为纯内存丢最旧行为。
func (ch *mqttChannel) spoolEnabled() bool {
	return ch.spool != nil && ch.cfg.spoolEnabled()
}

// isConnected 返回当前连接状态（由 paho 连接/断开回调维护）
func (ch *mqttChannel) isConnected() bool {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return ch.connected
}

// spoolEnqueue 非阻塞将批次写入本地缓存写入缓冲。
// 缓冲满时丢弃最旧（同 outbox 策略），保证采集线程永不阻塞。
func (ch *mqttChannel) spoolEnqueue(b push.PushBatch) {
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
		ch.droppedCount.Add(1) // 仍满，丢弃本条
	}
}

// spoolWriteLoop 本地缓存写协程：消费 spoolCh 串行写入 SQLite。
// 退出前（quit 关闭）排空在途批次，保证停服/热更不丢已入队的批次。
func (ch *mqttChannel) spoolWriteLoop() {
	defer close(ch.spoolDone)

	for {
		select {
		case <-ch.quit:
			// 退出前排空在途写入
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

// spoolInsert 将批次写入本地缓存；达到上限后每 N 批批量裁剪最旧以约束磁盘占用。
// Count 为内存计数（无查询），裁剪由原逐批 DeleteOldest(1) 改为批量 DeleteOldest(N)，
// 摊薄满盘期的 SQLite 写放大。
func (ch *mqttChannel) spoolInsert(b push.PushBatch) {
	if ch.spool == nil {
		return
	}
	ch.spoolTrimTick++
	if ch.spoolTrimTick >= spoolTrimInterval {
		ch.spoolTrimTick = 0
		if count, err := ch.spool.Count(); err == nil && count >= int64(ch.cfg.spoolBatchCap()) {
			if err := ch.spool.DeleteOldest(spoolTrimChunk); err != nil {
				logger.Error("mqtt: spool trim oldest failed: %v", err)
			}
		}
	}
	if err := ch.spool.Insert(b); err != nil {
		errMsg := fmt.Sprintf("spool insert: %v", err)
		logger.Error("%s", errMsg)
		ch.setLastErr(errMsg)
	}
}

// spoolDrainLoop 断网缓存补发协程：连接可用时按入队序取回未发完的批次，
// 发布成功即删除；失败（断连/超时）保留待重连重试。
// 空缓存时阻塞等待事件（新批次落盘/重连）唤醒，无固定轮询；
// 兜底 timer（spoolDrainBackstop）仅防漏 ping，空闲时对 SQLite 零无效查询。
func (ch *mqttChannel) spoolDrainLoop(client mqtt.Client) {
	for {
		// 未连接：等待重连（OnConnect ping）或新批次落盘唤醒，不做无效查询
		if client == nil || !client.IsConnected() {
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
			// 空缓存：阻塞等待写入/重连事件
			if !ch.waitSpoolDrain() {
				return
			}
			continue
		}

		// 本窗全部发布成功且恰好取满（可能仍有积压）则立即续取，不 sleep；
		// 有发布失败或已到尾部则回等待，避免对故障 broker 空转
		if ch.drainWindow(client, pend) && len(pend) == spoolDrainWindow {
			continue
		}
	}
}

// drainWindow 按序发布窗口内批次，成功批次收集后合并删除（单条 DELETE）；
// 任一发布失败即停止，保留该批及后续待重连重试。返回是否本窗口全部发布成功。
func (ch *mqttChannel) drainWindow(client mqtt.Client, pend []push.SpoolBatch) bool {
	successIDs := make([]int64, 0, len(pend))
	for _, p := range pend {
		batch := push.PushBatch{DeviceID: p.DeviceID, CollectedAt: p.CollectedAt, Records: p.Records}
		if !ch.publish(client, batch) {
			break
		}
		successIDs = append(successIDs, p.ID)
	}
	if len(successIDs) > 0 {
		if err := ch.spool.DeleteBatch(successIDs); err != nil {
			logger.Error("mqtt: spool delete batch failed (n=%d): %v", len(successIDs), err)
		}
	}
	return len(successIDs) == len(pend)
}

// waitSpoolDrain 阻塞等待补发信号：新批次落盘/重连事件即时唤醒，
// 兜底 timer 仅防漏 ping。返回 false 表示通道已停止。
func (ch *mqttChannel) waitSpoolDrain() bool {
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

// signalDrain 非阻塞通知补发协程有新批次可补发（cap=1 自动合流，无信号时 no-op）。
func (ch *mqttChannel) signalDrain() {
	select {
	case ch.drainNotify <- struct{}{}:
	default:
	}
}

// Snapshot 返回通道状态快照（用于状态查询）
func (ch *mqttChannel) Snapshot() push.ChannelStatusVO {
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
		Type:         "mqtt",
		Running:      ch.running,
		Connected:    ch.connected,
		Broker:       ch.cfg.brokerURL(),
		Topic:        ch.cfg.Topic,
		QueueDepth:   uint64(len(ch.outbox)),
		SpoolDepth:   spoolDepth,
		PublishCount: ch.publishCount.Load(),
		DroppedCount: ch.droppedCount.Load(),
		LastPublish:  push.FormatTime(ch.lastPublish),
		LastSuccess:  push.FormatTime(ch.lastSuccess),
		LastErr:      ch.lastErr,
	}
}

func (ch *mqttChannel) setConnected(ok bool) {
	ch.mu.Lock()
	ch.connected = ok
	ch.mu.Unlock()
}

func (ch *mqttChannel) setLastErr(msg string) {
	ch.mu.Lock()
	ch.lastErr = msg
	ch.mu.Unlock()
}

// resolveClientID 生成实际使用的 clientId：配置值为可辨识前缀 + 通道级随机后缀，
// 新建通道/重启进程即得到唯一值，多实例或残留进程无法再因同 clientId 互相踢断。
// 基础前缀截断至 clientIDBaseCap，加后缀后总长 23 字节，兼容 MQTT 3.1 的 clientId 长度上限。
func resolveClientID(base string) string {
	if base == "" {
		base = "iot-gateway"
	}
	if len(base) > clientIDBaseCap {
		base = base[:clientIDBaseCap]
	}
	return base + "-" + utils.GenerateUUID()[:clientIDSuffixLen]
}

// buildClientOptions 构建 paho 客户端选项
func (ch *mqttChannel) buildClientOptions() *mqtt.ClientOptions {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(ch.cfg.brokerURL())
	opts.SetClientID(ch.clientID)
	if ch.cfg.Username != "" {
		opts.SetUsername(ch.cfg.Username)
		opts.SetPassword(ch.cfg.Password)
	}
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)
	opts.SetConnectTimeout(5 * time.Second)
	opts.SetMaxReconnectInterval(30 * time.Second)
	opts.SetOrderMatters(false)

	opts.SetOnConnectHandler(func(mqtt.Client) {
		ch.setConnected(true)
		ch.setLastErr("")
		ch.signalDrain() // 重连后可能积压断连期间落盘的批次，即时唤醒补发
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		errMsg := err.Error()
		logger.Error("%s", errMsg)
		ch.setConnected(false)
		ch.setLastErr(errMsg)
	})
	opts.SetReconnectingHandler(func(_ mqtt.Client, _ *mqtt.ClientOptions) {
		ch.setConnected(false)
	})
	return opts
}
