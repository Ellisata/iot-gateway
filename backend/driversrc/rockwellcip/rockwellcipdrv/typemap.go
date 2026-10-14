// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"strings"

	"iot-gateway/driver"
)

// commonTypeMap Rockwell Logix 通用数据类型 → 内部类型名。
//
// **本文件是 Rockwell 类型映射的唯一真源**：内置时代它叫 rockwellTypes、放在
// driver/dataTypeMap.go 里由 driver.LookupDataType 查询；搬迁成插件后随实现
// 一并搬到这里，由 driversdk.Meta.TypeMap 上报网关（见 docs/driver-plugins.md §2.5）。
// driver/dataTypeMap.go 里已不再保留 Rockwell 的条目。
//
// Logix 数值类型原生有符号（SINT/INT/DINT/LINT），无 CIP 标准无符号类型码，
// Byte/Word/DWord 不映射（位访问用 TagName.N 语法）；Date/BCD/LBCD 无 CIP
// 原生类型，不映射（对齐 CIP）。
//
// 内容与内置时代逐项一致：改一个 value 就等于改现场点位的解码方式。
var commonTypeMap = map[string]string{
	driver.TypeBoolean: "bool",
	driver.TypeString:  "string",
	driver.TypeChar:    "int8",    // SINT
	driver.TypeShort:   "int16",   // INT
	driver.TypeLong:    "int32",   // DINT
	driver.TypeFloat:   "float32", // REAL
	driver.TypeDouble:  "float64", // LREAL
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
