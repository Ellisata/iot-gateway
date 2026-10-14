// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package modbusdrv 是 Modbus 协议的**自包含实现**，供进程外插件
// `driversrc/modbus` 使用。
//
// 它与内置包 `iot-gateway/driver/modbus` 没有任何依赖关系：类型表、配置解析、
// PLC 地址解析、区间规划、TCP/RTU 客户端与驱动全部在本包内实现，只依赖
// `iot-gateway/driver` 这一份插件与网关共用的契约（Driver 接口、DataType、
// RangeSig），以及插件开发套件 `iot-gateway/driversdk`。
//
// 与内置实现的三处刻意差异：
//
//  1. **类型真源在包内**（见 dataTypes.go 的 typeTable），不查全局 TypeRegistry
//     的 "modbus" scope —— 插件用全新的 scope（"modbusplugin"），本就查不到；
//  2. **日志走 driversdk.Logf**（stderr，网关按行转自己的日志），不引入
//     iot-gateway/logger；Debug 级日志由包级开关 Debug 门控（默认关闭），
//     避免每轮轮询都把区间规划明细刷进网关日志；
//  3. **不含写路径**：driver.Driver 没有写方法，内置实现里的
//     EncodeWriteValue / WriteSingleCoil / WriteSingleRegister 同为不可达代码。
package modbusdrv

// Modbus 功能码常量（只列驱动用到的读功能码；写功能码随写路径一并略去）。
const (
	FuncCodeReadCoils            = 1
	FuncCodeReadDiscreteInputs   = 2
	FuncCodeReadHoldingRegisters = 3
	FuncCodeReadInputRegisters   = 4
)

// 字节序常量
const (
	ByteOrderBigEndian    = "BIG_ENDIAN"
	ByteOrderLittleEndian = "LITTLE_ENDIAN"
)

// 字序常量（仅对 32/64 位值有效）
const (
	WordOrderBigEndian    = "BIG_ENDIAN"    // 高字在前
	WordOrderLittleEndian = "LITTLE_ENDIAN" // 低字在前
)

// defaultMergeWindow 自动模式下跨段合并的最大窗口跨度（寄存器/位单位）。
// 等于 Modbus 单帧寄存器上限 125：合并区间不超过该值即可单帧读取，
// 把稀疏点位的往返次数从"点数"降到"窗口数"。
const defaultMergeWindow = 125

// defaultStringLen string 类型单点默认读取字节数。
// Modbus string 以 16 位寄存器存储、每个寄存器 2 字节，16 字节 = 8 个寄存器；
// 无长度声明的 string 点位按此跨度读取。可通过配置 stringLen 覆盖。
const defaultStringLen = 16

// AddressRange 地址范围计算结果。
// 由 CalcReadRanges 返回，供 Read 和解析函数使用。
type AddressRange struct {
	StartAddress uint16
	Quantity     uint16
	// AddressMap 将 DeviceAddress.ID 映射为 0-based 寄存器地址。
	// 解析函数直接查此 map 避免重复解析 name。
	AddressMap map[string]uint16
}
