// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"iot-gateway/collector"
	"iot-gateway/logger"
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
)

// Sink 订阅数据的下游出口。
//
// 只暴露「投递到时序数据库类通道」：MQTT 订阅的语义是把外部数据落进时序库，
// 不应再经推送引擎里的 MQTT 通道回环发布（订阅与发布主题相同时会形成自激）。
type Sink interface {
	PushRecordsToTimeSeries(records []collector.CollectedRecord)
}

// subscriber 单个订阅的运行时：一个独立的 paho 客户端 + 一套映射规则。
type subscriber struct {
	cfg  *subscription
	sink Sink
	// info 设备/点位元信息落库器，可为 nil（管理器未传或库不可用）。
	info devicePointSink

	client mqtt.Client

	running   atomic.Bool
	connected atomic.Bool

	messages atomic.Uint64
	records  atomic.Uint64
	errors   atomic.Uint64

	lastErr     atomic.Value // string
	lastMessage atomic.Value // string

	stopOnce sync.Once
}

func newSubscriber(cfg *subscription, sink Sink, info devicePointSink) *subscriber {
	s := &subscriber{cfg: cfg, sink: sink, info: info}
	s.lastErr.Store("")
	s.lastMessage.Store("")
	return s
}

// Sig 返回配置签名，供热加载判断订阅配置是否变化。
func (s *subscriber) Sig() string { return s.cfg.sig }

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
	opts.SetOrderMatters(false)

	opts.SetOnConnectHandler(func(c mqtt.Client) {
		s.connected.Store(true)
		s.setErr("")
		token := c.Subscribe(s.cfg.topic, s.cfg.qos, s.onMessage)
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

// stop 断开连接并停止重连。
func (s *subscriber) stop() {
	s.stopOnce.Do(func() {
		s.running.Store(false)
		if s.client != nil {
			s.client.Disconnect(250)
		}
		s.connected.Store(false)
	})
}

// onMessage 处理一条订阅报文。
//
// 运行在 paho 的收包协程中：映射是纯 CPU 计算，投递走推送引擎的非阻塞入队，
// 因此这里不做任何可能阻塞的 I/O。
func (s *subscriber) onMessage(_ mqtt.Client, msg mqtt.Message) {
	s.messages.Add(1)

	result, err := s.cfg.rule.Convert(msg.Payload())
	if err != nil {
		s.errors.Add(1)
		s.setErr(err.Error())
		logger.Warn("mqtt subscription %q: topic %s 转换失败: %v", s.cfg.name, msg.Topic(), err)
		return
	}
	if result.Skipped > 0 {
		s.setErr(result.FirstError.Error())
		logger.Warn("mqtt subscription %q: 跳过 %d 条转换失败的记录: %v",
			s.cfg.name, result.Skipped, result.FirstError)
	}
	if len(result.Records) == 0 {
		return
	}
	if s.sink != nil {
		s.sink.PushRecordsToTimeSeries(result.Records)
	}
	if s.info != nil {
		// 非阻塞：只在内存里查重后入队，写 SQLite 由落库协程攒批完成。
		s.info.Record(result.Records)
	}
	s.records.Add(uint64(len(result.Records)))
	s.lastMessage.Store(time.Now().Format("2006-01-02 15:04:05"))
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
