package modbus

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"

	"iot-gateway/driver"
)

// ParseReadResult 将 Modbus 原始字节解析为指定类型的值。
//
// 参数：
//
//	raw:      从 Modbus 读取的原始字节
//	dataType: 目标数据类型（通过 TypeRegistry 查找，支持别名、裸名、协议前缀名）
//	byteOrder: 字节序（BIG_ENDIAN / LITTLE_ENDIAN）
//	wordOrder: 字序（仅对 32/64 位有效，BIG_ENDIAN=高字在前, LITTLE_ENDIAN=低字在前）
//
//	Modbus 协议传输时是大端字节序，但不同 PLC 内部表示可能不同。
//	byteOrder 控制字节级别的顺序，wordOrder 控制 16 位字级别的顺序。
func ParseReadResult(raw []byte, dataType, byteOrder, wordOrder string) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("modbus parser: empty raw data")
	}

	mb := driver.GetTypeRegistry().ForProtocol(protocolName)
	dt, ok := mb.Get(dataType)
	if !ok {
		return nil, fmt.Errorf("modbus parser: unsupported data type: %q", dataType)
	}

	order := parseByteOrder(byteOrder)
	swapWords := strings.ToUpper(wordOrder) == ByteOrderLittleEndian

	// ---- Modbus 特有预处理 ----

	switch dt.Name {
	case typeString:
		return parseString(raw, order), nil
	case typeBool:
		return raw[len(raw)-1]&0x01 != 0, nil
	case typeUInt8, typeInt8:
		// 单字节类型直接从低字节读取
	}

	// 32/64 位类型需要字序交换
	var data []byte
	switch dt.Size {
	case 4:
		if len(raw) < 4 {
			return nil, fmt.Errorf("modbus parser: %s needs 4 bytes, got %d", dataType, len(raw))
		}
		data = swapWords32(raw[:4], swapWords)
	case 8:
		if len(raw) < 8 {
			return nil, fmt.Errorf("modbus parser: %s needs 8 bytes, got %d", dataType, len(raw))
		}
		data = swapWords64(raw[:8], swapWords)
	default:
		// 2 字节类型或其它不需要字序处理的类型
		if len(raw) < dt.Size {
			return nil, fmt.Errorf("modbus parser: %s needs %d bytes, got %d", dataType, dt.Size, len(raw))
		}
		data = raw[:dt.Size]
	}

	return dt.Decode(data, order)
}

// FormatValue 将解析后的值格式化为字符串（用于存储和展示）。
// 通过 TypeRegistry 查找类型的格式化函数。
func FormatValue(v any, dataType string) string {
	mb := driver.GetTypeRegistry().ForProtocol(protocolName)
	dt, ok := mb.Get(dataType)
	if !ok || dt.Format == nil {
		return fmt.Sprintf("%v", v)
	}
	return dt.Format(v)
}

// swapWords32 对 4 字节数据进行字序交换（如果需要）
// 输入 [A1 A2 B1 B2]，大端字序保持 [A1 A2 B1 B2]，小端字序交换为 [B1 B2 A1 A2]
func swapWords32(data []byte, swap bool) []byte {
	if !swap || len(data) < 4 {
		return data
	}
	// 小端字序，交换高 16 位和低 16 位
	swapped := make([]byte, 4)
	copy(swapped[:2], data[2:4])
	copy(swapped[2:4], data[:2])
	return swapped
}

// swapWords64 对 8 字节数据进行字序交换
// 4 个寄存器 [R1 R2 R3 R4]：
//
//	大端字序保持 [R1 R2 R3 R4]
//	小端字序交换为 [R2 R1 R4 R3]（相邻字对交换）
func swapWords64(data []byte, swap bool) []byte {
	if !swap || len(data) < 8 {
		return data
	}
	swapped := make([]byte, 8)
	// 交换第 1、2 个字
	copy(swapped[:2], data[2:4])
	copy(swapped[2:4], data[:2])
	// 交换第 3、4 个字
	copy(swapped[4:6], data[6:8])
	copy(swapped[6:8], data[4:6])
	return swapped
}

// parseByteOrder 解析字节序字符串
func parseByteOrder(s string) binary.ByteOrder {
	switch strings.ToUpper(s) {
	case ByteOrderLittleEndian:
		return binary.LittleEndian
	default:
		return binary.BigEndian
	}
}

