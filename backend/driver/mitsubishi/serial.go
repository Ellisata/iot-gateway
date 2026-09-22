// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goburrow/serial"

	"iot-gateway/driver"
	"iot-gateway/logger"
)

// mcSerialClient 4C 帧 Format5（二进制）串口传输层客户端。
//
// 串口为独占串行资源：单次请求+响应内部持互斥锁，避免并发请求（采集轮询与
// 连接测试同时触发）在串口上交错、破坏帧结构。读超时/写失败标记断连，
// 由采集引擎按指数退避重连。
type mcSerialClient struct {
	mu      sync.Mutex
	port    serial.Port
	timeout time.Duration
	// connected 用原子量而非普通字段：IsConnected 会被驱动之外的调用方读到
	// （driver.PingDevice 复用采集引擎的连接时要先问一句连没连上），
	// 而写它的是采集协程里的 Read。用 c.mu 保护不行——Close 也不持 c.mu。
	connected atomic.Bool
}

// newMCSerialClient 创建并打开 4C Format5 串口连接。
func newMCSerialClient(cfg *MCConfig) (mcTransport, error) {
	if cfg.ComPort == "" {
		return nil, fmt.Errorf("mc serial: comPort is required")
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
		// 打开失败最常见的原因是端口已被本进程内另一个采集任务占着（Windows 下串口独占），
		// 而系统原文「Access is denied」对用户毫无指向性——补一句「被谁占用」。
		return nil, fmt.Errorf("mc serial: open %s failed: %w%s",
			cfg.ComPort, err, driver.SerialBusyHint(cfg.ComPort))
	}

	logger.Info("mc serial client connected to %s (baud=%d, data=%d, stop=%d, parity=%s)",
		cfg.ComPort, baud, dataBits, stopBits, parity)
	client := &mcSerialClient{port: port, timeout: timeout}
	client.connected.Store(true)
	return client, nil
}

// Read 通过 4C Format5 帧读取设备数据。
func (c *mcSerialClient) Read(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.port == nil || !c.connected.Load() {
		return nil, fmt.Errorf("mc serial: not connected")
	}

	frame := buildSerialFrame(device, head, points, bitMode)
	logger.Debug("mc serial send raw=% X", frame)
	if _, err := c.port.Write(frame); err != nil {
		c.connected.Store(false)
		return nil, fmt.Errorf("mc serial: write failed: %w", err)
	}

	resp, err := c.readFrame()
	if err != nil {
		c.connected.Store(false)
		return nil, fmt.Errorf("mc serial: read failed: %w", err)
	}

	data, err := parseSerialResponse(resp)
	if err != nil {
		return nil, err // 结束码/和校验错误：连接是通的，不标记断连
	}
	return data, nil
}

// readFrame 读取一个完整 4C Format5 响应帧（解填充后的帧体，含和校验字节）。
//
// 以 DLE 状态机逐字节解析：帧头 DLE STX 起始，帧体 DLE 字节被双写（0x10 0x10 表示
// 字面 0x10），帧尾为 DLE ETX（0x10 0x03）。因字面 0x10 恒被双写，帧尾 DLE ETX
// 不会与数据混淆。串口读超时由 serial.Config.Timeout 控制（返回 serial.ErrTimeout）。
func (c *mcSerialClient) readFrame() ([]byte, error) {
	buf := make([]byte, 0, 64)
	tmp := make([]byte, 1)
	var started, prevDLE bool

	for {
		n, err := c.port.Read(tmp)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		b := tmp[0]

		if !started {
			// 等待帧头 DLE STX
			if b == dleByte {
				prevDLE = true
			} else if prevDLE && b == stxByte {
				started = true
				prevDLE = false
			} else {
				prevDLE = false
			}
			continue
		}

		// 帧体内
		if prevDLE {
			if b == etxByte {
				return buf, nil // DLE ETX 帧尾
			}
			// 字面 0x10 对（含 0x10 0x10 双写），还原为单字节 0x10
			buf = append(buf, b)
			prevDLE = false
			continue
		}
		if b == dleByte {
			prevDLE = true
			continue
		}
		buf = append(buf, b)
		if len(buf) > maxSerialFrameLen {
			return nil, fmt.Errorf("mc serial: response too long (%d bytes)", len(buf))
		}
	}
}

// IsConnected 返回连接状态
func (c *mcSerialClient) IsConnected() bool {
	return c != nil && c.connected.Load()
}

// Close 关闭串口连接
func (c *mcSerialClient) Close() error {
	if c == nil || c.port == nil {
		return nil
	}
	err := c.port.Close()
	c.connected.Store(false)
	logger.Info("mc serial client closed")
	return err
}
