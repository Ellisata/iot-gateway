package s7

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"iot-gateway/driver"
)

// protocolName S7 协议名称，用于类型注册。
const protocolName = "s7"

// 重要：本包不注册任何全局裸名类型！
// driver.TypeRegistry 的 types map 是全局的、不区分协议，任何协议注册裸名
// （INT/REAL/WORD...）都可能互相覆盖。因此所有 S7 类型仅通过
// ForProtocol("s7") 作用域注册（自动加 "s7." 前缀），ProtocolScope.Get(name)
// 会优先命中 "s7."+name，裸名（INT/REAL/WORD...）即可正确解析。
// 配置层的通用类型名统一由 dataTypeMap（driver.LookupDataType）映射。

func init() {
	s7 := driver.GetTypeRegistry().ForProtocol(protocolName)

	// ========== 注册 S7 标准数据类型（自动添加 "s7." 前缀） ==========

	s7.Register(&driver.DataType{
		Name:   "bool",
		Kind:   driver.KindBool,
		Size:   1,
		Decode: decodeBool,
		Encode: encodeBool,
		Format: formatBool,
	})

	s7.Register(&driver.DataType{
		Name:   "byte",
		Kind:   driver.KindUInt,
		Size:   1,
		Decode: decodeByte,
		Encode: encodeByte,
		Format: formatDefault,
	})

	s7.Register(&driver.DataType{
		Name:   "char",
		Kind:   driver.KindString,
		Size:   1,
		Decode: decodeChar,
		Encode: encodeChar,
		Format: formatChar,
	})

	s7.Register(&driver.DataType{
		Name:   "word",
		Kind:   driver.KindUInt,
		Size:   2,
		Decode: decodeWord,
		Encode: encodeWord,
		Format: formatDefault,
	})

	s7.Register(&driver.DataType{
		Name:   "int",
		Kind:   driver.KindInt,
		Size:   2,
		Decode: decodeInt,
		Encode: encodeInt,
		Format: formatDefault,
	})

	s7.Register(&driver.DataType{
		Name:   "dword",
		Kind:   driver.KindUInt,
		Size:   4,
		Decode: decodeDWord,
		Encode: encodeDWord,
		Format: formatDefault,
	})

	s7.Register(&driver.DataType{
		Name:   "dint",
		Kind:   driver.KindInt,
		Size:   4,
		Decode: decodeDInt,
		Encode: encodeDInt,
		Format: formatDefault,
	})

	s7.Register(&driver.DataType{
		Name:   "real",
		Kind:   driver.KindFloat,
		Size:   4,
		Decode: decodeReal,
		Encode: encodeReal,
		Format: formatFloat,
	})

	s7.Register(&driver.DataType{
		Name:   "lreal",
		Kind:   driver.KindFloat,
		Size:   8,
		Decode: decodeLReal,
		Encode: encodeLReal,
		Format: formatFloat,
	})

	s7.Register(&driver.DataType{
		Name:   "time",
		Kind:   driver.KindTime,
		Size:   4,
		Decode: decodeTime,
		Encode: encodeTime,
		Format: formatDefault,
	})

	s7.Register(&driver.DataType{
		Name:   "tod",
		Kind:   driver.KindTime,
		Size:   4,
		Decode: decodeTOD,
		Encode: encodeTOD,
		Format: formatTOD,
	})

	s7.Register(&driver.DataType{
		Name:   "string",
		Kind:   driver.KindString,
		Size:   0, // 动态长度，跨度由 parser/range 计算（2 头字节 + 长度）
		Decode: decodeString,
		// Encode: 写入暂不支持（S7 STRING 编码涉及 maxLen 头部，见文档）
		Format: formatDefault,
	})

	s7.Register(&driver.DataType{
		Name:   "s5time",
		Kind:   driver.KindTime,
		Size:   2,
		Decode: decodeS5Time,
		// Encode: 写入暂不支持（S7 S5TIME 编码涉及时基选择）
		Format: formatDefault,
	})

	s7.Register(&driver.DataType{
		Name:   "date",
		Kind:   driver.KindTime,
		Size:   2,
		Decode: decodeDate,
		// Encode: 写入暂不支持
		Format: formatDate,
	})

	s7.Register(&driver.DataType{
		Name:   "dt",
		Kind:   driver.KindTime,
		Size:   8,
		Decode: decodeDT,
		// Encode: 写入暂不支持（8 字节 BCD 编码）
		Format: formatDT,
	})
}

