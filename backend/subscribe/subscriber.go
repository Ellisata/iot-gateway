// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"iot-gateway/collector"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/parser"
	"iot-gateway/utils"
)

const (
	// connectTimeout 单次建连等待上限。paho 在 ConnectRetry=true 时会持续重试，
	// 这里只约束「首次建连结果的日志观察窗口」，不决定是否继续重试。
	connectTimeout = 5 * time.Second
	// testConnectTimeout 连通性测试等待上限（略大于 connectTimeout）。
	testConnectTimeout = 6 * time.Second
	// subscribeTimeout 建连后 SUBSCRIBE 应答等待上限
	subscribeTimeout = 5 * time.Second

	// clientIDBaseCap 基础前缀截断长度：拼接随机后缀后整体控制在 23 字节内，
	// 兼容 MQTT 3.1 常见的 clientId 长度上限。
	clientIDBaseCap = 12
	// clientIDSuffixLen 运行时追加的随机后缀长度（hex 字符数）。
	clientIDSuffixLen = 10

	// subscriptionQueueSize 待解析报文缓冲区。满时丢最旧、计 dropped，
	// 绝不阻塞 paho 收包协程。
	subscriptionQueueSize = 8192
	// subscriptionBatchSize 单次交给解析器的报文数上限。
	subscriptionBatchSize = 256
	// parseBatchTimeout 单批解析的兜底超时。脚本层还有逐报文超时，
	// 这里的值只需防住「解析器整体卡死」。
	parseBatchTimeout = 30 * time.Second

	// controlQueueSize 控制指令缓冲区。控制是低频动作，队列只用于把
	// paho 收包协程与「写 SQLite + 等 PLC 应答」解耦，不需要很大。
	controlQueueSize = 256
	// controlWriteTimeout 单条 MQTT 控制指令的处理上限。
	// 必须小于 MQTT 客户端的 keepalive/会话超时，也不应比网关控制 API 更宽。
	controlWriteTimeout = 20 * time.Second
)

// Sink 订阅数据的下游出口。
//
// 只暴露「投递到时序数据库类通道」：MQTT 订阅的语义是把外部数据落进时序库，
// 不应再经推送引擎里的 MQTT 通道回环发布（订阅与发布主题相同时会形成自激）。
type Sink interface {
	PushRecordsToTimeSeries(records []collector.CollectedRecord)
}

// subscriber 单个订阅的运行时：一个独立的 paho 客户端 + 一个有界解析队列。
//
// 收包协程只把报文放进队列；解析在独立 worker 中按批完成 —— 脚本/进程外解析器
// 不再是「纯 CPU、可以在回调里做完」的映射。同一 worker 保证批内顺序。
type subscriber struct {
	cfg  *subscription
	sink Sink
	// info 设备/点位元信息落库器，可为 nil（管理器未传或库不可用）。
	info devicePointSink
	// ctrl 控制下发出口，可为 nil（未传控制服务时只记错误，不 panic）。
	ctrl ControlWriter

	client mqtt.Client

	running   atomic.Bool
	connected atomic.Bool
	// stopping 停机/热加载重建期间置位：控制 worker 丢弃积压指令，避免 Stop
	// 逐条等 PLC 应答。与 running 分开是因为测试直接构造 subscriber 时不调 start，
	// running 为 false 并不代表要丢弃控制指令。
	stopping atomic.Bool

	messages atomic.Uint64
	records  atomic.Uint64
	errors   atomic.Uint64
	skipped  atomic.Uint64
	dropped  atomic.Uint64

	lastErr     atomic.Value // string
	lastMessage atomic.Value // string

	// recent 最近收到的报文环形缓冲：收包协程写、HTTP 读，仅内存。
	recent *recentMessageRing

	queue       chan parser.Message
	queueMu     sync.RWMutex
	queueClosed bool
	pending     atomic.Int64
	workerDone  chan struct{}

	// 控制指令独立队列：控制是低频动作，不能与遥测报文抢同一个解析队列；
	// 也不能在 paho 收包协程里直接写 SQLite / 等 PLC 应答。
	control           chan controlMessage
	controlMu         sync.RWMutex
	controlClosed     bool
	controlPending    atomic.Int64
	controlWorkerDone chan struct{}

	parserMu  sync.RWMutex
	prs       parser.Parser
	parserSig string

	stopOnce sync.Once
}

// controlMessage 一条待下发的控制指令。
type controlMessage struct {
	topic      string
	payload    []byte
	receivedAt time.Time
}

