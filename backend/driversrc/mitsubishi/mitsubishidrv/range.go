// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"fmt"
	"sort"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// parsedAddr 解析后的单个点位（含读取模式与跨度），供区间聚类使用。
type parsedAddr struct {
	id      string
	addr    MCAddress
	bitMode bool
}

// CalcMCRanges 从点位列表计算读取区间。
//
// 策略：
//  1. 全量解析 name，非法地址直接报错（对齐 modbus/fins）；字模式下单点位跨度就
//     超过 maxReadWords 同样报错（数据须连续、不能拆块）。**两条都不该由采集路径
//     触发** —— partitionBadAddrs 已在上游把它们摘走，这里留着是给直接调用方
//     （契约测试）兜底；
//  2. 按 (设备, 读取模式) 分组——不同设备地址空间独立，不能混帧。读取模式：
//     - 位模式（bool 位设备，如 M10）：位单位读取（子命令 0x0001），1 点 = 1 位；
//     - 字模式（其余）：字单位读取（子命令 0x0000），1 点 = 1 字（16 位），
//     多字类型（int32/float64/string）按字节数折算占字数。
//  3. 组内排序合并：相邻/交叠/间隙 ≤ maxGap 的区间合并；合并后跨度超 maxReadWords 分块；
//  4. 位设备字访问（非 bool 类型）读头对齐到 16 位边界（floor(num/16)*16）；
//  5. 字设备 bool（D100 / D100.5）读取所在字，由 ParseMCValue 本地取位。
//
// 每个返回的 MCRange 含该段独有的 AddressMap（DeviceAddress.ID → 已填充
// Word/BitMode/SpanWords 的 MCAddress），供 Read 解码时直接使用。
func CalcMCRanges(addrs []po.DeviceAddress, cfg *MCConfig) ([]MCRange, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("mc: no addresses to read")
	}

	maxGap := cfg.MaxGap
	if maxGap < 0 {
		maxGap = 0
	}
	maxWords := mcMaxReadWords(cfg)
	stringLen := mcStringLen(cfg)

	// 全量解析 name，并计算读取模式与跨度
	parsed := make([]parsedAddr, 0, len(addrs))
	for _, a := range addrs {
		addr, bitMode, ok := mcParseAddr(a, stringLen)
		if !ok {
			return nil, fmt.Errorf("mc: address %q (id=%s) invalid format", a.Name, a.ID)
		}
		// 单点位跨度超过单请求上限时无法分块（数据须连续），直接报错而非静默超限。
		// 判据与 partitionBadAddrs 共用 mcSpanTooLarge —— 上游已先摘过一刀，正常采集
		// 路径走到这里的点位不会触发它；留着是给直接调用 CalcMCRanges 的调用方
		// （range_test.go / pointscan_test.go 钉着这条契约）兜底。
		if mcSpanTooLarge(addr, maxWords) {
			return nil, fmt.Errorf("mc: address %q (id=%s) spans %d words, exceeds maxReadWords %d",
				a.Name, a.ID, addr.SpanWords, maxWords)
		}
		parsed = append(parsed, parsedAddr{id: a.ID, addr: addr, bitMode: bitMode})
	}

	// 按 (设备名, 读取模式) 分组：不同设备/模式地址空间独立，不能混帧
	type groupKey struct {
		device  string
		bitMode bool
	}
	groups := make(map[groupKey][]parsedAddr)
	for _, p := range parsed {
		key := groupKey{device: p.addr.Device.name, bitMode: p.bitMode}
		groups[key] = append(groups[key], p)
	}

	var ranges []MCRange
	for key, list := range groups {
		if key.bitMode {
			ranges = append(ranges, calcBitRanges(list, maxGap, maxWords)...)
		} else {
			ranges = append(ranges, calcWordRanges(list, maxGap, maxWords)...)
		}
	}

	// 排序保证输出顺序稳定
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Device.code != ranges[j].Device.code {
			return ranges[i].Device.code < ranges[j].Device.code
		}
		if ranges[i].BitMode != ranges[j].BitMode {
			return !ranges[i].BitMode // 字模式在前，位模式在后
		}
		return ranges[i].Start < ranges[j].Start
	})

	logDebug("mc: calc %d read ranges from %d addresses (maxGap=%d, maxWords=%d)",
		len(ranges), len(parsed), maxGap, maxWords)
	for _, r := range ranges {
		logDebug("mc:   range device=%s bit=%v [%d, %d) points=%d (%d points)",
			r.Device.name, r.BitMode, r.Start, r.Start+uint32(r.Points), r.Points, len(r.AddressMap))
	}

	return ranges, nil
}

