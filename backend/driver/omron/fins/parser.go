package fins

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// ParseFINSValue 将读取到的原始字节解码为指定类型的 Go 值。
//
// raw 为该点位所在的字数据（每字 2 字节，FINS 线上大端）。
// 位点位（bool，地址携带 .bit）先从所在字中提取该位为 1 字节再走 bool 解码。
//
// byteOrder 控制字内字节序（默认大端）；wordOrder 控制 32/64 位值的字序
// （BIG_ENDIAN=高字在前，LITTLE_ENDIAN=低字在前）。
func ParseFINSValue(raw []byte, addr FINSAddress, dataType string, cfg *FINSConfig) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("fins parser: empty raw data")
	}

	dt, ok := finsLookup(dataType)
	if !ok {
		return nil, fmt.Errorf("fins parser: unsupported data type: %q", dataType)
	}

	order := parseByteOrder(cfg.ByteOrder)
	swapWords := strings.ToUpper(cfg.WordOrder) == WordOrderLittleEndian

	// 位点位：从 2 字节字中提取所在位。
	// FINS 位 0..7 位于低字节（raw[1]），位 8..15 位于高字节（raw[0]），
	// 字内位序 LSB-first。
	if addr.IsBit() {
		if len(raw) < 2 {
			return nil, fmt.Errorf("fins parser: bool needs a word (2 bytes), got %d", len(raw))
		}
		byteIdx := 0
		if addr.Bit < 8 {
			byteIdx = 1
		}
		bit := (raw[byteIdx] >> uint(addr.Bit%8)) & 0x01
		return dt.Decode([]byte{bit}, order)
	}

	// 动态长度类型（STRING）：整体传给解码函数
	if dt.Size == 0 {
		return dt.Decode(raw, order)
	}

	// 常规类型：截取类型大小字节。
	// 单字节类型（uint8/int8，及无位后缀的 bool）取字的低字节——
	// FINS 字节数据通常存于 16 位字的低字节。
	if len(raw) < dt.Size {
		return nil, fmt.Errorf("fins parser: %s needs %d bytes, got %d", dataType, dt.Size, len(raw))
	}
	data := raw[:dt.Size]
	if dt.Size == 1 {
		data = raw[len(raw)-1:]
	}

	// 32/64 位类型：按字序交换（相邻 16 位字对交换）
	switch dt.Size {
	case 4:
		data = swapWords32(data, swapWords)
	case 8:
		data = swapWords64(data, swapWords)
	}

	return dt.Decode(data, order)
}

// FormatFINSValue 将解码后的值格式化为字符串（用于存储和展示）。
func FormatFINSValue(v any, dataType string) string {
	dt, ok := finsLookup(dataType)
	if !ok || dt.Format == nil {
		return fmt.Sprintf("%v", v)
	}
	return dt.Format(v)
}

// swapWords32 对 4 字节数据进行字序交换（如果需要）。
// 输入 [A1 A2 B1 B2]，大端字序保持，小端字序交换为 [B1 B2 A1 A2]。
func swapWords32(data []byte, swap bool) []byte {
	if !swap || len(data) < 4 {
		return data
	}
	swapped := make([]byte, 4)
	copy(swapped[:2], data[2:4])
	copy(swapped[2:4], data[:2])
	return swapped
}

// swapWords64 对 8 字节数据进行字序交换（4 个相邻字两两交换）：
//
//	大端字序保持 [R1 R2 R3 R4]，小端字序交换为 [R2 R1 R4 R3]。
func swapWords64(data []byte, swap bool) []byte {
	if !swap || len(data) < 8 {
		return data
	}
	swapped := make([]byte, 8)
	copy(swapped[:2], data[2:4])
	copy(swapped[2:4], data[:2])
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