func newSubscriber(cfg *subscription, sink Sink, info devicePointSink, ctrl ControlWriter) *subscriber {
	s := &subscriber{
		cfg:               cfg,
		sink:              sink,
		info:              info,
		ctrl:              ctrl,
		prs:               cfg.prs,
		parserSig:         cfg.parserSig,
		recent:            newRecentMessageRing(recentMessageRingSize),
		queue:             make(chan parser.Message, subscriptionQueueSize),
		workerDone:        make(chan struct{}),
		control:           make(chan controlMessage, controlQueueSize),
		controlWorkerDone: make(chan struct{}),
	}
	s.lastErr.Store("")
	s.lastMessage.Store("")
	go s.runWorker()
	go s.runControlWorker()
	return s
}

// ConnSig 返回连接参数签名，供热加载判断是否需要重连。
func (s *subscriber) ConnSig() string { return s.cfg.connSig }

// ParserSig 返回解析配置签名。连接不变时只换解析器，不断开 MQTT。
func (s *subscriber) ParserSig() string {
	s.parserMu.RLock()
	defer s.parserMu.RUnlock()
	return s.parserSig
}

// swapParser 原地替换解析器。worker 在整批 Parse 期间持读锁，
// 因此旧实例被 Close 时不会再有调用。
func (s *subscriber) swapParser(next parser.Parser, sig string) error {
	if next == nil {
		return fmt.Errorf("解析器为空")
	}
	s.parserMu.Lock()
	old := s.prs
	s.prs = next
	s.parserSig = sig
	s.parserMu.Unlock()
	if old != nil {
		return old.Close()
	}
	return nil
}

// start 建立连接并订阅，调用方应放在独立 goroutine 中执行。
func (s *subscriber) start() {
	s.running.Store(true)

	opts := mqtt.NewClientOptions()
	opts.AddBroker(s.cfg.brokerURL())
	opts.SetClientID(resolveClientID(s.cfg.clientID))
	if s.cfg.username != "" {
		opts.SetUsername(s.cfg.username)
		opts.SetPassword(s.cfg.password)
	}
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)
	opts.SetConnectTimeout(connectTimeout)
	opts.SetMaxReconnectInterval(30 * time.Second)
	// 脚本可以持有跨报文状态，必须保证收包顺序。回调只做入队，代价可忽略。
	opts.SetOrderMatters(true)

	opts.SetOnConnectHandler(func(c mqtt.Client) {
		s.connected.Store(true)
		s.setErr("")
		// 一次 SUBSCRIBE 覆盖逗号分隔的全部过滤器；单 topic 也走同一路径。
		token := c.SubscribeMultiple(s.cfg.topicFilters(), s.onMessage)
		go func() {
			if !token.WaitTimeout(subscribeTimeout) {
				s.setErr("订阅应答超时")
				logger.Warn("mqtt subscription %q: SUBSCRIBE %s timed out", s.cfg.name, s.cfg.topic)
				return
			}
			if err := token.Error(); err != nil {
				s.setErr(err.Error())
				logger.Error("mqtt subscription %q: SUBSCRIBE %s failed: %v", s.cfg.name, s.cfg.topic, err)
			}
		}()

		// 控制主题单独订阅、单独回调：不能混进数据解析器。
		if ct := s.cfg.controlTopic; ct != "" {
			ctoken := c.Subscribe(ct, s.cfg.qos, s.onControlMessage)
			go func() {
				if !ctoken.WaitTimeout(subscribeTimeout) {
					s.setErr("控制主题订阅应答超时")
					logger.Warn("mqtt subscription %q: SUBSCRIBE control topic %s timed out", s.cfg.name, ct)
					return
				}
				if err := ctoken.Error(); err != nil {
					s.setErr(err.Error())
					logger.Error("mqtt subscription %q: SUBSCRIBE control topic %s failed: %v", s.cfg.name, ct, err)
				}
			}()
		}
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		s.connected.Store(false)
		if err != nil {
			s.setErr(err.Error())
			logger.Error("mqtt subscription %q: connection lost: %v", s.cfg.name, err)
		}
	})
	opts.SetReconnectingHandler(func(_ mqtt.Client, _ *mqtt.ClientOptions) {
		s.connected.Store(false)
	})

	s.client = mqtt.NewClient(opts)

	// Connect 在 ConnectRetry=true 时由 paho 在后台持续重试；这里只观察首次结果。
	token := s.client.Connect()
	go func() {
		if !token.WaitTimeout(connectTimeout) {
			s.setErr("连接超时，仍在后台重试")
			return
		}
		if err := token.Error(); err != nil {
			s.setErr(err.Error())
			logger.Error("mqtt subscription %q: connect %s failed: %v", s.cfg.name, s.cfg.endpoint(), err)
		}
	}()
}