// calcWordRanges 字模式区间聚类（按字地址排序、合并、分块）。
func calcWordRanges(list []parsedAddr, maxGap, maxWords int) []MCRange {
	sort.Slice(list, func(i, j int) bool {
		return list[i].addr.Word < list[j].addr.Word
	})

	curStart := list[0].addr.Word
	curEnd := curStart + list[0].addr.SpanWords - 1
	curMap := map[string]MCAddress{list[0].id: list[0].addr}

	var ranges []MCRange
	flush := func() {
		ranges = append(ranges, MCRange{
			Device:     list[0].addr.Device,
			BitMode:    false,
			Start:      curStart,
			Points:     uint16(curEnd - curStart + 1),
			AddressMap: curMap,
		})
	}

	for _, p := range list[1:] {
		start := p.addr.Word
		end := start + p.addr.SpanWords - 1

		// 分块：合并后跨度超过单请求上限时，先结束当前区间
		if int(end)-int(curStart)+1 > maxWords {
			flush()
			curStart, curEnd, curMap = start, end, map[string]MCAddress{p.id: p.addr}
			continue
		}

		// 相邻/交叠/间隙 ≤ maxGap：合并（跳过 gap 空洞读取）
		if gap := int(start) - int(curEnd) - 1; gap <= maxGap {
			if end > curEnd {
				curEnd = end
			}
			curMap[p.id] = p.addr
			continue
		}

		flush()
		curStart, curEnd, curMap = start, end, map[string]MCAddress{p.id: p.addr}
	}
	flush()
	return ranges
}

// calcBitRanges 位模式区间聚类（按位地址排序、合并、分块）。
func calcBitRanges(list []parsedAddr, maxGap, maxWords int) []MCRange {
	sort.Slice(list, func(i, j int) bool {
		return list[i].addr.Number < list[j].addr.Number
	})

	curStart := list[0].addr.Number
	curEnd := curStart // 每点 1 位
	curMap := map[string]MCAddress{list[0].id: list[0].addr}

	var ranges []MCRange
	flush := func() {
		ranges = append(ranges, MCRange{
			Device:     list[0].addr.Device,
			BitMode:    true,
			Start:      curStart,
			Points:     uint16(curEnd - curStart + 1),
			AddressMap: curMap,
		})
	}

	for _, p := range list[1:] {
		start := p.addr.Number
		end := start

		// 分块：合并后跨度超过单请求上限时，先结束当前区间
		if int(end)-int(curStart)+1 > maxWords {
			flush()
			curStart, curEnd, curMap = start, end, map[string]MCAddress{p.id: p.addr}
			continue
		}

		// 相邻/间隙 ≤ maxGap：合并
		if gap := int(start) - int(curEnd) - 1; gap <= maxGap {
			curMap[p.id] = p.addr
			curEnd = end
			continue
		}

		flush()
		curStart, curEnd, curMap = start, end, map[string]MCAddress{p.id: p.addr}
	}
	flush()
	return ranges
}

// mcMaxReadWords 归一化单请求读取上限（未配置/非法 → 默认，超硬上限 → 钳制）。
//
// 单独提出来是因为它有**两个调用点**：CalcMCRanges 的分块边界，与
// partitionBadAddrs 的「单点位就其本身就超限」判定。默认值与钳制只写在这里，
// 两处不会对同一份配置得出不同的上限。
func mcMaxReadWords(cfg *MCConfig) int {
	w := cfg.MaxReadWords
	if w <= 0 {
		w = defaultMaxReadWords
	}
	if w > maxReadWordsLimit {
		w = maxReadWordsLimit
	}
	return w
}

