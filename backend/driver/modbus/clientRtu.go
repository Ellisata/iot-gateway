package modbus

import (
	"fmt"
	"sync"
	"time"

	goburrowModbus "github.com/goburrow/modbus"

	"iot-gateway/logger"
)

// ModbusRTUClient Modbus RTU 串口客户端封装
//
// 通过 RS-232 / RS-485 串口与 Modbus RTU 设备通信。
// 使用 github.com/goburrow/modbus 库的 RTUClientHandler 实现。
type ModbusRTUClient struct {
	mu        sync.RWMutex
	comPort   string
	baudRate  int
	dataBits  int
	stopBits  int
	parity    string
	slaveID   byte
	timeout   time.Duration
	handler   *goburrowModbus.RTUClientHandler
	client    goburrowModbus.Client
	connected bool
}

// NewModbusRTUClient 创建并建立 Modbus RTU 串口连接
//
// 参数说明：
//   - comPort: 串口名称，Windows 下为 "COM1"、"COM2" 等，
//     Linux 下为 "/dev/ttyUSB0"、"/dev/ttyS0" 等
//   - baudRate: 波特率，常见值 9600、19200、38400、57600、115200
//   - dataBits: 数据位，通常为 8
//   - stopBits: 停止位，1 或 2
//   - parity: 校验位，"N"=无、"E"=偶、"O"=奇
//   - slaveID: 从站地址（1-247）
//   - timeout: 通信超时
func NewModbusRTUClient(comPort string, baudRate, dataBits, stopBits int,
	parity string, slaveID byte, timeout time.Duration) (*ModbusRTUClient, error) {

	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	handler := goburrowModbus.NewRTUClientHandler(comPort)
	handler.BaudRate = baudRate
	handler.DataBits = dataBits
	handler.StopBits = stopBits
	handler.Parity = parity
	handler.SlaveId = slaveID
	handler.Timeout = timeout
	handler.IdleTimeout = timeout * 2

	if err := handler.Connect(); err != nil {
		return nil, fmt.Errorf("modbus rtu: connect to %s failed: %w", comPort, err)
	}

	client := goburrowModbus.NewClient(handler)

	mc := &ModbusRTUClient{
		comPort:   comPort,
		baudRate:  baudRate,
		dataBits:  dataBits,
		stopBits:  stopBits,
		parity:    parity,
		slaveID:   slaveID,
		timeout:   timeout,
		handler:   handler,
		client:    client,
		connected: true,
	}

	logger.Info("modbus rtu client connected to %s (baud=%d, data=%d, stop=%d, parity=%s, slave=%d)",
		comPort, baudRate, dataBits, stopBits, parity, slaveID)
	return mc, nil
}

// SetUnitID 设置从站 ID（RS-485 总线挂多个从站时切换）
func (c *ModbusRTUClient) SetUnitID(unitID byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handler.SlaveId = unitID
	c.slaveID = unitID
}

// ReadHoldingRegisters 读取保持寄存器（功能码 03）
func (c *ModbusRTUClient) ReadHoldingRegisters(address, quantity uint16) ([]byte, error) {
	// 串口是独占的，整个收发过程必须持写锁，避免并发请求（如采集轮询与
	// 连接测试同时触发）在串口上交错，破坏 Modbus RTU 帧结构。
	c.mu.Lock()
	defer c.mu.Unlock()

	results, err := c.client.ReadHoldingRegisters(address, quantity)
	if err != nil {
		// 协议异常（设备响应但拒绝请求）说明串口/设备是通的，不判定断开
		if !isProtocolErr(err) {
			c.connected = false
		}
		return nil, fmt.Errorf("modbus rtu: read holding registers at %d len %d failed: %w",
			address, quantity, err)
	}
	return results, nil
}

// ReadInputRegisters 读取输入寄存器（功能码 04）
func (c *ModbusRTUClient) ReadInputRegisters(address, quantity uint16) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	results, err := c.client.ReadInputRegisters(address, quantity)
	if err != nil {
		// 协议异常（设备响应但拒绝请求）说明串口/设备是通的，不判定断开
		if !isProtocolErr(err) {
			c.connected = false
		}
		return nil, fmt.Errorf("modbus rtu: read input registers at %d len %d failed: %w",
			address, quantity, err)
	}
	return results, nil
}

// ReadCoils 读取线圈（功能码 01）
func (c *ModbusRTUClient) ReadCoils(address, quantity uint16) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	results, err := c.client.ReadCoils(address, quantity)
	if err != nil {
		// 协议异常（设备响应但拒绝请求）说明串口/设备是通的，不判定断开
		if !isProtocolErr(err) {
			c.connected = false
		}
		return nil, fmt.Errorf("modbus rtu: read coils at %d len %d failed: %w",
			address, quantity, err)
	}
	return results, nil
}

// ReadDiscreteInputs 读取离散输入（功能码 02）
func (c *ModbusRTUClient) ReadDiscreteInputs(address, quantity uint16) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	results, err := c.client.ReadDiscreteInputs(address, quantity)
	if err != nil {
		// 协议异常（设备响应但拒绝请求）说明串口/设备是通的，不判定断开
		if !isProtocolErr(err) {
			c.connected = false
		}
		return nil, fmt.Errorf("modbus rtu: read discrete inputs at %d len %d failed: %w",
			address, quantity, err)
	}
	return results, nil
}

// Ping 通过当前已建立的串口连接发送一次读取请求验证设备可达。
// 与常规读取不同，失败时不会将连接标记为断开，避免对采集引擎的既有状态产生副作用。
func (c *ModbusRTUClient) Ping() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return fmt.Errorf("modbus rtu: serial connection is closed")
	}
	if _, err := c.client.ReadHoldingRegisters(0, 1); err != nil {
		return fmt.Errorf("modbus rtu: ping read failed: %w", err)
	}
	return nil
}

// IsConnected 返回当前连接状态
func (c *ModbusRTUClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Reconnect 关闭旧连接并重新建立串口连接
func (c *ModbusRTUClient) Reconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 关闭旧连接
	if c.handler != nil {
		c.handler.Close()
	}

	// 创建新 handler
	handler := goburrowModbus.NewRTUClientHandler(c.comPort)
	handler.BaudRate = c.baudRate
	handler.DataBits = c.dataBits
	handler.StopBits = c.stopBits
	handler.Parity = c.parity
	handler.SlaveId = c.slaveID
	handler.Timeout = c.timeout
	handler.IdleTimeout = c.timeout * 2

	if err := handler.Connect(); err != nil {
		c.connected = false
		return fmt.Errorf("modbus rtu: reconnect to %s failed: %w", c.comPort, err)
	}

	c.handler = handler
	c.client = goburrowModbus.NewClient(handler)
	c.connected = true

	logger.Info("modbus rtu client reconnected to %s", c.comPort)
	return nil
}

// Close 关闭 Modbus RTU 串口连接
func (c *ModbusRTUClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.handler != nil {
		c.handler.Close()
	}
	c.connected = false
	logger.Info("modbus rtu client closed: %s", c.comPort)
	return nil
}
