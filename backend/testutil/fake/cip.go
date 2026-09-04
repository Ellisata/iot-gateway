// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"encoding/binary"
	"io"
	"net"
	"time"
)

// NewCIP 启动一个欧姆龙 EtherNet/IP（CIP）显式报文模拟器。
//
//	欧姆龙 CIP：0x0A 多服务读（按 maxTagsPerRequest 批量），每标签固定 2 字节 INT
//
// 帧格式与 driver/omron/cip 的解封一致——0x4C 读响应 = 服务回显 0xCC | 保留 |
// 通用状态 | 类型码(2 LE) | 数据（欧姆龙方言，无 CIP 扩展状态字数字节）。
//
//	封装帧 = 命令(2 LE) | 长度(2 LE) | 会话句柄(4 LE) | 状态(4 LE) | 发送者上下文(8) | 选项(4) | 载荷
//	Register Session（0x0065）：载荷 = 协议版本(2) + 选项(2) → 回会话句柄 + 载荷回显
//	SendRRData（0x006F）：载荷 = 接口句柄(4)+超时(2)+项数(2)+CPF 项 → 回同构 CPF，
//	          数据项（0x00B2）内含 CIP 响应
func NewCIP() (*Server, error) {
	return newServer(func(c net.Conn) { handleCIPConn(c, respOmron, 0) })
}

// NewCIPWithLatency 同 NewCIP，但每次 SendRRData 事务注入固定延迟，模拟真实网络 RTT。
// CIP 驱动逐标签串行读，RTT 直接叠加进轮询周期，是容量评估的主导项。
func NewCIPWithLatency(d time.Duration) (*Server, error) {
	return newServer(func(c net.Conn) { handleCIPConn(c, respOmron, d) })
}

// NewCIPAB 启动一个罗克韦尔（Allen-Bradley Logix）EtherNet/IP 模拟器。
//
// 响应为标准 CIP Message Router 方言：0x4C 读响应 = 服务回显 0xCC | 保留 |
// 通用状态 | 扩展状态字数(1，成功为 0) | 类型码(2 LE) | 数据。
// goindustrial 严格按该规范解码（扩展状态字数字节缺失时类型码首字节会被
// 误读为字数导致 EOF），与欧姆龙方言的差异见 NewCIP 注释。
// 数组 Read Tag Elements 按请求末 2 字节 ElementCount 回等量元素。
func NewCIPAB() (*Server, error) {
	return newServer(func(c net.Conn) { handleCIPConn(c, respAB, 0) })
}

// NewCIPABWithLatency 同 NewCIPAB，但每次事务注入固定延迟。
func NewCIPABWithLatency(d time.Duration) (*Server, error) {
	return newServer(func(c net.Conn) { handleCIPConn(c, respAB, d) })
}

// respDialect CIP 0x4C 读响应方言：欧姆龙与 AB 在通用状态后的字段布局不同。
type respDialect int

const (
	respOmron respDialect = iota
	respAB
)

