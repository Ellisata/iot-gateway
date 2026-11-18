// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

// FANUC EtherNet/IP（CIP 显式消息）假设备。
//
// 放在 loadtest 包内而不是 SDK testkit：它除了 TCP 44818 还需要在**同一端口**
// 上应答 UDP ListIdentity（驱动连接前会做厂商校验），而 testkit 的 Server 骨架
// 只管理 TCP。为一个协议去改 testkit 的发布版本，不如把这份假设备留在使用它的
// 工具里；其它 8 个协议仍直接用 testkit/fake。
//
// 支持：
//   - RegisterSession / SendRRData / UnregisterSession；
//   - 0x0E Get_Attribute_Single：整型 0x6B / 实型 0x6C，instance 1，attr=寄存器号；
//   - 0x01 Get_Attribute_All 块读：instance 高字节=数量、低字节=1，attr=起始号；
//   - UDP ListIdentity：厂商号 591、产品名 R-30iB Plus。
//
// 寄存器值由公式生成（int32(index%1000) / float32(index%1000)+0.5），单读与块读
// 用同一公式，因此驱动的首次块读交叉校验能通过。

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"time"
)

const (
	fanucEncapHeaderSize = 24
	fanucVendorID        = 591
)

type fanucServer struct {
	ln  net.Listener
	udp *net.UDPConn

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
	once   sync.Once
}

func newFANUC(latency time.Duration) (*fanucServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		ln.Close()
		return nil, err
	}
	s := &fanucServer{ln: ln, udp: udp, conns: make(map[net.Conn]struct{})}
	go s.acceptLoop(latency)
	go s.udpLoop()
	return s, nil
}

func (s *fanucServer) Addr() string { return s.ln.Addr().String() }
func (s *fanucServer) Port() int    { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fanucServer) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		conns := make([]net.Conn, 0, len(s.conns))
		for c := range s.conns {
			conns = append(conns, c)
		}
		s.mu.Unlock()

		s.ln.Close()
		s.udp.Close()
		for _, c := range conns {
			c.Close()
		}
	})
}

func (s *fanucServer) acceptLoop(latency time.Duration) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			conn.Close()
			return
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()

		go func() {
			defer func() {
				conn.Close()
				s.mu.Lock()
				delete(s.conns, conn)
				s.mu.Unlock()
			}()
			s.handleConn(conn, latency)
		}()
	}
}

func (s *fanucServer) udpLoop() {
	buf := make([]byte, 2048)
	for {
		n, from, err := s.udp.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < fanucEncapHeaderSize || binary.LittleEndian.Uint16(buf[0:2]) != 0x0063 {
			continue
		}
		_, _ = s.udp.WriteToUDP(fanucIdentity(s.Port()), from)
	}
}

func (s *fanucServer) handleConn(conn net.Conn, latency time.Duration) {
	header := make([]byte, fanucEncapHeaderSize)
	for {
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		length := int(binary.LittleEndian.Uint16(header[2:4]))
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}

		cmd := binary.LittleEndian.Uint16(header[0:2])
		session := binary.LittleEndian.Uint32(header[4:8])
		var resp []byte
		switch cmd {
		case 0x0065: // RegisterSession
			resp = fanucEncap(cmd, 1, payload)
		case 0x006F: // SendRRData
			if latency > 0 {
				time.Sleep(latency)
			}
			resp = fanucSendRRDataResp(session, payload)
		case 0x0063: // ListIdentity（驱动走 UDP，这里兼容 TCP）
			resp = fanucIdentity(s.Port())
		case 0x0066: // UnregisterSession
			return
		default:
			return
		}
		if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}

func fanucIdentity(port int) []byte {
	body := make([]byte, 0, 64)
	body = append(body, 0x01, 0x00) // 封装协议版本

	sa := make([]byte, 16) // family(2 LE) | port(2 BE) | addr(4) | zero(8)
	binary.LittleEndian.PutUint16(sa[0:2], 2)
	binary.BigEndian.PutUint16(sa[2:4], uint16(port))
	copy(sa[4:8], net.IPv4(127, 0, 0, 1).To4())
	body = append(body, sa...)

	body = appendLE16(body, fanucVendorID)
	body = appendLE16(body, 0x000C)
	body = appendLE16(body, 0x1234)
	body = append(body, 9, 40)
	body = appendLE16(body, 0)
	body = appendLE32(body, 0xDEADBEEF)

	name := "R-30iB Plus"
	body = append(body, byte(len(name)))
	body = append(body, name...)
	if len(body)%2 == 1 {
		body = append(body, 0)
	}
	body = append(body, 0) // state

	payload := make([]byte, 2)
	binary.LittleEndian.PutUint16(payload, 1)
	payload = append(payload, 0x0C, 0x00)
	payload = appendLE16(payload, uint16(len(body)))
	payload = append(payload, body...)

	h := make([]byte, fanucEncapHeaderSize)
	binary.LittleEndian.PutUint16(h[0:2], 0x0063)
	binary.LittleEndian.PutUint16(h[2:4], uint16(len(payload)))
	return append(h, payload...)
}

