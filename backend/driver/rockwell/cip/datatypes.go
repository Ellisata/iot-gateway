// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"iot-gateway/driver"
)

// rockwellScope Rockwell 类型注册表作用域缓存。
// protocolName 为常量，作用域前缀固定；每点位直接复用该作用域，避免反复
// 构造 ProtocolScope 与拼接 "{protocol}." 前缀。
var rockwellScope = driver.GetTypeRegistry().ForProtocol(protocolName)

var (
	rockwellLookupOnce sync.Once
	rockwellTypes      map[string]*driver.DataType
)

// rockwellLookup 按裸名查 DataType：优先命中静态缓存（零分配、无锁），
// 未命中回退注册表查询（兼容大小写变体，保持原有不区分大小写语义）。
// 类型注册表在 init() 后不再变化（dataTypeMap 仅查询不注册），
// 按裸名缓存可消除每点位对全局注册表 RWMutex 的查询与锁竞争。
func rockwellLookup(name string) (*driver.DataType, bool) {
	rockwellLookupOnce.Do(func() {
		m := make(map[string]*driver.DataType)
		prefix := protocolName + "."
		for _, full := range driver.GetTypeRegistry().Names() {
			bare, ok := strings.CutPrefix(full, prefix)
			if !ok {
				continue
			}
			dt, ok := rockwellScope.Get(bare)
			if !ok {
				continue
			}
			m[bare] = dt
		}
		rockwellTypes = m
	})
	if dt, ok := rockwellTypes[name]; ok {
		return dt, true
	}
	return rockwellScope.Get(name)
}

// 重要：本包不注册任何全局别名！
// 所有 Rockwell 类型仅通过 ForProtocol("rockwell") 作用域注册，
// 配置层通用类型名统一由 dataTypeMap（driver.LookupDataType）映射为内部裸名，
// ProtocolScope.Get(name) 会优先命中 "rockwell."+name。

// Logix 数据原生小端，解码固定传 binary.LittleEndian，无字节序/字序配置
// （对比 FINS 因 16 位字与字内大端而需要 ByteOrder/WordOrder）。
// Encode 暂无调用方（Driver 接口无写路径），保留以对齐类型注册惯例。

func init() {
	f := driver.GetTypeRegistry().ForProtocol(protocolName)

	// ========== 注册 Logix 标准数据类型（自动添加 "rockwell." 前缀） ==========
	// 仅注册 CIP 标准类型码（0xC1~0xCB）对应的类型；uint16/uint32/word/dword/bcd/lbcd
	// 无 Logix 原生类型码（数值类型原生有符号），不注册（对比 omron/cip）。

	f.Register(&driver.DataType{
		Name:   "bool",
		Kind:   driver.KindBool,
		Size:   1,
		Decode: decodeBool,
		Encode: encodeBool,
		Format: formatBool,
	})

	f.Register(&driver.DataType{
		Name:   "int16",
		Kind:   driver.KindInt,
		Size:   2,
		Decode: decodeInt16,
		Encode: encodeInt16,
		Format: formatDefault,
	})

	f.Register(&driver.DataType{
		Name:   "int32",
		Kind:   driver.KindInt,
		Size:   4,
		Decode: decodeInt32,
		Encode: encodeInt32,
		Format: formatDefault,
	})

	f.Register(&driver.DataType{
		Name:   "int64",
		Kind:   driver.KindInt,
		Size:   8,
		Decode: decodeInt64,
		Encode: encodeInt64,
		Format: formatDefault,
	})

	f.Register(&driver.DataType{
		Name:   "float32",
		Kind:   driver.KindFloat,
		Size:   4,
		Decode: decodeFloat32,
		Encode: encodeFloat32,
		Format: formatFloat,
	})

	f.Register(&driver.DataType{
		Name:   "float64",
		Kind:   driver.KindFloat,
		Size:   8,
		Decode: decodeFloat64,
		Encode: encodeFloat64,
		Format: formatFloat,
	})

	f.Register(&driver.DataType{
		Name:   "string",
		Kind:   driver.KindString,
		Size:   0, // 动态长度：4 字节长度前缀 + 字符，截断由 parser.go 按 stringLen 处理
		Decode: decodeString,
		Format: formatDefault,
	})

	// ========== 注册扩展类型 ==========

	f.Register(&driver.DataType{
		Name:   "int8",
		Kind:   driver.KindInt,
		Size:   1,
		Decode: decodeInt8,
		Encode: encodeInt8,
		Format: formatDefault,
	})

	// 注意：不注册 date/word/dword/bcd/lbcd。Logix 数值类型原生有符号
	// （SINT/INT/DINT/LINT），无 CIP 标准无符号类型码；位访问用 TagName.N 语法；
	// BCD/日期无 CIP 标准类型码（rockwellTypes 映射中也不含，
	// driver.LookupDataType("Rockwell.CIP","Date") 返回 false）。
}