// stop 断开连接、排空解析队列并关闭解析器。
func (s *subscriber) stop() {
	s.stopOnce.Do(func() {
		s.stopping.Store(true)
		s.running.Store(false)
		if s.client != nil {
			s.client.Disconnect(250)
		}
		s.connected.Store(false)

		// 先关队列：持写锁等待所有在飞入队完成，避免向已关闭 channel 发送。
		s.queueMu.Lock()
		if !s.queueClosed {
			s.queueClosed = true
			close(s.queue)
		}
		s.queueMu.Unlock()
		<-s.workerDone

		// 控制队列同样先关再等：worker 退出前会把已入队的指令处理完。
		s.controlMu.Lock()
		if !s.controlClosed {
			s.controlClosed = true
			close(s.control)
		}
		s.controlMu.Unlock()
		<-s.controlWorkerDone

		s.parserMu.Lock()
		p := s.prs
		s.prs = nil
		s.parserMu.Unlock()
		if p != nil {
			_ = p.Close()
		}
	})
}

// onMessage 处理一条订阅报文。
//
// 运行在 paho 的收包协程中：只做大小检查与入队，不做任何可能阻塞的 I/O 或解析。
func (s *subscriber) onMessage(_ mqtt.Client, msg mqtt.Message) {
	s.messages.Add(1)

	payload := msg.Payload()
	receivedAt := time.Now()
	oversized := len(payload) > parser.DefaultMaxPayloadBytes

	// 收包即记录：即使接下来因超限 / 解析失败 / 队列满被丢弃，也能在页面上看到。
	s.recordRecent(msg, payload, receivedAt, oversized)

	if oversized {
		s.errors.Add(1)
		s.setErr(fmt.Sprintf("报文 %d 字节，超过上限 %d", len(payload), parser.DefaultMaxPayloadBytes))
		logger.Warn("mqtt subscription %q: topic %s 报文过大（%d 字节），已丢弃",
			s.cfg.name, msg.Topic(), len(payload))
		return
	}

	// 回调返回后 worker 才会解析，复制一份 payload，避免依赖 paho 的缓冲生命周期。
	buf := make([]byte, len(payload))
	copy(buf, payload)

	s.enqueue(parser.Message{
		Topic:      msg.Topic(),
		Payload:    buf,
		ReceivedAt: receivedAt,
		QoS:        msg.Qos(),
		Retained:   msg.Retained(),
	})
}

// onControlMessage 处理一条控制主题报文。运行在 paho 收包协程中：
// 只做大小检查与入队，真正的写 PLC 在独立 worker 中执行。
func (s *subscriber) onControlMessage(_ mqtt.Client, msg mqtt.Message) {
	s.messages.Add(1)

	payload := msg.Payload()
	receivedAt := time.Now()
	oversized := len(payload) > parser.DefaultMaxPayloadBytes
	s.recordRecent(msg, payload, receivedAt, oversized)

	if oversized {
		s.errors.Add(1)
		s.setErr(fmt.Sprintf("控制报文 %d 字节，超过上限 %d", len(payload), parser.DefaultMaxPayloadBytes))
		logger.Warn("mqtt subscription %q: control topic %s 报文过大（%d 字节），已丢弃",
			s.cfg.name, msg.Topic(), len(payload))
		return
	}

	buf := make([]byte, len(payload))
	copy(buf, payload)
	s.enqueueControl(controlMessage{topic: msg.Topic(), payload: buf, receivedAt: receivedAt})
}

// enqueueControl 非阻塞入队；满时丢最旧，绝不让控制指令阻塞 paho 收包协程。
func (s *subscriber) enqueueControl(msg controlMessage) {
	s.controlMu.RLock()
	defer s.controlMu.RUnlock()
	if s.controlClosed {
		return
	}

	s.controlPending.Add(1)
	select {
	case s.control <- msg:
		return
	default:
	}
	select {
	case <-s.control:
		s.controlPending.Add(-1)
		s.dropped.Add(1)
	default:
	}
	select {
	case s.control <- msg:
	default:
		s.controlPending.Add(-1)
		s.dropped.Add(1)
	}
}

