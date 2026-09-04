// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"encoding/binary"
	"io"
	"net"
)

// NewFINSTCP 启动一个欧姆龙 FINS/TCP 模拟器。
//
// 帧格式与 driver/omron/fins 完全对齐（见其 frame.go）：
//
//	帧 = "FINS"(4) | 长度(4 BE) | 命令(4 BE) | 错误码(4 BE) | 载荷
//
//	连接请求：命令 0x00000000，载荷 4 字节节点号 → 连接响应 0x00000001（载荷回显）
//	数据发送：命令 0x00000002，载荷 = FINS 帧（10 字节头 + 命令体）
//	          → 数据响应 0x00000002，载荷 = FINS 响应（头回显 + 命令回显 0x0101 + 结束码 + 数据）
//
// 仅处理内存区读取（0x0101，字单位），返回 count×2 字节数据；位读取由驱动读字本地解析。
func NewFINSTCP() (*Server, error) {
	return newServer(handleFINSConn)
}

func handleFINSConn(conn net.Conn) {
	hdr := make([]byte, 16)
	for {
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		if string(hdr[0:4]) != "FINS" {
			return
		}
		length := int(binary.BigEndian.Uint32(hdr[4:8]))
		if length < 8 {
			return
		}
		payload := make([]byte, length-8)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}

		cmd := binary.BigEndian.Uint32(hdr[8:12])
		var resp []byte
		switch cmd {
		case 0x00000000: // 连接请求 → 连接响应（命令 0x1，载荷回显节点号）
			resp = buildFINSTCPFrame(0x00000001, 0, payload)
		case 0x00000002: // 数据发送 → 数据响应
			resp = buildFINSDataResp(payload)
		default:
			return
		}
		if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}

// buildFINSDataResp 构造内存区读取响应。
// payload 为请求的 FINS 帧（10 字节头 + 命令体）；响应回显 FINS 头（DA/SA 互换）、
// 命令回显 0x0101、结束码 0x0000 与 count×2 字节数据。
func buildFINSDataResp(payload []byte) []byte {
	if len(payload) < 18 {
		return buildFINSTCPFrame(0x00000002, 0, nil)
	}
	// 命令体：命令(2)=0x0101 | 区域(1) | 字地址(2 BE) | 位(1) | 数量(2 BE)
	count := int(binary.BigEndian.Uint16(payload[16:18]))

	// FINS 响应头：ICF/RSV/GCT/DNA/DA1/DA2/SNA/SA1/SA2/SID，DA 与 SA 互换
	head := make([]byte, 10)
	copy(head, payload[0:10])
	head[4], head[7] = payload[7], payload[4] // DA1 ↔ SA1
	head[5], head[8] = payload[8], payload[5] // DA2 ↔ SA2

	body := make([]byte, 0, 10+2+2+count*2)
	body = append(body, head...)
	body = append(body, 0x01, 0x01) // 命令回显：内存区读取
	body = append(body, 0x00, 0x00) // 结束码 0x0000
	body = append(body, make([]byte, count*2)...)
	return buildFINSTCPFrame(0x00000002, 0, body)
}

// buildFINSTCPFrame 组装 FINS/TCP 帧（"FINS" + 长度 + 命令 + 错误码 + 载荷）。
func buildFINSTCPFrame(cmd, errCode uint32, payload []byte) []byte {
	frame := make([]byte, 16, 16+len(payload))
	copy(frame[0:4], "FINS")
	binary.BigEndian.PutUint32(frame[4:8], uint32(8+len(payload)))
	binary.BigEndian.PutUint32(frame[8:12], cmd)
	binary.BigEndian.PutUint32(frame[12:16], errCode)
	return append(frame, payload...)
}
