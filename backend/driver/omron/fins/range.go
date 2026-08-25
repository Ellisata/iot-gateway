package fins

import (
	"fmt"
	"sort"

	"iot-gateway/driver"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// CalcFINSRanges 从点位列表计算读取区间（字区间）。
//
// 策略：
//  1. 全量解析 name，非法地址直接报错（对齐 modbus/s7）；
//  2. 按内存区（CIO/WR/HR/DM）分组——不同区域字地址空间独立，不能混帧；
//  3. 组内按字地址排序，相邻/交叠/间隙 ≤ maxGap 的字合并为同一区间；
//     区间只延伸到真实点位的跨度尽头，gap 位于两个合法点位之间，读取恒安全；
//  4. 单区间读取字数上限 maxReadWords（默认 100，各系列均安全），超限分块；
//  5. 多字类型（int32/float64/string）按字节数折算占字数（每 2 字节一字），
//     区间末端延伸覆盖其完整数据；bool 点位占其所在字，混入同区字区间
//     （由 ParseFINSValue 从读回的字中本地取位，避免 FINS 位读同字限制）。
//
// 每个返回的 FINSRange 含该段独有的 AddressMap（DeviceAddress.ID → FINSAddress），
// 供 Read 解码时直接使用。
func CalcFINSRanges(addrs []po.DeviceAddress, cfg *FINSConfig) ([]FINSRange, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("fins: no addresses to read")
	}

	maxGap := int(cfg.MergeWindow)
	if maxGap <= 0 {
		maxGap = defaultMergeWindow
	}
	maxWords := cfg.MaxReadWords
	if maxWords <= 0 {
		maxWords = defaultMaxReadWords
	}

	// 全量解析 name，并计算每个点位的占字数（多字类型折算）
	type parsedAddr struct {
		id   string
		addr FINSAddress
		span uint16 // 该点位占用的字数（≥1）
	}
	parsed := make([]parsedAddr, 0, len(addrs))
	for _, a := range addrs {
		addr, ok := ParseFINSAddress(a.Name)
		if !ok {
			return nil, fmt.Errorf("fins: address %q (id=%s) invalid format", a.Name, a.ID)
		}
		span := finsTypeWords(a.DataType, cfg.StringLen)
		// 单点位跨度超过单请求上限时无法分块（数据须连续），直接报错而非静默超限
		if int(span) > maxWords {
			return nil, fmt.Errorf("fins: address %q (id=%s) spans %d words, exceeds maxReadWords %d",
				a.Name, a.ID, span, maxWords)
		}
		parsed = append(parsed, parsedAddr{id: a.ID, addr: addr, span: span})
	}

	// 按内存区分组：不同区域字地址空间独立，不能混帧
	groups := make(map[finsArea][]parsedAddr)
	for _, p := range parsed {
		groups[p.addr.Area] = append(groups[p.addr.Area], p)
	}

	var ranges []FINSRange
	for area, list := range groups {
		sort.Slice(list, func(i, j int) bool {
			return list[i].addr.Word < list[j].addr.Word
		})

		// 组内聚类：curStart..curEnd 为当前区间覆盖的字范围（含空洞）
		curStart := list[0].addr.Word
		curEnd := curStart + list[0].span - 1
		curMap := map[string]FINSAddress{list[0].id: list[0].addr}

		flush := func() {
			ranges = append(ranges, FINSRange{
				Area:       area,
				StartWord:  curStart,
				Count:      curEnd - curStart + 1,
				AddressMap: curMap,
			})
		}

		for _, p := range list[1:] {
			start := p.addr.Word
			end := start + p.span - 1

			// 分块：合并后跨度超过单请求上限时，先结束当前区间
			if int(end)-int(curStart)+1 > maxWords {
				flush()
				curStart = start
				curEnd = end
				curMap = map[string]FINSAddress{p.id: p.addr}
				continue
			}

			// 相邻/交叠/间隙 ≤ maxGap：合并（跳过 gap 空洞读取）
			gap := int(start) - int(curEnd) - 1
			if gap <= maxGap {
				if int(end) > int(curEnd) {
					curEnd = end
				}
				curMap[p.id] = p.addr
				continue
			}

			flush()
			curStart = start
			curEnd = end
			curMap = map[string]FINSAddress{p.id: p.addr}
		}
		flush()
	}

	// 排序保证输出顺序稳定
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Area != ranges[j].Area {
			return ranges[i].Area < ranges[j].Area
		}
		return ranges[i].StartWord < ranges[j].StartWord
	})

	logger.Debug("fins: calc %d read ranges from %d addresses (maxGap=%d, maxWords=%d)",
		len(ranges), len(parsed), maxGap, maxWords)
	for _, r := range ranges {
		logger.Debug("fins:   range area=0x%02X [%d, %d) count=%d (%d points)",
			r.Area, r.StartWord, r.StartWord+r.Count, r.Count, len(r.AddressMap))
	}

	return ranges, nil
}

// finsTypeWords 计算数据类型占用的字数（每 2 字节一字，向上取整）。
// 通过 TypeRegistry 查询；动态长度类型（string）按配置 stringLen；未注册类型默认 1 字。
func finsTypeWords(dataType string, stringLen int) uint16 {
	f := driver.GetTypeRegistry().ForProtocol(protocolName)
	dt, ok := f.Get(dataType)
	if !ok {
		return 1
	}
	size := dt.Size
	if size <= 0 {
		if stringLen <= 0 {
			stringLen = defaultStringLen
		}
		size = stringLen
	}
	words := (size + 1) / 2
	if words < 1 {
		words = 1
	}
	return uint16(words)
}
