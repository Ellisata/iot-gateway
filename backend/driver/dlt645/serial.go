// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goburrow/serial"

	"iot-gateway/driver"
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
	mu   sync.Mutex
	port serial.Port
	// connected 用原子量而非普通字段：IsConnected 会被驱动之外的调用方读到
	// （driver.PingDevice 复用采集引擎的连接时要先问一句连没连上），
	// 而写它的是采集协程里的 Read。用 c.mu 保护不行——Close 也不持 c.mu，
	// 且 IsConnected 不该排在一次在途读后面空等一个读超时。
	connected atomic.Bool
	// dirty 上一轮交互没有干净收尾（写失败 / 读超时 / 读到噪声帧）。
	// 串口拿不到裸 fd、查不了接收缓冲，只能靠这个标记决定 Drain 要不要真去读。
	// 仅在持有 mu 的交互期间读写（exchange 全程持锁）。
	dirty bool
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
		// 打开失败最常见的原因是端口已被本进程内另一个采集任务占着（Windows 下串口独占），
		// 而系统原文「Access is denied」对用户毫无指向性——补一句「被谁占用」。
		return nil, fmt.Errorf("dlt645 serial: 打开 %s 失败: %w%s",
			cfg.ComPort, err, driver.SerialBusyHint(cfg.ComPort))
	}

	logger.Info("dlt645 serial 已连接 %s（baud=%d, data=%d, stop=%d, parity=%s）",
		cfg.ComPort, baud, dataBits, stopBits, parity)
	client := &serialClient{port: port}
	client.connected.Store(true)
	return client, nil
}

func (c *serialClient) Lock()   { c.mu.Lock() }
func (c *serialClient) Unlock() { c.mu.Unlock() }

func (c *serialClient) Write(p []byte) (int, error) {
	if c.port == nil || !c.connected.Load() {
		return 0, fmt.Errorf("dlt645 serial: 连接已关闭")
	}
	n, err := c.port.Write(p)
	if err != nil {
		c.connected.Store(false)
		return n, fmt.Errorf("dlt645 serial: 写入失败: %w", err)
	}
	return n, nil
}

func (c *serialClient) Read(p []byte) (int, error) {
	if c.port == nil || !c.connected.Load() {
		return 0, fmt.Errorf("dlt645 serial: 连接已关闭")
	}
	n, err := c.port.Read(p)
	if err != nil && !isTimeoutErr(err) {
		c.connected.Store(false)
	}
	return n, err
}

// SetReadDeadline 串口不支持可变的读截止时间（go burrow/serial 的超时在打开时固定），
// 由 serialReadTimeout 的短超时配合 codec 的截止时间循环共同实现等效语义。
func (c *serialClient) SetReadDeadline(time.Time) error { return nil }

// Drain 丢弃接收缓冲中的残留字节，最多读取 serialDrainRounds 轮。
//
// 串口读用的是 goburrow/serial 的 select + 打开时固定的超时，
// 缓冲为空时**每次读都要空等到 serialReadTimeout（20ms）**才返回 ErrTimeout。
// 而 serial.Port 只有 io.ReadWriteCloser、拿不到裸 fd，做不了 TCP 那样的
// 待读字节探测，因此改用 dirty 标记：上一轮干净收尾就整段跳过。
//
// 这不削弱「值慢一拍」的防护——迟到的应答只可能在某轮没按时收到应答时才在途，
// 而那种轮次恰恰会把 dirty 置上。参考：同仓库的 ModBus.RTU（goburrow/modbus）
// 从不 pre-drain，只按预期长度读 + 校验 CRC，本策略比它更保守。
func (c *serialClient) Drain() {
	if c.port == nil || !c.connected.Load() {
		return
	}
	if !c.dirty {
		return
	}
	c.dirty = false

	buf := make([]byte, 256)
	for i := 0; i < serialDrainRounds; i++ {
		n, err := c.port.Read(buf)
		if err != nil || n == 0 {
			return
		}
		logger.Debug("dlt645 serial: 丢弃残留字节 raw=% X", buf[:n])
	}
}

// MarkDirty 见 transport.go 的接口说明。
func (c *serialClient) MarkDirty() { c.dirty = true }

func (c *serialClient) IsConnected() bool {
	return c != nil && c.connected.Load()
}

func (c *serialClient) Close() error {
	if c == nil || c.port == nil {
		return nil
	}
	err := c.port.Close()
	c.connected.Store(false)
	c.port = nil
	logger.Info("dlt645 serial 连接已关闭")
	return err
}
