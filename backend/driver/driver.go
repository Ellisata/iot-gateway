// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import (
	"fmt"

	"iot-gateway/model/po"
)

// ReadResult 驱动读取单个地址的结果
type ReadResult struct {
	DeviceAddressID string `json:"deviceAddressId"`
	Value           string `json:"value"`
	DataType        string `json:"dataType"`
	Kind            string `json:"kind"`    // 数据类型类别（driver.KindXxx），由驱动从已解析类型填充
	Quality         int    `json:"quality"` // 192=正常, 0=异常
}

// Driver 协议驱动接口
// 每种协议（Modbus、OPC UA、S7、BACnet 等）实现此接口
type Driver interface {
	// Connect 根据协议 JSON 配置建立连接
	Connect(protocolJSON string) error
	// Ping 测试协议连通性，轻量级、无副作用（不修改驱动内部状态）
	// 建立临时连接执行握手后立即关闭，返回 nil 表示可达
	Ping(protocolJSON string) error
	// Read 读取指定地址列表的值，返回与 addrs 对应的结果
	Read(addrs []po.DeviceAddress) ([]ReadResult, error)
	// IsConnected 返回连接状态
	IsConnected() bool
	// Close 关闭连接释放资源
	Close() error
}

// SerialExclusive 标记驱动的底层连接为独占串行资源（如 Modbus RTU 的 RS-232/RS-485 串口总线）。
// 采集引擎据此决定设备锁粒度：
//   - 独占驱动：整轮持锁（重连 + 全部批次读取 + 转换 + 推送），保证总线上帧不被交错；
//   - 非独占驱动（Modbus TCP / S7）：客户端内部已线程安全（tcpTransporter.Send 对单次
//     请求+响应内部持锁），仅对「重连 + 单次 Read」加锁，转换与推送不持锁，
//     避免同一设备慢频率分组的长轮询拖住快频率分组的实际采集周期。
//
// 未实现该接口的驱动一律视为非独占（线程安全）。
type SerialExclusive interface {
	// SerialExclusive 返回驱动是否独占串行总线。
	SerialExclusive() bool
}

// NewFunc 驱动构造函数
type NewFunc func() Driver

var registry = make(map[string]NewFunc)

// Register 注册协议驱动，由各驱动的 init() 调用
func Register(protocol string, fn NewFunc) {
	if _, ok := registry[protocol]; ok {
		panic(fmt.Sprintf("driver: protocol %q already registered", protocol))
	}
	registry[protocol] = fn
}

// Create 根据协议名创建驱动实例
func Create(protocol string) (Driver, error) {
	fn, ok := registry[protocol]
	if !ok {
		return nil, fmt.Errorf("driver: unsupported protocol %q", protocol)
	}
	return fn(), nil
}

// PingDevice 测试指定协议设备的连通性
// 用于设备列表中的"测试连接"功能。
//
// 优先复用采集引擎当前持有的同协议实例（见 SerialOwner）：Windows 下串口是独占资源
// （goburrow/serial 以共享模式 0 打开），引擎已持有 COM 句柄时，临时实例再打开一次
// 必然报 Access is denied —— 那会把「设备其实可达」误报成失败。
//
// 找不到可复用实例时（绝大多数情况：采集未运行、或传输层是 TCP），
// 退回原路径：创建临时实例执行 Ping，不保存任何状态。
func PingDevice(protocol, protocolJSON string) error {
	// findLiveSerialOwner 只是候选筛子：返回之后、驱动拿到自己的锁之前，实例可能已被
	// 重连到另一台设备上。复用与否最终以驱动 Ping 分支内的校验为准，这里不做二次补救。
	if live := findLiveSerialOwner(protocol, protocolJSON); live != nil {
		return live.Ping(protocolJSON)
	}

	fn, ok := registry[protocol]
	if !ok {
		return fmt.Errorf("driver: unsupported protocol %q", protocol)
	}
	return fn().Ping(protocolJSON)
}
