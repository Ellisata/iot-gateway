// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package messagechannel 是消息类推送通道的通用骨架。
//
// 它把「编码一次、发送一条或多条消息」的通道（HTTP/Webhook、未来的 Kafka）
// 从 Outbox/断网缓存/补发/状态统计中解耦出来；通道只需实现 Transport。
// MQTT 仍保留自己的 paho 循环（AutoReconnect 语义不同），不走这里。
package messagechannel

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"iot-gateway/logger"
	"iot-gateway/push"
)

// FailureKind 一次发送失败的语义。
type FailureKind int

const (
	// FailureNone 成功。
	FailureNone FailureKind = iota
	// FailurePoison 下游明确拒绝（4xx 等），重试无意义，丢弃。
	FailurePoison
	// FailureRetry 连接/超时类失败，保留并等待重连补发。
	FailureRetry
)

// Outcome Transport 对一次发送的判定。
type Outcome struct {
	Kind       FailureKind
	StatusCode int
	Err        error
}

// Transport 具体传输实现：探测可达性 + 发送一条载荷。
type Transport interface {
	Probe(ctx context.Context) error
	Send(ctx context.Context, payload []byte) Outcome
}

// Config 构造参数。
type Config struct {
	Tag, ID, Name, Type string
	Target, Detail      string // 状态快照的 Broker/Topic
	ConfigSig           string // 热加载签名：ID|Name|Type|ConfigJSON
	DB                  *gorm.DB
	Encoder             push.Encoder
	Transport           Transport
	Workers             int
	Timeout             time.Duration
	SpoolEnabled        bool
	SpoolCap            int
	ReconnectInterval   time.Duration
}

// Channel 通用消息通道，实现 push.Channel / push.Connectivity /
// push.Previewer / push.TestSender。
type Channel struct {
	cfg Config
	ob  *push.Outbox

	done chan struct{}
	ctx  context.Context
	stop context.CancelFunc

	mu      sync.Mutex
	running bool

	connected atomic.Bool
}

// New 创建通用消息通道（未启动）。
func New(cfg Config) *Channel {
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.ReconnectInterval <= 0 {
		cfg.ReconnectInterval = 5 * time.Second
	}
	c := &Channel{cfg: cfg}
	c.ob = push.NewOutbox(push.OutboxConfig{
		Tag:          cfg.Tag,
		ID:           cfg.ID,
		Name:         cfg.Name,
		DB:           cfg.DB,
		SpoolEnabled: cfg.SpoolEnabled,
		SpoolCap:     cfg.SpoolCap,
		Conn:         c,
	})
	return c
}

// ChannelID 实现 push.Channel。
func (c *Channel) ChannelID() string { return c.cfg.ID }

// ConfigSig 实现 push.Channel。
func (c *Channel) ConfigSig() string { return c.cfg.ConfigSig }

// Connected 实现 push.Connectivity。
func (c *Channel) Connected() bool { return c.connected.Load() }

// Enqueue 实现 push.Channel。
func (c *Channel) Enqueue(b push.PushBatch) { c.ob.Enqueue(b) }

// Start 实现 push.Channel。
func (c *Channel) Start() {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.done = make(chan struct{})
	c.ctx, c.stop = context.WithCancel(context.Background())
	c.mu.Unlock()

	c.ob.Start()
	go c.run()
}

// Stop 实现 push.Channel：先取消在途请求，再按 Outbox 的固定顺序收尾。
func (c *Channel) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	stop, done := c.stop, c.done
	c.mu.Unlock()

	if stop != nil {
		stop()
	}
	c.ob.Close()
	<-done
	c.ob.Wait()
}

func (c *Channel) run() {
	defer close(c.done)

	if c.probe() {
		c.setConnected(true)
		c.ob.Notify()
	} else {
		c.setConnected(false)
	}

	var wg sync.WaitGroup
	for i := 0; i < c.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.sendLoop()
		}()
	}
	if c.ob.SpoolEnabled() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.ob.DrainLoop(c.drainWindow, push.DrainPolicy{})
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.ob.ReconnectLoop(c.cfg.ReconnectInterval, c.tryConnect)
	}()
	wg.Wait()
}

func (c *Channel) sendLoop() {
	for {
		select {
		case <-c.ob.Quit():
			return
		case b := <-c.ob.Batches():
			c.sendBatch(b, false)
		}
	}
}

func (c *Channel) probe() bool {
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	if err := c.cfg.Transport.Probe(ctx); err != nil {
		c.ob.SetLastErr("probe: " + err.Error())
		return false
	}
	return true
}