// typeKind 解析(裸)内部类型名对应的类别 Kind，供构建 ReadResult 时填充。
// 未注册类型返回 ""（推送通道回退为数值推断）。
func typeKind(name string) string {
	return driver.GetTypeRegistry().ForProtocol(protocolName).KindOf(name)
}

// ==================== 解码函数 ====================
// S7 数据全部大端存储，bo 统一为 binary.BigEndian。
// raw 已由驱动层按地址跨度切好。

func decodeBool(raw []byte, _ binary.ByteOrder) (any, error) {
	if len(raw) == 0 {
		return false, nil
	}
	return raw[len(raw)-1]&0x01 != 0, nil
}

func decodeByte(raw []byte, _ binary.ByteOrder) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("byte: need at least 1 byte")
	}
	return raw[0], nil
}

func decodeChar(raw []byte, _ binary.ByteOrder) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("char: need 1 byte")
	}
	return raw[0], nil
}

func decodeWord(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("word: need 2 bytes, got %d", len(raw))
	}
	return bo.Uint16(raw[:2]), nil
}

func decodeInt(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("int: need 2 bytes, got %d", len(raw))
	}
	return int16(bo.Uint16(raw[:2])), nil
}

func decodeDWord(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("dword: need 4 bytes, got %d", len(raw))
	}
	return bo.Uint32(raw[:4]), nil
}

func decodeDInt(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("dint: need 4 bytes, got %d", len(raw))
	}
	return int32(bo.Uint32(raw[:4])), nil
}

func decodeReal(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("real: need 4 bytes, got %d", len(raw))
	}
	return math.Float32frombits(bo.Uint32(raw[:4])), nil
}

func decodeLReal(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 8 {
		return nil, fmt.Errorf("lreal: need 8 bytes, got %d", len(raw))
	}
	return math.Float64frombits(bo.Uint64(raw[:8])), nil
}

func decodeTime(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("time: need 4 bytes, got %d", len(raw))
	}
	return int32(bo.Uint32(raw[:4])), nil
}

func decodeTOD(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("tod: need 4 bytes, got %d", len(raw))
	}
	return int32(bo.Uint32(raw[:4])), nil
}

// decodeString 解码 S7 STRING：raw[0]=maxLen, raw[1]=curLen，随后 curLen 个 ASCII 字符。
func decodeString(raw []byte, _ binary.ByteOrder) (any, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("string: need at least 2 header bytes, got %d", len(raw))
	}
	maxLen := int(raw[0])
	curLen := int(raw[1])
	// 防御：curLen 非法时收敛到合理范围
	if curLen > maxLen {
		curLen = maxLen
	}
	if curLen > len(raw)-2 {
		curLen = len(raw) - 2
	}
	if curLen < 0 {
		curLen = 0
	}
	return string(raw[2 : 2+curLen]), nil
}

// decodeS5Time 解码 S7 S5TIME（2 字节 BCD，bit12-13 为时基）。
// 值 = BCD 数 × 时基（00=10ms, 01=100ms, 10=1s, 11=10s），返回毫秒数。
func decodeS5Time(raw []byte, _ binary.ByteOrder) (any, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("s5time: need 2 bytes, got %d", len(raw))
	}
	t := decodeBCD(raw[0]&0x0F)*100 + decodeBCD(raw[1])
	switch raw[0] & 0x30 {
	case 0x10:
		t *= 100
	case 0x20:
		t *= 1000
	case 0x30:
		t *= 10000
	default:
		t *= 10
	}
	return int64(t), nil
}

// decodeDate 解码 S7 DATE（2 字节，天自 1990-01-01）。
func decodeDate(raw []byte, bo binary.ByteOrder) (any, error) {
	if len(raw) < 2 {
		return nil, fmt.Errorf("date: need 2 bytes, got %d", len(raw))
	}
	days := int16(bo.Uint16(raw[:2]))
	return time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(days)), nil
}

// decodeDT 解码 S7 DATE_AND_TIME（8 字节 BCD）。
// 年偏移：<90 → 2000+，否则 1900+；毫秒取字节 6 的 BCD + 字节 7 高半字节。
func decodeDT(raw []byte, _ binary.ByteOrder) (any, error) {
	if len(raw) < 8 {
		return nil, fmt.Errorf("dt: need 8 bytes, got %d", len(raw))
	}
	year := decodeBCD(raw[0])
	if year < 90 {
		year += 2000
	} else {
		year += 1900
	}
	month := decodeBCD(raw[1])
	day := decodeBCD(raw[2])
	hour := decodeBCD(raw[3])
	min := decodeBCD(raw[4])
	sec := decodeBCD(raw[5])
	msec := decodeBCD(raw[6])*10 + decodeBCD(raw[7]>>4)
	return time.Date(year, time.Month(month), day, hour, min, sec, msec*1000000, time.UTC), nil
}

