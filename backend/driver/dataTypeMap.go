package driver

import (
	"slices"
	"sort"
	"strings"

	"iot-gateway/model/po"
)

// ==================== 通用数据类型 ====================
//
// 各协议在配置界面 / API 中统一使用以下通用数据类型名称，
// 通过 DataTypeMap 映射到协议内部注册的数据类型名（裸名，可经
// TypeRegistry.ForProtocol(protocol).Get(name) 解析）。

const (
	TypeBoolean = "Boolean"
	TypeDate    = "Date"
	TypeString  = "String"
	TypeByte    = "Byte"
	TypeChar    = "Char"
	TypeShort   = "Short"
	TypeWord    = "Word"
	TypeDWord   = "DWord"
	TypeLong    = "Long"
	TypeFloat   = "Float"
	TypeDouble  = "Double"
	TypeBCD     = "BCD"
	TypeLBCD    = "LBCD"
)

// CommonDataTypes 通用数据类型列表（顺序即配置界面展示顺序）。
var CommonDataTypes = []string{
	TypeBoolean, TypeDate, TypeString, TypeByte, TypeChar,
	TypeShort, TypeWord, TypeDWord, TypeLong, TypeFloat,
	TypeDouble, TypeBCD, TypeLBCD,
}

// ==================== 各协议数据类型映射 ====================

// modbusTypes Modbus 通用数据类型 → 内部类型名。
// ModBus.Net.TCP / ModBus.Net.RTU 共用同一套类型注册。
// Int / Real 为 PLC 风格命名（仅 Modbus 开放，不在 CommonDataTypes 通用列表中）：
// Modbus 无独立 16 位 INT，统一按 int32 处理；Real 等价于 Float。
var modbusTypes = map[string]string{
	TypeBoolean: "bool",
	TypeDate:    "date",
	TypeString:  "string",
	TypeByte:    "uint8",
	TypeChar:    "int8",
	TypeShort:   "int16",
	TypeWord:    "word",
	TypeDWord:   "uint32",
	TypeLong:    "int32",
	TypeFloat:   "float32",
	TypeDouble:  "float64",
	TypeBCD:     "bcd",
	TypeLBCD:    "lbcd",
	"Int":       "int32",
	"Real":      "float32",
}

// s7Types S7 通用数据类型 → 内部类型名。
// S7 无独立 BCD/LBCD 类型（BCD 仅内嵌于 S5TIME / DATE_AND_TIME 的编码中），故不映射。
var s7Types = map[string]string{
	TypeBoolean: "bool",
	TypeDate:    "date",
	TypeString:  "string",
	TypeByte:    "byte",
	TypeChar:    "char",
	TypeShort:   "int", // S7 INT（16 位有符号）
	TypeWord:    "word",
	TypeDWord:   "dword",
	TypeLong:    "dint", // S7 DINT（32 位有符号）
	TypeFloat:   "real",
	TypeDouble:  "lreal",
}

// cipTypes CIP 通用数据类型 → 内部类型名。
// CIP 数据原生小端，类型映射与 FINS 保持一致；Date 不支持
// （CIP 数据表读取无 DATE 类型码），故不映射。
var cipTypes = map[string]string{
	TypeBoolean: "bool",
	TypeString:  "string",
	TypeByte:    "uint8",
	TypeChar:    "int8",
	TypeShort:   "int16",
	TypeWord:    "word",
	TypeDWord:   "uint32",
	TypeLong:    "int32",
	TypeFloat:   "float32",
	TypeDouble:  "float64",
	TypeBCD:     "bcd",
	TypeLBCD:    "lbcd",
}

// finsTypes FINS 通用数据类型 → 内部类型名。
// FINS 与 Modbus 同为 16 位字寻址、字内大端，类型映射保持一致。
var finsTypes = map[string]string{
	TypeBoolean: "bool",
	TypeDate:    "date",
	TypeString:  "string",
	TypeByte:    "uint8",
	TypeChar:    "int8",
	TypeShort:   "int16",
	TypeWord:    "word",
	TypeDWord:   "uint32",
	TypeLong:    "int32",
	TypeFloat:   "float32",
	TypeDouble:  "float64",
	TypeBCD:     "bcd",
	TypeLBCD:    "lbcd",
}

// ==================== 协议作用域（scope）组织 ====================

// scopeTypeMap 可配置类型的唯一真源：TypeRegistry scope 前缀 → 通用/扩展类型映射。
// scope 前缀与各驱动包 protocolName 常量一致（ForProtocol(scope) 使用）。
// 值 map 的 key 既包含 CommonDataTypes 通用类型，也允许协议专属扩展名
// （如 Modbus 的 "Int" / "Real"）；在表内新增 key，校验（LookupDataType）
// 与类型列表接口（AvailableTypes）即同时生效。
var scopeTypeMap = map[string]map[string]string{
	"modbus": modbusTypes,
	"s7":     s7Types,
	"cip":    cipTypes,
	"fins":   finsTypes,
}

