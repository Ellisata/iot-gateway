// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"strconv"
	"strings"
)

// ParseFINSAddress 解析 FINS 地址字符串（大小写不敏感）。
//
// 支持的格式（TrimSpace + ToUpper 后解析）：
//
//	CIO{n}[.{bit}]   CIO 区（I/O 及内部继电器）
//	W{n}[.{bit}] / WR{n}[.{bit}]   WR 工作区
//	H{n}[.{bit}] / HR{n}[.{bit}]   HR 保持区
//	D{n}[.{bit}] / DM{n}[.{bit}]   DM 数据内存
//	{n}  裸数字，默认 CIO 区
//
// 字地址按十进制解释（D100 = DM 字 100，与本网关 S7/Modbus 约定一致）；
// 位后缀 .bit 为 0..15（bool 点位使用，如 D100.05）。
func ParseFINSAddress(name string) (FINSAddress, bool) {
	s := strings.ToUpper(strings.TrimSpace(name))
	if s == "" {
		return FINSAddress{}, false
	}

	area := AreaCIO
	body := s
	switch {
	case strings.HasPrefix(s, "CIO"):
		area = AreaCIO
		body = s[len("CIO"):]
	case strings.HasPrefix(s, "WR"):
		area = AreaWR
		body = s[len("WR"):]
	case strings.HasPrefix(s, "W"):
		area = AreaWR
		body = s[1:]
	case strings.HasPrefix(s, "HR"):
		area = AreaHR
		body = s[len("HR"):]
	case strings.HasPrefix(s, "H"):
		area = AreaHR
		body = s[1:]
	case strings.HasPrefix(s, "DM"):
		area = AreaDM
		body = s[len("DM"):]
	case strings.HasPrefix(s, "D"):
		area = AreaDM
		body = s[1:]
	default:
		// 裸数字：默认 CIO 区
		body = s
	}
	if body == "" {
		return FINSAddress{}, false
	}

	// 拆分位后缀
	bit := -1
	if dot := strings.IndexByte(body, '.'); dot >= 0 {
		b, err := strconv.Atoi(body[dot+1:])
		if err != nil || b < 0 || b > 15 {
			return FINSAddress{}, false
		}
		bit = b
		body = body[:dot]
	}
	if body == "" {
		return FINSAddress{}, false
	}

	word, err := strconv.Atoi(body)
	if err != nil || word < 0 || word > 0xFFFF {
		return FINSAddress{}, false
	}

	return FINSAddress{Area: area, Word: uint16(word), Bit: bit}, true
}
