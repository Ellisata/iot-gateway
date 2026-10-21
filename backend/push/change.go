// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"sync"

	"iot-gateway/collector"
)

// changeTracker 记录每个点位上次**真正交给下游**的值与质量码，用于「变化才推送」
// （configFile push.changeOnly）。
//
// 语义只有三条：
//   - 没有上次记录（首轮、重启后、通道重建后）→ 投递；
//   - 值或质量码任一不同 → 投递；
//   - 两者都相同 → 跳过。
//
// 质量码参与比较是刻意的：点位读数从正常（192）掉到"坏"（<128）时 value 可能是
// 同一个占位串，只看值会让下游完全看不到这次状态变化。
//
// 键为 (deviceID, deviceAddressID)。地址 ID 是库里的 UUID，本身全局唯一，带上设备 ID
// 只是为了防御外部来源或历史数据里出现重复地址 ID。**缺失任一 ID 的记录一律透传**：
// 去重键退化成空串时，宁可重复投递也不能把不同点位合并成一条。
//
// 并发：PushRecords 由 workerPool 里多个设备的采集协程并发调用，故整体加锁。
// 临界区只有 map 查找与字符串比较，单批上限 maxPointsPerRead（2000）条，
// 持锁时间是微秒级，不构成采集热点。
//
// 内存：条目数与「本次运行以来见过的点位」同阶，正常部署下就是配置的点位总数；
// 删除点位/设备留下的旧条目不会被回收，但每个条目只有两个短字符串与一个整数，
// 相对网关常驻内存可忽略。刻意不做定期清理：那需要额外的时钟与阈值，
// 而清理逻辑本身一旦写错就会表现为"值变了却不再推送"。
type changeTracker struct {
	mu   sync.Mutex
	last map[changeKey]changeState
}

// changeKey 点位身份。字段取地址 ID（而非数组下标）—— 下标会随点位增删漂移，
// 用它做键会让「新增一个点位」把后面所有点位的上次值错位对上。
type changeKey struct {
	deviceID  string
	addressID string
}

// changeState 一次投递的点位事实。只放参与比较的字段：
// 名称、说明、数据类型变了但值没变时不算"点位数据变化"，
// 因此改个点位名不会触发一轮全量重推。
type changeState struct {
	value   string
	quality int
}

func newChangeTracker() *changeTracker {
	return &changeTracker{last: make(map[changeKey]changeState)}
}

// filter 返回 records 中自上次投递以来发生了变化的子集，并把它们的当前值记为
// 「已投递」。
//
// 输入切片不会被修改（采集侧仍持有它，实时值快照也已读过同一份数据）；
// 输出是新切片，保持原顺序 —— 下游的批次切分与点位顺序依赖这一点。
//
// 调用方必须确认这批记录确实会进入至少一个通道（见 Engine.pushRecords）：
// 如果通道尚未就绪就把状态记成"已投递"，稳定不变的点位会在通道恢复后一直等不到
// 下一次变化，表现为下游缺数。
func (c *changeTracker) filter(records []collector.CollectedRecord) []collector.CollectedRecord {
	out := make([]collector.CollectedRecord, 0, len(records))

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, r := range records {
		if r.DeviceID == "" || r.DeviceAddressID == "" {
			out = append(out, r)
			continue
		}
		key := changeKey{deviceID: r.DeviceID, addressID: r.DeviceAddressID}
		state := changeState{value: r.Value, quality: r.Quality}
		if prev, ok := c.last[key]; ok && prev == state {
			continue
		}
		c.last[key] = state
		out = append(out, r)
	}
	return out
}

// reset 清空已投递记录，使下一轮采集的所有点位都被视为"首次出现"。
//
// 用在推送通道集合发生实质变化时（新增通道或配置变更导致通道实例重建）：
// 新通道没有见过任何历史值，若沿用旧的已投递状态，稳定不变的点位会在它上面
// 永远没有初值。代价是既有通道也会收到一次全量 —— 通道配置变更本就不频繁，
// 这一轮重复远比"新通道缺数且无从发现"更可接受。
func (c *changeTracker) reset() {
	c.mu.Lock()
	c.last = make(map[changeKey]changeState)
	c.mu.Unlock()
}
