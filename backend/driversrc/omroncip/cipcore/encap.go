// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package cip 提供 EtherNet/IP（CIP）协议的厂商无关通用实现：
// 封装层（会话管理、Common Packet Format）与 CIP 通用状态码。
//
// 该包只含 CIP/EtherNet-IP 标准协议逻辑，不含任何厂商专有服务
// （如 Omron Data Table Read），供各 EtherNet-IP 厂商驱动复用。
package cip

import (
	"encoding/binary"
	"fmt"
)

// EtherNet/IP 封装层命令码（EtherNet/IP spec vol.2, Common Packet Format）。
// 多字节字段一律小端。
const (
	CmdRegisterSession   = 0x0065 // 注册会话（连接握手）
	CmdUnregisterSession = 0x0066 // 注销会话（关闭时尽力发送）
	CmdSendRRData        = 0x006F // 无连接显式报文（UCMM）
	EncapHeaderSize      = 24     // 封装头长度（六段式）
	EncapProtocolVersion = 0x0001 // 协议版本（CIP vol.2 固定 1）
	// ItemCIPData SendRRData 数据项（无连接数据项）。
	//
	// UCMM 无连接报文地址项固定为 Null 项(0x0000, length 0)。
	// 连接地址项 0x00A1 仅用于连接报文（与 0x00B1 数据项配对），不可用于 SendRRData。
	ItemCIPData = 0x00B2
)

// ==================== EtherNet/IP 封装层 ====================
// 封装头（六段式，24 字节），多字节字段一律小端：
//
//	命令(2 LE) | 长度(2 LE) | 会话句柄(4 LE) | 状态(4 LE) | 发送者上下文(8) | 选项(4)
//
// 长度字段 = 其后的载荷字节数（不含这 24 字节头）。

// BuildEncapHeader 构建 24 字节封装头。status/senderContext/options 恒为 0。
func BuildEncapHeader(cmd uint16, session uint32, length int) []byte {
	buf := make([]byte, EncapHeaderSize)
	binary.LittleEndian.PutUint16(buf[0:2], cmd)
	binary.LittleEndian.PutUint16(buf[2:4], uint16(length))
	binary.LittleEndian.PutUint32(buf[4:8], session)
	return buf
}

// ParseEncapResponse 解析封装响应帧，返回命令码、会话句柄、状态与载荷数据。
func ParseEncapResponse(frame []byte) (cmd uint16, session, status uint32, data []byte, err error) {
	if len(frame) < EncapHeaderSize {
		return 0, 0, 0, nil, fmt.Errorf("cip: short encap frame (%d bytes)", len(frame))
	}
	cmd = binary.LittleEndian.Uint16(frame[0:2])
	length := int(binary.LittleEndian.Uint16(frame[2:4]))
	session = binary.LittleEndian.Uint32(frame[4:8])
	status = binary.LittleEndian.Uint32(frame[8:12])

	if EncapHeaderSize+length > len(frame) {
		return 0, 0, 0, nil, fmt.Errorf("cip: encap length %d exceeds frame (%d bytes)", length, len(frame))
	}
	data = frame[EncapHeaderSize : EncapHeaderSize+length]
	return cmd, session, status, data, nil
}

// BuildRegisterSession 构建 Register Session 请求（命令 0x0065）。
// 载荷 4 字节：协议版本(2 LE) | 选项(2 LE)。
func BuildRegisterSession(session uint32, protocolVersion uint16) []byte {
	h := BuildEncapHeader(CmdRegisterSession, session, 4)
	return append(h, byte(protocolVersion), byte(protocolVersion>>8), 0x00, 0x00)
}

// BuildSendRRData 构建 SendRRData 请求（命令 0x006F，UCMM 无连接显式报文）。
//
//	载荷 = 接口句柄(4 LE)=0 | 超时(2 LE)=0 | 项数(2 LE)=2
//	      | 地址项: 类型(2 LE)=0x0000(Null) | 长度(2 LE)=0
//	      | 数据项: 类型(2 LE)=0x00B2 | 长度(2 LE)=len(cipData) | cipData
//
// 载荷总长 = 8 + 4 + 4 + len(cipData) = 16 + len(cipData)。
//
// 注意：UCMM 无连接报文的地址项必须用 Null 地址项(0x0000)，不能用连接地址项
// 0x00A1 —— 0x00A1 仅与 0x00B1 连接数据项配对（连接报文）。0x00A1+0x00B2 混用
// 属非法组合，欧姆龙 NX/NJ 等严格实现会以封装层状态 0x0001(Invalid Command) 拒绝。
func BuildSendRRData(session uint32, cipData []byte) []byte {
	body := make([]byte, 0, 16+len(cipData))
	// 接口句柄(4) + 超时(2) + 项数(2)：前 8 字节，最后填项数
	body = append(body, 0, 0, 0, 0, 0, 0, 0, 0)
	binary.LittleEndian.PutUint16(body[6:8], 2)

	// 地址项（4 字节）：Null 地址项 0x0000 + length=0
	body = append(body, 0x00, 0x00, 0x00, 0x00)
	// 数据项：0x00B2 + length + cipData（length 占位后回填）
	body = append(body, 0xB2, 0x00, 0x00, 0x00)
	binary.LittleEndian.PutUint16(body[len(body)-2:], uint16(len(cipData)))
	body = append(body, cipData...)

	h := BuildEncapHeader(CmdSendRRData, session, len(body))
	return append(h, body...)
}

// ParseSendRRData 解析 SendRRData 响应载荷，返回 type=0x00B2 数据项中的 CIP 响应字节。
//
//	载荷 = 接口句柄(4) | 超时(2) | 项数(2 LE) | 项1..项n（每项: 类型(2 LE) | 长度(2 LE) | 载荷）
func ParseSendRRData(respData []byte) ([]byte, error) {
	if len(respData) < 8 {
		return nil, fmt.Errorf("cip: short send rr data (%d bytes)", len(respData))
	}
	itemCount := int(binary.LittleEndian.Uint16(respData[6:8]))
	offset := 8
	for i := 0; i < itemCount; i++ {
		if offset+4 > len(respData) {
			return nil, fmt.Errorf("cip: truncated send rr data item %d", i)
		}
		itemType := binary.LittleEndian.Uint16(respData[offset : offset+2])
		itemLen := int(binary.LittleEndian.Uint16(respData[offset+2 : offset+4]))
		offset += 4
		if offset+itemLen > len(respData) {
			return nil, fmt.Errorf("cip: send rr data item %d length %d truncated", i, itemLen)
		}
		if itemType == ItemCIPData {
			return respData[offset : offset+itemLen], nil
		}
		offset += itemLen
	}
	return nil, fmt.Errorf("cip: no CIP data item in send rr data response")
}
