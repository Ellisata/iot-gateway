package modbus

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"iot-gateway/driver"
)

// protocolName Modbus 协议名称，用于类型注册。
const protocolName = "modbus"

// protocolPrefix Modbus 类型注册前缀，避免跨协议类型名冲突。
const protocolPrefix = protocolName + "."

// Modbus 数据类型内部名（带协议前缀，注册后 dt.Name 即为此值）
const (
	typeBool   = protocolPrefix + "bool"
	typeInt16  = protocolPrefix + "int16"
	typeUInt16 = protocolPrefix + "uint16"
	typeInt32  = protocolPrefix + "int32"
	typeUInt32 = protocolPrefix + "uint32"
	typeFloat  = protocolPrefix + "float32"
	typeString = protocolPrefix + "string"
	typeWord   = protocolPrefix + "word"
	// 新扩展类型
	typeUInt8 = protocolPrefix + "uint8"
	typeInt8  = protocolPrefix + "int8"
)

func init() {
	reg := driver.GetTypeRegistry()
	mb := reg.ForProtocol(protocolName)

	// ========== 注册标准数据类型（自动添加 "modbus." 前缀） ==========

	mb.Register(&driver.DataType{
		Name:   "bool",
		Kind:   driver.KindBool,
		Size:   1,
		Decode: decodeBool,
		Encode: encodeBool,
		Format: formatBool,
	})

	mb.Register(&driver.DataType{
		Name:   "int16",
		Kind:   driver.KindInt,
		Size:   2,
		Decode: decodeInt16,
		Encode: encodeInt16,
		Format: formatDefault,
	})

	mb.Register(&driver.DataType{
		Name:   "uint16",
		Kind:   driver.KindUInt,
		Size:   2,
		Decode: decodeUint16,
		Encode: encodeUint16,
		Format: formatDefault,
	})

	mb.Register(&driver.DataType{
		Name:   "word",
		Kind:   driver.KindUInt,
		Size:   2,
		Decode: decodeUint16,
		Encode: encodeUint16,
		Format: formatDefault,
	})

	mb.Register(&driver.DataType{
		Name:   "int32",
		Kind:   driver.KindInt,
		Size:   4,
		Decode: decodeInt32,
		Encode: encodeInt32,
		Format: formatDefault,
	})

	mb.Register(&driver.DataType{
		Name:   "uint32",
		Kind:   driver.KindUInt,
		Size:   4,
		Decode: decodeUint32,
		Encode: encodeUint32,
		Format: formatDefault,
	})

	mb.Register(&driver.DataType{
		Name:   "float32",
		Kind:   driver.KindFloat,
		Size:   4,
		Decode: decodeFloat32,
		Encode: encodeFloat32,
		Format: formatFloat,
	})

	mb.Register(&driver.DataType{
		Name:   "float64",
		Kind:   driver.KindFloat,
		Size:   8,
		Decode: decodeFloat64,
		Encode: encodeFloat64,
		Format: formatFloat,
	})

	mb.Register(&driver.DataType{
		Name:   "string",
		Kind:   driver.KindString,
		Size:   0, // 动态长度
		Decode: decodeString,
		Format: formatDefault,
	})

	// ========== 注册扩展类型 ==========

	mb.Register(&driver.DataType{
		Name:   "uint8",
		Kind:   driver.KindUInt,
		Size:   1,
		Decode: decodeUint8,
		Encode: encodeUint8,
		Format: formatDefault,
	})

	mb.Register(&driver.DataType{
		Name:   "int8",
		Kind:   driver.KindInt,
		Size:   1,
		Decode: decodeInt8,
		Encode: encodeInt8,
		Format: formatDefault,
	})

	mb.Register(&driver.DataType{
		Name:   "bcd",
		Kind:   driver.KindUInt,
		Size:   2,
		Decode: decodeBCD,
		Encode: encodeBCD,
		Format: formatDefault,
	})

	mb.Register(&driver.DataType{
		Name:   "lbcd",
		Kind:   driver.KindUInt,
		Size:   4,
		Decode: decodeLBCD,
		Encode: encodeLBCD,
		Format: formatDefault,
	})

	mb.Register(&driver.DataType{
		Name:   "date",
		Kind:   driver.KindTime,
		Size:   4,
		Decode: decodeDate,
		Encode: encodeDate,
		Format: formatDate,
	})

	// 本包不注册任何全局别名。类型仅通过 ForProtocol 作用域注册，
	// 配置层的通用类型名由 dataTypeMap（driver.LookupDataType）统一映射为内部名。
}

// typeKind 解析(裸)内部类型名对应的类别 Kind，供构建 ReadResult 时填充。
// 未注册类型返回 ""（推送通道回退为数值推断）。
func typeKind(name string) string {
	return driver.GetTypeRegistry().ForProtocol(protocolName).KindOf(name)
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
	// raw 已在 Modbus parseString 中按字节序重新排列
	return strings.TrimRight(string(raw), "\x00"), nil
}

// ---- 扩展类型 ----

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

// ---- 扩展类型编码 ----

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
