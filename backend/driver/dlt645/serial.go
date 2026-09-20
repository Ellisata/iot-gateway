// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"fmt"
	"sync"
	"time"

	"github.com/goburrow/serial"

	"iot-gateway/logger"
)

// serialReadTimeout 串口单次读的超时。
//
// 刻意取小值：帧的截断由 codec.go 的截止时间循环统一裁决，串口这里只需
// 保证「有字节就尽快返回、无字节就尽快回来复核截止时间」。
// 取大值会让每次请求前的 Drain（清理迟到应答）白等一个超时周期，
// 在 2400bps 下会显著拖慢轮询。
const serialReadTimeout = 20 * time.Millisecond

// serialDrainRounds Drain 最多读取的轮数，避免链路上持续有噪声时死循环。
const serialDrainRounds = 8

// serialClient RS-485 / RS-232 串口传输。
type serialClient struct {
	mu        sync.Mutex
	port      serial.Port
	connected bool
}

// newSerialClient 创建并打开串口连接。
func newSerialClient(cfg *DLT645Config) (dlt645Transport, error) {
	if cfg.ComPort == "" {
		return nil, fmt.Errorf("dlt645 serial: comPort 未配置")
	}
	baud := cfg.BaudRate
	if baud <= 0 {
		baud = defaultBaudRate
	}
	dataBits := cfg.DataBits
	if dataBits == 0 {
		dataBits = defaultDataBits
	}
	stopBits := cfg.StopBits
	if stopBits == 0 {
		stopBits = defaultStopBits
	}
	parity := cfg.Parity
	if parity == "" {
		parity = defaultParity
	}

	port, err := serial.Open(&serial.Config{
		Address:  cfg.ComPort,
		BaudRate: baud,
		DataBits: dataBits,
		StopBits: stopBits,
		Parity:   parity,
		Timeout:  serialReadTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("dlt645 serial: 打开 %s 失败: %w", cfg.ComPort, err)
	}

	logger.Info("dlt645 serial 已连接 %s（baud=%d, data=%d, stop=%d, parity=%s）",
		cfg.ComPort, baud, dataBits, stopBits, parity)
	return &serialClient{port: port, connected: true}, nil
}

func (c *serialClient) Lock()   { c.mu.Lock() }
func (c *serialClient) Unlock() { c.mu.Unlock() }

func (c *serialClient) Write(p []byte) (int, error) {
	if c.port == nil || !c.connected {
		return 0, fmt.Errorf("dlt645 serial: 连接已关闭")
	}
	n, err := c.port.Write(p)
	if err != nil {
		c.connected = false
		return n, fmt.Errorf("dlt645 serial: 写入失败: %w", err)
	}
	return n, nil
}

func (c *serialClient) Read(p []byte) (int, error) {
	if c.port == nil || !c.connected {
		return 0, fmt.Errorf("dlt645 serial: 连接已关闭")
	}
	n, err := c.port.Read(p)
	if err != nil && !isTimeoutErr(err) {
		c.connected = false
	}
	return n, err
}

// SetReadDeadline 串口不支持可变的读截止时间（go burrow/serial 的超时在打开时固定），
// 由 serialReadTimeout 的短超时配合 codec 的截止时间循环共同实现等效语义。
func (c *serialClient) SetReadDeadline(time.Time) error { return nil }

// Drain 丢弃接收缓冲中的残留字节，最多读取 serialDrainRounds 轮。
func (c *serialClient) Drain() {
	if c.port == nil || !c.connected {
		return
	}
	buf := make([]byte, 256)
	for i := 0; i < serialDrainRounds; i++ {
		n, err := c.port.Read(buf)
		if err != nil || n == 0 {
			return
		}
		logger.Debug("dlt645 serial: 丢弃残留字节 raw=% X", buf[:n])
	}
}

func (c *serialClient) IsConnected() bool {
	return c != nil && c.connected
}

func (c *serialClient) Close() error {
	if c == nil || c.port == nil {
		return nil
	}
	err := c.port.Close()
	c.connected = false
	c.port = nil
	logger.Info("dlt645 serial 连接已关闭")
	return err
}