// runControlWorker 串行处理控制指令。串行是刻意的：同一条订阅上的控制
// 指令不应并发打到 PLC，否则设备侧看到的顺序与报文到达顺序无关。
func (s *subscriber) runControlWorker() {
	defer close(s.controlWorkerDone)
	for msg := range s.control {
		// 停机/热加载重连时不再下发积压的控制指令：它们可能已经过时，
		// 而逐条等 PLC 应答会让 Stop 拖到十几秒。已在执行的那一条会自然结束。
		if s.stopping.Load() {
			s.controlPending.Add(-1)
			continue
		}
		func() {
			// pending 在整条指令处理完（含等 PLC 应答）后才减一，waitIdle
			// 因此能真正等到控制写入结束，而不是刚出队就返回。
			defer s.controlPending.Add(-1)
			s.handleControl(msg)
		}()
	}
}

func (s *subscriber) handleControl(msg controlMessage) {
	var p controlPayload
	if err := json.Unmarshal(msg.payload, &p); err != nil {
		s.errors.Add(1)
		s.setErr("控制报文不是合法 JSON: " + err.Error())
		logger.Warn("mqtt subscription %q: control topic %s 报文解析失败: %v",
			s.cfg.name, msg.topic, err)
		return
	}

	items := p.Items
	if len(items) == 0 && p.AddressID != "" {
		items = []dto.ControlItemDTO{{DeviceAddressID: p.AddressID, Value: p.Value}}
	}
	if p.DeviceID == "" || len(items) == 0 {
		s.errors.Add(1)
		s.setErr("控制报文缺少 deviceId 或点位")
		logger.Warn("mqtt subscription %q: control topic %s 缺少 deviceId/items",
			s.cfg.name, msg.topic)
		return
	}
	if p.RequestID == "" {
		// MQTT QoS1 会重投；没有幂等键时一次重投就会再写一遍 PLC。
		// 宁可拒绝，也不接受"无法安全重试"的控制报文。
		s.errors.Add(1)
		s.setErr("控制报文缺少 requestId（MQTT 重投需要幂等键）")
		logger.Warn("mqtt subscription %q: control topic %s 缺少 requestId，已拒绝",
			s.cfg.name, msg.topic)
		return
	}
	if s.ctrl == nil {
		s.errors.Add(1)
		s.setErr("控制服务未启用")
		logger.Warn("mqtt subscription %q: 收到控制报文但控制服务未启用", s.cfg.name)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), controlWriteTimeout)
	res, err := s.ctrl.Write(ctx, &dto.ControlDTO{
		DeviceID:  p.DeviceID,
		RequestID: p.RequestID,
		Verify:    p.Verify,
		Items:     items,
	}, "mqtt", "mqtt:"+s.cfg.name)
	cancel()

	if err != nil {
		s.errors.Add(1)
		s.setErr(err.Error())
		logger.Error("mqtt subscription %q: control write device=%s request=%s failed: %v",
			s.cfg.name, p.DeviceID, p.RequestID, err)
		return
	}

	failed := 0
	for _, r := range res.Results {
		if !r.OK {
			failed++
		}
	}
	if failed > 0 {
		s.errors.Add(uint64(failed))
		s.setErr(fmt.Sprintf("%d/%d 个控制点写入失败", failed, len(res.Results)))
		logger.Warn("mqtt subscription %q: control write device=%s request=%s, %d/%d points failed",
			s.cfg.name, p.DeviceID, p.RequestID, failed, len(res.Results))
		return
	}
	s.lastMessage.Store(time.Now().Format("2006-01-02 15:04:05"))
	logger.Info("mqtt subscription %q: control write device=%s request=%s items=%d ok",
		s.cfg.name, p.DeviceID, p.RequestID, len(items))
}

// recordRecent 保存一条最近报文快照。运行在 paho 收包协程中：
// 只做一次至多 2 KiB 的预览拷贝与一次环形写入，不阻塞、不写库。
func (s *subscriber) recordRecent(msg mqtt.Message, payload []byte, at time.Time, oversized bool) {
	preview, truncated := previewPayload(payload)
	s.recent.add(RecentMessage{
		Topic:      msg.Topic(),
		Payload:    preview,
		Size:       len(payload),
		Truncated:  truncated,
		Oversized:  oversized,
		ReceivedAt: at,
		QoS:        msg.Qos(),
		Retained:   msg.Retained(),
	})
}

// recentMessages 返回该订阅最近收到的报文（新→旧）。
func (s *subscriber) recentMessages() []RecentMessage { return s.recent.snapshot() }

// enqueue 非阻塞入队。队列满时丢最旧的一条并计数 —— 遥测优先保新鲜度，
// 绝不把 paho 收包协程卡住（会反过来把整个 broker 会话的 QoS 流控拖死）。
func (s *subscriber) enqueue(msg parser.Message) {
	s.queueMu.RLock()
	defer s.queueMu.RUnlock()
	if s.queueClosed {
		return
	}

	s.pending.Add(1)
	select {
	case s.queue <- msg:
		return
	default:
	}

	// 丢最旧，为新报文腾位置。
	select {
	case <-s.queue:
		s.pending.Add(-1)
		s.dropped.Add(1)
	default:
	}
	select {
	case s.queue <- msg:
	default:
		s.pending.Add(-1)
		s.dropped.Add(1)
	}
}

