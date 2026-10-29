// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// recentMessageRingSize 每个订阅在内存中保留的最近报文条数。
	// 与接口 limit 的 max（model/dto 的 binding:"max=100"）保持一致。
	recentMessageRingSize = 100
	// recentPayloadPreviewBytes 单条报文保留的载荷预览上限。
	// 报文本身可能到 1 MiB（parser.DefaultMaxPayloadBytes），只存预览避免内存放大。
	recentPayloadPreviewBytes = 2 << 10
)

// RecentMessage 一条最近收到的订阅报文快照。
//
// 仅内存、仅观测：收包即记录，因此解析失败、队列满丢弃、超限未解析的报文
// 也会出现在这里 —— 这正是排查"报文到底有没有进来"需要的信息。
// 订阅因连接参数变化被重建时缓冲清空；仅换解析器时保留。
type RecentMessage struct {
	Seq        uint64 // 单调递增，供前端做稳定列表 key
	Topic      string
	Payload    string // 载荷预览，最多 recentPayloadPreviewBytes 字节
	Size       int    // 原始载荷字节数
	Truncated  bool   // Payload 是否被截断
	Oversized  bool   // 原始载荷超过 parser.DefaultMaxPayloadBytes，未进入解析管线
	ReceivedAt time.Time
	QoS        byte
	Retained   bool
}

// recentMessageRing 固定容量的环形缓冲：写满后覆盖最旧的一条。
//
// 写入发生在 paho 收包协程（SetOrderMatters(true)，实际单写者），
// 读取发生在 HTTP 协程；锁只保护指针与副本，临界区里不做 I/O。
type recentMessageRing struct {
	mu    sync.Mutex
	items []RecentMessage // 长度固定
	next  int             // 下一条写入位置
	count int             // 已填条数，≤ len(items)
	seq   uint64          // 仅在锁内递增
}

func newRecentMessageRing(size int) *recentMessageRing {
	if size <= 0 {
		size = 1
	}
	return &recentMessageRing{items: make([]RecentMessage, size)}
}

// add 追加一条报文（满时覆盖最旧）并分配递增序号。
func (r *recentMessageRing) add(m RecentMessage) {
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

// snapshot 返回新→旧的副本，调用方可安全长期持有。
func (r *recentMessageRing) snapshot() []RecentMessage {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]RecentMessage, r.count)
	n := len(r.items)
	for i := 0; i < r.count; i++ {
		idx := (r.next - 1 - i + n) % n
		out[i] = r.items[idx]
	}
	return out
}

// previewPayload 生成载荷预览。超过上限时按 UTF-8 边界回退，
// 不会把多字节字符截成半个。
//
// 二进制载荷原样保留（JSON 序列化时无效字节变 U+FFFD）—— 观测用途，
// 不做 base64；需要逐字节查看时仍应旁挂 MQTT 客户端。
func previewPayload(payload []byte) (string, bool) {
	if len(payload) <= recentPayloadPreviewBytes {
		return string(payload), false
	}

	b := payload[:recentPayloadPreviewBytes]
	// 截断点可能落在多字节字符中间。最多回退 utf8.UTFMax-1 字节即可覆盖
	// 任何不完整序列；再加个上界，避免二进制载荷里连串的续字节被一路剥光。
	for i := 0; i < utf8.UTFMax-1 && len(b) > 0; i++ {
		r, size := utf8.DecodeLastRune(b)
		if r != utf8.RuneError || size > 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return string(b), true
}