func fanucSendRRDataResp(session uint32, cpf []byte) []byte {
	cipReq := extractFANUCCIPItem(cpf)
	cipResp := fanucHandleCIP(cipReq)

	body := make([]byte, 8)
	binary.LittleEndian.PutUint16(body[6:8], 2)
	body = append(body, 0, 0, 0, 0) // Null 地址项
	body = append(body, 0xB2, 0x00)
	body = appendLE16(body, uint16(len(cipResp)))
	body = append(body, cipResp...)
	return fanucEncap(0x006F, session, body)
}

func fanucEncap(cmd uint16, session uint32, payload []byte) []byte {
	h := make([]byte, fanucEncapHeaderSize)
	binary.LittleEndian.PutUint16(h[0:2], cmd)
	binary.LittleEndian.PutUint16(h[2:4], uint16(len(payload)))
	binary.LittleEndian.PutUint32(h[4:8], session)
	return append(h, payload...)
}

func fanucHandleCIP(req []byte) []byte {
	if len(req) < 2 {
		return fanucCIPResponse(0x00, 0x03, nil)
	}
	service := req[0]
	pathLen := int(req[1]) * 2
	if len(req) < 2+pathLen {
		return fanucCIPResponse(service, 0x04, nil)
	}
	class, instance, attr, err := parseFANUCPath(req[2 : 2+pathLen])
	if err != nil {
		return fanucCIPResponse(service, 0x04, nil)
	}

	switch service {
	case 0x0E: // Get_Attribute_Single
		if instance != 1 {
			return fanucCIPResponse(service, 0x05, nil)
		}
		v, ok := fanucValue(class, attr)
		if !ok {
			return fanucCIPResponse(service, 0x16, nil)
		}
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, v)
		return fanucCIPResponse(service, 0x00, buf)

	case 0x01: // Get_Attribute_All：FANUC 块读
		count := int(instance >> 8)
		if instance&0xFF != 1 || count < 1 || count > 124 {
			return fanucCIPResponse(service, 0x05, nil)
		}
		buf := make([]byte, 0, count*4)
		for i := 0; i < count; i++ {
			v, ok := fanucValue(class, uint16(int(attr)+i))
			if !ok {
				return fanucCIPResponse(service, 0x16, nil)
			}
			buf = appendLE32(buf, v)
		}
		return fanucCIPResponse(service, 0x00, buf)

	case 0x10: // Set_Attribute_Single：压测只读，接受即成功
		return fanucCIPResponse(service, 0x00, nil)

	default:
		return fanucCIPResponse(service, 0x08, nil)
	}
}

func fanucCIPResponse(service, status byte, data []byte) []byte {
	out := []byte{service | 0x80, 0x00, status, 0x00}
	return append(out, data...)
}

func fanucValue(class uint16, index uint16) (uint32, bool) {
	switch class {
	case 0x6B:
		return uint32(int32(index % 1000)), true
	case 0x6C:
		return math.Float32bits(float32(index%1000) + 0.5), true
	default:
		return 0, false
	}
}

func parseFANUCPath(path []byte) (class, instance, attr uint16, err error) {
	for i := 0; i < len(path); {
		switch path[i] {
		case 0x20:
			if i+2 > len(path) {
				return 0, 0, 0, fmt.Errorf("truncated class segment")
			}
			class = uint16(path[i+1])
			i += 2
		case 0x21:
			if i+4 > len(path) {
				return 0, 0, 0, fmt.Errorf("truncated class segment")
			}
			class = binary.LittleEndian.Uint16(path[i+1 : i+3])
			i += 4
		case 0x24:
			if i+2 > len(path) {
				return 0, 0, 0, fmt.Errorf("truncated instance segment")
			}
			instance = uint16(path[i+1])
			i += 2
		case 0x25:
			if i+4 > len(path) {
				return 0, 0, 0, fmt.Errorf("truncated instance segment")
			}
			instance = binary.LittleEndian.Uint16(path[i+1 : i+3])
			i += 4
		case 0x30:
			if i+2 > len(path) {
				return 0, 0, 0, fmt.Errorf("truncated attribute segment")
			}
			attr = uint16(path[i+1])
			i += 2
		case 0x31:
			if i+4 > len(path) {
				return 0, 0, 0, fmt.Errorf("truncated attribute segment")
			}
			attr = binary.LittleEndian.Uint16(path[i+1 : i+3])
			i += 4
		default:
			return 0, 0, 0, fmt.Errorf("unsupported path segment 0x%02X", path[i])
		}
	}
	return class, instance, attr, nil
}

func extractFANUCCIPItem(cpf []byte) []byte {
	if len(cpf) < 8 {
		return nil
	}
	count := int(binary.LittleEndian.Uint16(cpf[6:8]))
	offset := 8
	for i := 0; i < count && offset+4 <= len(cpf); i++ {
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

func appendLE16(b []byte, v uint16) []byte { return append(b, byte(v), byte(v>>8)) }
func appendLE32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}