// scopeProtocols scope 前缀 → 该 scope 下的 config 协议名（driver.Register / iot_protocol.name）。
// 一个 scope 可能对应多个 config 协议名（如 modbus 的 TCP 与 RTU 共用同一套类型注册）。
var scopeProtocols = map[string][]string{
	"modbus": {"ModBus.TCP", "ModBus.RTU"},
	"s7":     {"Siemens.S7"},
	"cip":    {"Omron.CIP"},
	"fins":   {"Omron.FINS.UDP", "Omron.FINS.TCP", "Omron.FINS.Serial", "Omron.FINS.HostLinkTCP"},
}

// protocolScope config 协议名 → scope 前缀（由 scopeProtocols 派生）。
var protocolScope = func() map[string]string {
	m := make(map[string]string, len(scopeProtocols))
	for scope, protocols := range scopeProtocols {
		for _, p := range protocols {
			m[p] = scope
		}
	}
	return m
}()

// scopeLookup 小写化 config 协议名 → scope 前缀（ScopeOf 查询用）。
var scopeLookup = func() map[string]string {
	m := make(map[string]string, len(protocolScope))
	for protocol, scope := range protocolScope {
		m[strings.ToLower(protocol)] = scope
	}
	return m
}()

// ==================== 常量映射表 ====================

// DataTypeMap 协议数据类型常量映射。
//
//	key   = "{协议名}@{通用数据类型名}"
//	value = 该协议在 TypeRegistry 中注册的内部类型名（裸名）
//
// 协议名与 driver.Register() 注册的协议名保持一致，例如：
//
//	DataTypeMap["ModBus.Net.TCP@Boolean"] == "bool"
var DataTypeMap = func() map[string]string {
	m := make(map[string]string, len(protocolScope)*len(CommonDataTypes))
	for protocol, scope := range protocolScope {
		for common, internal := range scopeTypeMap[scope] {
			m[protocol+"@"+common] = internal
		}
	}
	return m
}()

// ScopeOf 返回 config 协议名对应的 TypeRegistry scope 前缀（如 "ModBus.Net.TCP" → "modbus"）。
// 协议名不区分大小写；未知协议（无驱动注册）返回空串。
func ScopeOf(protocol string) string {
	return scopeLookup[strings.ToLower(protocol)]
}

// LookupScopeType 按 scope 前缀查询通用/扩展类型 → 内部类型名（裸名）。
// scope 与类型名均不区分大小写；scope 未知或该 scope 不支持该类型时返回 false。
func LookupScopeType(scope, commonType string) (string, bool) {
	types, ok := scopeTypeMap[scope]
	if !ok {
		return "", false
	}
	// 映射表 key 为规范写法（如 "Float"），小写化匹配保证不区分大小写
	for name, internal := range types {
		if strings.EqualFold(name, commonType) {
			return internal, true
		}
	}
	return "", false
}

// LookupDataType 根据协议名和通用数据类型名查询该协议对应的内部类型名。
// 协议名与类型名均不区分大小写（如 "ModBus.Net.TCP@Boolean" 与 "modbus.net.tcp@boolean" 等价）。
// 协议不支持该通用类型时返回 false。
func LookupDataType(protocol, commonType string) (string, bool) {
	scope := ScopeOf(protocol)
	if scope == "" {
		return "", false
	}
	return LookupScopeType(scope, commonType)
}

// AvailableTypes 返回协议 scope 支持的全部可配置类型名，供配置界面类型下拉使用。
// 返回值：
//
//	common   通用类型（CommonDataTypes 子集，按展示顺序）
//	extended 协议专属扩展类型（不在通用列表中，按字母序，如 Modbus 的 Int/Real）
//
// 候选类型映射到的内部类型必须已在 TypeRegistry 注册才返回，
// 避免"映射表有、注册表无"的新漂移（此时配置界面不应展示该类型）。
func AvailableTypes(scope string) (common, extended []string) {
	types, ok := scopeTypeMap[scope]
	if !ok {
		return nil, nil
	}
	reg := GetTypeRegistry().ForProtocol(scope)

	for _, name := range CommonDataTypes {
		internal, ok := types[name]
		if !ok {
			continue
		}
		if _, ok := reg.Get(internal); !ok {
			continue
		}
		common = append(common, name)
	}

	for name, internal := range types {
		if slices.Contains(common, name) {
			continue
		}
		if _, ok := reg.Get(internal); !ok {
			continue
		}
		extended = append(extended, name)
	}
	sort.Strings(extended)
	return common, extended
}

// NormalizeAddressType 读取路径的类型回退：返回地址应使用的协议内部类型名。
//
// 规则（保守，只修坏的）：
//  1. DataType 内部名仍能在协议 scope 注册表解析 → 原样返回（不干扰有效但不同的类型）；
//  2. 否则用 CommonDataType 经 LookupDataType 重新解析；
//  3. 两者都失败 → 原样返回 DataType（驱动照常报"unsupported data type"）。
//
// 用于采集引擎加载点位时修正漂移的 data_type 派生缓存（协议注册名变更 / 旧数据 / 外部改库）。
func NormalizeAddressType(protocol string, addr po.DeviceAddress) string {
	scope := ScopeOf(protocol)
	if scope == "" {
		return addr.DataType
	}
	if _, ok := GetTypeRegistry().ForProtocol(scope).Get(addr.DataType); ok {
		return addr.DataType
	}
	if internal, ok := LookupDataType(protocol, addr.CommonDataType); ok {
		return internal
	}
	return addr.DataType
}
