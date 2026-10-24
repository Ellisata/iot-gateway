// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package opcuaserver 把网关采集到的数据暴露成一个 OPC UA Server
// （推送通道类型 "opcua"）。
//
// 与 MQTT/Webhook 等消息类通道不同，OPC UA 是「客户端 Read / 订阅」的
// 状态型协议：本通道不编码载荷、不做断网补发，只维护一棵地址空间和每个
// 点位的最新值。采集记录到达后覆盖节点值，并在值/质量变化时通知订阅者。
package opcuaserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"gorm.io/gorm"

	"iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/push"
)

const (
	channelType = "opcua"

	// enqueueDepth 采集批次队列长度。一批最多 500 条记录（push 引擎分片），
	// 1024 批足够吸收客户端慢/订阅阻塞时的抖动；满则整批丢弃并计数。
	enqueueDepth = 1024

	// reconcileInterval 设备/点位结构变化的检查周期。值更新不走这里。
	reconcileInterval = 10 * time.Second
)

func init() {
	push.RegisterType(push.ChannelTypeMeta{
		Type:  channelType,
		Label: "OPC UA Server",
		// OPC UA 没有「输出格式/映射」概念：节点类型由点位配置决定。
		MessageBased:     false,
		SupportsPreview:  false,
		SupportsTestSend: false,
	}, newChannel)
}

type channel struct {
	id        string
	name      string
	typeName  string
	rawConfig string
	cfg       *config
	db        *gorm.DB

	queue chan push.PushBatch

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu             sync.RWMutex
	state          *serverState
	running        bool
	lastErr        string
	lastRebuildErr string
	lastSuccess    time.Time

	applied atomic.Uint64
	dropped atomic.Uint64
}

func newChannel(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
	cfg, err := parseConfig(ch.ConfigJSON)
	if err != nil {
		return nil, err
	}
	typ := ch.Type
	if typ == "" {
		typ = channelType
	}
	return &channel{
		id:        ch.ID,
		name:      ch.Name,
		typeName:  typ,
		rawConfig: ch.ConfigJSON,
		cfg:       cfg,
		db:        db,
		queue:     make(chan push.PushBatch, enqueueDepth),
	}, nil
}

func (c *channel) ChannelID() string { return c.id }

func (c *channel) ConfigSig() string {
	return c.id + "|" + c.name + "|" + c.typeName + "|" + c.rawConfig
}

func (c *channel) Start() {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.ctx, c.cancel = context.WithCancel(context.Background())
	ctx := c.ctx
	c.mu.Unlock()

	// 首次建树放在 goroutine 之前：地址空间按数据库建，不依赖第一条采集数据，
	// 客户端在采集开始前连接也能 Browse 到完整树（值为 BadWaitingForInitialData）。
	c.reconcile(ctx)

	c.wg.Add(2)
	go func() {
		defer c.wg.Done()
		c.applyLoop(ctx)
	}()
	go func() {
		defer c.wg.Done()
		c.reconcileLoop(ctx)
	}()

	c.mu.RLock()
	ready := c.state != nil
	lastErr := c.lastErr
	c.mu.RUnlock()
	if ready {
		logger.Info("push/opcuaserver: 通道 %s 已启动，监听 %s", c.name, c.cfg.endpoint())
		return
	}
	logger.Warn("push/opcuaserver: 通道 %s 已启动，但服务端未就绪（每 %s 重试）: %s",
		c.name, reconcileInterval, lastErr)
}

func (c *channel) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	cancel := c.cancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	c.wg.Wait()

	c.mu.Lock()
	st := c.state
	c.state = nil
	c.mu.Unlock()
	if st != nil {
		st.close()
	}

	logger.Info("push/opcuaserver: 通道 %s 已停止", c.name)
}

// Enqueue 实现 push.Channel：只做非阻塞入队。
// 采集协程同步调用本方法，绝不能让慢客户端或 gopcua 的内部锁拖住采集。
func (c *channel) Enqueue(b push.PushBatch) {
	c.mu.RLock()
	running := c.running
	c.mu.RUnlock()
	if !running {
		c.dropped.Add(uint64(len(b.Records)))
		return
	}
	select {
	case c.queue <- b:
	default:
		c.dropped.Add(uint64(len(b.Records)))
	}
}

func (c *channel) applyLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-c.queue:
			c.mu.RLock()
			st := c.state
			c.mu.RUnlock()
			if st == nil {
				c.dropped.Add(uint64(len(b.Records)))
				continue
			}
			applied, dropped := st.apply(b)
			if dropped > 0 {
				c.dropped.Add(uint64(dropped))
			}
			if applied > 0 {
				c.applied.Add(uint64(applied))
				c.mu.Lock()
				c.lastSuccess = time.Now()
				c.mu.Unlock()
			}
		}
	}
}

func (c *channel) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.reconcile(ctx)
		}
	}
}

