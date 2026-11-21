// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tsdb

import "time"

// RecentPushMessage 一条最近写入快照。
//
// 仅内存、仅观测：每次向传输层写入（无论成功/失败）记录一条，用于回答
// 「后端到底写了什么、下游回了什么」。不落库，进程重启即清空。
type RecentPushMessage struct {
	Seq        uint64    // 单调递增，供前端做稳定列表 key
	At         time.Time // 写入结束时间
	Target     string    // 目标描述（库名 / URL）
	DeviceCode string
	GroupCode  string
	Records    int // 该批次包含的记录数
	StatusCode int // HTTP 状态码；无此概念的后端恒为 0
	DurationMs int64
	Size       int    // 原始载荷字节数
	Payload    string // 载荷预览，最多 2 KiB
	Truncated  bool   // Payload 是否被截断
	Error      string // 失败原因；成功为空
	FromDrain  bool   // 该批次来自断网缓存补发
}

// WriteObservation 一次聚合写入（实时刷盘或补发分组）的结果。
//
// 由 AggregateWriter / GroupedDrainer 的可选 Observe 回调上报。Target、
// StatusCode 与错误文案由各后端在回调里补齐 —— 只有它们知道库/measurement
// 与连接错误的最新文案。
type WriteObservation struct {
	Payload   string
	Rows      int // 本次负载实际写出的行数（Aggregator.Rows）
	OK        bool
	Duration  time.Duration
	FromDrain bool
}