func (c *Channel) tryConnect() {
	if c.Connected() {
		return
	}
	if !c.probe() {
		return
	}
	c.ob.SetLastErr("")
	c.setConnected(true)
	c.ob.Notify()
}

// sendBatch 编码并发送一个批次。fromDrain 表示该批次来自断网缓存。
func (c *Channel) sendBatch(b push.PushBatch, fromDrain bool) FailureKind {
	msgs, err := c.cfg.Encoder.Encode(b)
	if err != nil {
		c.fail("encode batch: "+err.Error(), fromDrain)
		return FailurePoison
	}
	for _, msg := range msgs {
		ctx, cancel := context.WithTimeout(c.ctx, c.cfg.Timeout)
		out := c.cfg.Transport.Send(ctx, msg.Payload)
		cancel()

		switch out.Kind {
		case FailureNone:
			c.ob.MarkPublished(1)
		case FailureRetry:
			c.fail(outcomeError(out), fromDrain)
			c.setConnected(false)
			if !fromDrain && c.ob.SpoolEnabled() {
				c.ob.SpoolEnqueue(b)
			}
			return FailureRetry
		default:
			c.fail(outcomeError(out), fromDrain)
			return FailurePoison
		}
	}
	if len(msgs) == 0 {
		c.ob.MarkPublished(1)
	}
	return FailureNone
}

func outcomeError(out Outcome) string {
	if out.Err != nil {
		return out.Err.Error()
	}
	return "send failed"
}

func (c *Channel) fail(msg string, fromDrain bool) {
	logger.Error("%s: %s", c.cfg.Tag, msg)
	c.ob.SetLastErr(msg)
	if !fromDrain {
		c.ob.Drop(1)
	}
}

// drainWindow 按入队序回放；毒批次立即删除，retry 批次保留并停止本窗。
func (c *Channel) drainWindow(pend []push.SpoolBatch, _ bool) (bool, int64) {
	success := make([]int64, 0, len(pend))
	deleteSuccess := func() {
		if len(success) == 0 {
			return
		}
		if err := c.ob.Spool().DeleteBatch(success); err != nil {
			logger.Error("%s: spool delete batch failed (n=%d): %v", c.cfg.Tag, len(success), err)
		}
	}
	for _, p := range pend {
		b := push.PushBatch{DeviceID: p.DeviceID, CollectedAt: p.CollectedAt, Records: p.Records}
		switch c.sendBatch(b, true) {
		case FailureNone:
			success = append(success, p.ID)
		case FailurePoison:
			// 下游明确拒绝：删除该行，避免队头永久阻塞；继续处理后续批次。
			success = append(success, p.ID)
			c.ob.Drop(1)
			logger.Error("%s: spool batch %d rejected permanently, dropped", c.cfg.Tag, p.ID)
		default:
			deleteSuccess()
			return false, p.ID
		}
	}
	deleteSuccess()
	return true, 0
}

// Snapshot 实现 push.Channel。
func (c *Channel) Snapshot() push.ChannelStatusVO {
	vo := push.ChannelStatusVO{
		Type:   c.cfg.Type,
		Broker: c.cfg.Target,
		Topic:  c.cfg.Detail,
	}
	c.ob.FillStatus(&vo)
	return vo
}

// TestConnectivity 实现 push.Channel。
func (c *Channel) TestConnectivity() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.cfg.Transport.Probe(ctx)
}

// Preview 实现 push.Previewer。
func (c *Channel) Preview(b push.PushBatch) push.PreviewResult {
	return push.PreviewWithEncoder(c.cfg.Encoder, b)
}

// TestSend 实现 push.TestSender：真的发送一次，不触碰 outbox/计数。
func (c *Channel) TestSend(b push.PushBatch) push.TestSendResult {
	start := time.Now()
	msgs, err := c.cfg.Encoder.Encode(b)
	if err != nil {
		return push.TestSendResult{Error: "编码失败: " + err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	res := push.TestSendResult{Success: true, Messages: len(msgs)}
	for _, msg := range msgs {
		ctx, cancel := context.WithTimeout(context.Background(), c.cfg.Timeout)
		out := c.cfg.Transport.Send(ctx, msg.Payload)
		cancel()
		res.StatusCode = out.StatusCode
		if out.Kind != FailureNone {
			res.Success = false
			res.Error = outcomeError(out)
			break
		}
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

func (c *Channel) setConnected(ok bool) { c.connected.Store(ok) }
