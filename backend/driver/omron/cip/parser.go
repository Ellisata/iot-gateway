package cip

import (
	"encoding/binary"
	"fmt"

	"iot-gateway/driver"
)

// ParseCIPValue 将 Data Table Read 响应的原始字节解码为指定类型的 Go 值。
//
// raw 为 0x4C 响应的数据区（已去掉响应头与数据类型码回显）。
// CIP 数据原生小端；无位地址、无字序交换逻辑（对比 FINS parser.go）。
func ParseCIPValue(raw []byte, dataType string, cfg *CIPConfig) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("cip parser: empty raw data")
	}

	f := driver.GetTypeRegistry().ForProtocol(protocolName)
	dt, ok := f.Get(dataType)
	if !ok {
		return nil, fmt.Errorf("cip parser: unsupported data type: %q", dataType)
	}

	// 动态长度类型（STRING）：按 stringLen 截断后整体解码，尾部 \x00 由解码函数去除
	if dt.Size == 0 {
		n := cfg.StringLen
		if n <= 0 {
			n = defaultStringLen
		}
		if len(raw) > n {
			raw = raw[:n]
		}
		return dt.Decode(raw, binary.LittleEndian)
	}

	// 常规类型：截取类型大小字节
	if len(raw) < dt.Size {
		return nil, fmt.Errorf("cip parser: %s needs %d bytes, got %d", dataType, dt.Size, len(raw))
	}
	return dt.Decode(raw[:dt.Size], binary.LittleEndian)
}

// FormatCIPValue 将解码后的值格式化为字符串（用于存储和展示）。
func FormatCIPValue(v any, dataType string) string {
	f := driver.GetTypeRegistry().ForProtocol(protocolName)
	dt, ok := f.Get(dataType)
	if !ok || dt.Format == nil {
		return fmt.Sprintf("%v", v)
	}
	return dt.Format(v)
}
