// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"iot-gateway/alarm"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// webhook 单个 webhook 的运行时：有界队列 + 单 worker 协程 + 指数退避重试。
//
// 队列满时丢弃最旧事件（与 push.Outbox 同策略）：宁可丢历史报警，也要保证
// 最新报警能发出去。失败重试耗尽只记日志并计数，不落盘（报警时效性强，
// 几小时后的补发没有意义）。
type webhook struct {
	id     string
	name   string
	typ    string
	cfgSig string // 配置签名，热加载时用于判断是否需要重建实例
	format Formatter
	send   *sender

	maxAttempts    int
	initialBackoff time.Duration
	maxBackoff     time.Duration

	q         chan alarm.Event
	quit      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	mu       sync.RWMutex
	drainBy  time.Time // 停机排空截止时刻
	lastSent string
	lastOK   string
	lastErr  string

	sent    atomic.Uint64
	failed  atomic.Uint64
	dropped atomic.Uint64
}

// newWebhook 按配置构建 webhook 运行时（未启动）；配置非法时返回错误。
// send 为多个 webhook 共享的投递器（复用同一 http.Client 连接池）。
func newWebhook(shared *sender, opts Options, cfg *po.AlarmWebhook) (*webhook, error) {
	formatter, err := NewFormatter(cfg)
	if err != nil {
		return nil, err
	}
	return &webhook{
		id:             cfg.ID,
		name:           cfg.Name,
		typ:            cfg.Type,
		cfgSig:         configSig(cfg),
		format:         formatter,
		send:           shared,
		maxAttempts:    opts.MaxAttempts,
		initialBackoff: opts.InitialBackoff,
		maxBackoff:     opts.MaxBackoff,
		q:              make(chan alarm.Event, opts.QueueSize),
		quit:           make(chan struct{}),
		done:           make(chan struct{}),
	}, nil
}

// configSig 计算配置签名：任一会影响发送行为的字段变化都会改变签名。
func configSig(cfg *po.AlarmWebhook) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%s|%d",
		cfg.ID, cfg.Name, cfg.Type, cfg.URL, cfg.Secret,
		cfg.MsgType, cfg.AtAll, cfg.AtList, cfg.Status)
}

// start 启动 worker 协程。
func (w *webhook) start() {
	go w.run()
}

// stop 关闭队列并要求排空，阻塞至 worker 退出（幂等）。
// drainBy 为排空截止时刻，超时剩余事件被丢弃并计数。
func (w *webhook) stop(drainBy time.Time) {
	w.mu.Lock()
	w.drainBy = drainBy
	w.mu.Unlock()

	w.closeOnce.Do(func() { close(w.quit) })
	<-w.done
}

// enqueue 非阻塞入队；队列满时丢最旧一条再入队，绝不阻塞调用方
// （调用方是 fanout 协程，阻塞它会拖慢所有 webhook 的分发）。
func (w *webhook) enqueue(ev alarm.Event) {
	select {
	case w.q <- ev:
		return
	default:
	}
	// 队列满：腾出最旧的一条给最新事件
	select {
	case <-w.q:
		w.dropped.Add(1)
	default:
	}
	select {
	case w.q <- ev:
	default:
		w.dropped.Add(1)
	}
}

// run worker 主循环。
func (w *webhook) run() {
	defer close(w.done)
	for {
		select {
		case ev := <-w.q:
			w.deliver(ev, true)
		case <-w.quit:
			w.drain()
			return
		}
	}
}

// drain 停机排空：对剩余事件各做一次投递尝试（不重试），到截止时刻放弃。
func (w *webhook) drain() {
	w.mu.RLock()
	deadline := w.drainBy
	w.mu.RUnlock()

	abandoned := 0
	for {
		select {
		case ev := <-w.q:
			if !deadline.IsZero() && time.Now().After(deadline) {
				abandoned++
				continue
			}
			w.deliver(ev, false)
		default:
			if abandoned > 0 {
				w.dropped.Add(uint64(abandoned))
				logger.Warn("notify: webhook %s 停机排空超时，丢弃 %d 条待发报警", w.name, abandoned)
			}
			return
		}
	}
}

// deliver 投递单条事件：失败按指数退避重试，直至成功、判定永久失败或重试耗尽。
// allowRetry 为 false 时只尝试一次（停机排空阶段）。
func (w *webhook) deliver(ev alarm.Event, allowRetry bool) {
	backoff := w.initialBackoff

	for attempt := 1; ; attempt++ {
		// 每次尝试都重新 Format：加签含时间戳，必须在发送时刻生成
		req, err := w.format.Format(ev)
		if err == nil {
			err = w.send.send(req)
		}
		if err == nil {
			w.markSent()
			return
		}

		w.setLastErr(err.Error())

		if !allowRetry || !isRetriable(err) || attempt >= w.maxAttempts {
			w.failed.Add(1)
			logger.Error("notify: webhook %s 投递报警失败（第 %d 次尝试后放弃）alarmID=%s: %v",
				w.name, attempt, ev.AlarmID, err)
			return
		}

		select {
		case <-w.quit:
			// 停机中：放弃剩余重试，事件由排空阶段兜底或丢弃
			w.failed.Add(1)
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, w.maxBackoff)
	}
}

// markSent 记录一次成功投递。
func (w *webhook) markSent() {
	now := time.Now().Format("2006-01-02 15:04:05")
	w.mu.Lock()
	w.lastSent = now
	w.lastOK = now
	w.lastErr = ""
	w.mu.Unlock()
	w.sent.Add(1)
}

// setLastErr 记录最近一次错误（同时刷新最近尝试时刻）。
func (w *webhook) setLastErr(msg string) {
	w.mu.Lock()
	w.lastSent = time.Now().Format("2006-01-02 15:04:05")
	w.lastErr = msg
	w.mu.Unlock()
}

// snapshot 返回运行状态快照。
func (w *webhook) snapshot() WebhookStatusVO {
	w.mu.RLock()
	lastSent, lastOK, lastErr := w.lastSent, w.lastOK, w.lastErr
	w.mu.RUnlock()

	return WebhookStatusVO{
		ID:              w.id,
		Name:            w.name,
		Type:            w.typ,
		QueueDepth:      uint64(len(w.q)),
		SentCount:       w.sent.Load(),
		FailedCount:     w.failed.Load(),
		DroppedCount:    w.dropped.Load(),
		LastSentTime:    lastSent,
		LastSuccessTime: lastOK,
		LastErr:         lastErr,
	}
}
