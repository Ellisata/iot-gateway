// Package fake 提供协议假 PLC 服务器（真实 TCP 监听），供端到端容量压测使用。
//
// 每个协议一个构造函数（NewModbusTCP / NewMC3E ...），返回监听 127.0.0.1 随机端口的
// Server。假服务器只做「按请求返回合法响应（全零/定长数据）」，不对协议数量做上限校验，
// 从而把被测对象收敛到网关侧（驱动 + 采集引擎 + worker 池）本身的容量。
package fake

import (
	"net"
	"sync"
)

// Server 极简并发 TCP 服务器骨架：
// 每个连接一个 goroutine，由 handler 自行循环读请求、写响应，直到连接关闭。
// Close 会关闭监听器并主动关闭所有已建立连接，保证压测进程退出后无 goroutine 泄漏。
type Server struct {
	ln      net.Listener
	handler func(net.Conn)

	// externalAddr/externalClose 非标准 TCP 骨架协议（如 OPC UA 复用
	// gopcua 进程内服务器自带监听）时的端口与关闭入口；设置后
	// Port/Addr/Close 走外部路径，conn 跟踪不生效（由外部服务器自理）。
	externalAddr  net.Addr
	externalClose func()

	mu    sync.Mutex
	conns map[net.Conn]struct{}
	done  chan struct{}
	once  sync.Once
}

// newManagedServer 构造一个由外部机制监听/关闭的 Server（无 conn 跟踪）。
func newManagedServer(addr net.Addr, closeFn func()) *Server {
	return &Server{externalAddr: addr, externalClose: closeFn, conns: map[net.Conn]struct{}{}, done: make(chan struct{})}
}

// newServer 创建监听 127.0.0.1 随机端口的 TCP 服务器。
func newServer(handler func(net.Conn)) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{
		ln:      ln,
		handler: handler,
		conns:   make(map[net.Conn]struct{}),
		done:    make(chan struct{}),
	}
	go s.acceptLoop()
	return s, nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // 监听器已关闭
		}
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()

		go func() {
			defer func() {
				conn.Close()
				s.mu.Lock()
				delete(s.conns, conn)
				s.mu.Unlock()
			}()
			s.handler(conn)
		}()
	}
}

// Addr 返回 "127.0.0.1:port"。
func (s *Server) Addr() string {
	if s.externalAddr != nil {
		return s.externalAddr.String()
	}
	return s.ln.Addr().String()
}

// Port 返回监听端口。
func (s *Server) Port() int {
	if s.externalAddr != nil {
		if tcp, ok := s.externalAddr.(*net.TCPAddr); ok {
			return tcp.Port
		}
	}
	return s.ln.Addr().(*net.TCPAddr).Port
}

// Close 关闭监听器并关闭所有活动连接。
func (s *Server) Close() {
	s.once.Do(func() {
		if s.externalClose != nil {
			s.externalClose()
			return
		}
		close(s.done)
		s.ln.Close()
		s.mu.Lock()
		for c := range s.conns {
			c.Close()
		}
		s.mu.Unlock()
	})
}
