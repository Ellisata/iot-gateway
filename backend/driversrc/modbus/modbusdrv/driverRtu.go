// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"fmt"
	"sync"
	"time"

	goburrowModbus "github.com/goburrow/modbus"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// NewRTU 创建 Modbus RTU 驱动实例（插件工厂用）。
func NewRTU() driver.Driver {
	return &modbusRTUDriver{}
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
	// 对半降级后的区间也在同一张缓存里（见 planCache.degraded）。
	plans planCache

	// warns 日志去重（读路径的坏地址与瞬时状态各一条，每轮都成立）
	warns warnSet
}

// SerialExclusive 标记 RTU 驱动独占串口总线。
// 采集引擎需对独占驱动整轮持锁（重连+全部批次读取+转换+推送），
// 防止不同频率分组的轮询在串口上交错。
//
// 插件场景下网关实际依据的是 driversdk.Meta 里该协议的 SerialExclusive 声明
// （pluginhost 的代理驱动据此实现本接口）；这里照常实现，是为了让本包单独
// 使用时（不经插件进程）语义也成立。
func (d *modbusRTUDriver) SerialExclusive() bool { return true }

// 编译期断言：签名漂移时类型断言会静默失败 → 复用退回临时实例 → 又变回
// Access is denied，且没有任何编译错误提示。这条断言是它的唯一防线。
var _ driver.ConnectionMatcher = (*modbusRTUDriver)(nil)

// matchConfig 返回本实例当前持有的、能直接服务 cfg 的那条连接（没有则 nil）。
//
// 返回连接对象而不是 bool，是为了让调用方拿到"刚才被判定可用的那一个" ——
// 否则判定与取用是两次独立的快照，中间 Connect 换掉 client 时会对着一个
// 已关闭的句柄收发。
func (d *modbusRTUDriver) matchConfig(cfg *ModbusRTUConfig) *ModbusRTUClient {
	if cfg.ComPort == "" {
		return nil
	}
	d.mu.RLock()
	live := d.client
	d.mu.RUnlock()

	if live == nil || !live.matchConfig(cfg) {
		return nil
	}
	return live
}

// MatchConnection 见 driver.ConnectionMatcher。
//
// RTU 只有串口一种传输，无需按 transport 门控（对照 MC/FINS/DLT645 的同类方法）。
func (d *modbusRTUDriver) MatchConnection(protocolJSON string) bool {
	cfg, err := ParseModbusRTUConfig(protocolJSON)
	if err != nil {
		// JSON 非法时不复用，交给 Ping 去报那条真正的解析错误
		return false
	}
	return d.matchConfig(cfg) != nil
}

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

	// 任何重连尝试都使规划缓存失效：配置可能已变化，新连接必须使用重新计算的规划。
	// 降级结果也在同一张缓存里，一并失效 —— 重连后设备侧可能已经补上了那个地址。
	d.plans.clear()
	d.warns.clear()

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

// Ping 校验串口与设备可达，不保留任何临时资源
// （driver.PingDevice 不会调用 Close，见 driver.Driver 的 Ping 契约）。
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
	//
	// 判据与 MatchConnection 共用同一个 matchConfig，"这次 ping 该不该复用"
	// 在会话层与驱动层只有一个答案，不会各判各的。
	if liveClient := d.matchConfig(cfg); liveClient != nil {
		if err := liveClient.Ping(); err != nil {
			return fmt.Errorf("modbus rtu: ping %s failed (device no response): %w",
				cfg.ComPort, err)
		}
		logf("rtu ping success: %s (baud=%d, slave=%d, reused connection)",
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
		return fmt.Errorf("modbus rtu: ping %s failed (serial): %w%s",
			cfg.ComPort, err, driver.SerialOpenFailureHint)
	}
	defer handler.Close()

	// Step 2: Modbus 协议层握手（发送读取请求，验证设备可达）
	client := goburrowModbus.NewClient(handler)
	if _, err := client.ReadHoldingRegisters(0, 1); err != nil {
		return fmt.Errorf("modbus rtu: ping %s failed (device no response): %w",
			cfg.ComPort, err)
	}

	logf("rtu ping success: %s (baud=%d, slave=%d)", cfg.ComPort, cfg.BaudRate, cfg.UnitID)
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
	sharedCfg := sharedTCPConfig(cfg)

	client.SetUnitID(cfg.UnitID)

	// 按功能码分组 + 每组区间计算一次性规划并缓存（内容指纹命中则跳过重复解析）。
	// 线圈/保持寄存器等是不同的地址空间，同一设备混合点位时分组各自读取，而非整组报错。
	sig := driver.RangeSig(addrs)
	plan, err := d.plans.planFor(sig, addrs, sharedCfg)
	if err != nil {
		return nil, fmt.Errorf("modbus rtu: calc address ranges failed: %w", err)
	}

	return readBatch("modbus rtu", client, plan, &d.plans, sig, addrs, sharedCfg, &d.warns)
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
