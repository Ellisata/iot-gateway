// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcuaserver

import (
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"

	"iot-gateway/driver"
)

// valueCodec 描述一个点位的「OPC UA 数据类型 + 字符串值解析」。
//
// 节点的 DataType 在建树时就由 device_address.common_data_type 定死，
// 之后采集到的 CollectedRecord.Value（字符串）必须解析成同一种 Go 类型，
// 否则 Variant 的类型会和节点的 DataType 不一致，客户端会看到错值。
type valueCodec struct {
	typeID uint32
	parse  func(string) (any, error)
	zero   func() any
}

// codecFor 按通用数据类型选择编码器；commonDataType 为空或不认识时
// 回落到 driver.KindXxx，再不行按字符串暴露（永不失败）。
func codecFor(commonDataType, kind string) valueCodec {
	switch strings.ToLower(strings.TrimSpace(commonDataType)) {
	case "boolean", "bool":
		return boolCodec()
	case "byte", "uint8", "usint":
		return uintCodec(8, id.Byte, func(v uint64) any { return uint8(v) })
	case "char", "string":
		return stringCodec()
	case "short", "int16", "sint":
		return intCodec(16, id.Int16, func(v int64) any { return int16(v) })
	case "word", "uint16", "uint":
		return uintCodec(16, id.UInt16, func(v uint64) any { return uint16(v) })
	case "long", "int32", "dint":
		return intCodec(32, id.Int32, func(v int64) any { return int32(v) })
	case "dword", "uint32", "udint":
		return uintCodec(32, id.UInt32, func(v uint64) any { return uint32(v) })
	case "int64", "lint":
		return intCodec(64, id.Int64, func(v int64) any { return v })
	case "uint64", "ulint":
		return uintCodec(64, id.UInt64, func(v uint64) any { return v })
	case "float", "float32", "real":
		return floatCodec(32, id.Float, func(v float64) any { return float32(v) })
	case "double", "float64", "lreal":
		return floatCodec(64, id.Double, func(v float64) any { return v })
	case "date", "datetime", "time":
		return dateCodec()
	case "bcd", "lbcd":
		// BCD 是编码过的十进制数字，按原串暴露比强行转整数更安全。
		return stringCodec()
	}

	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "bool":
		return boolCodec()
	case "int":
		return intCodec(64, id.Int64, func(v int64) any { return v })
	case "uint":
		return uintCodec(64, id.UInt64, func(v uint64) any { return v })
	case "float":
		return floatCodec(64, id.Double, func(v float64) any { return v })
	case "time":
		return dateCodec()
	case "string":
		return stringCodec()
	}
	return stringCodec()
}

func boolCodec() valueCodec {
	return valueCodec{
		typeID: id.Boolean,
		parse: func(s string) (any, error) {
			v, err := strconv.ParseBool(strings.TrimSpace(s))
			return v, err
		},
		zero: func() any { return false },
	}
}

func stringCodec() valueCodec {
	return valueCodec{
		typeID: id.String,
		parse:  func(s string) (any, error) { return s, nil },
		zero:   func() any { return "" },
	}
}

func intCodec(bits int, typeID uint32, conv func(int64) any) valueCodec {
	return valueCodec{
		typeID: typeID,
		parse: func(s string) (any, error) {
			v, err := strconv.ParseInt(strings.TrimSpace(s), 10, bits)
			if err != nil {
				return nil, err
			}
			return conv(v), nil
		},
		zero: func() any { return conv(0) },
	}
}

func uintCodec(bits int, typeID uint32, conv func(uint64) any) valueCodec {
	return valueCodec{
		typeID: typeID,
		parse: func(s string) (any, error) {
			v, err := strconv.ParseUint(strings.TrimSpace(s), 10, bits)
			if err != nil {
				return nil, err
			}
			return conv(v), nil
		},
		zero: func() any { return conv(0) },
	}
}

func floatCodec(bits int, typeID uint32, conv func(float64) any) valueCodec {
	return valueCodec{
		typeID: typeID,
		parse: func(s string) (any, error) {
			v, err := strconv.ParseFloat(strings.TrimSpace(s), bits)
			if err != nil {
				return nil, err
			}
			return conv(v), nil
		},
		zero: func() any { return conv(0) },
	}
}

func dateCodec() valueCodec {
	return valueCodec{
		typeID: id.DateTime,
		parse: func(s string) (any, error) {
			return parseTime(s)
		},
		zero: func() any { return time.Time{} },
	}
}

var timeLayouts = []string{
	"2006-01-02 15:04:05.000",
	"2006-01-02 15:04:05",
	time.RFC3339Nano,
	time.RFC3339,
}

// parseTime 解析采集时间；带时区的写法按自身时区，其余按本机时区。
func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, strconv.ErrSyntax
	}
	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, strconv.ErrSyntax
}

// parseCollectedAt 解析批次采集时间；无法解析时回落到当前时间。
// CollectedAt 语义见 collector.CollectedRecord：空表示「按批次落地时间」。
func parseCollectedAt(s string) time.Time {
	if t, err := parseTime(s); err == nil {
		return t
	}
	return time.Now()
}

// qualityStatus 把驱动质量码翻译成 OPC UA StatusCode。
//
// 质量码的布局是「类别在高 2 位、成因在低 6 位」（driver/types.go），
// 所以未知子码先按类别兜底，再具体匹配已知成因。
func qualityStatus(q int) ua.StatusCode {
	switch q {
	case driver.QualityGood:
		return ua.StatusGood
	case driver.QualityUncertain:
		return ua.StatusUncertain
	case driver.QualityBadAddress:
		return ua.StatusBadConfigurationError
	case driver.QualityBadComms:
		return ua.StatusBadCommunicationError
	case driver.QualityBadParse:
		return ua.StatusBadDataEncodingInvalid
	case driver.QualityBad:
		return ua.StatusBadInternalError
	}
	switch q & 0xC0 {
	case driver.QualityGood:
		return ua.StatusGood
	case driver.QualityUncertain:
		return ua.StatusUncertain
	default:
		return ua.StatusBadInternalError
	}
}

// buildDataValue 把一条采集记录转成节点的 DataValue。
//
// 解析失败时优先保留上一个值（prev），没有上一个值就退化为该类型的零值：
// 节点类型不可变，放进一个字符串会让客户端拿到类型错误的值。
func buildDataValue(c valueCodec, raw string, prev *ua.DataValue, quality int, at time.Time) *ua.DataValue {
	status := qualityStatus(quality)
	v, err := c.parse(raw)
	if err != nil {
		if status == ua.StatusGood || status == ua.StatusUncertain {
			status = ua.StatusBadDataEncodingInvalid
		}
		v = nil
		if prev != nil && prev.Value != nil {
			v = prev.Value.Value()
		}
		if v == nil {
			v = c.zero()
		}
	}
	return &ua.DataValue{
		EncodingMask:    ua.DataValueValue | ua.DataValueStatusCode | ua.DataValueSourceTimestamp | ua.DataValueServerTimestamp,
		Value:           ua.MustVariant(v),
		Status:          status,
		SourceTimestamp: at,
		ServerTimestamp: time.Now(),
	}
}

// sameDataValue 判断两个 DataValue 的「值 + 状态」是否相同。
// 时间戳不参与比较：OPC UA 的数据变化通知本来就忽略仅时间戳变化。
func sameDataValue(a, b *ua.DataValue) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Status != b.Status {
		return false
	}
	if a.Value == nil || b.Value == nil {
		return a.Value == nil && b.Value == nil
	}
	return reflect.DeepEqual(a.Value.Value(), b.Value.Value())
}
