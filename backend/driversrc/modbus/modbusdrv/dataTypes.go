// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"iot-gateway/driver"
)

// 类型内部裸名。这些名字既是包内查找表的键、也是 addr.DataType 的取值
// （网关按插件 TypeMap 把配置层的通用类型名归一化为裸名后再下发）。
const (
	typeNameBool    = "bool"
	typeNameInt16   = "int16"
	typeNameUint16  = "uint16"
	typeNameWord    = "word"
	typeNameInt32   = "int32"
	typeNameUint32  = "uint32"
	typeNameFloat32 = "float32"
	typeNameFloat64 = "float64"
	typeNameString  = "string"
	typeNameUint8   = "uint8"
	typeNameInt8    = "int8"
	typeNameBCD     = "bcd"
	typeNameLBCD    = "lbcd"
	typeNameDate    = "date"
)

// typeTable 本插件的**类型唯一真源**：内部裸名 → DataType。
//
// 与内置实现的区别：内置包把类型注册进全局 TypeRegistry 的 "modbus" scope，
// 再用 sync.Once 缓存裸名 → *DataType 映射；插件用的是全新 scope，且类型表
// 在插件进程内只此一份，直接查表即可，无需经过注册表与锁。
//
// uint16 在内表中，但**不在 CommonTypeMap 里**（内置的 modbusTypes 同样不映射它），
// 因此它不会被上报进 Meta.Types：它只是让"地址里直接写了 uint16"这种历史数据
// 仍能解析，与内置实现的行为保持一致。
var typeTable = map[string]*driver.DataType{
	typeNameBool: {
		Name:   typeNameBool,
		Kind:   driver.KindBool,
		Size:   1,
		Decode: decodeBool,
		Encode: encodeBool,
		Format: formatBool,
	},
	typeNameInt16: {
		Name:   typeNameInt16,
		Kind:   driver.KindInt,
		Size:   2,
		Decode: decodeInt16,
		Encode: encodeInt16,
		Format: formatDefault,
	},
	typeNameUint16: {
		Name:   typeNameUint16,
		Kind:   driver.KindUInt,
		Size:   2,
		Decode: decodeUint16,
		Encode: encodeUint16,
		Format: formatDefault,
	},
	typeNameWord: {
		Name:   typeNameWord,
		Kind:   driver.KindUInt,
		Size:   2,
		Decode: decodeUint16,
		Encode: encodeUint16,
		Format: formatDefault,
	},
	typeNameInt32: {
		Name:   typeNameInt32,
		Kind:   driver.KindInt,
		Size:   4,
		Decode: decodeInt32,
		Encode: encodeInt32,
		Format: formatDefault,
	},
	typeNameUint32: {
		Name:   typeNameUint32,
		Kind:   driver.KindUInt,
		Size:   4,
		Decode: decodeUint32,
		Encode: encodeUint32,
		Format: formatDefault,
	},
	typeNameFloat32: {
		Name:   typeNameFloat32,
		Kind:   driver.KindFloat,
		Size:   4,
		Decode: decodeFloat32,
		Encode: encodeFloat32,
		Format: formatFloat,
	},
	typeNameFloat64: {
		Name:   typeNameFloat64,
		Kind:   driver.KindFloat,
		Size:   8,
		Decode: decodeFloat64,
		Encode: encodeFloat64,
		Format: formatFloat,
	},
	typeNameString: {
		Name:   typeNameString,
		Kind:   driver.KindString,
		Size:   0, // 动态长度
		Decode: decodeString,
		Format: formatDefault,
	},
	typeNameUint8: {
		Name:   typeNameUint8,
		Kind:   driver.KindUInt,
		Size:   1,
		Decode: decodeUint8,
		Encode: encodeUint8,
		Format: formatDefault,
	},
	typeNameInt8: {
		Name:   typeNameInt8,
		Kind:   driver.KindInt,
		Size:   1,
		Decode: decodeInt8,
		Encode: encodeInt8,
		Format: formatDefault,
	},
	typeNameBCD: {
		Name:   typeNameBCD,
		Kind:   driver.KindUInt,
		Size:   2,
		Decode: decodeBCD,
		Encode: encodeBCD,
		Format: formatDefault,
	},
	typeNameLBCD: {
		Name:   typeNameLBCD,
		Kind:   driver.KindUInt,
		Size:   4,
		Decode: decodeLBCD,
		Encode: encodeLBCD,
		Format: formatDefault,
	},
	typeNameDate: {
		Name:   typeNameDate,
		Kind:   driver.KindTime,
		Size:   4,
		Decode: decodeDate,
		Encode: encodeDate,
		Format: formatDate,
	},
}

