// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbus

// Modbus 功能码常量
const (
	FuncCodeReadCoils              = 1
	FuncCodeReadDiscreteInputs     = 2
	FuncCodeReadHoldingRegisters   = 3
	FuncCodeReadInputRegisters     = 4
	FuncCodeWriteSingleCoil        = 5
	FuncCodeWriteSingleRegister    = 6
	FuncCodeWriteMultipleCoils     = 15
	FuncCodeWriteMultipleRegisters = 16
)

// 数据类型常量
const (
	DataTypeBool    = "bool"
	DataTypeInt16   = "int16"
	DataTypeUInt16  = "uint16"
	DataTypeInt32   = "int32"
	DataTypeUInt32  = "uint32"
	DataTypeFloat32 = "float32"
	DataTypeFloat64 = "float64"
	DataTypeString  = "string"
	DataTypeWord    = "word" // 等同于 uint16
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

// ModbusReadResult Modbus 读取结果
type ModbusReadResult struct {
	Address uint16
	Raw     []byte
	Quality int // 192 = 正常, 0 = 异常
}
