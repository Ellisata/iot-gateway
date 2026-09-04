// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"iot-gateway/logger"
)

// finsTCPClient FINS/TCP 传输层客户端。
//
// 连接生命周期：TCP 连接 → 连接请求（命令=0x00000000，载荷=4 字节节点号）
// → 连接响应（命令=0x00000001）→ 数据发送（命令=0x00000002）→ 数据响应（命令=0x00000002）。
// 单次请求+响应内部持互斥锁，并发 Read 安全。采集轮询本身持续通信即可保活连接。
type finsTCPClient struct {
	mu        sync.Mutex
	conn      net.Conn
	timeout   time.Duration
	dstNode   byte
	dstUnit   byte
	srcNode   byte
	srcUnit   byte
	sid       byte
	connected bool
}

// newFINSTCPClient 创建并建立 FINS/TCP 连接（含握手）。
func newFINSTCPClient(cfg *FINSConfig) (finsTransport, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("fins tcp: host is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("fins tcp: dial %s failed: %w", addr, err)
	}

	// SA1 必须为合法节点号（1..254）：真机核实 SA1=0 会被设备拒绝（0x2108）。
	// 未显式配置时取本机到目标的源 IP 末段（FINS 惯例），详见 resolveSrcNode。
	var localIP net.IP
	if la, ok := conn.LocalAddr().(*net.TCPAddr); ok {
		localIP = la.IP
	}

	c := &finsTCPClient{
		conn:      conn,
		timeout:   timeout,
		dstNode:   cfg.DstNode,
		dstUnit:   cfg.DstUnit,
		srcNode:   resolveSrcNode(cfg.SrcNode, localIP),
		srcUnit:   cfg.SrcUnit,
		connected: true,
	}
	if err := c.connect(); err != nil {
		conn.Close()
		c.connected = false
		return nil, err
	}
	return c, nil
}

// connect 执行 FINS/TCP 连接握手。
func (c *finsTCPClient) connect() error {
	// 连接请求：命令=0x00000000，载荷 = 4 字节 FINS 节点号（登记源节点，PLC 据此路由响应）。
	// 节点号取配置的 srcNode，与后续 FINS 帧 SA1 保持一致。
	frame := buildConnectFrame(c.srcNode)
	if err := c.writeAll(frame); err != nil {
		return fmt.Errorf("fins tcp: connect request failed: %w", err)
	}

	resp, err := c.readFrame()
	if err != nil {
		return fmt.Errorf("fins tcp: connect response failed: %w", err)
	}
	cmd, errCode, _, err := parseTCPFrame(resp)
	if err != nil {
		return err
	}
	if errCode != 0 {
		return fmt.Errorf("fins tcp: connect error code 0x%08X", errCode)
	}
	if cmd != finsTCPCmdConnectResp {
		return fmt.Errorf("fins tcp: unexpected connect response command 0x%08X", cmd)
	}

	logger.Info("fins tcp client connected")
	return nil
}

// Read 通过 FINS/TCP 数据发送命令读取内存区。
func (c *finsTCPClient) Read(area finsArea, word, count uint16) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil || !c.connected {
		return nil, fmt.Errorf("fins tcp: not connected")
	}

	c.sid++
	sid := c.sid
	finsFrame := buildReadFrame(area, word, count, c.dstNode, c.dstUnit, c.srcNode, c.srcUnit, sid)
	frame := append(buildTCPHeader(finsTCPCmdDataSend, 0, len(finsFrame)), finsFrame...)

	if err := c.writeAll(frame); err != nil {
		c.connected = false
		return nil, fmt.Errorf("fins tcp: write failed: %w", err)
	}

	resp, err := c.readFrame()
	if err != nil {
		c.connected = false
		return nil, fmt.Errorf("fins tcp: read failed: %w", err)
	}
	cmd, errCode, payload, err := parseTCPFrame(resp)
	if err != nil {
		c.connected = false
		return nil, err
	}
	if errCode != 0 {
		c.connected = false
		return nil, fmt.Errorf("fins tcp: error code 0x%08X", errCode)
	}
	if cmd != finsTCPCmdDataResp {
		return nil, fmt.Errorf("fins tcp: unexpected data response command 0x%08X", cmd)
	}

	data, err := parseReadResponse(payload, sid)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// readFrame 读取一个完整 FINS/TCP 帧（16 字节头 + 载荷）。
func (c *finsTCPClient) readFrame() ([]byte, error) {
	if err := c.conn.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, err
	}
	hdr := make([]byte, 16)
	if _, err := io.ReadFull(c.conn, hdr); err != nil {
		return nil, err
	}
	if string(hdr[0:4]) != "FINS" {
		return nil, fmt.Errorf("fins tcp: bad protocol identifier %q", hdr[0:4])
	}
	length := binary.BigEndian.Uint32(hdr[4:8])
	if length < 8 || length > 65535 {
		return nil, fmt.Errorf("fins tcp: invalid length %d", length)
	}
	// 长度字段 = 命令(4) + 错误码(4) + FINS 载荷，读取剩余的 FINS 载荷部分
	payload := make([]byte, length-8)
	if _, err := io.ReadFull(c.conn, payload); err != nil {
		return nil, err
	}
	return append(hdr, payload...), nil
}

// writeAll 写入全部字节
func (c *finsTCPClient) writeAll(b []byte) error {
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
func (c *finsTCPClient) IsConnected() bool {
	return c != nil && c.connected
}

// Close 关闭连接
func (c *finsTCPClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.connected = false
	logger.Info("fins tcp client closed")
	return err
}