func handleCIPConn(conn net.Conn, dialect respDialect, latency time.Duration) {
	hdr := make([]byte, 24)
	for {
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		length := int(binary.LittleEndian.Uint16(hdr[2:4]))
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}

		cmd := binary.LittleEndian.Uint16(hdr[0:2])
		session := binary.LittleEndian.Uint32(hdr[4:8])
		var resp []byte
		switch cmd {
		case 0x0065: // Register Session：分配会话句柄 1，载荷回显
			resp = buildEncapFrame(0x0065, 0x00000001, payload)
		case 0x006F: // SendRRData：按请求 CPF 回同构响应
			resp = cipSendRRDataResp(session, payload, dialect)
		case 0x0066: // Unregister Session：关闭
			return
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

// cipSendRRDataResp 构造 SendRRData 响应：解析请求 CPF 中的 0x00B2 数据项，
// 按 CIP 服务码分派——0x0A 多服务读回 0x8A 拼接各子响应（欧姆龙专属），
// 其余按单 0x4C 读回 0xCC。
func cipSendRRDataResp(session uint32, req []byte, dialect respDialect) []byte {
	cipReq := extractCIPItem(req)
	var cipResp []byte
	if len(cipReq) > 0 && cipReq[0] == 0x0A {
		cipResp = buildCIPMultiReadResp(cipReq, dialect)
	} else {
		cipResp = buildCIPSingleReadResp(cipReq, dialect)
	}

	body := make([]byte, 8) // 接口句柄(4) + 超时(2) + 项数(2，回填)
	binary.LittleEndian.PutUint16(body[6:8], 2)
	// 地址项：Null（0x0000，长度 0）
	body = append(body, 0x00, 0x00, 0x00, 0x00)
	// 数据项：0x00B2 + 长度 + CIP 响应
	body = append(body, 0xB2, 0x00)
	body = append(body, byte(len(cipResp)), byte(len(cipResp)>>8))
	body = append(body, cipResp...)
	return buildEncapFrame(0x006F, session, body)
}

// extractCIPItem 解析 SendRRData 载荷（CPF），返回 0x00B2 数据项中的 CIP 请求字节。
// 载荷 = 接口句柄(4) | 超时(2) | 项数(2 LE) | 项...（每项: 类型(2 LE) | 长度(2 LE) | 载荷）
func extractCIPItem(cpf []byte) []byte {
	if len(cpf) < 8 {
		return nil
	}
	itemCount := int(binary.LittleEndian.Uint16(cpf[6:8]))
	offset := 8
	for i := 0; i < itemCount && offset+4 <= len(cpf); i++ {
		itemType := binary.LittleEndian.Uint16(cpf[offset : offset+2])
		itemLen := int(binary.LittleEndian.Uint16(cpf[offset+2 : offset+4]))
		offset += 4
		if offset+itemLen > len(cpf) {
			return nil
		}
		if itemType == 0x00B2 {
			return cpf[offset : offset+itemLen]
		}
		offset += itemLen
	}
	return nil
}

// buildCIPSingleReadResp 单 0x4C 读响应。
//
// ElementCount 取 0x4C 请求末 2 字节 LE（请求 = 服务码 | 路径字数 | tag path | 元素数）：
// 欧姆龙单读/多服务子读请求元素数恒为 1，响应 2 字节数据与历史行为一致；
// 罗克韦尔数组 Read Tag Elements 请求 N 个元素，回 N×2 字节 INT 确定性数据
// （第 i 个元素 LE 编码 i%1000），保证数组合并读路径端到端可测。
//
// 方言差异（见 NewCIP/NewCIPAB 注释）：
//
//	欧姆龙：CC | 00 | 状态 | 类型码(2) | 数据
//	AB    ：CC | 00 | 状态 | 扩展状态字数(1) | 类型码(2) | 数据
func buildCIPSingleReadResp(cipReq []byte, dialect respDialect) []byte {
	count := 1
	if len(cipReq) >= 2 {
		count = int(binary.LittleEndian.Uint16(cipReq[len(cipReq)-2:]))
		if count < 1 {
			count = 1
		}
	}
	resp := []byte{
		0xCC, 0x00, 0x00, // 服务回显 + 保留 + 通用状态 0
	}
	if dialect == respAB {
		resp = append(resp, 0x00) // 扩展状态字数 0
	}
	resp = append(resp, 0xC3, 0x00) // 数据类型码：INT（2 字节）
	for i := 0; i < count; i++ {
		v := uint16(i % 1000)
		resp = append(resp, byte(v), byte(v>>8))
	}
	return resp
}

// buildCIPMultiReadResp 0x0A 多服务读响应：0x8A | 服务数(1) | [0x4C 子响应] x N。
// 每个子响应与单读一致（元素数 1 → 2 字节数据）；子响应间无长度分隔，
// 驱动按各标签固定尺寸切分。
func buildCIPMultiReadResp(cipReq []byte, dialect respDialect) []byte {
	count := int(cipReq[1])
	resp := []byte{0x8A, byte(count)}
	for i := 0; i < count; i++ {
		resp = append(resp, buildCIPSingleReadResp(nil, dialect)...)
	}
	return resp
}

// buildEncapFrame 组装 24 字节封装头 + 载荷。
func buildEncapFrame(cmd uint16, session uint32, payload []byte) []byte {
	frame := make([]byte, 0, 24+len(payload))
	hdr := make([]byte, 24)
	binary.LittleEndian.PutUint16(hdr[0:2], cmd)
	binary.LittleEndian.PutUint16(hdr[2:4], uint16(len(payload)))
	binary.LittleEndian.PutUint32(hdr[4:8], session)
	return append(append(frame, hdr...), payload...)
}
