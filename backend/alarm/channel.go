// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package alarm

import (
	"sync"
	"time"

	"gorm.io/gorm"

	"iot-gateway/push"
)

const (
	// channelScanInterval 推送通道连接状态巡检间隔。
	// 检测延迟 ≈ 巡检间隔 × 连续失败阈值（默认 2，约 20s）。
	channelScanInterval = 10 * time.Second
	// channelFailThreshold 推送通道连续离线判定阈值。
	// 略小于设备默认（3）：MQTT 自动重连，连续 2 次（≈20s）判离线足够，
	// 且能吸收热加载/重启时新实例的连接建立窗口。
	channelFailThreshold = 2
)

// StatusSource 推送通道连接状态来源。
// push.Engine 的 GetStatus 结构满足此接口；独立接口便于单元测试注入假源。
type StatusSource interface {
	GetStatus() []push.ChannelStatusVO
}

// ChannelMonitor 推送通道断联报警巡检器。
//
// 后台协程按 channelScanInterval 调 StatusSource.GetStatus()，把每通道的
// Running && Connected 当作在线信号喂给共享 Tracker（targetType=channel）。
// 只对运行中的通道判定：停用/配置非法的通道是运营选择，不报警。
// 通道由停止转运行（重启/热加载新实例）时重置其状态机，吸收连接建立窗口；
// 由运行转停止时清其 active 报警。
type ChannelMonitor struct {
	tr   *Tracker
	src  StatusSource
	wait time.Duration // 巡检间隔；0 时用 channelScanInterval

	// was: channelID -> 上轮是否运行（仅 run 协程读写，无并发）
	was map[string]bool

	mu     sync.Mutex
	stopCh chan struct{}
	doneCh chan struct{}
}

// NewChannelMonitor 创建推送通道断联报警巡检器（未启动）。
func NewChannelMonitor(db *gorm.DB, src StatusSource) *ChannelMonitor {
	return &ChannelMonitor{
		tr:   NewTracker(db, TypeChannel, "推送通道"),
		src:  src,
		wait: channelScanInterval,
		was:  make(map[string]bool),
	}
}

// Start 启动巡检协程。
func (m *ChannelMonitor) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopCh != nil {
		return
	}
	// 通道巡检用更敏感的阈值（连续 2 次判离线）
	m.tr.cfg.ConsecutiveFailures = channelFailThreshold
	m.stopCh = make(chan struct{})
	m.doneCh = make(chan struct{})
	go m.run()
}

// Stop 停止巡检协程，等待退出。
func (m *ChannelMonitor) Stop() {
	m.mu.Lock()
	if m.stopCh == nil {
		m.mu.Unlock()
		return
	}
	close(m.stopCh)
	done := m.doneCh
	m.mu.Unlock()
	<-done
}

// run 巡检主循环。
func (m *ChannelMonitor) run() {
	defer close(m.doneCh)

	interval := m.wait
	if interval <= 0 {
		interval = channelScanInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.scan()
		}
	}
}

// scan 快照当前所有通道状态并喂给状态机。
// 仅在 run 协程内调用，无并发。
func (m *ChannelMonitor) scan() {
	statuses := m.src.GetStatus()

	cur := make(map[string]bool, len(statuses))
	for _, st := range statuses {
		cur[st.ID] = st.Running
		prevRunning := m.wasRunning(st.ID)

		switch {
		case st.Running && !prevRunning:
			// 新实例/重启：重置状态机，吸收连接建立窗口
			m.tr.Reset(st.ID)
		case !st.Running && prevRunning:
			// 由运行转停止（运营停用）：清其 active 报警并移除状态
			m.tr.ClearActive(st.ID)
			continue
		case !st.Running:
			// 从未运行过（异常/配置非法）：不参与在线判定
			continue
		}
		m.tr.Report(st.ID, st.Name, st.Connected)
	}

	// 清理已从状态列表消失的通道记录
	m.mu.Lock()
	for id := range m.was {
		if _, ok := cur[id]; !ok {
			delete(m.was, id)
		}
	}
	m.was = cur
	m.mu.Unlock()
}

// wasRunning 读取通道上轮运行状态。
func (m *ChannelMonitor) wasRunning(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.was[id]
}