// EncodeWriteValue 将写入值编码为 Modbus 寄存器字。
// 通过 TypeRegistry 查找类型的编码函数。
func EncodeWriteValue(value interface{}, dataType, byteOrder, wordOrder string) ([]uint16, error) {
	mb := driver.GetTypeRegistry().ForProtocol(protocolName)
	dt, ok := mb.Get(dataType)
	if !ok {
		return nil, fmt.Errorf("modbus parser: unsupported write data type: %q", dataType)
	}

	order := parseByteOrder(byteOrder)
	swapWords := strings.ToUpper(wordOrder) == ByteOrderLittleEndian

	// ---- 特殊处理：2 字节类型直接返回单个寄存器 ----
	if dt.Size == 2 {
		v, ok := value.(float64)
		if !ok {
			return nil, fmt.Errorf("modbus parser: %s value expected number", dataType)
		}
		switch dt.Name {
		case typeUInt16, typeWord:
			return []uint16{uint16(v)}, nil
		case typeInt16:
			return []uint16{uint16(int16(v))}, nil
		default:
			// 通用路径：编码为 2 字节
			b, err := dt.Encode(value, order)
			if err != nil {
				return nil, err
			}
			if len(b) < 2 {
				b = append(b, make([]byte, 2-len(b))...)
			}
			return []uint16{order.Uint16(b[:2])}, nil
		}
	}

	// ---- 32 位类型：编码为 2 个寄存器，支持字序交换 ----
	if dt.Size == 4 {
		fv, ok := value.(float64)
		if !ok {
			return nil, fmt.Errorf("modbus parser: %s value expected number", dataType)
		}

		var bits uint32
		switch dt.Name {
		case typeUInt32:
			bits = uint32(fv)
		case typeInt32:
			bits = uint32(int32(fv))
		case typeFloat:
			bits = math.Float32bits(float32(fv))
		default:
			// 通用路径
			b, err := dt.Encode(value, order)
			if err != nil {
				return nil, err
			}
			bits = order.Uint32(b)
		}

		return uint32ToWords(bits, order, swapWords), nil
	}

	return nil, fmt.Errorf("modbus parser: unsupported write data type size: %s (%d bytes)", dataType, dt.Size)
}

// uint32ToWords 将 uint32 拆分为 2 个 uint16 寄存器
func uint32ToWords(v uint32, order binary.ByteOrder, swapWords bool) []uint16 {
	buf := make([]byte, 4)
	order.PutUint32(buf, v)

	// 字序处理：小端字序时交换高低字
	if swapWords {
		buf[0], buf[1], buf[2], buf[3] = buf[2], buf[3], buf[0], buf[1]
	}

	return []uint16{
		order.Uint16(buf[0:2]),
		order.Uint16(buf[2:4]),
	}
}

// ParsePLCAddress 解析 PLC 风格 Modbus 地址，支持多种常见格式。
//
// 格式按优先级匹配（各格式独立封装为 tryXxx 函数）：
//
//  1. 传统 Modicon "40001", "410001", "30001", "00001", "10001"
//     首位 → functionCode，后续 → 1-based 地址（向后兼容）
//
//  2. 显式前缀   "4x0001", "3x10001", "4x0"
//     'x' 前为功能码标识，后为 1-based 地址
//
//  3. IEC 61131-3 "%MW100", "%IW100", "%MX100"
//     %MW / %IW / %QW / %MX / %IX / %QX 前缀 + 0-based 地址
//
//  4. 十六进制   "0x100", "0xFF"
//     0x 前缀 + 十六进制 0-based 地址，functionCode=0（未知）
//
//  5. 纯数字     "0", "100", "65535"
//     直接作为 0-based 地址偏移量，functionCode=0（未知）
func ParsePLCAddress(name string) (functionCode byte, address uint16, ok bool) {
	s := strings.TrimSpace(name)
	if s == "" {
		return 0, 0, false
	}

	// 1) 传统 Modicon 格式（最精准，携带 functionCode）
	if fc, addr, ok := tryTraditionalFormat(s); ok {
		return fc, addr, true
	}

	// 2) 十六进制 "0x..." — 必须在显式前缀之前，避免 "0x100" 被误匹配为
	//    显式前缀（'0' 是合法的线圈前缀，后面跟着 'x' 和数字）。
	if addr, ok := tryHexFormat(s); ok {
		return 0, addr, true
	}

	// 3) 显式前缀格式 "4x0001"
	if fc, addr, ok := tryExplicitPrefixFormat(s); ok {
		return fc, addr, true
	}

	// 4) IEC 61131-3 格式 "%MW100"
	if fc, addr, ok := tryIECFormat(s); ok {
		return fc, addr, true
	}

	// 5) 纯数字
	if addr, ok := tryPlainNumberFormat(s); ok {
		return 0, addr, true
	}

	return 0, 0, false
}