// reconcile 读数据库拓扑，与当前树比较；结构没变就什么都不做。
// 结构变化（设备/点位增删改）时整棵重建，见 serverState 的说明。
func (c *channel) reconcile(ctx context.Context) {
	c.mu.RLock()
	running := c.running
	c.mu.RUnlock()
	if !running {
		return
	}

	topo, err := loadTopology(ctx, c.db, c.cfg)
	if err != nil {
		if ctx.Err() == nil {
			c.setLastErr(err)
			c.logRebuildErr(err)
		}
		return
	}

	c.mu.RLock()
	st := c.state
	c.mu.RUnlock()
	if st != nil && st.sig == topo.sig {
		return
	}
	c.rebuild(ctx, topo)
}

func (c *channel) rebuild(parent context.Context, topo *topology) {
	next, err := newServerState(c.cfg, topo)
	if err != nil {
		c.setLastErr(err)
		c.logRebuildErr(err)
		return
	}

	c.mu.Lock()
	old := c.state
	c.state = nil
	c.mu.Unlock()

	// 新树已经在内存里建好，再停旧 server 释放端口，窗口尽量短。
	next.seedFrom(old)
	if old != nil {
		old.close()
	}

	if err := startWithRetry(next, parent); err != nil {
		next.close()
		c.setLastErr(err)
		c.logRebuildErr(err)
		return
	}

	c.mu.Lock()
	c.state = next
	c.mu.Unlock()

	c.setLastErr(nil)
	c.mu.Lock()
	c.lastRebuildErr = ""
	c.mu.Unlock()

	// 客户端必须使用这里列出的地址之一（CreateSession 按 URL 精确匹配），
	// 排障时这一行能直接回答“为什么 UaExpert 连上了却 ActivateSession 失败”。
	urls := make([]string, 0, 4)
	seen := make(map[string]struct{})
	for _, ep := range next.srv.Endpoints() {
		if _, ok := seen[ep.EndpointURL]; ok {
			continue
		}
		seen[ep.EndpointURL] = struct{}{}
		urls = append(urls, ep.EndpointURL)
	}
	logger.Info("push/opcuaserver: %s 地址空间已重建（%d 台设备，%d 个点位），可用端点: %s",
		c.cfg.endpoint(), len(topo.devices), len(next.points), strings.Join(urls, ", "))
}

// startWithRetry 处理旧监听端口刚关闭时的短暂占用（Windows 上偶发）。
func startWithRetry(st *serverState, parent context.Context) error {
	var err error
	for i := 0; i < 3; i++ {
		if err = st.start(parent); err == nil {
			return nil
		}
		if !isAddrInUse(err) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return err
}

func (c *channel) setLastErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.lastErr = ""
		return
	}
	c.lastErr = err.Error()
}

// logRebuildErr 同一错误只打一次日志，避免端口被别的程序占用时每 10s 刷屏。
func (c *channel) logRebuildErr(err error) {
	c.mu.Lock()
	changed := c.lastRebuildErr != err.Error()
	c.lastRebuildErr = err.Error()
	c.mu.Unlock()
	if changed {
		logger.Error("push/opcuaserver: %s 启动失败: %v", c.cfg.endpoint(), err)
	}
}

// TestConnectivity 校验端点可用性。
//
// 不能直接 bind 后返回：管理端对「正在运行的通道」做测试时，端口本来就被
// 自己占着。因此先试绑定（可用即可），失败再看是否已被占用 —— 占用时再拨号
// 确认确实有服务在监听（大概率就是本通道），拨不通才判定为冲突。
func (c *channel) TestConnectivity() error {
	ln, err := net.Listen("tcp", c.cfg.addr())
	if err == nil {
		_ = ln.Close()
		return nil
	}
	if isAddrInUse(err) {
		conn, derr := net.DialTimeout("tcp", c.cfg.addr(), 2*time.Second)
		if derr == nil {
			_ = conn.Close()
			return nil
		}
		return fmt.Errorf("opcua: 端口 %s 已被其它程序占用", c.cfg.addr())
	}
	return fmt.Errorf("opcua: 监听 %s 失败: %w", c.cfg.addr(), err)
}

func (c *channel) Snapshot() push.ChannelStatusVO {
	c.mu.RLock()
	running := c.running
	connected := c.state != nil
	lastErr := c.lastErr
	lastSuccess := c.lastSuccess
	c.mu.RUnlock()

	return push.ChannelStatusVO{
		ID:           c.id,
		Name:         c.name,
		Type:         c.typeName,
		Running:      running,
		Connected:    connected,
		Broker:       c.cfg.endpoint(),
		Topic:        c.cfg.NamespaceURI,
		QueueDepth:   uint64(len(c.queue)),
		PublishCount: c.applied.Load(),
		DroppedCount: c.dropped.Load(),
		LastPublish:  push.FormatTime(lastSuccess),
		LastSuccess:  push.FormatTime(lastSuccess),
		LastErr:      lastErr,
	}
}

func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "address already in use") ||
		strings.Contains(s, "only one usage of each socket address")
}
