// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"encoding/binary"
	"io"
	"net"
)

// NewModbusTCP 启动一个 Modbus TCP 从站模拟器（MBAP + PDU）。
//
// 支持读功能码 1/2/3/4（返回定长全零数据）与写功能码 5/6/15/16（回显成功），
// 其余功能码返回 IllegalFunction 异常。不对读取数量做协议上限校验（数量即来自请求，
// 响应长度完全按请求回填），用于把网关侧容量从设备侧限制中隔离出来。
func NewModbusTCP() (*Server, error) {
	return newServer(handleModbusConn)
}

func handleModbusConn(conn net.Conn) {
	hdr := make([]byte, 7)
	for {
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		// MBAP 长度字段 = 1(unitID) + PDU 长度
		length := binary.BigEndian.Uint16(hdr[4:6])
		if length < 2 { // 至少 unitID + FC
			return
		}
		pdu := make([]byte, length-1)
		if _, err := io.ReadFull(conn, pdu); err != nil {
			return
		}
		resp := buildModbusResponse(hdr, pdu)
		if resp == nil {
			return
		}
		if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}

// buildModbusResponse 构造 Modbus TCP 响应帧。
// hdr 为请求的 7 字节 MBAP 头（回显事务号与 unitID），pdu 为请求 PDU。
func buildModbusResponse(hdr, pdu []byte) []byte {
	fc := pdu[0]
	var body []byte
	switch fc {
	case 1, 2: // 读线圈 / 读离散输入：响应数据 = ceil(数量/8) 字节位图
		if len(pdu) != 5 {
			return modbusException(hdr, fc, 3)
		}
		qty := binary.BigEndian.Uint16(pdu[3:5])
		n := (int(qty) + 7) / 8
		body = append([]byte{fc, byte(n)}, make([]byte, n)...)
	case 3, 4: // 读保持寄存器 / 读输入寄存器：响应数据 = 数量 × 2 字节
		if len(pdu) != 5 {
			return modbusException(hdr, fc, 3)
		}
		qty := binary.BigEndian.Uint16(pdu[3:5])
		data := make([]byte, int(qty)*2)
		body = append([]byte{fc, byte(len(data))}, data...)
	case 5, 6: // 写单线圈 / 写单寄存器：回显请求
		if len(pdu) != 5 {
			return modbusException(hdr, fc, 3)
		}
		body = pdu
	case 15, 16: // 写多线圈 / 写多寄存器：返回起始地址 + 数量
		if len(pdu) < 6 {
			return modbusException(hdr, fc, 3)
		}
		body = append([]byte{fc}, pdu[1:5]...)
	default:
		return modbusException(hdr, fc, 1) // IllegalFunction
	}

	resp := make([]byte, 0, 7+len(body))
	resp = append(resp, hdr[0], hdr[1]) // 事务号回显
	resp = append(resp, 0, 0)           // 协议号 = 0
	resp = append(resp, byte((len(body)+1)>>8), byte(len(body)+1)) // 长度 = unitID + body
	resp = append(resp, hdr[6])         // unitID 回显
	resp = append(resp, body...)
	return resp
}

// modbusException 构造 Modbus 异常响应（FC|0x80 + 异常码）。
func modbusException(hdr []byte, fc, code byte) []byte {
	body := []byte{fc | 0x80, code}
	resp := make([]byte, 0, 9)
	resp = append(resp, hdr[0], hdr[1], 0, 0, 0, byte(len(body)+1), hdr[6])
	resp = append(resp, body...)
	return resp
}
