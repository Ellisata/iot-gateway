// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"encoding/binary"
	"io"
	"net"
	"time"
)

// fakeControllerModel 假 PLC 自报的型号，与真机（CJ2M-CPU31）同形：
// 20 字节 ASCII、NUL 补齐，控制器数据读取（0x0501）的响应前 20 字节。
const fakeControllerModel = "CJ2M-CPU31"

// NewFINSTCP 启动一个欧姆龙 FINS/TCP 模拟器。
//
// 帧格式与 driver/omron/fins 完全对齐（见其 frame.go）：
//
//	帧 = "FINS"(4) | 长度(4 BE) | 命令(4 BE) | 错误码(4 BE) | 载荷
//
//	连接请求：命令 0x00000000，载荷 4 字节节点号 → 连接响应 0x00000001（载荷回显）
//	数据发送：命令 0x00000002，载荷 = FINS 帧（10 字节头 + 命令体）
//	          → 数据响应 0x00000002，载荷 = FINS 响应（见 buildFINSResponse）
//
// 处理内存区读取（0x0101，字单位）与控制器数据读取（0x0501），
// 后者用于设备扫描页的型号识别。位读取由驱动读字本地解析。
func NewFINSTCP() (*Server, error) {
	return newServer(func(c net.Conn) { handleFINSConn(c, 0) })
}

// NewFINSTCPWithLatency 同 NewFINSTCP，但每次事务注入固定延迟，模拟真实网络 RTT。
// 驱动按 maxReadWords 串行发帧（5000 点 ≈ 50 帧）。
func NewFINSTCPWithLatency(d time.Duration) (*Server, error) {
	return newServer(func(c net.Conn) { handleFINSConn(c, d) })
}

// NewFINSUDP 启动一个欧姆龙 FINS/UDP 模拟器（无连接，一次数据报一次应答）。
//
// 与 TCP 版共用同一套 FINS 应答构造（buildFINSResponse），差别只在没有
// FINS/TCP 的连接握手那一层。设备扫描页的 FINS/UDP 探测走的就是这条路径。
func NewFINSUDP() (*UDPServer, error) {
	return NewUDPServer(func(req []byte) []byte {
		// 认不出的帧不应答 —— 与真实设备面对非 FINS 流量的行为一致，
		// 也正是"端口上跑着别的服务不会误报"这条判据的来源。
		return buildFINSResponse(req)
	})
}

func handleFINSConn(conn net.Conn, latency time.Duration) {
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
			resp = buildFINSTCPFrame(0x00000002, 0, buildFINSResponse(payload))
		default:
			return
		}
		if latency > 0 {
			time.Sleep(latency)
		}
		if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}

// buildFINSResponse 由一条 FINS 命令帧构造应答帧（10 字节头 + 命令回显 + 结束码 + 数据）。
//
// req 为请求的 FINS 帧（10 字节头 + 命令码(2) + 命令体）；响应的 FINS 头**回显**请求头
// 但把 DA/SA 互换（真机如此，SID 也靠这一步原样带回，探测与采集都靠它匹配应答）。
//
// 处理两条命令，其余一律回结束码 0x0401（不可执行）：
//
//	0x0101 内存区读取     → count×2 字节零值数据
//	0x0501 控制器数据读取 → 20 字节型号（ASCII，NUL 补齐）+ 2 字节版本
//
// 帧太短或命令体不足以解析时返回 nil（TCP 侧会包一个空载荷帧，UDP 侧则不应答）。
func buildFINSResponse(req []byte) []byte {
	if len(req) < 12 {
		return nil
	}

	head := make([]byte, 10)
	copy(head, req[0:10])
	head[4], head[7] = req[7], req[4] // DA1 ↔ SA1
	head[5], head[8] = req[8], req[5] // DA2 ↔ SA2

	body := make([]byte, 0, 10+2+2+40)
	body = append(body, head...)
	body = append(body, req[10], req[11]) // 命令回显

	switch cmd := binary.BigEndian.Uint16(req[10:12]); cmd {
	case 0x0101:
		if len(req) < 18 {
			return nil
		}
		// 命令体：命令(2) | 区域(1) | 字地址(2 BE) | 位(1) | 数量(2 BE)
		count := int(binary.BigEndian.Uint16(req[16:18]))
		body = append(body, 0x00, 0x00) // 结束码 0x0000
		body = append(body, make([]byte, count*2)...)
	case 0x0501:
		body = append(body, 0x00, 0x00)
		model := make([]byte, 20)
		copy(model, fakeControllerModel)
		body = append(body, model...)
		body = append(body, 0x03, 0x00) // 控制器版本（BCD），仅凑出真机的形状
	default:
		body = append(body, 0x04, 0x01) // 结束码 0x0401：不可执行
	}
	return body
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
