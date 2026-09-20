// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"sync"

	"iot-gateway/driver"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// planEntry 单个点位的读取规划条目。
type planEntry struct {
	index    int    // 在传入 addrs 中的下标（结果需按此顺序回填）
	dataType string // 解析后的协议内部数据类型裸名（决定输出形态与 Kind）
	spec     DISpec // 数据标识规格
	err      error  // 地址解析失败原因；非 nil 时该点位固定 Quality=0
}

// planCache 批次读取规划缓存（内容指纹 → 规划）。
type planCache struct {
	mu    sync.Mutex
	plans map[uint64]*readPlan
}

// planFor 返回批次的读取规划：命中内容指纹缓存直接返回，未命中则计算并缓存。
//
// 规划结果只读共享（读取路径不修改 entries/groups），并发 Read 复用同一缓存安全。
// 缓存以 driver.RangeSig(addrs) 为键，而协议版本、数据标识字典等影响解析的配置
// 只在 Connect 时变化（Connect 会清空缓存），因此缓存键是充分的。
func (c *planCache) planFor(sig uint64, addrs []po.DeviceAddress, cfg *DLT645Config) *readPlan {
	c.mu.Lock()
	if c.plans == nil {
		c.plans = make(map[uint64]*readPlan)
	}
	if p, ok := c.plans[sig]; ok {
		c.mu.Unlock()
		return p
	}
	c.mu.Unlock()

	p := buildPlan(addrs, cfg)

	c.mu.Lock()
	// 再次检查，避免并发首轮重复计算同批规划
	if existing, ok := c.plans[sig]; ok {
		c.mu.Unlock()
		return existing
	}
	if len(c.plans) >= driver.RangeCacheMaxEntries {
		c.plans = make(map[uint64]*readPlan)
	}
	c.plans[sig] = p
	c.mu.Unlock()
	return p
}

// clear 清空规划缓存（Connect 时调用，配置可能已变化）。
func (c *planCache) clear() {
	c.mu.Lock()
	c.plans = nil
	c.mu.Unlock()
}

// buildPlan 解析每个点位的数据标识并按 maxDIsPerRead 分组。
//
// 单个点位地址非法（不在字典且未显式给出字节数、格式错误等）**不会**使整批失败：
// 该点位记为解析失败，读取时固定 Quality=0，其余点位照常采集。
// 这与「单个不支持的 DI 不能拖垮整台设备」的故障隔离原则一致。
func buildPlan(addrs []po.DeviceAddress, cfg *DLT645Config) *readPlan {
	p := &readPlan{entries: make([]planEntry, len(addrs))}

	perRead := cfg.MaxDIsPerRead
	if perRead < 1 {
		perRead = 1
	}
	// 1997 版读命令的数据域固定为一个 2 字节数据标识，不支持一次读多个
	if !cfg.Is2007() {
		perRead = 1
	}

	var pending []int
	flush := func() {
		if len(pending) > 0 {
			p.groups = append(p.groups, readGroup{idx: pending})
			pending = nil
		}
	}

	for i := range addrs {
		a := &addrs[i]
		spec, err := ParseAddress(a.Name, cfg.Version)
		p.entries[i] = planEntry{
			index:    i,
			dataType: a.DataType,
			spec:     spec,
			err:      err,
		}
		if err != nil {
			logger.Warn("dlt645: 点位 %q（id=%s）地址解析失败，该点位将标记为异常: %v",
				a.Name, a.ID, err)
			continue
		}
		pending = append(pending, i)
		if len(pending) >= perRead {
			flush()
		}
	}
	flush()

	return p
}

// readDI 从数据域头部还原数据标识（线上为低字节在前）。
func readDI(b []byte) uint32 {
	var v uint32
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | uint32(b[i])
	}
	return v
}
