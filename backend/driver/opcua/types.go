// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"iot-gateway/driver"
)

// protocolName OPC UA 协议名称（类型注册作用域前缀）。
const protocolName = "opcua"

// opcuaScope 类型注册表作用域缓存。
var opcuaScope = driver.GetTypeRegistry().ForProtocol(protocolName)

var (
	opcuaLookupOnce sync.Once
	opcuaTypes      map[string]*driver.DataType
)

// opcuaLookup 按裸名查 DataType：优先命中静态缓存（零分配、无锁），
// 未命中回退注册表查询。类型注册表在 init() 后不再变化，按裸名缓存
// 可消除每点位对全局注册表 RWMutex 的查询与锁竞争（对齐 s7 模式）。
func opcuaLookup(name string) (*driver.DataType, bool) {
	opcuaLookupOnce.Do(func() {
		m := make(map[string]*driver.DataType)
		prefix := protocolName + "."
		for _, full := range driver.GetTypeRegistry().Names() {
			bare, ok := strings.CutPrefix(full, prefix)
			if !ok {
				continue
			}
			dt, ok := opcuaScope.Get(bare)
			if !ok {
				continue
			}
			m[bare] = dt
		}
		opcuaTypes = m
	})
	if dt, ok := opcuaTypes[name]; ok {
		return dt, true
	}
	return opcuaScope.Get(name)
}

func init() {
	// ========== 注册 OPC UA 数据类型（自动添加 "opcua." 前缀） ==========
	//
	// OPC UA 服务器在 Read 响应中以原生值（bool/int/float/string/...）返回，
	// 无需字节级 Decode/Encode；Kind 为推送通道分类的唯一依据。
	reg := func(name, kind string) {
		opcuaScope.Register(&driver.DataType{Name: name, Kind: kind})
	}

	reg("bool", driver.KindBool)
	reg("byte", driver.KindUInt)
	reg("char", driver.KindInt)
	reg("int16", driver.KindInt)
	reg("uint16", driver.KindUInt)
	reg("int32", driver.KindInt)
	reg("uint32", driver.KindUInt)
	reg("int64", driver.KindInt)
	reg("uint64", driver.KindUInt)
	reg("float32", driver.KindFloat)
	reg("float64", driver.KindFloat)
	reg("string", driver.KindString)
	reg("byteString", driver.KindString)
	reg("datetime", driver.KindTime)
}

// typeKind 解析(裸)内部类型名对应的类别 Kind，供构建 ReadResult 时填充。
// 未注册类型返回 ""（驱动回退为按实际值推断，见 kindFor）。
func typeKind(name string) string {
	if dt, ok := opcuaLookup(name); ok {
		return dt.Kind
	}
	return ""
}

// kindFor 返回点位应使用的 Kind：优先按配置 DataType 解析；
// 配置类型未注册时回退为按实际值 Go 类型推断。
func kindFor(dataType string, val any) string {
	if k := typeKind(dataType); k != "" {
		return k
	}
	return kindFromVariant(val)
}

// kindFromVariant 按实际值 Go 类型推断 Kind（仅作配置类型未知时的回退）。
// 数组取首元素类型；空数组/未知类型按字符串处理。
func kindFromVariant(v any) string {
	if v == nil {
		return driver.KindString
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		if rv.Len() == 0 {
			return driver.KindString
		}
		return kindFromVariant(rv.Index(0).Interface())
	}

	switch v.(type) {
	case bool:
		return driver.KindBool
	case int, int8, int16, int32, int64:
		return driver.KindInt
	case uint, uint8, uint16, uint32, uint64:
		return driver.KindUInt
	case float32, float64:
		return driver.KindFloat
	case time.Time:
		return driver.KindTime
	case string, []byte:
		return driver.KindString
	default:
		return driver.KindString
	}
}

// formatVariant 将 OPC UA 服务器返回的原生值格式化为字符串（用于存储与展示）。
//
// 格式约定与推送通道解析保持一致：
//   - bool → "1"/"0"（push 通道 strconv.ParseBool 均接受）；
//   - 整数 → 十进制；float32/float64 → 'g' 最短往返表示（永不出现无谓精度损失）；
//   - string → 原样；time.Time → "2006-01-02 15:04:05"；
//   - []byte → hex 小写；数组/切片 → 逗号拼接；
//   - 其余（NodeID/LocalizedText/...）→ fmt.Sprintf（其 String() 实现）。
func formatVariant(v any) string {
	if v == nil {
		return ""
	}
	// ByteString 特判：hex 而非字节数字拼接
	if b, ok := v.([]byte); ok {
		return hex.EncodeToString(b)
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		parts := make([]string, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			parts = append(parts, formatVariant(rv.Index(i).Interface()))
		}
		return strings.Join(parts, ",")
	}

	switch x := v.(type) {
	case bool:
		if x {
			return "1"
		}
		return "0"
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint8:
		return strconv.FormatUint(uint64(x), 10)
	case uint16:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case string:
		return x
	case time.Time:
		return x.Format("2006-01-02 15:04:05")
	default:
		return fmt.Sprintf("%v", x)
	}
}
