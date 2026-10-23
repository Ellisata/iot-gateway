// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// DashboardTrendSampleVO 大盘趋势的一个分钟采样点。
//
// 字段含义与 metrics.Sample 一一对应；VO 单独声明是为了让接口契约与内部
// 存储结构解耦 —— 以后调整采样实现时，不必连带改前端。
type DashboardTrendSampleVO struct {
	Time        string `json:"time"`
	HasOverview bool   `json:"hasOverview"`
	Total       int    `json:"total"`
	Enabled     int    `json:"enabled"`
	Collected   int    `json:"collected"`
	Online      int    `json:"online"`
	Offline     int    `json:"offline"`
	UnCollected int    `json:"unCollected"`
	Collecting  bool   `json:"collecting"`
	Points      int    `json:"points"`
	BadPoints   int    `json:"badPoints"`
	QueueDepth  int    `json:"queueDepth"`
	SpoolDepth  int    `json:"spoolDepth"`
}

// DashboardTrendVO 大盘趋势响应。
//
// StartedAt 是环形窗口里**最早的采样点**时间（最多回看 24 小时），不是网关
// 进程启动时间：前端据此显示「本次运行已积累 N 分钟」。它独立于查询窗口 ——
// 休眠断开后 Trend 的 6h 窗口可能只剩十几分钟，起点仍应是整段历史的开头。
type DashboardTrendVO struct {
	IntervalSec int                      `json:"intervalSec"`
	StartedAt   string                   `json:"startedAt"`
	Samples     []DashboardTrendSampleVO `json:"samples"`
}
