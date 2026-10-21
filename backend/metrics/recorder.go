// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package metrics 保存管理端大盘需要的短期运行遥测。
//
// 网关的定位是「采集后立即转发」，本地不落采集历史（历史属于下游时序库，
// 见 realtime 包的说明）。但大盘需要一段能看出趋势的数据，本包用内存环形
// 窗口补上这一层：只保留最近 24 小时、每分钟一个采样点，进程退出即丢弃。
//
// 刻意不写 SQLite：一分钟一次写库对网关来说没有收益，而重启后从头积累
// 的诚实空窗，比用旧数据拼出一条假的趋势更有价值。需要跨重启历史时，
// 应由下游时序库承担，而不是把采集链路变成另一个存储。
package metrics

import (
	"sync"
	"time"

	"iot-gateway/collector"
	"iot-gateway/driver"
)

const (
	// Interval 采样间隔。1 分钟 / 点：24 小时 1440 点，曲线可读，内存可忽略。
	Interval = time.Minute
	// MaxSamples 环形窗口长度：24h ÷ 1min。
	MaxSamples = 24 * 60
)

// Sample 一个分钟的遥测快照。
//
// HasOverview 区分「取数失败」与「确实 0 台在线」：前者不能画成 0%，
// 否则一次数据库抖动就会在曲线上留下一个危险的假谷底。
type Sample struct {
	Time        string `json:"time"`
	HasOverview bool   `json:"hasOverview"`
	Total       int    `json:"total"`
	Enabled     int    `json:"enabled"`
	Collected   int    `json:"collected"`
	Online      int    `json:"online"`
	Offline     int    `json:"offline"`
	UnCollected int    `json:"unCollected"`
	Collecting  bool   `json:"collecting"`
	// Points / BadPoints 本分钟内采集到的点数与不可用点数（driver.QualityUsable 为假）。
	Points    int `json:"points"`
	BadPoints int `json:"badPoints"`
	// QueueDepth / SpoolDepth 所有推送通道的实时队列与断网缓存积压之和。
	QueueDepth int `json:"queueDepth"`
	SpoolDepth int `json:"spoolDepth"`
}

// Recorder 既是采集记录的接收者，也是趋势采样点的环形存储。
//
// 采集热路径只做一次加锁累加（按批次，不按点位），不引入任何 I/O。
type Recorder struct {
	mu        sync.Mutex
	samples   []Sample
	points    int
	badPoints int
}

// NewRecorder 创建空的遥测记录器。
func NewRecorder() *Recorder {
	return &Recorder{samples: make([]Sample, 0, MaxSamples+1)}
}

// PushRecords 实现 collector.RecordSink：按批次统计采集点数与坏点数。
// 放在推送引擎之前，即使下游阻塞或丢弃，采集吞吐仍然被准确记录。
func (r *Recorder) PushRecords(records []collector.CollectedRecord) {
	if len(records) == 0 {
		return
	}
	bad := 0
	for _, rec := range records {
		if !driver.QualityUsable(rec.Quality) {
			bad++
		}
	}
	r.mu.Lock()
	r.points += len(records)
	r.badPoints += bad
	r.mu.Unlock()
}

// takePoints 取出并清零自上次采样以来的点数计数。
func (r *Recorder) takePoints() (points, bad int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	points, bad = r.points, r.badPoints
	r.points, r.badPoints = 0, 0
	return points, bad
}

// append 写入一个采样点，超过窗口时丢弃最旧的一条。
func (r *Recorder) append(s Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.samples) == MaxSamples {
		copy(r.samples, r.samples[1:])
		r.samples = r.samples[:MaxSamples-1]
	}
	r.samples = append(r.samples, s)
}

// Trend 返回最近 hours 小时内的采样点副本（时间正序）。
// hours 会被收敛到 1..24；样本不足时返回现有的全部。
func (r *Recorder) Trend(hours int) []Sample {
	if hours < 1 {
		hours = 1
	}
	if hours > 24 {
		hours = 24
	}
	limit := hours * int(time.Hour/Interval)

	r.mu.Lock()
	defer r.mu.Unlock()

	start := 0
	if len(r.samples) > limit {
		start = len(r.samples) - limit
	}
	out := make([]Sample, len(r.samples)-start)
	copy(out, r.samples[start:])
	return out
}
