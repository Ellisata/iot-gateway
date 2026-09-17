// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import "iot-gateway/logger"

// DrainFunc 补发策略：把补发窗口内的批次按入队序写向下游，写成功的批次
// 由策略自身调用 Spool().DeleteBatch 删除（就地推进，不必等整窗结束）。
//
// 返回是否本窗全部写成功；失败时 failID 为首个未写成功的批次 ID（无失败为 0），
// 供上层的毒批次判定使用。各组件的实现见 mqttChannel.drainWindow 与 GroupedDrainer.Drain。
type DrainFunc func(pend []SpoolBatch, narrow bool) (ok bool, failID int64)

// DrainPolicy 补发失败的处理策略。各通道按自身错误语义选择：写入被下游明确
// 拒绝（SQL 报错 / HTTP 4xx）属数据类错误，需要判毒；纯粹的超时类失败
// （mqtt 发布超时，broker 慢而非数据坏）仍按无限重试处理，避免误删好数据。
type DrainPolicy struct {
	// Poison 同一队头批次在连接恢复后仍写失败即判为毒批次（数据类错误）：
	// 删除并计入 dropped，避免队头永久阻塞后续所有补发。
	Poison bool
	// Narrow 首次失败后强制逐批试写以隔离真正失败的批次，避免坏点连坐整组
	// 被上层毒批次判定误删。仅在 Poison 为 true 时有意义。
	Narrow bool
}

// DrainLoop 断网缓存补发协程：连接可用时按入队序取回未发完的批次，
// 写成功即删除；失败（断连/超时）保留待重连重试。
//
// 空缓存时阻塞等待事件（新批次落盘/重连）唤醒，无固定轮询；
// 兜底 timer（见 spoolDrainBackstop）仅防漏 ping，空闲时对 SQLite 零无效查询。
//
// 由通道在自己的 worker 组内以 goroutine 运行，随 quit 关闭而退出。
func (o *Outbox) DrainLoop(drain DrainFunc, policy DrainPolicy) {
	var poisonBatchID int64
	var narrow bool

	for {
		// 未连接：等待重连或新批次落盘唤醒，不做无效查询
		if !o.isConnected() {
			if !o.WaitDrain() {
				return
			}
			continue
		}

		pend, err := o.spool.FetchOldest(spoolDrainWindow)
		if err != nil {
			if !o.WaitDrain() {
				return
			}
			continue
		}
		if len(pend) == 0 {
			// 空缓存：阻塞等待写入/重连事件
			if !o.WaitDrain() {
				return
			}
			continue
		}

		ok, failID := drain(pend, narrow)
		if ok {
			poisonBatchID = 0
			narrow = false // 本窗全部写成功，恢复正常聚合
			// 本窗恰好取满说明可能仍有积压，立即续取，不 sleep；
			// 已到尾部则回等待，避免对空缓存空转
			if len(pend) == spoolDrainWindow {
				continue
			}
			if !o.WaitDrain() {
				return
			}
			continue
		}

		// 写入失败。同一队头批次若在连接恢复后（本循环仅在连接可用时进入，
		// 失败后须等重连事件才能再次进入）仍写失败 ⇒ 连接健康而写入被拒，
		// 是数据类错误（毒批次）。跳过（删除 + 记 dropped）而非无限重试，
		// 避免队头永久阻塞后续所有补发。
		if policy.Poison && poisonBatchID == failID {
			poisonBatchID = 0
			o.droppedCount.Add(1)
			logger.Error("%s: spool batch %d rejected permanently (data error), drop to unblock drain", o.tag, failID)
			if err := o.spool.Delete(failID); err != nil {
				logger.Error("%s: spool delete poison batch %d failed: %v", o.tag, failID, err)
			}
			continue // 保持窄化，继续定位后续可能存在的坏批次
		}
		poisonBatchID = failID
		if policy.Narrow {
			// 失败组可能混有多批次而坏点不在组头，直接按组头判毒会连坐误删好数据；
			// 置窄化后逐批试写，重连时即可隔离出真正失败的批次。
			narrow = true
		}

		if !o.WaitDrain() {
			return
		}
	}
}

// Aggregator 把补发窗口内的若干批次聚合为一次传输的负载。
// 由各通道的负载构造器实现（tdengine 的 insertBuilder、influxdb 的 lineBuilder），
// 使两者的实时写入与补发走同一套负载构造。
//
// 契约：**新构造的空聚合器必须接受任意批次**（Append 不得在空负载上返回 false）。
// 调用方（AggregateWriter / GroupedDrainer）在 Append 返回 false 时先刷出当前负载
// 再用同一个批次重试，若空负载也拒绝就会刷出空负载并陷入死循环。
type Aggregator interface {
	// Append 追加一个批次，返回是否与当前负载兼容
	// （false 表示冲突，本负载到此为止，该批次留给下一个负载）。
	Append(b PushBatch) bool
	// Rows 当前负载已聚合的记录行数。
	Rows() int
	// Empty 当前负载是否为空。
	Empty() bool
	// Payload 输出线上负载。
	Payload() string
}

// GroupedDrainer 「聚合写回」式的补发策略：把窗口内批次按行数上限聚合成
// 尽量少的负载发出，写成功即删除对应缓存行。tdengine 与 influxdb 共用。
type GroupedDrainer struct {
	Tag     string            // 日志前缀
	Spool   Spool             // 缓存，写成功的批次从中删除
	New     func() Aggregator // 每个新负载重建聚合器
	Write   func(string) bool // 下发一次负载，返回是否成功
	MaxRows int               // 单个负载的最大记录行数
}

// Drain 实现 DrainFunc。
//
//   - narrow 为 true 时强制单批一组：某次组写入失败后逐批试写，隔离真正失败的
//     批次，避免坏点连坐整组被上层毒批次判定误删好数据；
//   - 否则按行数达 MaxRows 切分负载，避免单次请求过大；
//   - 任一负载写入失败即停止，保留本组及后续待重连重试（断连/超时），
//     避免对故障下游空转；
//   - 空负载批次（理论上不会）不写下游但一并删除，避免阻塞后续补发。
//
// 返回是否本窗口全部写成功；失败时 failID 为首个未写入成功的批次 ID
// （成功写出的组已删除，故该 ID 即 spool 队头，重试仍会从它开始）。
func (d *GroupedDrainer) Drain(pend []SpoolBatch, narrow bool) (ok bool, failID int64) {
	for len(pend) > 0 {
		agg := d.New()
		var group []SpoolBatch

		for i := range pend {
			p := &pend[i]
			if narrow {
				if len(group) > 0 {
					break // 窄化：单批一组，逐个隔离
				}
			} else if agg.Rows() > 0 && agg.Rows()+len(p.Records) > d.MaxRows {
				break
			}
			if !agg.Append(PushBatch{DeviceID: p.DeviceID, CollectedAt: p.CollectedAt, Records: p.Records}) {
				break // 冲突：本负载到此为止，该批次留给下一个负载
			}
			group = append(group, pend[i])
		}
		if len(group) == 0 {
			return false, pend[0].ID // 防御：首个批次即无法追加，交由上层等待重试
		}

		if !agg.Empty() && !d.Write(agg.Payload()) {
			return false, group[0].ID
		}
		ids := make([]int64, 0, len(group))
		for i := range group {
			ids = append(ids, group[i].ID)
		}
		if err := d.Spool.DeleteBatch(ids); err != nil {
			logger.Error("%s: spool delete batch failed (n=%d): %v", d.Tag, len(ids), err)
		}
		pend = pend[len(group):]
	}
	return true, 0
}
