// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"iot-gateway/model/po"
)

// ReadResult 驱动读取单个地址的结果
type ReadResult struct {
	DeviceAddressID string `json:"deviceAddressId"`
	Value           string `json:"value"`
	DataType        string `json:"dataType"`
	Kind            string `json:"kind"`    // 数据类型类别（driver.KindXxx），由驱动从已解析类型填充
	Quality         int    `json:"quality"` // 采集质量码（driver.QualityXxx：类别在高 2 位、成因在低 6 位）
}

// Driver 协议驱动接口
// 每种协议（Modbus、OPC UA、S7、BACnet 等）实现此接口
type Driver interface {
	// Connect 根据协议 JSON 配置建立连接
	Connect(protocolJSON string) error
	// Ping 测试协议连通性，返回 nil 表示可达。
	//
	// 允许两种策略：建临时连接执行握手后立即关闭，或**复用本实例已有的活连接**
	// （后者由 ConnectionMatcher 选出目标实例，见 driver.ConnectionMatcher 与
	// driversdk 的 ping 处理）。串口设备正在采集时必须走复用 —— Windows 下串口
	// 独占，对同一端口二次打开必然 Access is denied。
	//
	// 无论走哪条路径，两条不变量都必须成立：
	//   - 不留下需要调用方显式释放的资源（PingDevice 从不调用 Close）；
	//   - 不让实例变得不可用 —— 不得 Close，不得把 IsConnected() 永久打成 false。
	//     （部分驱动的串口客户端在读写失败时会置断连标记，那一轮之后会自动重连，
	//     属可接受；"永久"指的是不能再连回来。）
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
//
// 串口类协议还须遵守一条跨协议约定：串口名必须放在 protocol_json 的
// comPort 字段（见 SerialPortField / ExtractSerialPort）。
type SerialExclusive interface {
	// SerialExclusive 返回驱动是否独占串行总线。
	SerialExclusive() bool
}

// ConnectionMatcher 由「独占串行资源的驱动」可选实现：报告**本实例此刻持有的
// 连接**是否就是 protocolJSON 所描述的那一个。
//
// driversdk 用它决定「测试连接」能否复用正在采集的会话：串口是独占资源，
// 对已被本进程某个会话打开的端口再开一次必然失败（Windows 报 Access is denied），
// 而复用同一句柄既能得出正确答案，又不打扰采集链路。
//
// 语义约束：
//   - 纯查询：不得打开/关闭连接，不得修改驱动内部状态，不得 panic；
//   - 调用方保证它与该实例上的 Connect/Read/Ping/Close 互斥，本方法无需自己加锁；
//   - **保守返回 false 永远安全**（退回临时实例路径），因此拿不准就返回 false；
//   - 仅在串口传输上返回 true。TCP 复用没有收益，且会让这次 ping 白排在一个
//     可能长达数十秒的批量读取后面。
//
// 比较必须覆盖**所有会改变链路语义的参数**：端口、波特率、数据位、停止位、
// 校验位，以及从站号/表号这类已经固化进连接的身份 —— 复用一条从站号不同的连接，
// 会把"测试 2 号站"的请求发到 1 号站上，1 号站正常应答 → 测试报成功，
// 而用户想验的 2 号站根本没被问到。
type ConnectionMatcher interface {
	// MatchConnection 报告本实例持有的连接能否直接服务这次测试。
	MatchConnection(protocolJSON string) bool
}

// NewFunc 驱动构造函数
type NewFunc func() Driver

var (
	registryMu sync.RWMutex
	registry   = make(map[string]NewFunc)
)

// ErrProtocolTaken 协议注册名已被占用。
var ErrProtocolTaken = errors.New("driver: protocol already registered")

// Register 注册协议驱动。内置协议已全部搬成进程外插件，所以现在的调用方
// 主要是插件实现包自己的 init()（插件进程内的本地注册表，与网关无涉）
// 与测试；网关侧的插件注册走 RegisterExternal。
//
// 重复注册 panic —— 启动期的配置错误必须立刻暴露，不允许静默覆盖。
// 运行期注册（外部插件）请用 RegisterExternal。
func Register(protocol string, fn NewFunc) {
	if err := register(protocol, fn); err != nil {
		panic(err)
	}
}

// RegisterExternal 运行期注册外部（插件）驱动，供 pluginhost 管理器调用。
//
// 与 Register 只有错误处理不同：插件在运行期被发现/消失是可恢复的运维状态，
// 重复注册返回 error 而非终止网关。仍然拒绝覆盖任何已注册的协议名
// （包括其它插件已认领的），因此不存在"后到的插件顶替先到的"的路径。
func RegisterExternal(protocol string, fn NewFunc) error {
	return register(protocol, fn)
}

func register(protocol string, fn NewFunc) error {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[protocol]; ok {
		return fmt.Errorf("%w: %q", ErrProtocolTaken, protocol)
	}
	registry[protocol] = fn
	return nil
}

// Unregister 注销协议驱动，仅由插件管理器在插件文件消失时调用。
//
// 协议未注册时返回 false。注销后 Create/PingDevice 对该协议返回 unsupported，
// 采集引擎已将其视为非致命（跳过使用该协议的设备，见 collector.newGatewayTask）。
func Unregister(protocol string) bool {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[protocol]; !ok {
		return false
	}
	delete(registry, protocol)
	return true
}

// Registered 返回当前已注册的全部协议名（全部来自插件），按字典序排列。
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// lookup 取出协议对应的构造函数。
//
// 调用方必须在 lookup 返回后（即已释放读锁）再调用构造函数：
// sync.RWMutex 不可重入，构造函数若（直接或间接）触发注册会自死锁；
// 且持锁做内存分配本就没有意义。
func lookup(protocol string) (NewFunc, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	fn, ok := registry[protocol]
	return fn, ok
}

// Create 根据协议名创建驱动实例
func Create(protocol string) (Driver, error) {
	fn, ok := lookup(protocol)
	if !ok {
		return nil, fmt.Errorf("driver: unsupported protocol %q", protocol)
	}
	return fn(), nil
}

// PingDevice 测试指定协议设备的连通性，用于设备列表中的"测试连接"功能。
//
// 网关口只能从注册表取一个**临时实例**来 Ping，因此它天然看不到任何已建立的
// 连接。真正持有串口句柄的是插件进程内的会话，复用与否由插件侧决定
// （见 driversdk 的 ping 处理）：本函数取到的实例是一个代理，它的 Ping 会把
// 请求发到插件进程，由那边挑选可复用的会话。
//
// 注意：不调用 Close —— 驱动的 Ping 因此不得留下需要释放的资源。
func PingDevice(protocol, protocolJSON string) error {
	fn, ok := lookup(protocol)
	if !ok {
		return fmt.Errorf("driver: unsupported protocol %q", protocol)
	}
	// 创建临时实例执行 Ping，不保存任何状态
	return fn().Ping(protocolJSON)
}