// tryTraditionalFormat 匹配传统 Modicon 格式：
//
//	"40001"  → FC=3, addr=0
//	"410001" → FC=3, addr=10000
//	"30001"  → FC=4, addr=0
//	"00001"  → FC=1, addr=0
//	"10001"  → FC=2, addr=0
func tryTraditionalFormat(s string) (functionCode byte, address uint16, ok bool) {
	if len(s) < 5 {
		return 0, 0, false
	}

	var fc byte
	switch s[0] {
	case '0':
		fc = FuncCodeReadCoils
	case '1':
		fc = FuncCodeReadDiscreteInputs
	case '3':
		fc = FuncCodeReadInputRegisters
	case '4':
		fc = FuncCodeReadHoldingRegisters
	default:
		return 0, 0, false
	}

	addr, err := strconv.ParseUint(s[1:], 10, 16)
	if err != nil || addr == 0 {
		return 0, 0, false
	}

	return fc, uint16(addr - 1), true
}

// tryExplicitPrefixFormat 匹配显式前缀格式：
//
//	"4x0001"  → FC=3, addr=0
//	"4x10001" → FC=3, addr=10000
//	"3x0001"  → FC=4, addr=0
//	"4x0"     → FC=3, addr=0  (0-based 写法)
func tryExplicitPrefixFormat(s string) (functionCode byte, address uint16, ok bool) {
	if len(s) < 3 || !(s[1] == 'x' || s[1] == 'X') {
		return 0, 0, false
	}

	var fc byte
	switch s[0] {
	case '0':
		fc = FuncCodeReadCoils
	case '1':
		fc = FuncCodeReadDiscreteInputs
	case '3':
		fc = FuncCodeReadInputRegisters
	case '4':
		fc = FuncCodeReadHoldingRegisters
	default:
		return 0, 0, false
	}

	addrStr := s[2:]
	if addrStr == "" {
		return 0, 0, false
	}

	addr, err := strconv.ParseUint(addrStr, 10, 16)
	if err != nil {
		return 0, 0, false
	}

	if addr == 0 {
		return fc, 0, true // "4x0" → address 0（0-based 直接映射）
	}
	return fc, uint16(addr - 1), true
}

// tryIECFormat 匹配 IEC 61131-3 格式：
//
//	"%MW100" → FC=3, addr=100  (Memory Word, 保持寄存器)
//	"%IW100" → FC=4, addr=100  (Input Word, 输入寄存器)
//	"%QW100" → FC=3, addr=100  (Output Word, 保持寄存器)
//	"%MX100" → FC=1, addr=100  (Memory bit, 线圈)
//	"%IX100" → FC=2, addr=100  (Input bit, 离散输入)
//	"%QX100" → FC=1, addr=100  (Output bit, 线圈)
//
// IEC 地址为 0-based，直接映射。
func tryIECFormat(s string) (functionCode byte, address uint16, ok bool) {
	if len(s) < 4 || s[0] != '%' {
		return 0, 0, false
	}

	prefix := strings.ToUpper(s[:3])
	var fc byte
	switch prefix {
	case "%MW": // Memory Word
		fc = FuncCodeReadHoldingRegisters
	case "%IW": // Input Word
		fc = FuncCodeReadInputRegisters
	case "%QW": // Output Word
		fc = FuncCodeReadHoldingRegisters
	case "%MX": // Memory bit (coil)
		fc = FuncCodeReadCoils
	case "%IX": // Input bit
		fc = FuncCodeReadDiscreteInputs
	case "%QX": // Output bit
		fc = FuncCodeReadCoils
	default:
		return 0, 0, false
	}

	numStr := s[3:]
	if numStr == "" {
		return 0, 0, false
	}

	addr, err := strconv.ParseUint(numStr, 10, 16)
	if err != nil {
		return 0, 0, false
	}

	return fc, uint16(addr), true
}

// tryHexFormat 匹配十六进制格式：
//
//	"0x100" → addr=256
//	"0xFF"  → addr=255
//	"0x0"   → addr=0
func tryHexFormat(s string) (address uint16, ok bool) {
	if len(s) < 3 {
		return 0, false
	}

	// 不区分 0x / 0X 前缀
	rest, found := strings.CutPrefix(strings.ToLower(s), "0x")
	if !found || rest == "" {
		return 0, false
	}

	addr, err := strconv.ParseUint(rest, 16, 16)
	if err != nil {
		return 0, false
	}

	return uint16(addr), true
}

// tryPlainNumberFormat 匹配纯数字格式（0-based 地址偏移量）：
//
//	"0"      → addr=0
//	"100"    → addr=100
//	"65535"  → addr=65535
func tryPlainNumberFormat(s string) (address uint16, ok bool) {
	addr, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, false
	}
	return uint16(addr), true
}
