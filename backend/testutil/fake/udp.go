// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"net"
	"sync"
)

// UDPServer 极简 UDP 假设备骨架：每个数据报一个 goroutine，handler 按请求字节
// 返回响应字节；返回 nil 表示**不应答**（模拟"这个端口上没有该协议的设备"）。
//
// 接口与 TCP 的 Server 对齐（Addr / Port / Close），让两种传输的用例写法一致 ——
// 广播型与无连接协议（FINS/UDP、EtherNet/IP 的 ListIdentity）的探测路径与 TCP
// 完全不同（没有连接建立这一步，失败只表现为超时），必须能单独造出来。
type UDPServer struct {
	conn    *net.UDPConn
	handler func(req []byte) []byte

	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

// NewUDPServer 在 127.0.0.1 的随机端口上起一个 UDP 假设备。
func NewUDPServer(handler func(req []byte) []byte) (*UDPServer, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	s := &UDPServer{conn: conn, handler: handler, done: make(chan struct{})}
	s.wg.Add(1)
	go s.readLoop()
	return s, nil
}

func (s *UDPServer) readLoop() {
	defer s.wg.Done()

	// 一次请求的最大长度：FINS 的读取请求只有十几字节，EtherNet/IP 的
	// ListIdentity 也不长。2048 对两者都绰绰有余。
	buf := make([]byte, 2048)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return // 套接字已关闭
		}
		// 每个数据报一份拷贝再交给 goroutine：buf 会被下一次 ReadFromUDP 覆盖。
		req := append([]byte(nil), buf[:n]...)

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			resp := s.handler(req)
			if resp == nil {
				return // 不应答 = 这个端口上没有我的设备
			}
			_, _ = s.conn.WriteToUDP(resp, from)
		}()
	}
}

// Addr 返回 "127.0.0.1:port"。
func (s *UDPServer) Addr() string { return s.conn.LocalAddr().String() }

// Port 返回监听端口。
func (s *UDPServer) Port() int { return s.conn.LocalAddr().(*net.UDPAddr).Port }

// Close 关闭套接字并等待在途应答收尾。
//
// **必须等**：应答 goroutine 手里还攥着 s.conn，不等它们退出就关闭套接字，
// 写入会撞上 "use of closed network connection" —— 在测试里表现为一堆
// 与断言无关的噪音日志。
func (s *UDPServer) Close() {
	s.once.Do(func() {
		close(s.done)
		s.conn.Close()
		s.wg.Wait()
	})
}