// commonTypeMap 通用/扩展类型名 → 内部裸名（上报给网关的 Meta.TypeMap）。
//
// 内容与内置 Modbus 的 modbusTypes（driver/dataTypeMap.go）逐项一致：
// 前 13 项是 driver.CommonDataTypes 中的规范名，会出现在配置界面类型下拉的
// 「通用」组；Int / Real 是 PLC 风格扩展名，落在「扩展」组按字母序排列。
//
// 刻意与内置保持一致而非"重新设计"：插件版与内置版是同一协议的两个入口，
// 类型语义不同会让同一份现场配置在两个入口下采出不同的值。
var commonTypeMap = map[string]string{
	driver.TypeBoolean: typeNameBool,
	driver.TypeDate:    typeNameDate,
	driver.TypeString:  typeNameString,
	driver.TypeByte:    typeNameUint8,
	driver.TypeChar:    typeNameInt8,
	driver.TypeShort:   typeNameInt16,
	driver.TypeWord:    typeNameWord,
	driver.TypeDWord:   typeNameUint32,
	driver.TypeLong:    typeNameInt32,
	driver.TypeFloat:   typeNameFloat32,
	driver.TypeDouble:  typeNameFloat64,
	driver.TypeBCD:     typeNameBCD,
	driver.TypeLBCD:    typeNameLBCD,
	"Int":              typeNameInt32,
	"Real":             typeNameFloat32,
}

// CommonTypeMap 返回通用/扩展类型名 → 内部裸名，供 driversdk.Meta.TypeMap 使用。
// 返回副本，调用方修改不会影响包内真源。
func CommonTypeMap() map[string]string {
	out := make(map[string]string, len(commonTypeMap))
	for name, internal := range commonTypeMap {
		out[name] = internal
	}
	return out
}

// Types 返回上报给网关的插件类型表（driversdk.Meta.Types）。
//
// 上报**全部**已注册类型，而不只是 CommonTypeMap 能到达的那些。
// 少报的症状很隐蔽：网关的 NormalizeAddressType 靠 reg.Get(data_type) 判断
// 地址里存的内部名是否仍然有效 —— 插件漏报一个 uint16，历史数据里写着
// uint16 的点位就会被判为"漂移"，按 common_data_type 改写成 word，
// 并**静默回写库**。值恰好一样，所以现场只会看到 data_type 自己变了。
//
// 内置实现是把整个 scope 注册进注册表的，所以上报全集才是行为等价的做法。
// 顺带保证不变式「TypeMap 的每个 value 都出现在 Types 中」天然成立。
//
// Name 为裸名（driversdk 会按插件的 scope 重新加前缀）。编解码函数随 DataType
// 一并返回，但它们带 json:"-" 不会过线，只在本进程内使用。
func Types() []*driver.DataType {
	out := make([]*driver.DataType, 0, len(typeTable))
	for _, dt := range typeTable {
		copied := *dt
		out = append(out, &copied)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// lookupType 按裸名查 DataType。精确命中为主路径（零分配），
// 未命中回退大小写不敏感匹配 —— 与内置 mbLookup 的语义一致：
// 历史配置里可能存着 "Float32" 这种大小写变体。
func lookupType(name string) (*driver.DataType, bool) {
	if dt, ok := typeTable[name]; ok {
		return dt, true
	}
	for bare, dt := range typeTable {
		if strings.EqualFold(bare, name) {
			return dt, true
		}
	}
	return nil, false
}

// typeKind 解析内部类型名对应的类别 Kind，供构建 ReadResult 时填充。
// 未注册类型返回 ""（推送通道回退为数值推断）。
func typeKind(name string) string {
	if dt, ok := lookupType(name); ok {
		return dt.Kind
	}
	return ""
}

// ==================== 解码函数 ====================

func decodeBool(raw []byte, _ binary.ByteOrder) (any, error) {
	if len(raw) == 0 {
		return false, nil
	}
	return raw[len(raw)-1]&0x01 != 0, nil
}

func decodeInt16(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("int16: need 2 bytes, got %d", len(raw))
	}
	return int16(bo.Uint16(raw[:2])), nil
}

func decodeUint16(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("uint16: need 2 bytes, got %d", len(raw))
	}
	return bo.Uint16(raw[:2]), nil
}

func decodeInt32(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("int32: need 4 bytes, got %d", len(raw))
	}
	return int32(bo.Uint32(raw[:4])), nil
}

func decodeUint32(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("uint32: need 4 bytes, got %d", len(raw))
	}
	return bo.Uint32(raw[:4]), nil
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
	// raw 已在 ParseReadResult 中按字节序经 parseString 重新排列
	return strings.TrimRight(string(raw), "\x00"), nil
}

func decodeUint8(raw []byte, _ binary.ByteOrder) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("uint8: need at least 1 byte")
	}
	return raw[len(raw)-1], nil
}

func decodeInt8(raw []byte, _ binary.ByteOrder) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("int8: need at least 1 byte")
	}
	return int8(raw[len(raw)-1]), nil
}

// decodeBCD 解码 2 字节压缩 BCD 码。如 0x1234 → 1234
func decodeBCD(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("bcd: need 2 bytes, got %d", len(raw))
	}
	v := bo.Uint16(raw[:2])
	return int(bcdToUint64(uint64(v), 4)), nil
}

