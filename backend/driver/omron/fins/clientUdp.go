package fins

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"iot-gateway/logger"
)

// finsUDPClient FINS/UDP 传输层客户端。
//
// UDP 无连接：一次 Dial 得到套接字，每次 Read 发送一个数据报并等待对应 SID 响应。
// 单次请求+响应内部持互斥锁，并发 Read 安全；读超时不判定断连（UDP 无连接语义，
// 一次超时可能只是丢包，套接字仍可用，下一轮轮询自然重试）。
type finsUDPClient struct {
	mu        sync.Mutex
	conn      *net.UDPConn
	timeout   time.Duration
	dstNode   byte
	dstUnit   byte
	srcNode   byte
	srcUnit   byte
	sid       byte
	connected bool
}

// newFINSUDPClient 创建并建立 FINS/UDP 连接（套接字）。
func newFINSUDPClient(cfg *FINSConfig) (finsTransport, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("fins udp: host is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	if err != nil {
		return nil, fmt.Errorf("fins udp: resolve %s failed: %w", cfg.Host, err)
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("fins udp: dial %s failed: %w", addr, err)
	}

	// SA1 必须为合法节点号（1..254）：真机核实 SA1=0 会被设备拒绝（0x2108）。
	// 未显式配置时取本机到目标的源 IP 末段（FINS 惯例），详见 resolveSrcNode。
	var localIP net.IP
	if la, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		localIP = la.IP
	}

	return &finsUDPClient{
		conn:      conn,
		timeout:   timeout,
		dstNode:   cfg.DstNode,
		dstUnit:   cfg.DstUnit,
		srcNode:   resolveSrcNode(cfg.SrcNode, localIP),
		srcUnit:   cfg.SrcUnit,
		connected: true,
	}, nil
}

// Read 发送内存区读取命令并等待响应。
func (c *finsUDPClient) Read(area finsArea, word, count uint16) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil || !c.connected {
		return nil, fmt.Errorf("fins udp: not connected")
	}

	c.sid++
	sid := c.sid
	frame := buildReadFrame(area, word, count, c.dstNode, c.dstUnit, c.srcNode, c.srcUnit, sid)

	if err := c.conn.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, fmt.Errorf("fins udp: set deadline failed: %w", err)
	}
	if _, err := c.conn.Write(frame); err != nil {
		c.connected = false
		return nil, fmt.Errorf("fins udp: write failed: %w", err)
	}

	// 响应上限：960 字（约 1934 字节）；4096 留有富余
	buf := make([]byte, 4096)
	n, err := c.conn.Read(buf)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, fmt.Errorf("fins udp: read timeout (area=0x%02X word=%d count=%d)", area, word, count)
		}
		c.connected = false
		return nil, fmt.Errorf("fins udp: read failed: %w", err)
	}

	data, err := parseReadResponse(buf[:n], sid)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// IsConnected 返回连接状态
func (c *finsUDPClient) IsConnected() bool {
	return c != nil && c.connected
}

// Close 关闭连接
func (c *finsUDPClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.connected = false
	logger.Info("fins udp client closed")
	return err
}
