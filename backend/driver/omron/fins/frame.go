// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"encoding/binary"
	"fmt"
)

// FINS 帧头字段常量（10 字节头，UDP/TCP 共用）
const (
	finsICFCommand = 0x80 // ICF：命令帧、需响应
	finsRSV        = 0x00
	finsGCT        = 0x02
	finsDNA        = 0x00 // 目标网络：本地
	finsSNA        = 0x00 // 源网络：本地
)

// FINS/TCP 头命令码。
//
// 采用 Omron CP 系列内置以太网 / HslCommunication 通行的 FINS/TCP 方言
// （欧姆龙 ETN 系列单元手册中命令码定义与此不同，两者不通用）：
//
//	连接请求 0x00000000 → 连接响应 0x00000001
//	数据发送 0x00000002 → 数据响应 0x00000002（收发同码，以 FINS 载荷区分）
const (
	finsTCPCmdConnect     = 0x00000000 // 连接请求（握手）
	finsTCPCmdConnectResp = 0x00000001 // 连接响应
	finsTCPCmdDataSend    = 0x00000002 // 数据发送
	finsTCPCmdDataResp    = 0x00000002 // 数据发送响应
)

// buildFINSHeader 构建 10 字节 FINS 头：
//
//	ICF | RSV | GCT | DNA | DA1 | DA2 | SNA | SA1 | SA2 | SID
func buildFINSHeader(dstNode, dstUnit, srcNode, srcUnit, sid byte) []byte {
	return []byte{
		finsICFCommand, // ICF：命令、需响应
		finsRSV,        // RSV
		finsGCT,        // GCT
		finsDNA,        // DNA：本地网络
		dstNode,        // DA1：目标节点
		dstUnit,        // DA2：目标单元
		finsSNA,        // SNA：本地网络
		srcNode,        // SA1：源节点
		srcUnit,        // SA2：源单元
		sid,            // SID：服务 ID（响应回显匹配）
	}
}

// buildReadFrame 构建内存区读取命令帧（FINS 头 + 命令体）。
//
//	命令体（8 字节）：命令码(2) | 区域码(1) | 字地址(2 BE) | 位(1, 字访问=00) | 数量(2 BE)
func buildReadFrame(area finsArea, word, count uint16, dstNode, dstUnit, srcNode, srcUnit, sid byte) []byte {
	frame := make([]byte, 0, 18)
	frame = append(frame, buildFINSHeader(dstNode, dstUnit, srcNode, srcUnit, sid)...)
	frame = append(frame,
		cmdMemoryAreaReadHi, cmdMemoryAreaReadLo,
		byte(area),
		byte(word>>8), byte(word),
		0x00, // 位地址：字访问
		byte(count>>8), byte(count),
	)
	return frame
}

// parseReadResponse 解析读取响应帧，返回数据字节（去除 FINS 头 + 命令回显 + 结束码）。
//
// 响应体：命令码回显(2) | 结束码(2 BE) | 数据。结束码非 0 时返回 finsEndCodeError
// （连接是通的，设备拒绝了该请求），供上层将该区间点位标 Quality=0 而非断连。
func parseReadResponse(resp []byte, sid byte) ([]byte, error) {
	if len(resp) < 14 { // 10 头 + 2 命令 + 2 结束码
		return nil, fmt.Errorf("fins: short response (%d bytes)", len(resp))
	}
	if resp[9] != sid {
		return nil, fmt.Errorf("fins: SID mismatch (want 0x%02X, got 0x%02X)", sid, resp[9])
	}
	if resp[10] != cmdMemoryAreaReadHi || resp[11] != cmdMemoryAreaReadLo {
		return nil, fmt.Errorf("fins: unexpected command echo 0x%02X%02X", resp[10], resp[11])
	}
	endCode := binary.BigEndian.Uint16(resp[12:14])
	if endCode != 0 {
		return nil, newEndCodeError(endCode)
	}
	return resp[14:], nil
}

// buildTCPHeader 构建 FINS/TCP 帧头（16 字节）：
//
//	"FINS"(4) | 长度(4) | 命令(4) | 错误码(4)
//
// 长度字段 = 其后的字节数 = 命令(4) + 错误码(4) + payloadLen。
func buildTCPHeader(cmd, errCode uint32, payloadLen int) []byte {
	buf := make([]byte, 16)
	copy(buf[0:4], []byte{'F', 'I', 'N', 'S'})
	binary.BigEndian.PutUint32(buf[4:8], uint32(8+payloadLen))
	binary.BigEndian.PutUint32(buf[8:12], cmd)
	binary.BigEndian.PutUint32(buf[12:16], errCode)
	return buf
}

// buildConnectFrame 构建 FINS/TCP 连接请求帧（16 字节头 + 4 字节节点号）。
// 命令=0x00000000，载荷为本端 FINS 节点号（srcNode），PLC 据此路由后续响应。
func buildConnectFrame(srcNode byte) []byte {
	node := make([]byte, 4)
	binary.BigEndian.PutUint32(node, uint32(srcNode))
	return append(buildTCPHeader(finsTCPCmdConnect, 0, len(node)), node...)
}

// parseTCPFrame 解析 FINS/TCP 帧，返回命令码、错误码与 FINS 载荷（头 + 命令体）。
func parseTCPFrame(frame []byte) (cmd, errCode uint32, payload []byte, err error) {
	if len(frame) < 16 {
		return 0, 0, nil, fmt.Errorf("fins tcp: short frame (%d bytes)", len(frame))
	}
	if string(frame[0:4]) != "FINS" {
		return 0, 0, nil, fmt.Errorf("fins tcp: bad protocol identifier %q", frame[0:4])
	}
	cmd = binary.BigEndian.Uint32(frame[8:12])
	errCode = binary.BigEndian.Uint32(frame[12:16])
	payload = frame[16:]
	return cmd, errCode, payload, nil
}
