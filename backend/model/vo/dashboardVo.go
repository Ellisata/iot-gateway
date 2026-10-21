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
// StartedAt 是**第一个采样点**的时间，不是网关启动时间：前端据此显示
// 「已积累 N 分钟」，而不是假装已经有一整个窗口的历史。
type DashboardTrendVO struct {
	IntervalSec int                      `json:"intervalSec"`
	StartedAt   string                   `json:"startedAt"`
	Samples     []DashboardTrendSampleVO `json:"samples"`
}
