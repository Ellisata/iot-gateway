package s7

import (
	"fmt"
	"sort"

	"iot-gateway/logger"
	"iot-gateway/model/po"
)

const (
	// defaultStringLen STRING 默认最大读取长度（S7 STRING 最大 254 字符）
	defaultStringLen = 254
	// maxStringLen S7 STRING 声明长度硬上限。
	// S7 的长度字节为单字节（0..255），规范限 254 字符：
	// 地址中显式长度（STRING{n}.{len}）超过时视为非法地址直接报错；
	// 配置 stringLen 超过时由 calcSpan 钳制到该值，避免产生超大数据读取区间。
	maxStringLen = 254
)

// CalcS7Ranges 从点位列表计算读取区间。
//
// 策略：
//  1. 全量解析 name，非法地址直接报错（对齐 modbus CalcReadRanges）
//  2. STRING 点位与其它类型隔离成独立区间——STRING 跨度大（2 头字节 + 长度），
//     若混入普通区间会把区间末端推出 DB 实际边界导致整段读取失败，
//     隔离后 STRING 越界只影响该 STRING 自己
//  3. 组内按 (Area, DB) 分组，按字节偏移排序，相邻/交叠区间合并；
//     间隙 ≤ maxGap 的区间也合并（读区间内部跳过 gap 字节），
//     用于收敛零散点位、减少串行网络往返。
//     区间 EndOffset 只延伸到真实点位的跨度尽头（不超出最后点位），
//     不会越界；gap 位于同一 (area,db) 内两个合法点位之间，读取恒安全。
//     maxGap=0 时仅合并相邻/交叠区间，行为与旧版一致。
//     （STRING 除外：每个 STRING 独立成区间，互不合并，确保越界只影响自身）
//
// 每个返回的 S7Range 包含该段独有的 AddressMap（DeviceAddress.ID → 已填充
// Span 的 S7Address），供 Read 解码时直接使用。
func CalcS7Ranges(addrs []po.DeviceAddress, cfgStringLen, maxGap int) ([]S7Range, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("s7: no addresses to read")
	}

	// 全量解析 name
	type parsedAddr struct {
		id    string
		addr  S7Address
		isStr bool // 是否按 STRING 处理（类型为 string 或地址名是 STRING）
	}
	parsed := make([]parsedAddr, 0, len(addrs))
	for _, a := range addrs {
		addr, ok := ParseS7Address(a.Name)
		if !ok {
			return nil, fmt.Errorf("s7: address %q (id=%s) invalid format", a.Name, a.ID)
		}
		ts, isString := s7TypeInfo(a.DataType)
		isStr := addr.IsString || isString
		addr.Span = calcSpan(addr, ts, isString, cfgStringLen)
		parsed = append(parsed, parsedAddr{id: a.ID, addr: addr, isStr: isStr})
	}

	// 按 (isStr, area, db) 分组
	type groupKey struct {
		isStr bool
		area  s7Area
		db    int
	}
	groups := make(map[groupKey][]parsedAddr)
	for _, p := range parsed {
		key := groupKey{isStr: p.isStr, area: p.addr.Area, db: p.addr.DB}
		groups[key] = append(groups[key], p)
	}

	// 组内排序并聚类
	var ranges []S7Range
	for _, list := range groups {
		sort.Slice(list, func(i, j int) bool {
			return list[i].addr.ByteOffset < list[j].addr.ByteOffset
		})

		if list[0].isStr {
			// STRING 组：每个 STRING 独立成区间，互不合并。
			// 组内某个 STRING 越界只让该点位 Quality=0，不会因合并拖垮同组其它 STRING。
			for _, p := range list {
				ranges = append(ranges, newRange(p.addr.Area, p.addr.DB,
					p.addr.ByteOffset, p.addr.Span, p.id, p.addr))
			}
			continue
		}

		cur := newRange(list[0].addr.Area, list[0].addr.DB,
			list[0].addr.ByteOffset, list[0].addr.Span, list[0].id, list[0].addr)
		for _, p := range list[1:] {
			if p.addr.ByteOffset <= cur.EndOffset+maxGap {
				// 相邻/交叠/间隙 ≤ maxGap，合并。
				// EndOffset 仍只延伸到当前点位跨度尽头，gap 字节被跳过读取。
				end := p.addr.ByteOffset + p.addr.Span
				if end > cur.EndOffset {
					cur.EndOffset = end
				}
				cur.AddressMap[p.id] = p.addr
			} else {
				ranges = append(ranges, cur)
				cur = newRange(p.addr.Area, p.addr.DB,
					p.addr.ByteOffset, p.addr.Span, p.id, p.addr)
			}
		}
		ranges = append(ranges, cur)
	}

	// 排序保证输出顺序稳定
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Area != ranges[j].Area {
			return ranges[i].Area < ranges[j].Area
		}
		if ranges[i].DB != ranges[j].DB {
			return ranges[i].DB < ranges[j].DB
		}
		return ranges[i].StartOffset < ranges[j].StartOffset
	})

	logger.Debug("s7: calc %d read ranges from %d addresses", len(ranges), len(parsed))
	for _, r := range ranges {
		logger.Debug("s7:   range area=0x%02X db=%d [%d, %d) (%d points)",
			r.Area, r.DB, r.StartOffset, r.EndOffset, len(r.AddressMap))
	}

	return ranges, nil
}

// newRange 构造单点位区间
func newRange(area s7Area, db, start, span int, id string, addr S7Address) S7Range {
	return S7Range{
		Area:        area,
		DB:          db,
		StartOffset: start,
		EndOffset:   start + span,
		AddressMap:  map[string]S7Address{id: addr},
	}
}

// calcSpan 计算点位最终读取跨度（字节数）。
//
//	位地址：1（所在字节）
//	STRING（类型为 string 或地址名 STRING）：2（头字节）+ 长度（显式长度优先，缺省用配置值）
//	其它已注册类型：max(地址名宽度, 类型注册大小)
//	未知类型：用地址名宽度保守处理（解码会失败 → Quality=0，避免过度读取）
func calcSpan(addr S7Address, typeSize int, isString bool, cfgStringLen int) int {
	if addr.IsBit() {
		return 1
	}
	if addr.IsString || isString {
		l := addr.StringLen
		if l <= 0 {
			l = cfgStringLen
		}
		if l <= 0 {
			l = defaultStringLen
		}
		if l > maxStringLen {
			l = maxStringLen
		}
		return 2 + l
	}
	if typeSize > 0 {
		if typeSize > addr.Width {
			return typeSize
		}
		return addr.Width
	}
	// 未知类型：退回地址名宽度（最小 1 字节）
	if addr.Width > 0 {
		return addr.Width
	}
	return 1
}

// s7TypeInfo 查询 S7 数据类型的字节数与是否为动态（STRING）类型。
// 未注册类型返回 (0, false)，解码阶段会因此失败并标 Quality=0。
func s7TypeInfo(dataType string) (size int, isString bool) {
	dt, ok := s7Lookup(dataType)
	if !ok {
		return 0, false
	}
	// 以类型身份（而非 Size==0 启发式）判定 STRING：
	// Size==0 仅表示"动态长度"，将来注册其它动态类型时不应被误判为 STRING。
	strDT, _ := s7Lookup("string")
	return dt.Size, dt == strDT
}