// runWorker 串行消费队列，保证同一订阅的报文按到达顺序解析。
func (s *subscriber) runWorker() {
	defer close(s.workerDone)
	for first := range s.queue {
		s.processBatch(s.collectBatch(first))
	}
}

// collectBatch 在不阻塞的前提下尽量多收几条，降低进程外解析器将来按批 RPC 的开销。
func (s *subscriber) collectBatch(first parser.Message) []parser.Message {
	batch := make([]parser.Message, 1, subscriptionBatchSize)
	batch[0] = first
	for len(batch) < subscriptionBatchSize {
		select {
		case msg, ok := <-s.queue:
			if !ok {
				return batch
			}
			batch = append(batch, msg)
		default:
			return batch
		}
	}
	return batch
}

func (s *subscriber) processBatch(batch []parser.Message) {
	defer s.pending.Add(-int64(len(batch)))

	s.parserMu.RLock()
	p := s.prs
	s.parserMu.RUnlock()
	if p == nil {
		s.errors.Add(uint64(len(batch)))
		s.setErr("解析器未初始化")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), parseBatchTimeout)
	res, err := p.Parse(ctx, batch)
	cancel()

	if err != nil {
		s.errors.Add(uint64(len(batch)))
		s.setErr(err.Error())
		logger.Error("mqtt subscription %q: 解析 %d 条报文失败: %v", s.cfg.name, len(batch), err)
		return
	}
	if len(res.Errors) > 0 {
		s.errors.Add(uint64(len(res.Errors)))
		s.setErr(res.Errors[0].Reason)
		logger.Warn("mqtt subscription %q: %d/%d 条报文解析失败，首条原因: %s",
			s.cfg.name, len(res.Errors), len(batch), res.Errors[0].Reason)
	}
	if res.Skipped > 0 {
		s.skipped.Add(uint64(res.Skipped))
		logger.Warn("mqtt subscription %q: 跳过 %d 条转换失败的记录", s.cfg.name, res.Skipped)
	}
	if len(res.Records) == 0 {
		return
	}

	if s.sink != nil {
		s.sink.PushRecordsToTimeSeries(res.Records)
	}
	if s.info != nil {
		// 非阻塞：只在内存里查重后入队，写 SQLite 由落库协程攒批完成。
		s.info.Record(res.Records)
	}
	s.records.Add(uint64(len(res.Records)))
	s.lastMessage.Store(time.Now().Format("2006-01-02 15:04:05"))
}

// waitIdle 等待所有已入队报文处理完毕（测试用）。
func (s *subscriber) waitIdle(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for s.pending.Load() > 0 || s.controlPending.Load() > 0 {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	return true
}

func (s *subscriber) setErr(msg string) {
	s.lastErr.Store(msg)
}

func (s *subscriber) lastError() string {
	if v, ok := s.lastErr.Load().(string); ok {
		return v
	}
	return ""
}

func (s *subscriber) lastMessageTime() string {
	if v, ok := s.lastMessage.Load().(string); ok {
		return v
	}
	return ""
}

// resolveClientID 生成唯一的 clientId：配置值前缀 + 随机后缀。
// 多实例或残留进程使用相同配置时不会被 broker 互相踢下线。
func resolveClientID(base string) string {
	if base == "" {
		base = "iot-gateway-sub"
	}
	if len(base) > clientIDBaseCap {
		base = base[:clientIDBaseCap]
	}
	return base + "-" + utils.GenerateUUID()[:clientIDSuffixLen]
}

// testConnect 建立一次连接验证 broker 可达与鉴权，成功后立即断开。
// 测试模式关闭自动重连/重试，避免连接失败后无限重试挂住测试请求。
func testConnect(cfg *subscription) error {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(cfg.brokerURL())
	opts.SetClientID(resolveClientID(cfg.clientID))
	if cfg.username != "" {
		opts.SetUsername(cfg.username)
		opts.SetPassword(cfg.password)
	}
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(false)
	opts.SetConnectRetry(false)
	opts.SetConnectTimeout(connectTimeout)

	client := mqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(testConnectTimeout) {
		return errConnectTimeout
	}
	if err := token.Error(); err != nil {
		return err
	}
	client.Disconnect(0)
	return nil
}
