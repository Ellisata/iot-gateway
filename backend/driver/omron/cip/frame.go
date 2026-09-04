// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"encoding/binary"
	"fmt"

	cipcore "iot-gateway/driver/cip"
)

// TagSpec 批量读取中单个标签的请求规格。
type TagSpec struct {
	Name string // 标签名
	Code uint16 // 类型码（请求不携带，仅用于响应解析时的尺寸推断参考）
}

// TagResult 批量读取中单个标签的结果。
// Err 为通用状态错误（设备响应但拒绝该标签，连接仍通）；Data 为成功时的数据区字节。
type TagResult struct {
	Data []byte
	Err  error
}

// buildMultipleDataTableRead 构建 0x0A 多服务读请求体：一个 SendRRData 内拼接多个 0x4C 服务。
//
//	请求 = 服务码 0x0A | 服务数(1) | [0x4C 请求] x N
//	每个 0x4C 请求 = buildDataTableRead 的完整请求（服务码 + ANSI 路径 + 元素数）。
//
// 一次请求携带多个标签，把 N 次网络往返收敛为 1 次，是 CIP 点位容量扩展的关键。
// 【真机核实】0x0A 多服务报文在 Omron NJ/NX 上的字节布局（尤其是否带路径/偏移表）
// 以 HslCommunication OmronCipNet 的简洁格式为参照；与真机不符时仅需调整本函数
// 与 parseMultipleDataTableRead 两处。
func buildMultipleDataTableRead(specs []TagSpec) []byte {
	req := make([]byte, 0, 2+len(specs)*16)
	req = append(req, cipServiceMultipleRead, byte(len(specs)))
	for _, s := range specs {
		req = append(req, buildDataTableRead(s.Name, s.Code)...)
	}
	return req
}

// parseMultipleDataTableRead 解析 0x8A 多服务读响应，按请求顺序返回各标签结果。
//
//	响应 = 服务回显 0x8A | 服务数(1) | [0xCC | 保留 | 通用状态 | 类型码(2 LE) | 数据] x N
//
// 各子响应无长度分隔字段，成功子响应按「标签的固定数据尺寸」切分（驱动批量只收纳
// 定长类型标签）；错误子响应为 4 字节起（CC|00|状态|附加状态字数[|附加状态]），可自定位。
func parseMultipleDataTableRead(resp []byte, sizes []int) ([]TagResult, error) {
	if len(resp) < 2 {
		return nil, fmt.Errorf("cip: short multiple read response (%d bytes)", len(resp))
	}
	if resp[0] != cipServiceMultipleRead|0x80 {
		return nil, fmt.Errorf("cip: unexpected multiple read service echo 0x%02X", resp[0])
	}
	count := int(resp[1])
	if count != len(sizes) {
		return nil, fmt.Errorf("cip: multiple read response count %d != requested %d", count, len(sizes))
	}

	results := make([]TagResult, count)
	offset := 2
	for i := 0; i < count; i++ {
		if offset+4 > len(resp) {
			return nil, fmt.Errorf("cip: multiple read response truncated at service %d", i)
		}
		if resp[offset] != cipServiceDataTableRead|0x80 {
			return nil, fmt.Errorf("cip: unexpected service echo 0x%02X at %d", resp[offset], i)
		}
		if status := resp[offset+2]; status != 0 {
			// 错误响应：CC | 00 | 状态 | 附加状态字数 | [附加状态]（4 字节起，可自定位）
			addl := int(resp[offset+3])
			results[i].Err = cipcore.NewGeneralStatusError(status)
			offset += 4 + addl*2
			continue
		}
		// 数据长度优先取响应回显的类型码（子响应自描述）；未知类型码回退请求侧期望尺寸
		dataLen := sizes[i]
		if s, ok := cipTypeSize(binary.LittleEndian.Uint16(resp[offset+3 : offset+5])); ok {
			dataLen = s
		}
		if offset+5+dataLen > len(resp) {
			return nil, fmt.Errorf("cip: multiple read response service %d data truncated", i)
		}
		results[i].Data = resp[offset+5 : offset+5+dataLen]
		offset += 5 + dataLen
	}
	return results, nil
}

// ==================== Omron Data Table Read（服务码 0x4C） ====================
// EtherNet/IP 封装层（Register Session / SendRRData / Common Packet Format）
// 为厂商无关通用逻辑，已上移至 iot-gateway/driver/cip。

// buildDataTableRead 构建 Data Table Read（服务码 0x4C）请求体。
//
// Omron 变量读取请求格式（NJ/NX 手册 "Read Service for Variables"）：
//
//	服务码(1)=0x4C | 路径字数(1) | 路径 | 元素个数(2 LE)=1
//
// 路径 = ANSI Extended Symbol 段：0x91 | 名字节数 | 标签名 ASCII | [奇数时补 0x00]
// 标签名位于路径内部，而非路径后的独立字段。
//
// 注意：手册规定请求服务数据仅「元素个数」，无数据类型码字段；类型由响应中的
// 数据类型码回显给出（驱动侧按点位配置的类型解码）。故 dataTypeCode 不再编入请求，
// 参数保留以便将来对响应类型码做校验。
func buildDataTableRead(tagName string, dataTypeCode uint16) []byte {
	path := make([]byte, 0, 2+len(tagName)+1)
	path = append(path, 0x91, byte(len(tagName))) // ANSI Extended Symbol 段头
	path = append(path, []byte(tagName)...)
	if len(path)%2 == 1 {
		path = append(path, 0x00) // 路径须整字对齐
	}

	req := make([]byte, 0, 1+1+len(path)+2)
	req = append(req, cipServiceDataTableRead, byte(len(path)/2)) // 服务码 + 路径字数(words)
	req = append(req, path...)
	req = append(req, 0x01, 0x00) // 元素个数 = 1（每地址一个标签，count=1）
	return req
}

// parseDataTableRead 解析 Data Table Read 响应体，返回数据区字节。
//
// 响应结构（Omron "Read Service for Variables"）：
//
//	服务回显(1)=0xCC | 保留(1)=0 | 通用状态(1) | 数据...
//
// 通用状态非 0 时为错误响应，最短 4 字节（CC | 00 | 状态 | 附加状态字数），
// 此时必须先判状态再要求数据长度——否则把 4 字节错误响应误报为 "short response"，
// 掩盖真正的拒绝原因（真机 2026-08-17 实测：标签不存在回 0x04 路径段错误）。
// 成功响应：CC | 00 | 00 | 数据类型码(2 LE) | 数据，数据从 offset 5 起。
func parseDataTableRead(resp []byte) ([]byte, error) {
	if len(resp) < 4 {
		return nil, fmt.Errorf("cip: short data table read response (%d bytes)", len(resp))
	}
	if resp[0] != cipServiceDataTableRead|0x80 {
		return nil, fmt.Errorf("cip: unexpected service echo 0x%02X", resp[0])
	}
	if status := resp[2]; status != 0 {
		return nil, cipcore.NewGeneralStatusError(status)
	}
	if len(resp) < 5 {
		return nil, fmt.Errorf("cip: short data table read response (%d bytes)", len(resp))
	}
	// 数据从 offset 5 起（跳过服务回显、保留、状态与 2 字节数据类型码回显）
	return resp[5:], nil
}
