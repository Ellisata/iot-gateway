package modbus

import (
	"fmt"
	"net"
	"sync"
	"time"

	goburrowModbus "github.com/goburrow/modbus"

	"iot-gateway/logger"
)

// ModbusClient Modbus TCP 客户端封装
type ModbusClient struct {
	mu        sync.RWMutex
	ip        string
	port      int
	timeout   time.Duration
	handler   *goburrowModbus.TCPClientHandler
	client    goburrowModbus.Client
	connected bool
	unitID    byte // 最近一次设置的从站 ID，用于跳过冗余写
}

// NewModbusTCPClient 创建并建立 Modbus TCP 连接
func NewModbusTCPClient(ip string, port int, timeout time.Duration) (*ModbusClient, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	handler := goburrowModbus.NewTCPClientHandler(fmt.Sprintf("%s:%d", ip, port))
	handler.Timeout = timeout
	handler.IdleTimeout = timeout * 2

	// 测试 TCP 连接（确保目标可达）
	addr := net.JoinHostPort(ip, fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("modbus: connect to %s:%d failed: %w", ip, port, err)
	}
	conn.Close()

	// 连接 handler
	if err := handler.Connect(); err != nil {
		return nil, fmt.Errorf("modbus: handler connect to %s:%d failed: %w", ip, port, err)
	}

	client := goburrowModbus.NewClient(handler)

	mc := &ModbusClient{
		ip:        ip,
		port:      port,
		timeout:   timeout,
		handler:   handler,
		client:    client,
		connected: true,
	}

	logger.Info("modbus client connected to %s:%d", ip, port)
	return mc, nil
}

// SetUnitID 设置从站 ID（网关后面挂多个从站时使用）。
// 值未变化时跳过写 handler.SlaveId，避免与在途请求 Encode 读 SlaveId 产生竞态。
func (c *ModbusClient) SetUnitID(unitID byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unitID == unitID {
		return
	}
	c.handler.SlaveId = unitID
	c.unitID = unitID
}

// ReadHoldingRegisters 读取保持寄存器 (功能码 03)
func (c *ModbusClient) ReadHoldingRegisters(address, quantity uint16) ([]byte, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	results, err := client.ReadHoldingRegisters(address, quantity)
	if err != nil {
		// 协议异常（设备响应但拒绝请求）说明连接是通的，不判定断开
		if !isProtocolErr(err) {
			c.markDisconnected()
		}
		return nil, fmt.Errorf("modbus: read holding registers at %d len %d failed: %w", address, quantity, err)
	}
	return results, nil
}

// ReadInputRegisters 读取输入寄存器 (功能码 04)
func (c *ModbusClient) ReadInputRegisters(address, quantity uint16) ([]byte, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	results, err := client.ReadInputRegisters(address, quantity)
	if err != nil {
		// 协议异常（设备响应但拒绝请求）说明连接是通的，不判定断开
		if !isProtocolErr(err) {
			c.markDisconnected()
		}
		return nil, fmt.Errorf("modbus: read input registers at %d len %d failed: %w", address, quantity, err)
	}
	return results, nil
}

// ReadCoils 读取线圈 (功能码 01)
func (c *ModbusClient) ReadCoils(address, quantity uint16) ([]byte, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	results, err := client.ReadCoils(address, quantity)
	if err != nil {
		// 协议异常（设备响应但拒绝请求）说明连接是通的，不判定断开
		if !isProtocolErr(err) {
			c.markDisconnected()
		}
		return nil, fmt.Errorf("modbus: read coils at %d len %d failed: %w", address, quantity, err)
	}
	return results, nil
}

// ReadDiscreteInputs 读取离散输入 (功能码 02)
func (c *ModbusClient) ReadDiscreteInputs(address, quantity uint16) ([]byte, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	results, err := client.ReadDiscreteInputs(address, quantity)
	if err != nil {
		// 协议异常（设备响应但拒绝请求）说明连接是通的，不判定断开
		if !isProtocolErr(err) {
			c.markDisconnected()
		}
		return nil, fmt.Errorf("modbus: read discrete inputs at %d len %d failed: %w", address, quantity, err)
	}
	return results, nil
}

// WriteSingleCoil 写单个线圈 (功能码 05)
func (c *ModbusClient) WriteSingleCoil(address uint16, value bool) error {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	var v uint16
	if value {
		v = 0xFF00
	}
	_, err := client.WriteSingleCoil(address, v)
	if err != nil {
		c.markDisconnected()
		return fmt.Errorf("modbus: write single coil at %d failed: %w", address, err)
	}
	return nil
}

// WriteSingleRegister 写单个寄存器 (功能码 06)
func (c *ModbusClient) WriteSingleRegister(address, value uint16) error {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	_, err := client.WriteSingleRegister(address, value)
	if err != nil {
		c.markDisconnected()
		return fmt.Errorf("modbus: write single register at %d failed: %w", address, err)
	}
	return nil
}

// IsConnected 返回当前连接状态
func (c *ModbusClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Reconnect 关闭旧连接并重新建立连接
func (c *ModbusClient) Reconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 关闭旧连接
	if c.handler != nil {
		c.handler.Close()
	}

	// 创建新 handler
	handler := goburrowModbus.NewTCPClientHandler(fmt.Sprintf("%s:%d", c.ip, c.port))
	handler.Timeout = c.timeout
	handler.IdleTimeout = c.timeout * 2

	if err := handler.Connect(); err != nil {
		c.connected = false
		return fmt.Errorf("modbus: reconnect to %s:%d failed: %w", c.ip, c.port, err)
	}

	c.handler = handler
	c.client = goburrowModbus.NewClient(handler)
	c.connected = true

	logger.Info("modbus client reconnected to %s:%d", c.ip, c.port)
	return nil
}

// Close 关闭 Modbus 连接
func (c *ModbusClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.handler != nil {
		c.handler.Close()
	}
	c.connected = false
	logger.Info("modbus client closed: %s:%d", c.ip, c.port)
	return nil
}

// markDisconnected 标记连接为断开状态
func (c *ModbusClient) markDisconnected() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = false
}
