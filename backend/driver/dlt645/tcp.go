// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"fmt"
	"net"
	"sync"
	"time"

	"iot-gateway/logger"
)

// tcpDrainDeadline Drain 时设置的读截止时间：仅丢弃已在缓冲中的数据。
const tcpDrainDeadline = time.Millisecond

// tcpDrainRounds Drain 最多读取的轮数。
const tcpDrainRounds = 8

// tcpClient 以太网传输（串口服务器 / DTU 透传模式）。
//
// 与串口的关键差异：TCP 连接被采集引擎视为线程安全（非独占），
// 因此这里必须用互斥锁包住整次请求-应答交互，避免回显与迟到应答互相错配。
type tcpClient struct {
	mu        sync.Mutex
	conn      net.Conn
	connected bool
}

// newTCPClient 创建并连接 TCP 传输。
func newTCPClient(cfg *DLT645Config) (dlt645Transport, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("dlt645 tcp: host 未配置")
	}
	if cfg.Port <= 0 {
		return nil, fmt.Errorf("dlt645 tcp: port 未配置")
	}
	address := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = time.Duration(defaultTimeoutMS) * time.Millisecond
	}
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return nil, fmt.Errorf("dlt645 tcp: 连接 %s 失败: %w", address, err)
	}
	// 关闭 Nagle：645 报文短小且要求应答及时，攒包只会增加往返延迟。
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}

	logger.Info("dlt645 tcp 已连接 %s", address)
	return &tcpClient{conn: conn, connected: true}, nil
}

func (c *tcpClient) Lock()   { c.mu.Lock() }
func (c *tcpClient) Unlock() { c.mu.Unlock() }

func (c *tcpClient) Write(p []byte) (int, error) {
	if c.conn == nil || !c.connected {
		return 0, fmt.Errorf("dlt645 tcp: 连接已关闭")
	}
	n, err := c.conn.Write(p)
	if err != nil {
		c.connected = false
		return n, fmt.Errorf("dlt645 tcp: 写入失败: %w", err)
	}
	return n, nil
}

func (c *tcpClient) Read(p []byte) (int, error) {
	if c.conn == nil || !c.connected {
		return 0, fmt.Errorf("dlt645 tcp: 连接已关闭")
	}
	n, err := c.conn.Read(p)
	if err != nil && !isTimeoutErr(err) {
		c.connected = false
	}
	return n, err
}

func (c *tcpClient) SetReadDeadline(t time.Time) error {
	if c.conn == nil {
		return fmt.Errorf("dlt645 tcp: 连接已关闭")
	}
	return c.conn.SetReadDeadline(t)
}

// Drain 丢弃接收缓冲中的残留字节（上一轮迟到的应答）。
//
// 先问一次「缓冲里有没有待读字节」再决定要不要进入下面的读循环：
// 干净链路的常态是缓冲为空，此时那次读只能空等到截止时间才返回超时——
// 每帧白等 1ms，而 645 的帧数随点位数线性增长（帧数 = ⌈点数/maxDIsPerRead⌉），
// 这一毫秒会直接变成单设备轮询周期的下限。探测是纯查询、不下读，
// 语义与原来完全一致：**已到达的**残留字节照样被清掉，只是不再为「空缓冲」付等待。
//
// 探测失败（拿不到裸 fd）时退化为原来的定时读，正确性不受影响。
func (c *tcpClient) Drain() {
	if c.conn == nil || !c.connected {
		return
	}
	if tcp, ok := c.conn.(*net.TCPConn); ok {
		if n, ok := sockPendingBytes(tcp); ok && n == 0 {
			return
		}
	}

	_ = c.conn.SetReadDeadline(time.Now().Add(tcpDrainDeadline))
	buf := make([]byte, 256)
	for i := 0; i < tcpDrainRounds; i++ {
		n, err := c.conn.Read(buf)
		if err != nil || n == 0 {
			break
		}
		logger.Debug("dlt645 tcp: 丢弃残留字节 raw=% X", buf[:n])
	}
	_ = c.conn.SetReadDeadline(time.Time{})
}

// MarkDirty TCP 侧为空操作。
//
// 串口要靠这个标记才敢跳过 Drain（它查不了缓冲），而 TCP 有 sockPendingBytes
// 可**直接**查出残留，不依赖「上一轮是否干净收尾」的推断，故无需记账。
func (c *tcpClient) MarkDirty() {}

func (c *tcpClient) IsConnected() bool {
	return c != nil && c.connected
}

func (c *tcpClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.connected = false
	c.conn = nil
	logger.Info("dlt645 tcp 连接已关闭")
	return err
}