// decodeLBCD 解码 4 字节长 BCD 码。如 0x12345678 → 12345678
func decodeLBCD(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("lbcd: need 4 bytes, got %d", len(raw))
	}
	v := bo.Uint32(raw[:4])
	return int(bcdToUint64(uint64(v), 8)), nil
}

// decodeDate 将 4 字节解码为 Unix 时间戳（秒）。
func decodeDate(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("date: need 4 bytes, got %d", len(raw))
	}
	ts := bo.Uint32(raw[:4])
	return int64(ts), nil
}

// ==================== 编码函数 ====================
//
// 编码路径在当前 driver.Driver 契约下不可达（接口无写方法），保留是为了让
// DataType 的两个函数成对出现 —— 缺 Encode 的类型表在人工审阅时像是个 bug。

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
	buf := make([]byte, 2)
	if v {
		buf[1] = 0x01
	}
	return buf, nil
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

func encodeUint16(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toUint64(val)
	if err != nil {
		return nil, fmt.Errorf("uint16: %w", err)
	}
	buf := make([]byte, 2)
	bo.PutUint16(buf, uint16(v))
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

func encodeUint32(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toUint64(val)
	if err != nil {
		return nil, fmt.Errorf("uint32: %w", err)
	}
	buf := make([]byte, 4)
	bo.PutUint32(buf, uint32(v))
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

func encodeUint8(val any, _ binary.ByteOrder) ([]byte, error) {
	v, err := toUint64(val)
	if err != nil {
		return nil, fmt.Errorf("uint8: %w", err)
	}
	return []byte{byte(v)}, nil
}

func encodeInt8(val any, _ binary.ByteOrder) ([]byte, error) {
	v, err := toInt64(val)
	if err != nil {
		return nil, fmt.Errorf("int8: %w", err)
	}
	return []byte{byte(int8(v))}, nil
}

func encodeBCD(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toUint64(val)
	if err != nil {
		return nil, fmt.Errorf("bcd: %w", err)
	}
	if v > 9999 {
		v = v % 10000
	}
	bcd := uint64ToBCD(v, 4)
	buf := make([]byte, 2)
	bo.PutUint16(buf, uint16(bcd))
	return buf, nil
}

func encodeLBCD(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toUint64(val)
	if err != nil {
		return nil, fmt.Errorf("lbcd: %w", err)
	}
	if v > 99999999 {
		v = v % 100000000
	}
	bcd := uint64ToBCD(v, 8)
	buf := make([]byte, 4)
	bo.PutUint32(buf, uint32(bcd))
	return buf, nil
}

func encodeDate(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toInt64(val)
	if err != nil {
		return nil, fmt.Errorf("date: %w", err)
	}
	buf := make([]byte, 4)
	bo.PutUint32(buf, uint32(v))
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

func formatDate(val any) string {
	// 归一化为 UTC ISO-8601（RFC3339），外部消费端无需知道单位约定（unix 秒）。
	switch v := val.(type) {
	case int64:
		return time.Unix(v, 0).UTC().Format(time.RFC3339)
	case float64:
		return time.Unix(int64(v), 0).UTC().Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// ==================== BCD 工具函数 ====================

func bcdToUint64(bcd uint64, digits int) uint64 {
	var result uint64
	for i := digits - 1; i >= 0; i-- {
		digit := (bcd >> (i * 4)) & 0xF
		result = result*10 + digit
	}
	return result
}

func uint64ToBCD(v uint64, digits int) uint64 {
	var bcd uint64
	for i := digits - 1; i >= 0; i-- {
		digit := v % 10
		bcd |= digit << (i * 4)
		v /= 10
	}
	return bcd
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

func toUint64(val any) (uint64, error) {
	switch v := val.(type) {
	case float64:
		if v < 0 {
			return 0, fmt.Errorf("negative value %f cannot convert to uint64", v)
		}
		return uint64(v), nil
	case uint64:
		return v, nil
	case uint:
		return uint64(v), nil
	case uint32:
		return uint64(v), nil
	case uint16:
		return uint64(v), nil
	case uint8:
		return uint64(v), nil
	case int64:
		if v < 0 {
			return 0, fmt.Errorf("negative value %d cannot convert to uint64", v)
		}
		return uint64(v), nil
	case int:
		if v < 0 {
			return 0, fmt.Errorf("negative value %d cannot convert to uint64", v)
		}
		return uint64(v), nil
	case int32:
		if v < 0 {
			return 0, fmt.Errorf("negative value %d cannot convert to uint64", v)
		}
		return uint64(v), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to uint64", val)
	}
}

// ==================== Modbus 特有字符串处理 ====================

// parseString 处理 Modbus 字符串的字节序。
// Modbus 字符串以 16 位寄存器存储，每个寄存器 2 字节按字节序排列，
// 处理后去除尾部 \x00 和空格。
func parseString(raw []byte, order binary.ByteOrder) string {
	buf := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 2 {
		if i+1 < len(raw) {
			v := order.Uint16(raw[i : i+2])
			binary.BigEndian.PutUint16(buf[i:i+2], v)
		}
	}
	result := strings.TrimRight(string(buf), "\x00")
	return strings.TrimSpace(result)
}