// typeKind 解析(裸)内部类型名对应的类别 Kind，供构建 ReadResult 时填充。
// 未注册类型返回 ""（推送通道回退为数值推断）。
func typeKind(name string) string {
	if dt, ok := rockwellLookup(name); ok {
		return dt.Kind
	}
	return ""
}

// ==================== 解码函数 ====================
// Logix 数据小端传输（CIP 标准编码，与 Omron 私有编码不同）。

func decodeBool(raw []byte, _ binary.ByteOrder) (any, error) {
	if len(raw) == 0 {
		return false, nil
	}
	return raw[len(raw)-1]&0x01 != 0, nil
}

func decodeInt8(raw []byte, _ binary.ByteOrder) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("int8: need at least 1 byte")
	}
	return int8(raw[len(raw)-1]), nil
}

func decodeInt16(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("int16: need 2 bytes, got %d", len(raw))
	}
	return int16(bo.Uint16(raw[:2])), nil
}

func decodeInt32(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("int32: need 4 bytes, got %d", len(raw))
	}
	return int32(bo.Uint32(raw[:4])), nil
}

func decodeInt64(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 8 {
		return nil, fmt.Errorf("int64: need 8 bytes, got %d", len(raw))
	}
	return int64(bo.Uint64(raw[:8])), nil
}

func decodeFloat32(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("float32: need 4 bytes, got %d", len(raw))
	}
	return math.Float32frombits(bo.Uint32(raw[:4])), nil
}

func decodeFloat64(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 8 {
		return nil, fmt.Errorf("float64: need 8 bytes, got %d", len(raw))
	}
	return math.Float64frombits(bo.Uint64(raw[:8])), nil
}

func decodeString(raw []byte, _ binary.ByteOrder) (any, error) {
	return strings.TrimRight(string(raw), "\x00"), nil
}

// ==================== 编码函数 ====================
// 暂无写路径调用（保留对齐类型注册惯例）。

func encodeBool(val any, _ binary.ByteOrder) ([]byte, error) {
	var v bool
	switch x := val.(type) {
	case bool:
		v = x
	case float64:
		v = x != 0
	case string:
		v = x == "1" || strings.EqualFold(x, "true")
	default:
		return nil, fmt.Errorf("bool: unsupported value type %T", val)
	}
	buf := make([]byte, 1)
	if v {
		buf[0] = 0x01
	}
	return buf, nil
}

func encodeInt8(val any, _ binary.ByteOrder) ([]byte, error) {
	v, err := toInt64(val)
	if err != nil {
		return nil, fmt.Errorf("int8: %w", err)
	}
	return []byte{byte(int8(v))}, nil
}

func encodeInt16(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toInt64(val)
	if err != nil {
		return nil, fmt.Errorf("int16: %w", err)
	}
	buf := make([]byte, 2)
	bo.PutUint16(buf, uint16(int16(v)))
	return buf, nil
}

func encodeInt32(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toInt64(val)
	if err != nil {
		return nil, fmt.Errorf("int32: %w", err)
	}
	buf := make([]byte, 4)
	bo.PutUint32(buf, uint32(int32(v)))
	return buf, nil
}

func encodeInt64(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toInt64(val)
	if err != nil {
		return nil, fmt.Errorf("int64: %w", err)
	}
	buf := make([]byte, 8)
	bo.PutUint64(buf, uint64(v))
	return buf, nil
}

func encodeFloat32(val any, bo binary.ByteOrder) ([]byte, error) {
	var v float32
	switch x := val.(type) {
	case float64:
		v = float32(x)
	case float32:
		v = x
	default:
		return nil, fmt.Errorf("float32: unsupported value type %T", val)
	}
	buf := make([]byte, 4)
	bo.PutUint32(buf, math.Float32bits(v))
	return buf, nil
}

func encodeFloat64(val any, bo binary.ByteOrder) ([]byte, error) {
	var v float64
	switch x := val.(type) {
	case float64:
		v = x
	case float32:
		v = float64(x)
	default:
		return nil, fmt.Errorf("float64: unsupported value type %T", val)
	}
	buf := make([]byte, 8)
	bo.PutUint64(buf, math.Float64bits(v))
	return buf, nil
}

// ==================== 格式化函数 ====================

func formatBool(val any) string {
	if b, ok := val.(bool); ok {
		if b {
			return "1"
		}
		return "0"
	}
	return "0"
}

func formatFloat(val any) string {
	// 'f' 固定记法最短往返表示：字符串能无损还原解码后的浮点，且永不出现指数。
	// 避免 %.4f 固定 4 位小数截断导致下游无法恢复原始精度。
	switch v := val.(type) {
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func formatDefault(val any) string {
	return fmt.Sprintf("%v", val)
}

// ==================== 类型转换工具 ====================

func toInt64(val any) (int64, error) {
	switch v := val.(type) {
	case float64:
		return int64(v), nil
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case uint64:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint8:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", val)
	}
}
