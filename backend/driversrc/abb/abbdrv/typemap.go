// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"strings"

	"iot-gateway/driver"
)

// commonTypeMap 通用数据类型 → 内部类型名。
//
// **本文件是 ABB RWS 类型映射的唯一真源**：它随实现一起搬进插件包，由
// driversdk.Meta.TypeMap 上报网关（见 docs/driver-plugins.md §2.2）。
// driver/dataTypeMap.go 的内置基线已随内置驱动一并清空，那里查不到任何东西。
//
// 与其它协议不同，ABB RWS **返回的是已经解析好的原生值**（`"lvalue":"0"`、
// `"rax_1":"-0.000000"`），不走字节级解码，所以映射的目标是"这个点位该按哪一类
// 值存进时序库"（Kind），而不是"按几个字节、什么字节序解"。
//
// 刻意**不映射** Byte / Char / BCD / LBCD：RWS 没有对应概念（没有字节/字符
// 寄存器，也没有 BCD 编码的信号），映射过去等于在前端类型下拉里提供一个选了也
// 取不到值的选项。少一项比多一项错一项好。
//
// Auto 不在 CommonDataTypes 里，因此它落在类型下拉的「扩展」组按字母序排列 ——
// 这是刻意的：它是本协议的主推类型（按 RWS 自身声明的 type 解析 Kind），
// 但把它塞进通用组需要改 driver.CommonDataTypes 源码，那是网关级的事，
// 插件不该动（见 docs/driver-plugins.md §2.2 的边界说明）。
var commonTypeMap = map[string]string{
	driver.TypeBoolean: "bool",
	driver.TypeDate:    "time",
	driver.TypeString:  "string",
	driver.TypeShort:   "int",   // 16 位有符号
	driver.TypeWord:    "uint",  // 16 位无符号
	driver.TypeDWord:   "uint",  // 32 位无符号（RWS 无符号语义，归 uint）
	driver.TypeLong:    "int",   // 32 位有符号
	driver.TypeFloat:   "float", // 32 位浮点
	driver.TypeDouble:  "float", // 64 位浮点

	// Auto：按资源声明的 RWS 类型解析 Kind，详见 types.go 的 resolveKind。
	"Auto": "auto",
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

// LookupCommonType 按通用类型名查内部裸名：大小写不敏感，未命中返回 false。
//
// 先精确命中（零分配，主路径），未命中再回退 EqualFold —— 历史配置里可能存着
// "Float" 之外的写法（如 "float"）。
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
