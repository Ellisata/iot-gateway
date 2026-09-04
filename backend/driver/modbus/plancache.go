// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbus

import (
	"fmt"
	"sync"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// modbusPlan 单批次点位的区间规划：按功能码分组的地址列表 + 每组读取区间。
type modbusPlan struct {
	groups map[byte][]po.DeviceAddress
	ranges map[byte][]AddressRange
}

// planCache modbus 批次区间规划缓存（内容指纹 → 规划）。
type planCache struct {
	mu    sync.Mutex
	plans map[uint64]*modbusPlan
}

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

	groups, err := groupAddrsByFC(addrs, cfg)
	if err != nil {
		return nil, err
	}
	p := &modbusPlan{groups: groups, ranges: make(map[byte][]AddressRange, len(groups))}
	for fc, gAddrs := range groups {
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

// clear 清空规划缓存（Connect 时调用，配置可能变化）。
func (c *planCache) clear() {
	c.mu.Lock()
	c.plans = nil
	c.mu.Unlock()
}
