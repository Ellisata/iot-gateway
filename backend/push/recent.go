// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// RecentPushRingSize 每个通道在内存中保留的最近推送条数。
	// 与接口 limit 的 max（model/dto 的 binding:"max=100"）保持一致。
	RecentPushRingSize = 100
	// recentPushPayloadPreviewBytes 单条出站载荷保留的预览上限。
	// 一个批次最多 500 条记录（见 maxRecordsPerBatch），只存预览避免内存放大。
	recentPushPayloadPreviewBytes = 2 << 10
)

// RecentPushMessage 一条最近推送报文快照。
//
// 仅内存、仅观测：每次向传输层发送（无论成功/失败）记录一条，用于回答
// 「通道到底推出了什么、下游回了什么」。不落库，进程重启即清空。
type RecentPushMessage struct {
	Seq        uint64    // 单调递增，供前端做稳定列表 key
	At         time.Time // 发送结束时间
	Target     string    // MQTT 主题 / HTTP URL
	DeviceCode string
	GroupCode  string
	Records    int // 该批次包含的记录数
	StatusCode int // HTTP 状态码；MQTT 无此概念，恒为 0
	DurationMs int64
	Size       int    // 原始载荷字节数
	Payload    string // 载荷预览，最多 recentPushPayloadPreviewBytes 字节
	Truncated  bool   // Payload 是否被截断
	Error      string // 失败原因；成功为空
	FromDrain  bool   // 该批次来自断网缓存补发
}

// RecentRing 固定容量的环形缓冲：写满后覆盖最旧的一条。
//
// 写入发生在通道的发送协程（MQTT 是多个发布 worker，消息通道是多个 send
// worker 加补发协程）；读取发生在 HTTP 协程。锁只保护指针与副本，临界区里
// 不做 I/O。
type RecentRing struct {
	mu    sync.Mutex
	items []RecentPushMessage // 长度固定
	next  int                 // 下一条写入位置
	count int                 // 已填条数，≤ len(items)
	seq   uint64              // 仅在锁内递增
}

// NewRecentRing 创建指定容量的环形缓冲；size ≤ 0 时按 1 处理。
func NewRecentRing(size int) *RecentRing {
	if size <= 0 {
		size = 1
	}
	return &RecentRing{items: make([]RecentPushMessage, size)}
}

// Add 追加一条推送记录（满时覆盖最旧）并分配递增序号。
// Payload/Size/Truncated 由 payload 现算，调用方不需要自行截断。
func (r *RecentRing) Add(m RecentPushMessage, payload []byte) {
	m.Payload, m.Size, m.Truncated = PreviewPayload(payload)
	r.insert(m)
}

// AddString 同 Add，但载荷已经是字符串（时序库的 SQL / Line Protocol）。
// 单独提供是为了避免把整段聚合负载复制成 []byte 再转回字符串。
func (r *RecentRing) AddString(m RecentPushMessage, payload string) {
	m.Payload, m.Size, m.Truncated = PreviewPayloadString(payload)
	r.insert(m)
}

func (r *RecentRing) insert(m RecentPushMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.seq++
	m.Seq = r.seq
	r.items[r.next] = m
	r.next = (r.next + 1) % len(r.items)
	if r.count < len(r.items) {
		r.count++
	}
}

// Snapshot 返回新→旧的副本；limit ≤ 0 表示返回全部。
// 调用方可安全长期持有。
func (r *RecentRing) Snapshot(limit int) []RecentPushMessage {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := r.count
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]RecentPushMessage, n)
	total := len(r.items)
	for i := 0; i < n; i++ {
		idx := (r.next - 1 - i + total) % total
		out[i] = r.items[idx]
	}
	return out
}

// PreviewPayload 生成载荷预览并返回原始字节数。超过上限时按 UTF-8 边界回退，
// 不会把多字节字符截成半个。二进制载荷原样保留（观测用途，不做 base64）。
func PreviewPayload(payload []byte) (text string, size int, truncated bool) {
	size = len(payload)
	if size <= recentPushPayloadPreviewBytes {
		return string(payload), size, false
	}

	b := payload[:recentPushPayloadPreviewBytes]
	// 截断点可能落在多字节字符中间。最多回退 utf8.UTFMax-1 字节即可覆盖
	// 任何不完整序列；再加个上界，避免二进制载荷里连串的续字节被一路剥光。
	for i := 0; i < utf8.UTFMax-1 && len(b) > 0; i++ {
		r, n := utf8.DecodeLastRune(b)
		if r != utf8.RuneError || n > 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return string(b), size, true
}

// PreviewPayloadString 同 PreviewPayload，输入为字符串。
// 时序库一次刷出的 SQL / Line Protocol 可能达数百 KB，这里只切预览片段，
// 不复制整段负载。
func PreviewPayloadString(payload string) (text string, size int, truncated bool) {
	size = len(payload)
	if size <= recentPushPayloadPreviewBytes {
		return payload, size, false
	}

	b := payload[:recentPushPayloadPreviewBytes]
	for i := 0; i < utf8.UTFMax-1 && len(b) > 0; i++ {
		r, n := utf8.DecodeLastRuneInString(b)
		if r != utf8.RuneError || n > 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return b, size, true
}

// WriteObservation 一次聚合写入（实时刷盘或补发分组）的结果。
//
// 由 AggregateWriter / GroupedDrainer 的可选 Observe 回调上报。Target、
// StatusCode 与错误文案由各通道在回调里补齐 —— 只有它们知道库/measurement
// 与连接错误的最新文案。
type WriteObservation struct {
	Payload   string
	Rows      int // 本次负载实际写出的行数（Aggregator.Rows）
	OK        bool
	Duration  time.Duration
	FromDrain bool
}

// RecentMessagesProvider 可选接口：通道可提供最近推送报文（仅内存）。
//
// 用可选接口而不是给 Channel 增加方法：观测能力不该成为每个通道的强制义务，
// 与 Previewer / TestSender 的处理方式一致。
type RecentMessagesProvider interface {
	RecentMessages(limit int) []RecentPushMessage
}
