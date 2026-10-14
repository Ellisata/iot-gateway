// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"encoding/binary"
	"io"
	"net"
	"time"
)

// NewMC3E 启动一个三菱 MC 3E 帧（SLMP，TCP）模拟器。
//
// 帧格式与 driver/mitsubishi 完全对齐（见其 frame.go）。
//
//	请求：子头 0x5000 | 网络/PC/IO/站(5) | 请求数据长度(2 LE) | [监视定时器(2 LE) | 命令(2 LE) | 子命令(2 LE) | 首地址(3 LE) | 设备码(1) | 点数(2 LE)]
//	响应：子头 0xD000 | 网络/PC/IO/站(5) 回显 | 响应数据长度(2 LE) | 结束码(2 LE) | [数据]
//
// 注意请求与响应头长度不同：请求为 9 字节前缀 + 数据段（总 21 字节）；
// 响应为 11 字节头（含结束码）+ 数据。长度字段都在 [7:9]。
// 字单位（子命令 0x0000）返回点数×2 字节；位单位（子命令 0x0001）返回点数×1 字节。
//
// 这里按**真机**的字段顺序解析（首地址号 3 字节在前、设备码 1 字节在后），
// 不是按驱动当时的实现解析：本类型曾经跟着驱动的错顺序一起写，两边一致地错，
// 于是驱动打真机全回结束码时单元测试依然全绿。
func NewMC3E() (*Server, error) {
	return newServer(func(c net.Conn) { handleMC3EConn(c, 0) })
}

// NewMC3EWithLatency 同 NewMC3E，但每次事务注入固定延迟，模拟真实网络 RTT。
// 驱动按 maxReadWords 串行发帧（5000 点 ≈ 50 帧）。
func NewMC3EWithLatency(d time.Duration) (*Server, error) {
	return newServer(func(c net.Conn) { handleMC3EConn(c, d) })
}

func handleMC3EConn(conn net.Conn, latency time.Duration) {
	// 请求前缀 9 字节：子头(2) + 网络/PC/IO/站(5) + 请求数据长度(2 LE)
	hdr := make([]byte, 9)
	for {
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		reqLen := int(binary.LittleEndian.Uint16(hdr[7:9]))
		if reqLen < 12 { // 至少 监视定时器(2) + 请求体(10)
			return
		}
		body := make([]byte, reqLen)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}

		var data []byte
		if len(body) >= 12 {
			// body: 监视定时器(2) + 命令(2) + 子命令(2) + 首地址(3) + 设备码(1) + 点数(2)
			cmd := binary.LittleEndian.Uint16(body[2:4])
			subCmd := binary.LittleEndian.Uint16(body[4:6])
			points := binary.LittleEndian.Uint16(body[10:12])
			if cmd == 0x0401 { // 批量读
				if subCmd == 0x0001 { // 位单位
					data = make([]byte, points)
				} else { // 字单位
					data = make([]byte, int(points)*2)
				}
			}
		}

		// 响应：子头 0xD0 0x00 + 路由字段回显 + 数据长度(2 LE) + 结束码 0x0000 + 数据
		resp := make([]byte, 0, 11+len(data))
		resp = append(resp, 0xD0, 0x00)
		resp = append(resp, hdr[2], hdr[3], hdr[4], hdr[5], hdr[6]) // 网络/PC/IO/站 回显
		dl := 2 + len(data)                                         // 数据长度 = 结束码(2) + 数据
		resp = append(resp, byte(dl&0xFF), byte(dl>>8))
		resp = append(resp, 0x00, 0x00) // 结束码正常
		resp = append(resp, data...)
		if latency > 0 {
			time.Sleep(latency)
		}
		if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}
