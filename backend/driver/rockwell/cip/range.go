// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"fmt"
	"hash/maphash"
	"sort"
	"strconv"
	"strings"
	"sync"

	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// RockwellTag 共享同一读取键的点位组。
// 常规标签一组一发 0x4C 请求，结果复制给组内各点位（各点位按自己的
// DataType 解码）；数组区间组整组只发一次 Read Tag Elements。
type RockwellTag struct {
	Name  string              // 读取键：原始标签名（常规）或数组基础名（合并区间）
	Addrs []*po.DeviceAddress // 共享该读取键的点位
}

// maxTagElements 单次 Read Tag Elements 的元素数上限。
// Logix 控制器单报文 PDU 典型 508 字节：4 字节元素 × 125 = 500 字节 < 508，
// 基本类型数组在该上限内可单帧读完；更大的数组组按下标切分为多个区间分帧读。
const maxTagElements = 125

// arrayRange 一个可合并的数组区间分块（一次 Read Tag Elements 事务）。
// 以基础标签名读 elemCount 个元素，响应 payload 为连续小端元素数据，
// 各点位按 (index-offset)*elemSize 切片解码。
type arrayRange struct {
	tag       RockwellTag // 该区间覆盖的点位组（Name = 数组基础名）
	indices   []int       // 组内各点位的原始数组下标（与 tag.Addrs 一一对应）
	offset    int         // 区间起始下标 = min(indices)
	elemCount uint16      // 发给 0x4C 的元素个数 = max(indices)-offset+1
}

// tagPlan 一次 Read 调用的完整执行计划：常规标签组 + 数组区间分块与元素大小。
type tagPlan struct {
	tags  []RockwellTag
	arrs  []*arrayRange  // 数组区间分块（每个分块一次 Read Tag Elements）
	sizes map[string]int // 数组基础名 -> 元素字节数（区间切片步长）
}

// arrCache 标签读取计划的进程级缓存（指纹 -> *tagPlan）。
// 地址集合在任务运行期不变（collector 热加载时才重建点位列表），
// 以点位内容指纹缓存解析/聚合/数组分析结果，消除海量点位下每个采集周期
// 的重复解析与 map 聚合（纯 CPU/GC 开销）。缓存条目常量级（每地址集合一份），
// 随热刷新旧任务丢弃而自然被新指纹取代，不设过期。
var arrCache sync.Map

// planHash 点位列表指纹的哈希种子（进程内随机，防外部构造碰撞）。
var planHash = maphash.MakeSeed()

// getPlan 返回点位列表的读取计划（命中缓存直接复用）。
func getPlan(addrs []po.DeviceAddress) (*tagPlan, error) {
	key := planKey(addrs)
	if v, ok := arrCache.Load(key); ok {
		return v.(*tagPlan), nil
	}

	plan, err := buildPlan(addrs)
	if err != nil {
		return nil, err
	}

	v, _ := arrCache.LoadOrStore(key, plan)
	return v.(*tagPlan), nil
}

