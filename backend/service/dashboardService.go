// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"time"

	"iot-gateway/metrics"
	"iot-gateway/model/vo"
)

// DashboardService 大盘聚合查询服务。
type DashboardService struct {
	recorder *metrics.Recorder
}

// NewDashboardService 创建大盘服务。
func NewDashboardService(recorder *metrics.Recorder) *DashboardService {
	return &DashboardService{recorder: recorder}
}

// GetTrend 返回最近 hours 小时的遥测趋势。
//
// 没有采样点时返回空切片而不是 nil：JSON 里 [] 让前端能直接判断
// 「本次运行尚未积累数据」，不必额外区分 null。
func (s *DashboardService) GetTrend(hours int) *vo.DashboardTrendVO {
	samples := s.recorder.Trend(hours)
	out := make([]vo.DashboardTrendSampleVO, 0, len(samples))
	for _, sample := range samples {
		out = append(out, vo.DashboardTrendSampleVO{
			Time:        sample.Time,
			HasOverview: sample.HasOverview,
			Total:       sample.Total,
			Enabled:     sample.Enabled,
			Collected:   sample.Collected,
			Online:      sample.Online,
			Offline:     sample.Offline,
			UnCollected: sample.UnCollected,
			Collecting:  sample.Collecting,
			Points:      sample.Points,
			BadPoints:   sample.BadPoints,
			QueueDepth:  sample.QueueDepth,
			SpoolDepth:  sample.SpoolDepth,
		})
	}

	return &vo.DashboardTrendVO{
		IntervalSec: int(metrics.Interval / time.Second),
		// 起点取环形窗口里最旧的点，而不是本次查询窗口的第一个点：
		// 休眠断开后 6h 窗口里可能只剩十几分钟，那样会把「已积累」说小。
		StartedAt: s.recorder.Oldest(),
		Samples:   out,
	}
}
