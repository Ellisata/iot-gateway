// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package delivery 提供可选的「可靠投递」骨架：非阻塞出站队列、断网本地
// 缓存与补发、攒批写入与最近写入观测。
//
// 它不属于时序库语义，因此与 tsdb 核心分开：只需要同步写入的项目可以直接
// 使用各后端的 Writer，不需要引入本包；需要断网不丢的项目再用 Sink 组合
// Writer + delivery。
package delivery

import (
	"sync"
	"unicode/utf8"

	"github.com/Ellisata/iot-gateway/tsdb"
)

const (
	// RecentPushRingSize 每个后端在内存中保留的最近写入条数。
	RecentPushRingSize = 100
	// recentPushPayloadPreviewBytes 单条出站载荷保留的预览上限。
	recentPushPayloadPreviewBytes = 2 << 10
)

// RecentRing 固定容量的环形缓冲：写满后覆盖最旧的一条。
//
// 写入发生在后端的写协程；读取发生在 HTTP 协程。锁只保护指针与副本，
// 临界区里不做 I/O。
type RecentRing struct {
	mu    sync.Mutex
	items []tsdb.RecentPushMessage // 长度固定
	next  int                      // 下一条写入位置
	count int                      // 已填条数，≤ len(items)
	seq   uint64                   // 仅在锁内递增
}

// NewRecentRing 创建指定容量的环形缓冲；size ≤ 0 时按 1 处理。
func NewRecentRing(size int) *RecentRing {
	if size <= 0 {
		size = 1
	}
	return &RecentRing{items: make([]tsdb.RecentPushMessage, size)}
}

// Add 追加一条写入记录（满时覆盖最旧）并分配递增序号。
// Payload/Size/Truncated 由 payload 现算，调用方不需要自行截断。
func (r *RecentRing) Add(m tsdb.RecentPushMessage, payload []byte) {
	m.Payload, m.Size, m.Truncated = PreviewPayload(payload)
	r.insert(m)
}

// AddString 同 Add，但载荷已经是字符串（时序库的 SQL / Line Protocol）。
// 单独提供是为了避免把整段聚合负载复制成 []byte 再转回字符串。
func (r *RecentRing) AddString(m tsdb.RecentPushMessage, payload string) {
	m.Payload, m.Size, m.Truncated = PreviewPayloadString(payload)
	r.insert(m)
}

func (r *RecentRing) insert(m tsdb.RecentPushMessage) {
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
func (r *RecentRing) Snapshot(limit int) []tsdb.RecentPushMessage {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := r.count
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]tsdb.RecentPushMessage, n)
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