// buildPlan 从点位列表构建读取计划。
//
// 分组策略：
//  1. 全量 ParseRockwellAddress，非法标签直接报错（对齐 modbus/s7/fins/cip）；
//  2. "base[N]" 形态的标签按基础名聚合为候选数组组，其余按完整标签名聚合
//     （同一标签只发一次请求），保序输出；
//  3. 数组组满足「类型一致且已注册、元素大小固定」时按下标排序切分为
//     ≤maxTagElements 的区间分块，每块一次 Read Tag Elements；否则拆回
//     原始标签名逐点读（组内混有结构体成员、多维下标、STRING 等动态长度
//     类型时无法安全按固定步长切片——显式保守，不猜类型布局）；
//  4. 常规组内多个点位类型不同时允许：读回的原始数据各点位按自己的类型解码
//     （bool 与 int32 读同一 DINT 标签的场景）。
func buildPlan(addrs []po.DeviceAddress) (*tagPlan, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("rockwell: no addresses to read")
	}

	plan := &tagPlan{
		sizes: make(map[string]int),
	}

	// 常规组（完整标签名聚合）
	type keyKind int
	const (
		kindPlain keyKind = iota
		kindArray
	)
	type groupKey struct {
		kind keyKind
		name string // 完整标签名（plain）或基础名（array）
	}

	order := make([]groupKey, 0, len(addrs))
	plains := make(map[string]*RockwellTag)

	type arrayAcc struct {
		addrs    []*po.DeviceAddress
		indices  []int
		dataType string // 首个点位类型，用于一致性校验
	}
	arrays := make(map[string]*arrayAcc)

	for i := range addrs {
		a := &addrs[i]
		tag, ok := ParseRockwellAddress(a.Name)
		if !ok {
			return nil, fmt.Errorf("rockwell: address %q (id=%s) invalid tag name", a.Name, a.ID)
		}
		if base, idx, isArr := splitArrayIndex(tag); isArr {
			acc, ok := arrays[base]
			if !ok {
				acc = &arrayAcc{dataType: a.DataType}
				arrays[base] = acc
				order = append(order, groupKey{kindArray, base})
			}
			acc.addrs = append(acc.addrs, a)
			acc.indices = append(acc.indices, idx)
			continue
		}
		g, ok := plains[tag]
		if !ok {
			g = &RockwellTag{Name: tag}
			plains[tag] = g
			order = append(order, groupKey{kindPlain, tag})
		}
		g.Addrs = append(g.Addrs, a)
	}

	// 常规组直接输出
	for _, k := range order {
		if k.kind == kindPlain {
			plan.tags = append(plan.tags, *plains[k.name])
		}
	}

	// 数组组：可合并 → 按下标切分为 ≤maxTagElements 的区间分块，逐块一次
	// Read Tag Elements；否则（混类型/动态长度元素）拆回原始标签名逐点读
	for _, k := range order {
		if k.kind != kindArray {
			continue
		}
		base := k.name
		acc := arrays[base]
		elemSize := elemSizeOf(acc.dataType, acc.addrs)
		if elemSize <= 0 {
			// 不可合并：拆回原始标签名，每组一发普通 0x4C
			for _, a := range acc.addrs {
				plan.tags = append(plan.tags, RockwellTag{
					Name:  strings.TrimSpace(a.Name),
					Addrs: []*po.DeviceAddress{a},
				})
			}
			continue
		}

		// 下标排序后贪心切分：每块以最小未分配下标为起点，收满 maxTagElements
		// 个（或下标超出 elemCount 上限）即封块，保证块内 (index-offset) 连续覆盖
		plan.sizes[base] = elemSize
		ord := make([]int, len(acc.addrs))
		for i := range ord {
			ord[i] = i
		}
		sort.Slice(ord, func(x, y int) bool { return acc.indices[ord[x]] < acc.indices[ord[y]] })
		for s := 0; s < len(ord); {
			e := s
			start := acc.indices[ord[s]]
			for e < len(ord) && e-s < maxTagElements && acc.indices[ord[e]]-start < maxTagElements {
				e++
			}
			chunk := &arrayRange{offset: start}
			for _, i := range ord[s:e] {
				chunk.tag.Addrs = append(chunk.tag.Addrs, acc.addrs[i])
				chunk.indices = append(chunk.indices, acc.indices[i])
				if acc.indices[i]-start+1 > int(chunk.elemCount) {
					chunk.elemCount = uint16(acc.indices[i] - start + 1)
				}
			}
			chunk.tag.Name = base
			plan.arrs = append(plan.arrs, chunk)
			s = e
		}
	}

	logger.Debug("rockwell: calc %d read tags (%d array chunks) from %d addresses",
		len(plan.tags), len(plan.arrs), len(addrs))

	return plan, nil
}

// planKey 计算点位列表的内容指纹。
// 各字段以 uvarint 长度前缀写入 maphash（复用栈缓冲，零堆分配），
// 长度分隔保证不同点位序列哈希空间不相交。
func planKey(addrs []po.DeviceAddress) uint64 {
	var h maphash.Hash
	h.SetSeed(planHash)
	var buf [10]byte
	writeField := func(s string) {
		n := binaryPutUvarint(&buf, uint64(len(s)))
		h.Write(buf[:n])
		h.WriteString(s)
	}
	for i := range addrs {
		a := &addrs[i]
		writeField(a.Name)
		writeField(a.DataType)
		writeField(a.ID)
	}
	return h.Sum64()
}

// binaryPutUvarint 与 encoding/binary.PutUvarint 相同（免引包，返回写入字节数）。
func binaryPutUvarint(buf *[10]byte, v uint64) int {
	i := 0
	for v >= 0x80 {
		buf[i] = byte(v) | 0x80
		v >>= 7
		i++
	}
	buf[i] = byte(v)
	return i + 1
}

// splitArrayIndex 识别 "base[N]" 形态的标签名，返回基础名与非负十进制下标 N。
// 其余形态（结构体成员、多维下标 base[i,j]、位访问 base.N、程序作用域等）
// 一律不识别 → 返回 false。
func splitArrayIndex(name string) (base string, index int, ok bool) {
	l := strings.LastIndexByte(name, '[')
	if l <= 0 || !strings.HasSuffix(name, "]") {
		return "", 0, false
	}
	sub := name[l+1 : len(name)-1]
	idx, err := strconv.Atoi(sub)
	if err != nil || idx < 0 {
		return "", 0, false
	}
	// 基础名内不允许再出现下标/成员符号（防 "A[0].B[1]" 被误判为单维数组）
	base = name[:l]
	if strings.ContainsAny(base, "[].") {
		return "", 0, false
	}
	return base, idx, true
}

// elemSizeOf 数组元素字节数。
// 组内所有点位类型一致且已注册、大小为固定值时返回其大小；
// STRING（动态长度，Size=0）、混合类型或未注册类型返回 0 → 不合并。
func elemSizeOf(first string, addrs []*po.DeviceAddress) int {
	for _, a := range addrs {
		if a.DataType != first {
			return 0
		}
	}
	dt, ok := rockwellLookup(first)
	if !ok || dt.Size <= 0 {
		return 0
	}
	return dt.Size
}
