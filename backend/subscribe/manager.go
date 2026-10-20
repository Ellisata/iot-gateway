// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package subscribe 管理外部 MQTT broker 的多条订阅。
//
// 每条订阅 = 独立 paho 客户端 + 一套「JSON → CollectedRecord」映射规则：
// 收到报文后按规则转换，转换成功的记录交给 Sink（push.Engine），
// 最终只写入时序数据库类推送通道（TDengine / InfluxDB）。
//
// 生命周期与 push.Engine 一致：Start 同步加载一次配置，随后每 10s 比对配置
// 校验和，新增/修改/删除/启停订阅都会在不重启进程的前提下热加载。
package subscribe

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"sync"
	"time"

	"gorm.io/gorm"

	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// errConnectTimeout 连通性测试超时。
var errConnectTimeout = errors.New("连接超时")

// Status 单个订阅的运行状态快照。
type Status struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Broker      string `json:"broker"`
	Topic       string `json:"topic"`
	Running     bool   `json:"running"`
	Connected   bool   `json:"connected"`
	Messages    uint64 `json:"messages"`
	Records     uint64 `json:"records"`
	Errors      uint64 `json:"errors"`
	LastMessage string `json:"lastMessageTime"`
	Error       string `json:"error"`
}

// Manager 订阅管理器。
//
// 与 push.Engine 一样使用「全量比对 + 保留未变化实例」的热加载策略：
// 配置未变化的订阅保持原连接不断开，只有变化的订阅才重建客户端。
type Manager struct {
	db   *gorm.DB
	sink Sink
	// points 订阅数据中的设备/点位元信息索引，与记录投递共用一个生命周期。
	points *devicePointStore

	mu      sync.RWMutex
	subs    map[string]*subscriber
	running bool

	watchCtx    context.Context
	watchCancel context.CancelFunc
	watchOnce   sync.Once
}

// NewManager 创建订阅管理器。
func NewManager(db *gorm.DB, sink Sink) *Manager {
	return &Manager{
		db:     db,
		sink:   sink,
		points: newDevicePointStore(db),
		subs:   make(map[string]*subscriber),
	}
}

// Start 加载活跃订阅并启动配置热加载监视器。
func (m *Manager) Start() error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = true
	m.mu.Unlock()

	// 先启动元信息落库器再建订阅：收包协程可能在 refresh 返回前就开始投递。
	m.points.Start()

	m.refresh()

	m.mu.Lock()
	m.startWatcherLocked()
	m.mu.Unlock()

	logger.Info("mqtt subscription manager started")
	return nil
}

// Stop 停止监视器并断开所有订阅。
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	m.running = false
	m.stopWatcherLocked()

	subs := make([]*subscriber, 0, len(m.subs))
	for _, s := range m.subs {
		subs = append(subs, s)
	}
	m.subs = make(map[string]*subscriber)
	m.mu.Unlock()

	for _, s := range subs {
		s.stop()
	}
	// 订阅已全部断开，不会再有新记录进入；此时停止落库器会把队列中的元信息刷完。
	m.points.Stop()
	logger.Info("mqtt subscription manager stopped")
}

// Refresh 热刷新订阅配置（新增/修改/删除/启停无需重启）。
func (m *Manager) Refresh() error {
	m.mu.RLock()
	running := m.running
	m.mu.RUnlock()
	if !running {
		return fmt.Errorf("subscribe: manager not running")
	}
	m.refresh()
	return nil
}