// mcStringLen 归一化 string 类型的读取字数（未配置/非法 → 默认）。
// 与 mcMaxReadWords 同理：两个调用点必须拿到同一个值，否则对同一个 string 点位
// 会算出不同的 SpanWords，进而对「是否超限」给出相反答案。
func mcStringLen(cfg *MCConfig) int {
	if cfg.StringLen > 0 {
		return cfg.StringLen
	}
	return defaultStringLen
}

// mcParseAddr 解析点位并算出它的读取模式与跨度（含位/字模式判定与读头对齐）。
//
// ok=false 表示地址名解析不出来（该点位不进区间规划）。
//
// **这段判定只有这一份**：CalcMCRanges 与 partitionBadAddrs 都调它。此前它是
// CalcMCRanges 里内联的一段，而「这个点位走位模式还是字模式、占几个字」正是
// partitionBadAddrs 用来判断超限的输入 —— 在那边复制一份必然漂移（位模式下
// SpanWords 恒为 1、根本不参与跨度检查，抄漏这一条就会把位点位误判成超限）。
func mcParseAddr(a po.DeviceAddress, stringLen int) (addr MCAddress, bitMode, ok bool) {
	addr, ok = ParseMCAddress(a.Name)
	if !ok {
		return
	}
	_, kind, isStr := mcTypeInfo(a.DataType)
	bitMode = addr.Device.isBit && kind == driver.KindBool

	// 字设备 bool：无 .bit 后缀默认取 bit 0
	if !addr.Device.isBit && kind == driver.KindBool && addr.Bit < 0 {
		addr.Bit = 0
	}

	addr.IsString = isStr
	addr.StringLen = stringLen
	addr.BitMode = bitMode

	if bitMode {
		// 位模式：1 点 = 1 位，Word 不参与偏移
		addr.SpanWords = 1
	} else {
		// 字模式：计算字地址与占字数
		if addr.Device.isBit {
			addr.Word = (addr.Number / 16) * 16 // 位设备字访问读头对齐 16 位边界
		} else {
			addr.Word = addr.Number
		}
		addr.SpanWords = mcTypeWords(a.DataType, stringLen)
	}
	return
}

// mcSpanTooLarge 判定点位是否**单点位就**超过单次请求上限。
//
// 位模式点位恒为 false：位模式下 1 点 = 1 位，Word 不参与偏移，所以字跨度检查
// 对它不成立 —— 这正是 mcParseAddr 把检查放在「非位模式」分支里的原因，两处判据一致。
func mcSpanTooLarge(addr MCAddress, maxWords int) bool {
	return !addr.BitMode && int(addr.SpanWords) > maxWords
}

// mcTypeInfo 查询 MC 数据类型的字节数、类别 Kind 与是否为 string。
// 未注册类型返回 (0, "", false)，解码阶段会因此失败并标 Quality=0。
func mcTypeInfo(dataType string) (size int, kind string, isString bool) {
	dt, ok := mcLookup(dataType)
	if !ok {
		return 0, "", false
	}
	// 以类型身份（而非 Size==0 启发式）判定 string：Size==0 仅表示"动态长度"
	strDT, _ := mcLookup("string")
	return dt.Size, dt.Kind, dt == strDT
}

// mcTypeWords 计算数据类型占用的字数（每 2 字节一字，向上取整）。
// 动态长度类型（string）按配置 stringLen（字数）；未注册类型默认 1 字。
func mcTypeWords(dataType string, stringLen int) uint32 {
	dt, ok := mcLookup(dataType)
	if !ok {
		return 1
	}
	strDT, _ := mcLookup("string")
	if dt == strDT {
		if stringLen <= 0 {
			stringLen = defaultStringLen
		}
		return uint32(stringLen)
	}
	size := dt.Size
	if size <= 0 {
		return 1
	}
	words := (size + 1) / 2
	if words < 1 {
		words = 1
	}
	return uint32(words)
}
