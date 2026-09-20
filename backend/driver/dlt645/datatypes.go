// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"encoding/binary"
	"strings"
	"sync"

	"iot-gateway/driver"
)

// protocolName DL/T 645 类型注册 scope 前缀（与 driver.dataTypeMap 的 dlt645 scope 一致）。
const protocolName = "dlt645"

// dltScope 协议类型注册表作用域缓存（protocolName 为常量，作用域前缀固定）。
var dltScope = driver.GetTypeRegistry().ForProtocol(protocolName)

// dltTypeMeta 类型元数据静态缓存条目。
type dltTypeMeta struct {
	kind string
	dt   *driver.DataType
}

var (
	dltMetaOnce sync.Once
	dltMetaMap  map[string]dltTypeMeta
)

// dltMeta 返回协议内类型元数据静态缓存（键为内部裸名，如 "float"）。
// 类型注册表在 init() 后不再变化，据此消除每点位对全局注册表 RWMutex 的查询与锁竞争。
func dltMeta() map[string]dltTypeMeta {
	dltMetaOnce.Do(func() {
		m := make(map[string]dltTypeMeta)
		prefix := protocolName + "."
		for _, full := range driver.GetTypeRegistry().Names() {
			bare, ok := strings.CutPrefix(full, prefix)
			if !ok {
				continue
			}
			dt, ok := dltScope.Get(bare)
			if !ok {
				continue
			}
			m[bare] = dltTypeMeta{kind: dt.Kind, dt: dt}
		}
		dltMetaMap = m
	})
	return dltMetaMap
}

// dltLookup 按裸名查 DataType：优先命中静态缓存，未命中回退注册表查询
// （兼容大小写变体，保持不区分大小写语义）。
func dltLookup(name string) (*driver.DataType, bool) {
	if meta, ok := dltMeta()[name]; ok {
		return meta.dt, true
	}
	return dltScope.Get(name)
}

// typeKind 解析(裸)内部类型名对应的类别 Kind，供构建 ReadResult 时填充。
// 未注册类型返回 ""（推送通道回退为数值推断）。
func typeKind(name string) string {
	if meta, ok := dltMeta()[name]; ok {
		return meta.kind
	}
	return dltScope.KindOf(name)
}

// 重要：本包不注册任何全局别名！
// 所有 DL/T 645 类型仅通过 ForProtocol("dlt645") 作用域注册，
// 配置层通用类型名统一由 dataTypeMap（driver.LookupDataType）映射为内部裸名。
//
// ========== 关于 Size 与 Decode 的重要说明 ==========
//
// 所有类型 Size 均为 0，因为 DL/T 645 的数据长度由**数据标识 DI** 决定，
// 与通用数据类型无关（见 dictionary.go / address.go）。
//
// 因此注册表里的 Decode/Format 只提供「无规格上下文」的缺省解码（BCD、无符号、
// 0 位小数），仅用于注册表自洽；实际读取路径一律走 decode.go 的
// DecodeValue/FormatValue —— 它们持有 DISpec，是解码的唯一事实来源。
func init() {
	dlt := driver.GetTypeRegistry().ForProtocol(protocolName)

	// 布尔：数值非零判定（常用于电表运行状态字等位域的整体判断）
	dlt.Register(&driver.DataType{
		Name:   "bool",
		Kind:   driver.KindBool,
		Size:   0,
		Decode: decodeDefaultBool,
		Format: formatBool,
	})

	// 有符号整数（不应用 DI 小数位，输出原始整数值）
	dlt.Register(&driver.DataType{
		Name:   "int",
		Kind:   driver.KindInt,
		Size:   0,
		Decode: decodeDefaultInt,
		Format: formatAny,
	})

	// 无符号整数（不应用 DI 小数位）
	dlt.Register(&driver.DataType{
		Name:   "uint",
		Kind:   driver.KindUInt,
		Size:   0,
		Decode: decodeDefaultUint,
		Format: formatAny,
	})

	// 工程值（应用 DI 小数位，输出定标后的小数）
	dlt.Register(&driver.DataType{
		Name:   "float",
		Kind:   driver.KindFloat,
		Size:   0,
		Decode: decodeDefaultFloat,
		Format: formatAny,
	})

	// BCD / 长 BCD：输出 BCD 数字串对应的整数（不应用小数位）。
	// 645 的 BCD 长度可变，两者语义一致，保留两个名字只为让配置层
	// 的 BCD / LBCD 两个通用类型都能解析到已注册的内部类型。
	dlt.Register(&driver.DataType{
		Name:   "bcd",
		Kind:   driver.KindUInt,
		Size:   0,
		Decode: decodeDefaultUint,
		Format: formatAny,
	})
	dlt.Register(&driver.DataType{
		Name:   "lbcd",
		Kind:   driver.KindUInt,
		Size:   0,
		Decode: decodeDefaultUint,
		Format: formatAny,
	})

	// 文本：BCD 数字串（表号、通信地址）或 ASCII（二进制编码时）
	dlt.Register(&driver.DataType{
		Name:   "string",
		Kind:   driver.KindString,
		Size:   0,
		Decode: decodeDefaultString,
		Format: formatAny,
	})

	// 日期时间：按 DI 的字节布局解码为 "YYYY-MM-DD HH:MM:SS"
	dlt.Register(&driver.DataType{
		Name:   "datetime",
		Kind:   driver.KindTime,
		Size:   0,
		Decode: decodeDefaultDateTime,
		Format: formatAny,
	})
}

// ==================== 缺省解码（无 DISpec 上下文） ====================
// 仅用于注册表自洽，读取路径不使用。

func decodeDefaultBool(raw []byte, _ binary.ByteOrder) (any, error) {
	v, err := bcdValue(raw, false)
	if err != nil {
		return false, err
	}
	return v != 0, nil
}

func decodeDefaultInt(raw []byte, _ binary.ByteOrder) (any, error) {
	return bcdValue(raw, true)
}

func decodeDefaultUint(raw []byte, _ binary.ByteOrder) (any, error) {
	return bcdValue(raw, false)
}

func decodeDefaultFloat(raw []byte, _ binary.ByteOrder) (any, error) {
	v, err := bcdValue(raw, false)
	if err != nil {
		return nil, err
	}
	return scaledValue{raw: v, decimals: 0}, nil
}

func decodeDefaultString(raw []byte, _ binary.ByteOrder) (any, error) {
	return bcdString(raw)
}

func decodeDefaultDateTime(raw []byte, _ binary.ByteOrder) (any, error) {
	switch len(raw) {
	case 3:
		return decodeTimeLayout(raw)
	case 4:
		return decodeDateLayout(raw)
	default:
		return bcdString(raw)
	}
}

// 格式化统一由 decode.go 的 formatAny / formatBool 提供（另见 FormatValue）。
