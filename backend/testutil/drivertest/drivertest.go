// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package drivertest 提供测试用的**幂等**协议注册助手。
//
// 内置协议驱动已全部迁移为进程外插件（见 docs/driver-plugins.md），主二进制里
// 不再有任何协议实现。网关侧的测试（collector / service）需要「某个协议可用」
// 这一前提，但它们既不该拉起插件进程，也不该依赖某个真实协议的内容 ——
// 那是各插件自带测试与 pluginhost 的 parity 黄金比对负责的事。于是这里用
// 合成 scope + 桩驱动自备前提。
//
// 全部助手都是**幂等**的：协议已由内置基线或更早的调用注册过时直接返回。
// 这一点让同一份测试代码在「内置驱动仍在」与「已移除」两种仓库状态下都通过，
// 迁移因此可以逐协议原子推进，不必等全部协议搬完才恢复绿灯。
package drivertest

import (
	"errors"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// 网关侧测试的合成协议。刻意不用任何真实协议名：名字若叫 ModBus.TCP，
// 读代码的人会以为这些测试覆盖了 Modbus 的语义，而实际上它只验证网关侧的
// 任务编排与类型解析 —— 协议语义另有 goldens 守着，混淆比不覆盖更危险。
const (
	// TestProtocol 合成协议名，可被 driver.Create 创建，连接与读取都成功。
	TestProtocol = "Test.Synthetic.TCP"

	// TestFailingProtocol 合成协议名，连接必然失败。
	// 给"采集失败时不留成功记录"这类用例一个不依赖真实网络行为的失败源 ——
	// 曾经靠"指向一个刚关闭的本地端口"来制造失败，那既依赖 modbus 驱动的
	// 拨号行为，也把断言绑在了"连接被拒"这一种失败原因上。
	TestFailingProtocol = "Test.Synthetic.Unreachable"

	// TestScope 合成协议的类型 scope（两个合成协议共用）。
	TestScope = "testsynthetic"
)

// syntheticTypeMap 合成协议的类型映射。形状刻意贴近真实协议：
// 大多数通用类型 + 两个仅本协议开放的扩展类型（Int/Real），
// 好让 AvailableTypes 的「通用按展示序、扩展按字母序」两条规则都被覆盖。
var syntheticTypeMap = map[string]string{
	driver.TypeBoolean: "bool",
	driver.TypeShort:   "int16",
	driver.TypeFloat:   "float32",
	driver.TypeDouble:  "float64",
	driver.TypeString:  "string",
	"Int":              "int32",
	"Real":             "float32",
}

// syntheticTypes 与 syntheticTypeMap 的 value 逐一对应。
// Kind/Size 照实声明（AvailableTypes 只做存在性门禁，不读 Kind/Size，
// 但读路径要它们，所以别留空）。
var syntheticTypes = []*driver.DataType{
	{Name: "bool", Kind: driver.KindBool, Size: 1},
	{Name: "int16", Kind: driver.KindInt, Size: 2},
	{Name: "int32", Kind: driver.KindInt, Size: 4},
	{Name: "float32", Kind: driver.KindFloat, Size: 4},
	{Name: "float64", Kind: driver.KindFloat, Size: 8},
	{Name: "string", Kind: driver.KindString, Size: 0},
}

// EnsureSyntheticProtocol 一步备好网关侧测试所需的最小协议环境：
// scope + 类型表 + 两个合成驱动。幂等，可在每个测试开头直接调用。
func EnsureSyntheticProtocol() {
	EnsureScope(TestScope, []string{TestProtocol, TestFailingProtocol}, syntheticTypeMap, syntheticTypes...)
	EnsureStubDriver(TestProtocol)
	EnsureDriver(TestFailingProtocol, func() driver.Driver { return FailingDriver{} })
}

// EnsureScope 确保 scope 及其类型映射对 driver 包可见。
//
// 幂等：protocols[0] 已被任何 scope 认领时直接返回（内置基线或更早的调用）。
// 调用方因此必须为合成协议取一个不会被真实协议占用的名字。
//
// types 先注册进 TypeRegistry 再注册 scope：AvailableTypes 用 reg.Get(internal)
// 做门禁，顺序反了会让类型下拉静默少项。已存在的类型条目不覆盖 ——
// 它可能带着某个真实驱动注册的 Decode，覆盖成空壳会让该协议解不出值。
func EnsureScope(scope string, protocols []string, typeMap map[string]string, types ...*driver.DataType) {
	if len(protocols) == 0 {
		panic("drivertest: EnsureScope 需要至少一个协议名")
	}
	if driver.ScopeOf(protocols[0]) != "" {
		return
	}
	reg := driver.GetTypeRegistry()
	for _, dt := range types {
		if _, ok := reg.Get(scope + "." + dt.Name); ok {
			continue
		}
		reg.ForProtocol(scope).Register(dt)
	}
	if err := driver.RegisterScope(scope, protocols, typeMap); err != nil {
		// 测试基建配置错误，不是被测行为 —— 直接 panic 比让每个用例
		// 各自报「类型查不到」更容易定位。
		panic("drivertest: RegisterScope(" + scope + ") 失败: " + err.Error())
	}
}

// EnsureStubDriver 确保协议名有驱动可创建。幂等：已注册（内置基线或插件）时不动。
func EnsureStubDriver(protocol string) {
	EnsureDriver(protocol, func() driver.Driver { return StubDriver{} })
}

// EnsureDriver 确保协议名有驱动可创建，未注册时用 factory 注册。幂等。
func EnsureDriver(protocol string, factory driver.NewFunc) {
	if _, err := driver.Create(protocol); err == nil {
		return
	}
	driver.Register(protocol, factory)
}

// errFailingDriver 合成驱动失败的原因，供断言匹配。
var errFailingDriver = errors.New("drivertest: 合成驱动按设计连接失败")

// FailingDriver 连接必然失败的驱动。
//
// 用来复现"采集失败"这一状态，而不必依赖真实网络行为：以前是靠指向一个
// 刚关闭的本地端口让 modbus 驱动的拨号被拒，那既绑定了具体驱动的实现，
// 也只覆盖了"连接被拒"一种失败原因。
type FailingDriver struct{}

func (FailingDriver) Connect(string) error { return errFailingDriver }
func (FailingDriver) Ping(string) error    { return errFailingDriver }
func (FailingDriver) IsConnected() bool    { return false }
func (FailingDriver) Close() error         { return nil }

func (FailingDriver) Read([]po.DeviceAddress) ([]driver.ReadResult, error) {
	return nil, errFailingDriver
}

// StubDriver 不做任何 I/O 的驱动，只为让 collector 建得出采集任务。
type StubDriver struct{}

func (StubDriver) Connect(string) error { return nil }
func (StubDriver) Ping(string) error    { return nil }
func (StubDriver) IsConnected() bool    { return true }
func (StubDriver) Close() error         { return nil }

// Read 按 addrs 逐项返回零值，索引一一对应。
//
// 对齐是契约要求（见 docs/driver-plugins.md §3.2）：数量或 ID 对不上会让
// 整批采集报错，而桩驱动出现在采集链路上时不该制造与被测逻辑无关的失败。
// Kind 一律留空 —— 桩不代表任何真实类型，编一个 Kind 只会误导下游。
func (StubDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	out := make([]driver.ReadResult, len(addrs))
	for i := range addrs {
		out[i] = driver.ReadResult{
			DeviceAddressID: addrs[i].ID,
			Value:           "0",
			DataType:        addrs[i].DataType,
			Quality:         driver.QualityGood,
		}
	}
	return out, nil
}
