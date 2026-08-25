package cip

import (
	"fmt"

	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// CIPTag 共享同一标签名的点位组。
// 标签寻址下同一标签只需发一次 0x4C 请求（count=1），结果复制给组内各点位。
type CIPTag struct {
	Name  string              // 规范化后的标签名
	Addrs []*po.DeviceAddress // 共享该标签名的点位（各自按自己的 DataType 解码）
}

// CalcCIPTags 从点位列表计算标签读取组。
//
// 策略：
//  1. 全量 ParseCIPAddress，非法标签直接报错（对齐 modbus/s7/fins）；
//  2. 按标签名聚合去重（同一标签只发一次请求），保序输出；
//  3. 组内多个点位类型不同时允许：用组内第一个点位的类型码发请求，
//     各点位按自己的类型解码（bool 与 int16 读同一 WORD 标签的场景）。
func CalcCIPTags(addrs []po.DeviceAddress) ([]CIPTag, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("cip: no addresses to read")
	}

	type parsedAddr struct {
		tag  string
		addr *po.DeviceAddress
	}
	parsed := make([]parsedAddr, 0, len(addrs))
	for i := range addrs {
		tag, ok := ParseCIPAddress(addrs[i].Name)
		if !ok {
			return nil, fmt.Errorf("cip: address %q (id=%s) invalid tag name", addrs[i].Name, addrs[i].ID)
		}
		parsed = append(parsed, parsedAddr{tag: tag, addr: &addrs[i]})
	}

	// 按标签名聚合，保序（按首次出现顺序）
	order := make([]string, 0, len(parsed))
	groups := make(map[string]*CIPTag)
	for _, p := range parsed {
		g, ok := groups[p.tag]
		if !ok {
			g = &CIPTag{Name: p.tag}
			groups[p.tag] = g
			order = append(order, p.tag)
		}
		g.Addrs = append(g.Addrs, p.addr)
	}

	tags := make([]CIPTag, 0, len(order))
	for _, name := range order {
		tags = append(tags, *groups[name])
	}

	logger.Debug("cip: calc %d read tags from %d addresses", len(tags), len(parsed))
	for _, t := range tags {
		logger.Debug("cip:   tag %q (%d points)", t.Name, len(t.Addrs))
	}

	return tags, nil
}
