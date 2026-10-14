// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"strings"

	"iot-gateway/driver"
)

// commonTypeMap FINS 通用数据类型 → 内部类型名。
//
// **本文件是 FINS 类型映射的唯一真源**：内置时代它叫 finsTypes、放在
// driver/dataTypeMap.go 里由 driver.LookupDataType 查询；搬迁成插件后
// 随实现一并搬到这里，由 driversdk.Meta.TypeMap 上报网关
// （见 docs/driver-plugins.md §2.5）。driver/dataTypeMap.go 里已不再保留
// FINS 的条目。
//
// FINS 与 Modbus 同为 16 位字寻址、字内大端，类型映射保持一致；
// 四个协议（UDP/TCP/Serial/HostLinkTCP）共用同一套 FINS 类型注册，
// 因此也只有这一张表。
//
// 内容与内置时代逐项一致：改一个 value 就等于改现场点位的解码方式。
var commonTypeMap = map[string]string{
	driver.TypeBoolean: "bool",
	driver.TypeDate:    "date",
	driver.TypeString:  "string",
	driver.TypeByte:    "uint8",
	driver.TypeChar:    "int8",
	driver.TypeShort:   "int16",
	driver.TypeWord:    "word",
	driver.TypeDWord:   "uint32",
	driver.TypeLong:    "int32",
	driver.TypeFloat:   "float32",
	driver.TypeDouble:  "float64",
	driver.TypeBCD:     "bcd",
	driver.TypeLBCD:    "lbcd",
}

// CommonTypeMap 返回通用类型名 → 内部裸名，供 driversdk.Meta.TypeMap 使用。
// 返回副本，调用方修改不会影响包内真源。
func CommonTypeMap() map[string]string {
	out := make(map[string]string, len(commonTypeMap))
	for name, internal := range commonTypeMap {
		out[name] = internal
	}
	return out
}

// LookupCommonType 按通用类型名查内部裸名，语义照搬内置时代的
// driver.LookupScopeType：大小写不敏感，未命中返回 false。
//
// 先精确命中（零分配，主路径），未命中再回退 EqualFold —— 历史配置里可能
// 存着 "Float" 之外的写法（如 "float"）。
func LookupCommonType(common string) (string, bool) {
	if internal, ok := commonTypeMap[common]; ok {
		return internal, true
	}
	for name, internal := range commonTypeMap {
		if strings.EqualFold(name, common) {
			return internal, true
		}
	}
	return "", false
}
