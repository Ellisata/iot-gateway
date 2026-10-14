// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"fmt"
	"sync"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// modbusPlan 单批次点位的区间规划：按功能码分组的地址列表 + 每组读取区间。
type modbusPlan struct {
	groups map[byte][]po.DeviceAddress
	ranges map[byte][]AddressRange
	// bad 计划期就被排除的点位（见 partitionBadAddrs 与 modbusExcluded）。它们不在任何
	// 功能码组里，readBatch 末尾「addrs 里每个点位都恰好有一条记录」的重组覆盖不到 ——
	// 不带过来的话那条重组会产出一条零值记录（DeviceAddressID/DataType/Kind 全空），
	// 违反 read.go 里写死的契约，下游 MQTT 负载与时序库表结构正靠 DataType 决定怎么写。
	// 只读共享：在 planFor 的慢路径里随 groups/ranges 一起构造，此后不再修改。
	// 它是**计划输入的纯函数**（RangeSig 含 Name 与 DataType，把地址或类型改对即换新
	// 计划），因此不需要 TTL —— 降级计划那份 bad 才需要（它取决于设备侧回答）。
	bad []badAddr
}

// degradedPlan 对半降级后的区间（只有降过级的批次才有条目）：各功能码组的区间 + 时间戳。
type degradedPlan struct {
	ranges map[byte][]AddressRange
	// bad 已确认读不到的地址 → 质量码（恒为 driver.QualityBadAddress）。
	// 质量码只在**确认那一轮**由降级路径算出，而确认之后该地址就被摘出计划、
	// 不再参与任何一次解析 —— 不把它一并缓存，此后每轮的质量码都会是“未归类”，
	// "永不自我恢复"这件事在数据上就看不出来（点位级报警按连续 N 轮判定时永远触发不了）。
	// 生命周期与 ranges 完全一致：同一条 TTL，地址回到合并区间重试时它同时消失。
	bad map[string]int
	at  time.Time
}

// planCache modbus 批次区间规划缓存（内容指纹 → 规划）。
type planCache struct {
	mu    sync.Mutex
	plans map[uint64]*modbusPlan
	// degraded 对半降级后的区间：读不到的地址已被摘出合并区间。
	// 按 degradedPlanTTL 过期 —— 不设 TTL 的话，设备侧后来补上的地址永远拿不回来。
	degraded map[uint64]degradedPlan
}

// degradedPlanTTL 降级计划的有效期。
//
// 到期后按合并规划重读一次：设备侧把那个寄存器补上（或把 string 的声明长度改对）
// 之后，采集能自己恢复，不需要人工重启网关或改配置。取值与 ABB 的 hints 静默期
// 同量级（300s）。
const degradedPlanTTL = 5 * time.Minute

// planFor 返回批次的区间规划：命中内容指纹缓存直接返回，未命中则计算并缓存。
// 规划结果只读共享（读取路径不修改 groups/ranges），并发 Read 复用同一缓存安全。
// 缓存大小有界：超上限时清空重建，正确性不受影响（仅失去命中）。
func (c *planCache) planFor(sig uint64, addrs []po.DeviceAddress, cfg *ModbusTcpConfig) (*modbusPlan, error) {
	c.mu.Lock()
	if c.plans == nil {
		c.plans = make(map[uint64]*modbusPlan)
	}
	if p, ok := c.plans[sig]; ok {
		c.mu.Unlock()
		return p, nil
	}
	c.mu.Unlock()

	// 先把进不了计划的点位拆一刀（地址认不出，或配置模式下低于 startAddress）：
	// 它们不该让整批失败，而是随计划带出去、在 readBatch 里单独标 QualityBadAddress。
	// 这一步在 groupAddrsByFC 与 CalcReadRanges **之前** —— 两者见到的因此都是合法输入。
	valid, bad := partitionBadAddrs(addrs, cfg.StartAddress)

	groups, err := groupAddrsByFC(valid, cfg)
	if err != nil {
		return nil, err
	}
	p := &modbusPlan{groups: groups, ranges: make(map[byte][]AddressRange, len(groups)), bad: bad}
	for fc, gAddrs := range groups {
		// 配置模式下 groupAddrsByFC 对空输入也会写出一个 nil 值的键（read.go 的短路
		// 分支），喂给 CalcReadRanges 会撞上它那条 "no addresses to read" —— 而那是
		// 「这批点位一个都读不了」，不是「这批里一个点位都没有」。
		if len(gAddrs) == 0 {
			continue
		}
		opts := CalcReadRangeOptions{
			StartAddress: cfg.StartAddress,
			Quantity:     cfg.Quantity,
			MergeWindow:  cfg.MergeWindow,
			StringLen:    cfg.StringLen,
		}
		ranges, err := CalcReadRanges(gAddrs, opts)
		if err != nil {
			return nil, fmt.Errorf("modbus: calc address ranges (fc=%d) failed: %w", fc, err)
		}
		p.ranges[fc] = ranges
	}

	c.mu.Lock()
	// 再次检查，避免并发首轮重复计算同批规划
	if existing, ok := c.plans[sig]; ok {
		c.mu.Unlock()
		return existing, nil
	}
	if len(c.plans) >= driver.RangeCacheMaxEntries {
		c.plans = make(map[uint64]*modbusPlan)
	}
	c.plans[sig] = p
	c.mu.Unlock()
	return p, nil
}

