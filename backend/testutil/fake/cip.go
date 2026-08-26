package fake

import (
	"encoding/binary"
	"io"
	"net"
)

// NewCIP 启动一个欧姆龙 EtherNet/IP（CIP）显式报文模拟器。
//
// 帧格式与 driver/cip（封装层）及 driver/omron/cip（0x4C 服务）完全对齐：
//
//	封装帧 = 命令(2 LE) | 长度(2 LE) | 会话句柄(4 LE) | 状态(4 LE) | 发送者上下文(8) | 选项(4) | 载荷
//	Register Session（0x0065）：载荷 = 协议版本(2) + 选项(2) → 回会话句柄 + 载荷回显
//	SendRRData（0x006F）：载荷 = 接口句柄(4)+超时(2)+项数(2)+CPF 项 → 回同结构 CPF，
//	          数据项（0x00B2）内含 0x4C Data Table Read 响应（0xCC 服务回显 + 类型码 + 定长全零数据）
func NewCIP() (*Server, error) {
	return newServer(handleCIPConn)
}

func handleCIPConn(conn net.Conn) {
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
			resp = cipSendRRDataResp(session, payload)
		case 0x0066: // Unregister Session：关闭
			return
		default:
			return
		}
		if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}

// cipSendRRDataResp 构造 SendRRData 响应：解析请求 CPF 中的 0x00B2 数据项，
// 按 CIP 服务码分派——0x0A 多服务读回 0x8A 拼接各子响应，其余按单 0x4C 读回 0xCC。
func cipSendRRDataResp(session uint32, req []byte) []byte {
	var cipResp []byte
	if cipReq := extractCIPItem(req); len(cipReq) > 0 && cipReq[0] == 0x0A {
		cipResp = buildCIPMultiReadResp(cipReq)
	} else {
		cipResp = buildCIPSingleReadResp()
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

// buildCIPSingleReadResp 单 0x4C 读响应：0xCC | 保留 | 状态 0 | 类型码(2) | 2 字节全零数据。
// 数据类型码回显 INT（0xC3，2 字节），数据长度与其一致，保证 0x8A 多服务响应的
// 子响应边界与「响应类型码 → 尺寸」的解析自洽。
func buildCIPSingleReadResp() []byte {
	return []byte{
		0xCC, 0x00, 0x00, // 服务回显 + 保留 + 通用状态 0
		0xC3, 0x00, // 数据类型码：INT（2 字节）
		0x00, 0x00, // 定长数据（int16 解码取 2 字节）
	}
}

// buildCIPMultiReadResp 0x0A 多服务读响应：0x8A | 服务数(1) | [0x4C 子响应] x N。
// 每个子响应与单读一致；子响应间无长度分隔，驱动按各标签固定尺寸切分。
func buildCIPMultiReadResp(cipReq []byte) []byte {
	count := int(cipReq[1])
	resp := []byte{0x8A, byte(count)}
	for i := 0; i < count; i++ {
		resp = append(resp, buildCIPSingleReadResp()...)
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
