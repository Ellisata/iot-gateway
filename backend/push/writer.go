// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import "time"

// AggregateWriter 通道的「攒批下发」写协程：消费 Outbox 的批次，把多个批次聚合进
// 一个负载，行数达 MaxRows 或时间到 Interval 双触发刷出，直至 Outbox 停止。
//
// tdengine 与 influxdb 的写协程此前各有一份逐字相同的实现，差异只在负载构造器与
// 下发方式，现由本类型统一 —— 把「每设备每轮一次往返」合并为「攒批一次往返」，
// 降低设备量大时的固定开销。mqtt 不适用：它按设备路由主题、一批一个载荷，不走聚合。
//
// 一个通道可以有多个 Run 并发运行（各自独立攒批，见各通道的 batchWorkers）。
type AggregateWriter struct {
	// New 每个新负载重建聚合器（tdengine 的 insertBuilder / influxdb 的 lineBuilder）。
	New func() Aggregator
	// Write 下发一次负载，返回是否成功。
	Write func(string) bool
	// MaxRows 单个负载的最大记录行数，达到即刷出。
	MaxRows int
	// Interval 未达行数时的强制刷出间隔，约束写入延迟。
	Interval time.Duration
}

// Run 运行写协程，直至 Outbox 停止。
func (w *AggregateWriter) Run(o *Outbox) {
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	agg := w.New()
	var pending []PushBatch
	flush := func() {
		w.flush(o, agg, pending)
		agg = w.New()
		pending = pending[:0]
	}

	// draining 标记：quit 关闭后改为「排空 outbox 直至队列空」，不再看时间窗口。
	draining := false
	for {
		var b PushBatch
		if draining {
			select {
			case b = <-o.Batches():
			default:
				// 队列已空：刷出最后一窗后退出。写失败转本地缓存，
				// 避免优雅停机/热更时队列中尚未写入的批次静默丢失。
				w.flush(o, agg, pending)
				return
			}
		} else {
			select {
			case <-o.Quit():
				draining = true
				continue
			case b = <-o.Batches():
			case <-ticker.C:
				flush() // 未达行数也按时间窗口刷出（空窗时 no-op）
				continue
			}
		}

		// 追加本批次后按行数决定是否刷出；追加与刷出两条路径共用
		//（draining 分支的批次也走这里，停服收尾与实时写入语义一致）。
		for !agg.Append(b) {
			// 与已聚合的负载冲突（跨采集轮次同一设备+点位）：先刷出本窗再追加，
			// 避免跨轮同地址被批内去重丢弃。Aggregator 契约保证新负载必定接受
			// 本批次，故最多刷出一次。
			flush()
		}
		pending = append(pending, b)
		if agg.Rows() >= w.MaxRows {
			flush()
		}
	}
}

// flush 把当前聚合窗写入下游。
// 失败且启用本地缓存时，窗内批次全部转投缓存待重连补发（不计数，补发成功仍计入
// publishCount）；未启用本地缓存时本窗数据被丢弃，计入 droppedCount。
func (w *AggregateWriter) flush(o *Outbox, agg Aggregator, pending []PushBatch) {
	if agg.Empty() {
		return
	}
	if w.Write(agg.Payload()) {
		return
	}
	if o.SpoolEnabled() {
		for _, b := range pending {
			o.SpoolEnqueue(b)
		}
		return
	}
	o.Drop(uint64(len(pending)))
}