// rangesFor 返回本功能码组本轮要读的区间：有未过期的降级结果就用它，否则用合并规划。
//
// 降级结果过期即删除并回退到合并规划 —— 让被摘出的地址回到合并区间里试一次
// （设备侧补上那个寄存器之后，采集能自愈，不必人工重启或改配置）。
func (c *planCache) rangesFor(sig uint64, fc byte, merged []AddressRange) []AddressRange {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.degraded[sig]
	if !ok {
		return merged
	}
	if time.Since(d.at) >= degradedPlanTTL {
		delete(c.degraded, sig)
		return merged
	}
	if r, ok := d.ranges[fc]; ok {
		return r
	}
	return merged
}

// storeDegraded 记下降级后的区间与已确认坏点（各功能码组一份，只有降过级的批次才写）。
//
// 不回写的话，每轮都要为同一个坏地址重付一遍"整段失败 + 对半降级"的往返；
// 回写之后坏地址被摘在计划之外，后续轮次只发那些读得通的子区间。
//
// bad 是**本轮为止**全部已知坏点的质量码并集（上一轮缓存下来的 + 本轮新确认的），
// 由调用方传入的 st.qualities 天然满足 —— 它在 Read 开头由 badFor 播种。
//
// **已有降级结果时保留它原来的时间戳**，不刷新成 now：每轮刷新会把 TTL 变成
// "永不过期"，而被摘出的地址正是靠 TTL 到期才回到合并区间里重试一次 ——
// 只要批次里还有另一个区间每轮都在降级（或每轮预算耗尽），它就会一直被推迟，
// 设备侧补上地址之后采集**永远**自愈不了，而不是"最迟 5 分钟"。
func (c *planCache) storeDegraded(sig uint64, ranges map[byte][]AddressRange, bad map[string]int) {
	at := time.Now()

	c.mu.Lock()
	if prev, ok := c.degraded[sig]; ok {
		at = prev.at
	}
	if c.degraded == nil || len(c.degraded) >= driver.RangeCacheMaxEntries {
		c.degraded = make(map[uint64]degradedPlan)
	}
	c.degraded[sig] = degradedPlan{ranges: ranges, bad: bad, at: at}
	c.mu.Unlock()
}

// badFor 返回缓存中已确认读不到的地址及其质量码，供本轮预填骨架回填。
// 没有降级计划、或它已过期时返回 nil（过期由 rangesFor 负责删除，这里只判定不删）。
func (c *planCache) badFor(sig uint64) map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.degraded[sig]
	if !ok || time.Since(d.at) >= degradedPlanTTL {
		return nil
	}
	return d.bad
}

// clear 清空规划缓存（Connect 时调用，配置可能变化）。
// 降级结果在同一张缓存里，一并失效 —— 重连后设备侧可能已经补上了那个地址。
func (c *planCache) clear() {
	c.mu.Lock()
	c.plans = nil
	c.degraded = nil
	c.mu.Unlock()
}
