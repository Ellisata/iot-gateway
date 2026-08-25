package fins

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"iot-gateway/logger"
)

// finsHostLinkTCPClient Host Link over TCP 传输层客户端。
//
// 用于经「串口转以太网」设备（串口服务器，如 USR-TCP232 / Moxa NPort）采集远端 PLC 串口：
// 网关直连串口服务器的 TCP 端口，但帧仍为 Host Link（C-mode）格式——串口服务器只是字节透传，
// 把 TCP 收到的字节原样搬到 PLC 的 RS-232/485 上，并不认识 FINS/TCP 帧。
//
// 与 finsTCPClient（FINS/TCP，供 PLC 自带以太网口）帧格式不同，两者不可混用。
// 连接为点对点 TCP，与 FINS/TCP 同级：单次请求+响应内部持互斥锁，并发 Read 安全，
// 采集引擎按线程安全驱动处理（不走 SerialExclusive）。
type finsHostLinkTCPClient struct {
	mu        sync.Mutex
	conn      net.Conn
	timeout   time.Duration
	unitNo    byte
	dstUnit   byte
	srcUnit   byte
	sid       byte
	fcsMode   string
	connected bool
}

// newFINSHostLinkTCPClient 建立到串口服务器的 TCP 连接（无握手，直接收发 Host Link 帧）。
func newFINSHostLinkTCPClient(cfg *FINSConfig) (finsTransport, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("fins hostlink-tcp: host is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	port := cfg.Port
	if port <= 0 {
		port = defaultPort
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("fins hostlink-tcp: dial %s failed: %w", addr, err)
	}

	logger.Info("fins hostlink-tcp client connected to %s (unit=%d)", addr, cfg.UnitNo)
	return &finsHostLinkTCPClient{
		conn:      conn,
		timeout:   timeout,
		unitNo:    cfg.UnitNo,
		dstUnit:   cfg.DstUnit,
		srcUnit:   cfg.SrcUnit,
		fcsMode:   cfg.FCSMode,
		connected: true,
	}, nil
}

// Read 通过 Host Link 帧（经串口服务器透传）读取内存区。
func (c *finsHostLinkTCPClient) Read(area finsArea, word, count uint16) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil || !c.connected {
		return nil, fmt.Errorf("fins hostlink-tcp: not connected")
	}

	c.sid++
	sid := c.sid
	finsBody := []byte{
		cmdMemoryAreaReadHi, cmdMemoryAreaReadLo,
		byte(area),
		byte(word >> 8), byte(word),
		0x00, // 位地址：字访问
		byte(count >> 8), byte(count),
	}
	frame := buildSerialFrame(c.fcsMode, c.unitNo, c.dstUnit, c.srcUnit, sid, finsBody)
	if err := c.writeAll(frame); err != nil {
		c.connected = false
		return nil, fmt.Errorf("fins hostlink-tcp: write failed: %w", err)
	}

	resp, err := c.readFrame()
	if err != nil {
		c.connected = false
		return nil, fmt.Errorf("fins hostlink-tcp: read failed: %w", err)
	}

	data, err := parseSerialResponse(resp, sid, c.fcsMode)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// readFrame 读取一个完整 Host Link 响应帧（读到 CR 终止符）。
// 读超时由 SetReadDeadline 控制。
func (c *finsHostLinkTCPClient) readFrame() ([]byte, error) {
	if err := c.conn.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, err
	}
	buf := make([]byte, 0, 128)
	tmp := make([]byte, 1)
	for {
		n, err := c.conn.Read(tmp)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		// 跳过上一帧 CRLF 结尾遗留的 \n/\r（Host Link 帧必以 @ 开头，前导换行非法）
		if len(buf) == 0 && (tmp[0] == '\n' || tmp[0] == '\r') {
			continue
		}
		buf = append(buf, tmp[0])
		if tmp[0] == '\r' {
			return buf, nil
		}
		if len(buf) > 512 {
			return nil, fmt.Errorf("fins hostlink-tcp: response too long (%d bytes)", len(buf))
		}
	}
}

// writeAll 写入全部字节
func (c *finsHostLinkTCPClient) writeAll(b []byte) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return err
	}
	for len(b) > 0 {
		n, err := c.conn.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

// IsConnected 返回连接状态
func (c *finsHostLinkTCPClient) IsConnected() bool {
	return c != nil && c.connected
}

// Close 关闭连接
func (c *finsHostLinkTCPClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.connected = false
	logger.Info("fins hostlink-tcp client closed")
	return err
}
