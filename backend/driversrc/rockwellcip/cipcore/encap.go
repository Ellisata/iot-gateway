// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package cip 提供 EtherNet/IP（CIP）封装层里**本插件用到的那一小部分**：
// 六段式封装头与 ListIdentity 的构造/解析。
//
// **本目录是 driversrc/omroncip/cipcore 的一份裁剪副本，不是共享库。** 两个
// EtherNet/IP 插件刻意各持一份、互不依赖，理由见 listidentity.go 的文件头。
//
// 裁剪掉了什么、为什么：那份里还有 Register Session / SendRRData / 通用状态错误
// 三块，是欧姆龙**采集路径**手搓裸帧用的；本插件的采集路径走 goindustrial
// （见 rockwellcipdrv/client.go），一个调用点都没有。带着它们只会让下一个人
// 以为这里有第二条采集实现。
package cip

import (
	"encoding/binary"
	"fmt"
)

// EncapHeaderSize 封装头长度（六段式，24 字节）。
const EncapHeaderSize = 24

// BuildEncapHeader 构建 24 字节封装头。status/senderContext/options 恒为 0。
//
//	命令(2 LE) | 长度(2 LE) | 会话句柄(4 LE) | 状态(4 LE) | 发送者上下文(8) | 选项(4)
//
// 长度字段 = 其后的载荷字节数（不含这 24 字节头）。多字节字段一律小端。
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
