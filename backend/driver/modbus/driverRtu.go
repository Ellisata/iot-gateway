// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbus

import (
	"fmt"
	"sort"
	"sync"
	"time"

	goburrowModbus "github.com/goburrow/modbus"

	"iot-gateway/driver"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

func init() {
	driver.Register("ModBus.RTU", func() driver.Driver {
		return &modbusRTUDriver{}
	})
}

// modbusRTUDriver Modbus RTU 协议驱动，实现 driver.Driver 接口
//
// 通过 RS-232 / RS-485 串口连接 Modbus RTU 设备，
// 与 Modbus TCP 共享数据解析、地址范围计算等协议层逻辑。
type modbusRTUDriver struct {
	mu     sync.RWMutex
	config *ModbusRTUConfig
	client *ModbusRTUClient

	// plans 批次区间规划缓存：规避每轮重复解析地址、按功能码分组与计算区间。
	// 规划是 (addrs, cfg) 的纯函数，轮询间批次内容不变即可命中；
	// 驱动实例在配置热刷新时由采集引擎重建，Connect 时也显式清空，缓存随之失效。
	plans planCache
}

// SerialExclusive 标记 RTU 驱动独占串口总线。
// 采集引擎需对独占驱动整轮持锁（重连+全部批次读取+转换+推送），
// 防止不同频率分组的轮询在串口上交错。
func (d *modbusRTUDriver) SerialExclusive() bool { return true }

func (d *modbusRTUDriver) Connect(protocolJSON string) error {
	cfg, err := ParseModbusRTUConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("modbus rtu: parse config failed: %w", err)
	}

	if cfg.ComPort == "" {
		return fmt.Errorf("modbus rtu: comPort is required")
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// 任何重连尝试都使规划缓存失效：配置可能已变化，新连接必须使用重新计算的规划
	d.plans.clear()

	// 先关闭旧连接、释放串口，再建立新连接。
	// 否则读超时后旧 handler 仍占用端口，新连接会报 Access is denied。
	d.mu.Lock()
	if d.client != nil {
		d.client.Close()
		d.client = nil
	}
	d.mu.Unlock()

	client, err := NewModbusRTUClient(cfg.ComPort, cfg.BaudRate, cfg.DataBits,
		cfg.StopBits, cfg.Parity, cfg.UnitID, timeout)
	if err != nil {
		return fmt.Errorf("modbus rtu: connect %s failed: %w", cfg.ComPort, err)
	}

	d.mu.Lock()
	d.config = cfg
	d.client = client
	d.mu.Unlock()

	return nil
}

func (d *modbusRTUDriver) Ping(protocolJSON string) error {
	cfg, err := ParseModbusRTUConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("modbus rtu: ping parse config failed: %w", err)
	}

	if cfg.ComPort == "" {
		return fmt.Errorf("modbus rtu: comPort is required for ping")
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// 若当前驱动实例已持有该串口的连接（如采集引擎运行中已 Connect），
	// 直接复用现有连接做 Modbus 握手，避免对同一串口再开一个句柄——
	// Windows 下串口被独占时二次打开会报 Access is denied。
	d.mu.RLock()
	liveClient := d.client
	d.mu.RUnlock()
	if liveClient != nil && liveClient.IsConnected() && liveClient.comPort == cfg.ComPort {
		if err := liveClient.Ping(); err != nil {
			return fmt.Errorf("modbus rtu: ping %s failed (device no response): %w",
				cfg.ComPort, err)
		}
		logger.Info("modbus rtu ping success: %s (baud=%d, slave=%d, reused connection)",
			cfg.ComPort, cfg.BaudRate, cfg.UnitID)
		return nil
	}

	// Step 1: 串口连通性检查（打开串口）
	handler := goburrowModbus.NewRTUClientHandler(cfg.ComPort)
	handler.BaudRate = cfg.BaudRate
	handler.DataBits = cfg.DataBits
	handler.StopBits = cfg.StopBits
	handler.Parity = cfg.Parity
	handler.SlaveId = cfg.UnitID
	handler.Timeout = timeout
	handler.IdleTimeout = timeout * 2

	if err := handler.Connect(); err != nil {
		return fmt.Errorf("modbus rtu: ping %s failed (serial): %w", cfg.ComPort, err)
	}
	defer handler.Close()

	// Step 2: Modbus 协议层握手（发送读取请求，验证设备可达）
	client := goburrowModbus.NewClient(handler)
	if _, err := client.ReadHoldingRegisters(0, 1); err != nil {
		return fmt.Errorf("modbus rtu: ping %s failed (device no response): %w",
			cfg.ComPort, err)
	}

	logger.Info("modbus rtu ping success: %s (baud=%d, slave=%d)",
		cfg.ComPort, cfg.BaudRate, cfg.UnitID)
	return nil
}

func (d *modbusRTUDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	d.mu.RLock()
	client := d.client
	cfg := d.config
	d.mu.RUnlock()

	if client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("modbus rtu: not connected")
	}

	// 构建共享配置视图，复用 TCP 驱动的地址范围计算和解析逻辑
	sharedCfg := &ModbusTcpConfig{
		FunctionCode: cfg.FunctionCode,
		StartAddress: cfg.StartAddress,
		Quantity:     cfg.Quantity,
		ByteOrder:    cfg.ByteOrder,
		WordOrder:    cfg.WordOrder,
		UnitID:       cfg.UnitID,
		MergeWindow:  cfg.MergeWindow,
		StringLen:    cfg.StringLen,
	}

	client.SetUnitID(cfg.UnitID)

	// 按功能码分组 + 每组区间计算一次性规划并缓存（内容指纹命中则跳过重复解析）。
	// 线圈/保持寄存器等是不同的地址空间，同一设备混合点位时分组各自读取，而非整组报错。
	plan, err := d.plans.planFor(driver.RangeSig(addrs), addrs, sharedCfg)
	if err != nil {
		return nil, fmt.Errorf("modbus rtu: calc address ranges failed: %w", err)
	}

	fcs := make([]byte, 0, len(plan.groups))
	for fc := range plan.groups {
		fcs = append(fcs, fc)
	}
	sort.Slice(fcs, func(i, j int) bool { return fcs[i] < fcs[j] })

	// 按 FC 逐组读取：每组独立算范围/读/解析，汇总后按原始点位顺序重组。
	byID := make(map[string]driver.ReadResult, len(addrs))
	for _, fc := range fcs {
		gAddrs := plan.groups[fc]
		ranges := plan.ranges[fc]

		var groupResults []driver.ReadResult
		switch fc {
		case FuncCodeReadHoldingRegisters, FuncCodeReadInputRegisters:
			readFn := client.ReadHoldingRegisters
			if fc == FuncCodeReadInputRegisters {
				readFn = client.ReadInputRegisters
			}
			// 共享读取助手：逐区间读取，含空洞的合并区间失败时自动降级为严格子段重读。
			groupResults, err = readRegisterRanges(readFn, gAddrs, ranges, sharedCfg)
			if err != nil {
				return nil, fmt.Errorf("modbus rtu: read registers (fc=%d) failed: %w", fc, err)
			}

		case FuncCodeReadCoils, FuncCodeReadDiscreteInputs:
			readFn := client.ReadCoils
			if fc == FuncCodeReadDiscreteInputs {
				readFn = client.ReadDiscreteInputs
			}
			groupResults, err = readBitRanges(readFn, gAddrs, ranges)
			if err != nil {
				return nil, fmt.Errorf("modbus rtu: read bits (fc=%d) failed: %w", fc, err)
			}

		default:
			return nil, fmt.Errorf("modbus rtu: unsupported function code: %d", fc)
		}
		for _, r := range groupResults {
			byID[r.DeviceAddressID] = r
		}
	}

	// 按原始 addrs 顺序重组，保证返回结果与 addrs 一一对应
	results := make([]driver.ReadResult, 0, len(addrs))
	for _, a := range addrs {
		results = append(results, byID[a.ID])
	}
	return results, nil
}

func (d *modbusRTUDriver) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client != nil && d.client.IsConnected()
}

func (d *modbusRTUDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.client != nil {
		d.client.Close()
		d.client = nil
	}
	d.config = nil
	return nil
}
