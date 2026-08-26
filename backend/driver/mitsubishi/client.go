package mitsubishi

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"iot-gateway/logger"
)

// mcTransport MC 传输层接口。
// 各传输（TCP 3E 帧 / 串口 4C Format5）共享同一 MC 应用层帧与解析，仅底层 I/O 与封帧不同。
type mcTransport interface {
	// Read 读取指定设备从 head 起的 points 个点位，返回原始数据。
	// 字单位（bitMode=false）：points 个 16 位字，2*points 字节（小端）；
	// 位单位（bitMode=true）：points 个位，points 字节（0x00/0x01）。
	// 结束码错误返回 mcEndCodeError（连接是通的）；网络/超时错误返回普通 error。
	Read(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error)
	// IsConnected 返回连接状态
	IsConnected() bool
	// Close 关闭连接释放资源
	Close() error
}

// newTransport 按配置的传输方式创建对应传输层实例。
func newTransport(cfg *MCConfig) (mcTransport, error) {
	switch cfg.Transport {
	case TransportSerial:
		return newMCSerialClient(cfg)
	default:
		return newMCTCPClient(cfg)
	}
}

// mcTCPClient 3E 帧 TCP 传输层客户端。
//
// 连接生命周期：TCP 连接（MC 协议无握手，直接发帧读响应）。单次请求+响应内部持
// 互斥锁，并发 Read 安全（对齐 fins TCP / modbus TCP）。采集轮询持续通信即可保活。
type mcTCPClient struct {
	mu        sync.Mutex
	conn      net.Conn
	timeout   time.Duration
	connected bool
}

// newMCTCPClient 创建并建立 MC TCP 连接。
func newMCTCPClient(cfg *MCConfig) (mcTransport, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("mc tcp: host is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("mc tcp: dial %s failed: %w", addr, err)
	}

	logger.Info("mc tcp client connected to %s", addr)
	return &mcTCPClient{
		conn:      conn,
		timeout:   timeout,
		connected: true,
	}, nil
}

// Read 通过 3E 帧读取设备数据。
func (c *mcTCPClient) Read(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil || !c.connected {
		return nil, fmt.Errorf("mc tcp: not connected")
	}

	frame := buildReadFrame(device, head, points, bitMode)
	if err := c.writeAll(frame); err != nil {
		c.connected = false
		return nil, fmt.Errorf("mc tcp: write failed: %w", err)
	}

	// 响应头 11 字节：子头(2) + 网络/PC/IO/站(5) + 响应数据长度(2) + 结束码(2)
	if err := c.conn.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, err
	}
	hdr := make([]byte, 11)
	if _, err := io.ReadFull(c.conn, hdr); err != nil {
		c.connected = false
		return nil, fmt.Errorf("mc tcp: read header failed: %w", err)
	}
	// 响应数据长度（2 BE）= 结束码(2) + 数据字节数，按其读取数据而非按请求点数推算
	dataLen := tcpResponseDataLen(hdr)
	if dataLen < 2 {
		c.connected = false
		return nil, fmt.Errorf("mc tcp: invalid response data length %d", dataLen)
	}
	if _, err := parseTCPEndCode(hdr); err != nil {
		return nil, err // 结束码错误：连接是通的，不标记断连
	}

	data := make([]byte, dataLen-2)
	if _, err := io.ReadFull(c.conn, data); err != nil {
		c.connected = false
		return nil, fmt.Errorf("mc tcp: read data failed: %w", err)
	}
	return data, nil
}

// writeAll 写入全部字节
func (c *mcTCPClient) writeAll(b []byte) error {
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
func (c *mcTCPClient) IsConnected() bool {
	return c != nil && c.connected
}

// Close 关闭连接
func (c *mcTCPClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.connected = false
	logger.Info("mc tcp client closed")
	return err
}