// Status 返回所有活跃订阅的运行状态（按名称排序）。
func (m *Manager) Status() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Status, 0, len(m.subs))
	for _, s := range m.subs {
		out = append(out, Status{
			ID:          s.cfg.id,
			Name:        s.cfg.name,
			Broker:      s.cfg.endpoint(),
			Topic:       s.cfg.topic,
			Running:     s.running.Load(),
			Connected:   s.connected.Load(),
			Messages:    s.messages.Load(),
			Records:     s.records.Load(),
			Errors:      s.errors.Load(),
			LastMessage: s.lastMessageTime(),
			Error:       s.lastError(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// refresh 重建订阅列表并原子替换，未变化的订阅保留运行实例。
func (m *Manager) refresh() {
	rows, err := m.loadSubscriptions()
	if err != nil {
		logger.Error("subscribe: load subscriptions failed: %v", err)
		return
	}

	type built struct {
		cfg *subscription
	}
	next := make([]built, 0, len(rows))
	for i := range rows {
		cfg, err := parseSubscription(&rows[i])
		if err != nil {
			logger.Error("subscribe: subscription %q (id=%s) config invalid, skip: %v",
				rows[i].Name, rows[i].ID, err)
			continue
		}
		next = append(next, built{cfg: cfg})
	}

	m.mu.Lock()
	newMap := make(map[string]*subscriber, len(next))
	var toStop []*subscriber
	for _, b := range next {
		id := b.cfg.id
		if old, ok := m.subs[id]; ok && old.Sig() == b.cfg.sig {
			newMap[id] = old // 配置未变化，保留运行中的连接
			continue
		}
		s := newSubscriber(b.cfg, m.sink, m.points)
		newMap[id] = s
		go s.start() // 建连可能阻塞（broker 不可达时更明显），不阻塞管理器
		if old, ok := m.subs[id]; ok {
			toStop = append(toStop, old)
		}
	}
	for id, old := range m.subs {
		if _, ok := newMap[id]; !ok {
			toStop = append(toStop, old) // 被删除或停用
		}
	}
	m.subs = newMap
	m.mu.Unlock()

	for _, old := range toStop {
		old.stop()
	}
}

// loadSubscriptions 读取活跃订阅（status=1）。
func (m *Manager) loadSubscriptions() ([]po.MqttSubscription, error) {
	if m.db == nil {
		return nil, nil
	}
	var rows []po.MqttSubscription
	if err := m.db.Where("status = 1").Order("sort, created_at").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (m *Manager) startWatcherLocked() {
	m.watchOnce.Do(func() {
		m.watchCtx, m.watchCancel = context.WithCancel(context.Background())
		go m.watchLoop(m.watchCtx)
	})
}

func (m *Manager) stopWatcherLocked() {
	if m.watchCancel != nil {
		m.watchCancel()
		m.watchCancel = nil
		m.watchCtx = nil
		m.watchOnce = sync.Once{}
	}
}

// watchLoop 定期比对订阅配置，发生变化时热刷新。
func (m *Manager) watchLoop(ctx context.Context) {
	const checkInterval = 10 * time.Second

	lastChecksum := m.loadChecksum()
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	logger.Info("subscribe: config watcher started (interval=%s)", checkInterval)

	for {
		select {
		case <-ctx.Done():
			logger.Info("subscribe: config watcher stopped")
			return
		case <-ticker.C:
			cur := m.loadChecksum()
			if cur != lastChecksum {
				logger.Info("subscribe: subscription config changed (checksum %d->%d), hot reloading",
					lastChecksum, cur)
				m.refresh()
				lastChecksum = cur
			}
		}
	}
}

// loadChecksum 计算活跃订阅配置的校验和。
// 只包含影响连接/映射的字段：sample_json、description、sort 变化不应重连。
func (m *Manager) loadChecksum() uint64 {
	if m.db == nil {
		return 0
	}
	type row struct {
		ID          string
		Broker      string
		Port        string
		ClientID    string
		Username    string
		Password    string
		Topic       string
		QoS         int
		MappingJSON string
	}
	var rows []row
	if err := m.db.Model(&po.MqttSubscription{}).
		Select("id, broker, port, client_id, username, password, topic, qos, mapping_json").
		Where("status = 1").
		Order("id").
		Find(&rows).Error; err != nil {
		logger.Error("subscribe: load checksum failed: %v", err)
		return 0
	}

	h := fnv.New64a()
	for _, r := range rows {
		io.WriteString(h, r.ID)
		io.WriteString(h, "|")
		io.WriteString(h, r.Broker)
		io.WriteString(h, "|")
		io.WriteString(h, r.Port)
		io.WriteString(h, "|")
		io.WriteString(h, r.ClientID)
		io.WriteString(h, "|")
		io.WriteString(h, r.Username)
		io.WriteString(h, "|")
		io.WriteString(h, r.Password)
		io.WriteString(h, "|")
		io.WriteString(h, r.Topic)
		io.WriteString(h, "|")
		fmt.Fprintf(h, "%d", r.QoS)
		io.WriteString(h, "|")
		io.WriteString(h, r.MappingJSON)
		io.WriteString(h, "\n")
	}
	return h.Sum64()
}

// TestConnectivity 测试一组尚未保存的订阅参数：校验配置、连接 broker 后立即断开。
func TestConnectivity(broker, port, clientID, username, password, topic string) error {
	cfg, err := parseTestConfig(broker, port, clientID, username, password, topic)
	if err != nil {
		return err
	}
	return testConnect(cfg)
}
