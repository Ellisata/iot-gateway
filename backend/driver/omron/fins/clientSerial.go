// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"fmt"
	"sync"
	"time"

	"github.com/goburrow/serial"

	"iot-gateway/logger"
)

// finsSerialClient Host Link（FINS over serial）传输层客户端。
//
// 串口为独占串行资源：单次请求+响应内部持互斥锁，避免并发请求（采集轮询与
// 连接测试同时触发）在串口上交错、破坏帧结构。读超时/写失败标记断连，
// 由采集引擎按指数退避重连。
type finsSerialClient struct {
	mu        sync.Mutex
	port      serial.Port
	timeout   time.Duration
	unitNo    byte
	dstUnit   byte
	srcUnit   byte
	sid       byte
	fcsMode   string
	connected bool
}

// newFINSSerialClient 创建并打开 Host Link 串口连接。
func newFINSSerialClient(cfg *FINSConfig) (finsTransport, error) {
	if cfg.ComPort == "" {
		return nil, fmt.Errorf("fins serial: comPort is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	baud := cfg.BaudRate
	if baud <= 0 {
		baud = 9600
	}
	dataBits := cfg.DataBits
	if dataBits == 0 {
		dataBits = 8
	}
	stopBits := cfg.StopBits
	if stopBits == 0 {
		stopBits = 1
	}
	parity := cfg.Parity
	if parity == "" {
		parity = "N"
	}

	port, err := serial.Open(&serial.Config{
		Address:  cfg.ComPort,
		BaudRate: baud,
		DataBits: dataBits,
		StopBits: stopBits,
		Parity:   parity,
		Timeout:  timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("fins serial: open %s failed: %w", cfg.ComPort, err)
	}

	logger.Info("fins serial client connected to %s (baud=%d, data=%d, stop=%d, parity=%s, unit=%d)",
		cfg.ComPort, baud, dataBits, stopBits, parity, cfg.UnitNo)
	return &finsSerialClient{
		port:      port,
		timeout:   timeout,
		unitNo:    cfg.UnitNo,
		dstUnit:   cfg.DstUnit,
		srcUnit:   cfg.SrcUnit,
		fcsMode:   cfg.FCSMode,
		connected: true,
	}, nil
}

// Read 通过 Host Link 帧读取内存区。
func (c *finsSerialClient) Read(area finsArea, word, count uint16) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.port == nil || !c.connected {
		return nil, fmt.Errorf("fins serial: not connected")
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

	logger.Debug("fins serial send raw=%q", string(frame))
	if _, err := c.port.Write(frame); err != nil {
		c.connected = false
		return nil, fmt.Errorf("fins serial: write failed: %w", err)
	}

	resp, err := c.readFrame()
	if err != nil {
		c.connected = false
		return nil, fmt.Errorf("fins serial: read failed: %w", err)
	}

	data, err := parseSerialResponse(resp, sid, c.fcsMode)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// readFrame 读取一个完整 Host Link 响应帧（读到 CR 终止符）。
// 串口读超时由 serial.Config.Timeout 控制（返回 serial.ErrTimeout）。
func (c *finsSerialClient) readFrame() ([]byte, error) {
	buf := make([]byte, 0, 128)
	tmp := make([]byte, 1)
	for {
		n, err := c.port.Read(tmp)
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
			return nil, fmt.Errorf("fins serial: response too long (%d bytes)", len(buf))
		}
	}
}

// IsConnected 返回连接状态
func (c *finsSerialClient) IsConnected() bool {
	return c != nil && c.connected
}

// Close 关闭串口连接
func (c *finsSerialClient) Close() error {
	if c == nil || c.port == nil {
		return nil
	}
	err := c.port.Close()
	c.connected = false
	logger.Info("fins serial client closed")
	return err
}
