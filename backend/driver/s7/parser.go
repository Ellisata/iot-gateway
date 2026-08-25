package s7

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"iot-gateway/driver"
)

// maxOffset 24 位地址上限（S7 地址字段为 3 字节）
const maxOffset = 0xFFFFFF

// maxDB DB 号上限（协议中为 16 位）
const maxDB = 65535

// ParseS7Address 解析 S7 地址字符串（大小写不敏感）。
//
// 支持的格式（TrimSpace + ToUpper 后解析）：
//
//	DB 区:  DB{n}.DBX{byte}.{bit}   (BOOL，位 0..7)
//	        DB{n}.DBB{byte}         (BYTE)
//	        DB{n}.DBW{byte}         (WORD / INT，2 字节)
//	        DB{n}.DBD{byte}         (DWORD / DINT / REAL，4 字节)
//	        DB{n}.STRING{byte}      (STRING，长度取配置默认)
//	        DB{n}.STRING{byte}.{len} (STRING，显式长度)
//	M 区:   M{byte}.{bit} / MB{byte} / MW{byte} / MD{byte}
//	I 区:   I{byte}.{bit} / IB{byte} / IW{byte} / ID{byte}
//	Q 区:   Q{byte}.{bit} / QB{byte} / QW{byte} / QD{byte}
//
// 返回的 S7Address 含结构信息（Span 为 0，由 CalcS7Ranges 计算填充）。
func ParseS7Address(name string) (S7Address, bool) {
	s := strings.ToUpper(strings.TrimSpace(name))
	if s == "" {
		return S7Address{}, false
	}

	// DB 区
	if strings.HasPrefix(s, "DB") {
		return parseDBAddress(s)
	}

	// M/I/Q 区
	switch s[0] {
	case 'M':
		return parseFlagAddress(s, S7AreaM)
	case 'I':
		return parseFlagAddress(s, S7AreaI)
	case 'Q':
		return parseFlagAddress(s, S7AreaQ)
	}

	return S7Address{}, false
}

// parseDBAddress 解析 DB 区地址：DB{n}.DBX/DBB/DBW/DBD/STRING...
func parseDBAddress(s string) (S7Address, bool) {
	// s = "DB<db>.<rest>"
	dot := strings.IndexByte(s, '.')
	if dot <= 2 { // "DB" 后必须有至少 1 位数字
		return S7Address{}, false
	}

	db, err := strconv.Atoi(s[2:dot])
	if err != nil || db <= 0 || db > maxDB {
		return S7Address{}, false
	}

	rest := s[dot+1:]
	// 内层标识：DBX/DBB/DBW/DBD 带 "DB" 前缀，STRING 不带
	var body string
	if strings.HasPrefix(rest, "DB") {
		body = rest[2:]
	} else {
		body = rest
	}

	addr := S7Address{Area: S7AreaDB, DB: db, BitOffset: -1}

	switch {
	case strings.HasPrefix(body, "X"):
		// DBX<byte>.<bit>
		return parseBitSuffix(body[1:], &addr)
	case strings.HasPrefix(body, "STRING"):
		// STRING<byte>[.len]
		return parseStringSuffix(body[len("STRING"):], &addr)
	default:
		// DBB / DBW / DBD<byte>
		var width int
		switch body[0] {
		case 'B':
			width = 1
		case 'W':
			width = 2
		case 'D':
			width = 4
		default:
			return S7Address{}, false
		}
		off, err := parseOffset(body[1:])
		if err != nil {
			return S7Address{}, false
		}
		addr.ByteOffset = off
		addr.Width = width
		return addr, true
	}
}

// parseFlagAddress 解析 M/I/Q 区地址：M{byte}.{bit} / MB/MW/MD{byte}
func parseFlagAddress(s string, area s7Area) (S7Address, bool) {
	addr := S7Address{Area: area, BitOffset: -1}
	body := s[1:] // 去掉 M/I/Q 前缀
	if body == "" {
		return S7Address{}, false
	}

	// 宽度后缀 B/W/D
	if body[0] == 'B' || body[0] == 'W' || body[0] == 'D' {
		var width int
		switch body[0] {
		case 'B':
			width = 1
		case 'W':
			width = 2
		case 'D':
			width = 4
		}
		off, err := parseOffset(body[1:])
		if err != nil {
			return S7Address{}, false
		}
		addr.ByteOffset = off
		addr.Width = width
		return addr, true
	}

	// 位格式 "<byte>.<bit>"
	return parseBitSuffix(body, &addr)
}