// ==================== 编码函数 ====================
// 供未来写入支持使用；STRING/S5TIME/DATE/DT 的复杂编码暂未实现。

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

func encodeByte(val any, _ binary.ByteOrder) ([]byte, error) {
	v, err := toUint64(val)
	if err != nil {
		return nil, fmt.Errorf("byte: %w", err)
	}
	return []byte{byte(v)}, nil
}

func encodeChar(val any, _ binary.ByteOrder) ([]byte, error) {
	b, ok := val.(byte)
	if !ok {
		if s, ok := val.(string); ok && len(s) > 0 {
			b = s[0]
		} else {
			return nil, fmt.Errorf("char: unsupported value type %T", val)
		}
	}
	return []byte{b}, nil
}

func encodeWord(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toUint64(val)
	if err != nil {
		return nil, fmt.Errorf("word: %w", err)
	}
	buf := make([]byte, 2)
	bo.PutUint16(buf, uint16(v))
	return buf, nil
}

func encodeInt(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toInt64(val)
	if err != nil {
		return nil, fmt.Errorf("int: %w", err)
	}
	buf := make([]byte, 2)
	bo.PutUint16(buf, uint16(int16(v)))
	return buf, nil
}

func encodeDWord(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toUint64(val)
	if err != nil {
		return nil, fmt.Errorf("dword: %w", err)
	}
	buf := make([]byte, 4)
	bo.PutUint32(buf, uint32(v))
	return buf, nil
}

func encodeDInt(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toInt64(val)
	if err != nil {
		return nil, fmt.Errorf("dint: %w", err)
	}
	buf := make([]byte, 4)
	bo.PutUint32(buf, uint32(int32(v)))
	return buf, nil
}

func encodeReal(val any, bo binary.ByteOrder) ([]byte, error) {
	var v float32
	switch x := val.(type) {
	case float64:
		v = float32(x)
	case float32:
		v = x
	default:
		return nil, fmt.Errorf("real: unsupported value type %T", val)
	}
	buf := make([]byte, 4)
	bo.PutUint32(buf, math.Float32bits(v))
	return buf, nil
}

func encodeLReal(val any, bo binary.ByteOrder) ([]byte, error) {
	var v float64
	switch x := val.(type) {
	case float64:
		v = x
	case float32:
		v = float64(x)
	default:
		return nil, fmt.Errorf("lreal: unsupported value type %T", val)
	}
	buf := make([]byte, 8)
	bo.PutUint64(buf, math.Float64bits(v))
	return buf, nil
}

func encodeTime(val any, bo binary.ByteOrder) ([]byte, error) {
	v, err := toInt64(val)
	if err != nil {
		return nil, fmt.Errorf("time: %w", err)
	}
	buf := make([]byte, 4)
	bo.PutUint32(buf, uint32(int32(v)))
	return buf, nil
}

func encodeTOD(val any, bo binary.ByteOrder) ([]byte, error) {
	return encodeTime(val, bo)
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

func formatChar(val any) string {
	if b, ok := val.(byte); ok {
		if b >= 0x20 && b < 0x7F {
			return string(rune(b))
		}
		return fmt.Sprintf("%d", b)
	}
	return fmt.Sprintf("%v", val)
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

func formatTOD(val any) string {
	var ms int64
	switch v := val.(type) {
	case int32:
		ms = int64(v)
	case int64:
		ms = v
	case float64:
		ms = int64(v)
	default:
		return fmt.Sprintf("%v", val)
	}
	if ms < 0 {
		ms = 0
	}
	total := ms / 1000
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func formatDate(val any) string {
	if t, ok := val.(time.Time); ok {
		return t.Format("2006-01-02")
	}
	return fmt.Sprintf("%v", val)
}

func formatDT(val any) string {
	if t, ok := val.(time.Time); ok {
		return t.Format("2006-01-02 15:04:05")
	}
	return fmt.Sprintf("%v", val)
}

func formatDefault(val any) string {
	return fmt.Sprintf("%v", val)
}

// ==================== 工具函数 ====================

// decodeBCD 将单字节 BCD 转为十进制（0x12 → 12）。
func decodeBCD(b byte) int {
	return int(b>>4)*10 + int(b&0x0F)
}

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
