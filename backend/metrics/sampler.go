// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package metrics

import (
	"context"
	"sync"
	"time"

	"iot-gateway/logger"
	"iot-gateway/model/vo"
	"iot-gateway/push"
)

// OverviewSource 提供与 /device/overview 完全相同的聚合结果。
//
// 刻意复用 DeviceService 的实现而不是在采样器里另写一份 SQL：在线率一旦出现
// 两个口径，大盘上的环形图和趋势图就会互相矛盾，而那种矛盾很难被发现。
type OverviewSource interface {
	GetDeviceOverview(ctx context.Context) (*vo.DeviceOverviewVO, error)
}

// CollectionSource 提供采集引擎的当前状态。
type CollectionSource interface {
	GetCollectionStatus(ctx context.Context) vo.CollectionStatusVO
}

// PushSource 提供各推送通道的运行状态。
type PushSource interface {
	GetStatus() []push.ChannelStatusVO
}

// Sampler 每分钟把在线情况、采集吞吐和推送积压合成一个采样点。
type Sampler struct {
	rec        *Recorder
	overview   OverviewSource
	collection CollectionSource
	push       PushSource
	interval   time.Duration

	mu   sync.Mutex
	stop chan struct{}
	done chan struct{}
}

// NewSampler 创建采样器（未启动）。
func NewSampler(rec *Recorder, overview OverviewSource, collection CollectionSource, push PushSource) *Sampler {
	return &Sampler{
		rec:        rec,
		overview:   overview,
		collection: collection,
		push:       push,
		interval:   Interval,
	}
}

// Start 启动后台采样。可重复调用（已启动则 no-op）。
func (s *Sampler) Start() {
	s.mu.Lock()
	if s.stop != nil {
		s.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	s.stop, s.done = stop, done
	s.mu.Unlock()
	go s.loop(stop, done)
}

// Stop 停止采样并等待协程退出。未启动时是 no-op。
func (s *Sampler) Stop() {
	s.mu.Lock()
	stop, done := s.stop, s.done
	s.stop, s.done = nil, nil
	s.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

func (s *Sampler) loop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	// 立即落一个点：前端首次打开时不必空等一整分钟才看到基线。
	s.collect()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			s.collectAt(now)
		}
	}
}

func (s *Sampler) collect() {
	s.collectAt(time.Now())
}

// collectAt 采集一次快照。overview 取数失败时仍写入采样点（Points 等数据有效），
// 但把 HasOverview 置为 false，由前端把这一段画成断点而不是 0%。
func (s *Sampler) collectAt(now time.Time) {
	points, badPoints := s.rec.takePoints()
	sample := Sample{
		Time:      now.Format("2006-01-02 15:04:05"),
		Points:    points,
		BadPoints: badPoints,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 与大盘同一个上下文：采集状态也用于判断这一段曲线是否连续。
	if status := s.collection.GetCollectionStatus(ctx); status.Running {
		sample.Collecting = true
	}

	if overview, err := s.overview.GetDeviceOverview(ctx); err != nil {
		logger.Warn("metrics: device overview sample failed: %v", err)
	} else if overview != nil {
		sample.HasOverview = true
		sample.Total = overview.Total
		sample.Enabled = overview.Enabled
		sample.Collected = overview.Collected
		sample.Online = overview.Online
		sample.Offline = overview.Offline
		sample.UnCollected = overview.UnCollected
	}

	for _, ch := range s.push.GetStatus() {
		sample.QueueDepth += int(ch.QueueDepth)
		sample.SpoolDepth += int(ch.SpoolDepth)
	}

	s.rec.append(sample)
}
