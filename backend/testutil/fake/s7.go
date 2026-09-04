// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"encoding/binary"
	"io"
	"net"
)

// NewS7 启动一个西门子 S7（ISO-on-TCP + S7 PDU）模拟器。
//
// 与 driver/s7 使用的 gos7 客户端逐字节对齐（见其 tcpclient.go / telegram.go），
// 每个连接依次经历三个阶段：
//
//	1. ISO Connect：TPKT+COTP CR（0xE0，22 字节）→ 回 Connect Confirm（0xD0，22 字节）
//	2. S7 PDU 协商：TPKT+COTP+S7 0xF0（25 字节）→ 回 27 字节 ack（协商 PDU 长度 480）
//	3. 读变量：TPKT+COTP+S7 0x04（31 字节）→ 回 ack-data（返回码 0xFF + 数据）
//
// 仅响应字节/字/双字读取（按请求传输尺寸码返回定长全零数据）。
func NewS7() (*Server, error) {
	return newServer(handleS7Conn)
}

func handleS7Conn(conn net.Conn) {
	for {
		// 读 4 字节 TPKT 头
		hdr := make([]byte, 4)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		if hdr[0] != 0x03 { // RFC1006 版本
			return
		}
		length := int(binary.BigEndian.Uint16(hdr[2:4]))
		if length < 7 || length > 65535 {
			return
		}
		rest := make([]byte, length-4)
		if _, err := io.ReadFull(conn, rest); err != nil {
			return
		}
		frame := append(hdr, rest...) // 完整 TPKT 帧

		var resp []byte
		switch {
		case frame[5] == 0xE0: // ISO CR → CC
			resp = s7ConnectConfirm
		case len(frame) > 17 && frame[17] == 0xF0: // S7 PDU 协商 → ack
			resp = s7NegotiateAck
		case len(frame) > 24 && frame[17] == 0x04: // 读变量 → ack-data
			resp = s7ReadAck(frame)
		default:
			return
		}
		if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}

// s7ConnectConfirm ISO Connect Confirm（22 字节）。gos7 仅校验长度与 COTP PDU 类型 0xD0。
var s7ConnectConfirm = []byte{
	0x03, 0x00, 0x00, 0x16, // TPKT length 22
	0x11, 0xD0, 0x00, 0x01, 0x00, 0x01, 0x00, 0xC0, 0x01, 0x0A, // COTP CC
	0xC1, 0x02, 0x01, 0x00, // 源 TSAP
	0xC2, 0x02, 0x01, 0x02, // 目标 TSAP
}

// s7NegotiateAck S7 PDU 长度协商 ack（27 字节）。
// gos7 校验：长度==27、response[17]==0、response[18]==0，PDU 长度取 [25:27]=480。
var s7NegotiateAck = []byte{
	0x03, 0x00, 0x00, 0x1B, // TPKT length 27
	0x02, 0xF0, 0x80, // COTP
	0x32, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x08, 0x00, 0x00, // S7 头
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // 参数
	0x01, 0xE0, // 协商 PDU 长度 480
}

// s7ReadAck 构造读变量 ack-data 响应。
// 解析请求中的传输尺寸码（frame[22]）与元素数（frame[23:25]），返回定长全零数据。
func s7ReadAck(frame []byte) []byte {
	size := 1
	switch frame[22] {
	case 0x02: // byte
		size = 1
	case 0x04: // word
		size = 2
	case 0x06, 0x08: // dword / real
		size = 4
	default:
		size = 1
	}
	numElements := int(binary.BigEndian.Uint16(frame[23:25]))
	dataLen := numElements * size

	// 响应总长 = TPKT(4) + COTP(3) + S7头(10) + 参数(4) + 项头(4) + 数据
	total := 25 + dataLen
	resp := make([]byte, 0, total)
	resp = append(resp, 0x03, 0x00, byte(total>>8), byte(total)) // TPKT
	resp = append(resp, 0x02, 0xF0, 0x80)                        // COTP
	resp = append(resp, 0x32, 0x03, 0x00, 0x00)                  // S7 协议 + ACK_DATA
	resp = append(resp, frame[11], frame[12])                    // PDU 引用回显
	resp = append(resp, 0x00, 0x04)                              // 参数长度 = 4
	resp = append(resp, byte(dataLen>>8), byte(dataLen))         // 数据长度
	resp = append(resp, 0x04, 0x01, 0x00, 0x00)                  // 参数：读响应 + 项数 + 保留
	resp = append(resp, 0xFF, 0x04)                              // 项：返回码 0xFF + 传输尺寸 byte
	resp = append(resp, byte(dataLen>>8), byte(dataLen))         // 项数据长度
	resp = append(resp, make([]byte, dataLen)...)
	return resp
}
