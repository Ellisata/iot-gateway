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

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/collector"
)

const (
	// Interval 采样间隔。1 分钟 / 点：24 小时 1440 点，曲线可读，内存可忽略。
	Interval = time.Minute
	// MaxSamples 环形窗口长度：24h ÷ 1min。
	MaxSamples = 24 * 60
	// SampleTimeLayout 采样点时间戳格式。定宽，因此可直接按字符串比较先后。
	SampleTimeLayout = "2006-01-02 15:04:05"
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

// Oldest 返回环形窗口里最旧采样点的时间（本次运行已积累历史的起点）。
// 与 Trend 的窗口裁剪无关：查询窗口可以只覆盖最近几分钟，
// 「本次运行从什么时候开始」不应跟着一起被裁短。
func (r *Recorder) Oldest() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.samples) == 0 {
		return ""
	}
	return r.samples[0].Time
}

// Trend 返回最近 hours 小时内的采样点副本（时间正序）。
// hours 会被收敛到 1..24；样本不足时返回现有的全部。
func (r *Recorder) Trend(hours int) []Sample {
	return r.trendAt(hours, time.Now())
}

// trendAt 是 Trend 的可注入时钟版本，窗口按墙钟裁，测试需要固定 now。
//
// 只按点数截断是不够的：进程挂起期间（宿主休眠时 WSL / 笔记本会暂停整台
// 虚拟机，定时器不触发）采样点之间会留下数小时的空档，360 个点于是能横跨
// 远超 6 小时的实际时间 —— 横轴画出来是 17 小时，按钮上却写着「6 小时」。
// 因此在上限个数的窗口之外，再按时间戳裁掉窗口起点之前的点。
func (r *Recorder) trendAt(hours int, now time.Time) []Sample {
	if hours < 1 {
		hours = 1
	}
	if hours > 24 {
		hours = 24
	}
	limit := hours * int(time.Hour/Interval)
	// 采样点按 SampleTimeLayout 定宽格式化，可直接做字符串比较
	cutoff := now.Add(-time.Duration(hours) * time.Hour).Format(SampleTimeLayout)

	r.mu.Lock()
	defer r.mu.Unlock()

	start := 0
	if len(r.samples) > limit {
		start = len(r.samples) - limit
	}
	// 样本按时间正序追加，从数量上限的位置继续往后找到窗口起点即可
	for start < len(r.samples) && r.samples[start].Time < cutoff {
		start++
	}
	out := make([]Sample, len(r.samples)-start)
	copy(out, r.samples[start:])
	return out
}