// parseBitSuffix 解析位地址后缀："<byte>.<bit>"，Span=1（所在字节）
func parseBitSuffix(body string, addr *S7Address) (S7Address, bool) {
	dot := strings.IndexByte(body, '.')
	if dot <= 0 {
		return S7Address{}, false
	}

	off, err := parseOffset(body[:dot])
	if err != nil {
		return S7Address{}, false
	}
	bit, err := strconv.Atoi(body[dot+1:])
	if err != nil || bit < 0 || bit > 7 {
		return S7Address{}, false
	}

	addr.ByteOffset = off
	addr.BitOffset = bit
	addr.Width = 0
	return *addr, true
}

// parseStringSuffix 解析 STRING 后缀："<byte>" 或 "<byte>.<len>"
func parseStringSuffix(body string, addr *S7Address) (S7Address, bool) {
	dot := strings.IndexByte(body, '.')
	var off int
	var explicitLen int
	var err error

	if dot >= 0 {
		off, err = parseOffset(body[:dot])
		if err != nil {
			return S7Address{}, false
		}
		l, e := strconv.Atoi(body[dot+1:])
		if e != nil || l <= 0 || l > maxStringLen {
			return S7Address{}, false
		}
		explicitLen = l
	} else {
		off, err = parseOffset(body)
		if err != nil {
			return S7Address{}, false
		}
	}

	addr.ByteOffset = off
	addr.IsString = true
	addr.StringLen = explicitLen
	addr.Width = 0
	return *addr, true
}

// parseOffset 解析字节偏移，限制为 24 位非负整数
func parseOffset(s string) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid byte offset %q", s)
	}
	if v > maxOffset {
		return 0, fmt.Errorf("byte offset %d exceeds 24-bit limit", v)
	}
	return v, nil
}

// ParseS7Value 将读取到的原始字节解码为指定类型的 Go 值。
//
// raw 已按地址跨度切好（至少含该点位的 Span 字节）。
// 位类型先提取所在位为 1 字节再走 bool 解码。
func ParseS7Value(raw []byte, addr S7Address, dataType string) (any, error) {
	s7 := driver.GetTypeRegistry().ForProtocol(protocolName)
	dt, ok := s7.Get(dataType)
	if !ok {
		return nil, fmt.Errorf("s7 parser: unsupported data type: %q", dataType)
	}

	// 位地址：提取所在位
	if addr.IsBit() {
		if len(raw) < 1 {
			return nil, fmt.Errorf("s7 parser: bool needs 1 byte, got %d", len(raw))
		}
		bit := (raw[0] >> uint(addr.BitOffset)) & 0x01
		return dt.Decode([]byte{bit}, binary.BigEndian)
	}

	if len(raw) == 0 {
		return nil, fmt.Errorf("s7 parser: empty raw data")
	}

	// 动态长度类型（STRING）：整体传给解码函数
	if dt.Size == 0 {
		return dt.Decode(raw, binary.BigEndian)
	}

	// 常规类型：截取 typeSize 字节
	if len(raw) < dt.Size {
		return nil, fmt.Errorf("s7 parser: %s needs %d bytes, got %d", dataType, dt.Size, len(raw))
	}
	return dt.Decode(raw[:dt.Size], binary.BigEndian)
}

// FormatS7Value 将解码后的值格式化为字符串（用于存储和展示）。
func FormatS7Value(v any, dataType string) string {
	s7 := driver.GetTypeRegistry().ForProtocol(protocolName)
	dt, ok := s7.Get(dataType)
	if !ok || dt.Format == nil {
		return fmt.Sprintf("%v", v)
	}
	return dt.Format(v)
}
