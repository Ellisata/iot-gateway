// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"fmt"

	"iot-gateway/driver"
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

	logDebug("calc %d read tags from %d addresses", len(tags), len(parsed))
	for _, t := range tags {
		logDebug("  tag %q (%d points)", t.Name, len(t.Addrs))
	}

	return tags, nil
}

// partitionBadTags 挑出协议自己的解析器（ParseCIPAddress）认不出的标签名。
//
// 返回可解析的点位，以及「标签名非法」的点位按 id → QualityBadAddress 的记账。
//
// ParseCIPAddress 只收可打印 ASCII（0x21..0x7E），所以**一个空格或一个中文字符
// 就够**：把「Line 1.Temp」或中文说明填进地址栏是常见手误，而它此前会让整个
// Read 报错 → 采集侧把这个配置错误当成设备故障，判**离线**并推断联通知
// （设备全程在线、`IsConnected()` 为真，重连逻辑根本不启动），坏标签之后的
// 标签也一起被跳过。现在受损范围收敛到这一个点位。
//
// 为什么拆在 CalcCIPTags 上游而不是让它自己容错：CalcCIPTags 的契约是
// 「全量解析、遇非法标签名报错」（range_test.go 钉着它），拆在它之前它见到的
// 就永远是可解析的输入，那条错误退化为防御性代码。
func partitionBadTags(addrs []po.DeviceAddress) ([]po.DeviceAddress, map[string]int) {
	var valid []po.DeviceAddress
	var bad map[string]int
	for i := range addrs {
		a := &addrs[i]
		if _, ok := ParseCIPAddress(a.Name); ok {
			valid = append(valid, *a)
			continue
		}
		if bad == nil {
			bad = make(map[string]int, 2)
		}
		bad[a.ID] = driver.QualityBadAddress
		// 与「类型码不支持」「本地解码失败」两处同一形态的逐点日志：本文件对
		// **永不自愈的逐点位问题**一律每轮打一行（有意的，让现场看出"有个点位
		// 一直坏着"）。真正面向运维的信号是点位质量报警那条「地址无效」。
		logf("tag %q (id=%s) 标签名本地就解析不出来（非可打印 ASCII），mark quality=bad-address", a.Name, a.ID)
	}
	return valid, bad
}
