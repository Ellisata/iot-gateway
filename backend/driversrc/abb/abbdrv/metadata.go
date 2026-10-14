// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"sort"
	"strings"

	"iot-gateway/driver"
)

// NewDriver 创建 ABB RWS 驱动实例（插件工厂用）。
//
// 转调 newAbbDriver() 而不是在这里重复构造：newClient 是可注入的客户端工厂
// 字段，缺省值 newRealClient 由那个构造函数填 —— 直接写 &abbDriver{} 会让
// Connect 调到 nil 函数。
func NewDriver() driver.Driver {
	return newAbbDriver()
}

// Types 返回上报给网关的插件类型表（driversdk.Meta.Types）。
//
// 上报**全部**已注册类型，而不只是 CommonTypeMap 能到达的那些：网关的
// NormalizeAddressType 靠 reg.Get(data_type) 判断地址里存的内部名是否仍然
// 有效 —— 漏报一个类型，历史数据里写着它的点位就会被判为"漂移"、按
// common_data_type 改写成别的类型并**静默回写库**（现场只会看到 data_type
// 自己变了）。auto 就没有通用名之外的入口问题，但它同样必须在这里出现。
//
// 类型清单直读本包的 sync.Once 缓存（types.go 的 abbLookup）：它按 "abb."
// 前缀扫出注册表里本 scope 的全部类型，正是"该包注册进 TypeRegistry 的全部
// 类型"这个问题的唯一直读来源。另写一张 map 字面量必然与注册表漂移。
//
// 每项**整体拷贝** *dt，只把 Name 换成裸名（注册表里存的是带 "abb." 前缀的
// "abb.int"，插件必须上报裸名 "int"，网关会按 Scope 重新加前缀）。
//
// 裸名取自 *dt.Name（而非 Names() 返回的键）：TypeRegistry 的 map 键是
// strings.ToLower 过的，直接用键会与 types.go 里注册的原始 token 对不上。
//
// driversdk.Serve 会用同一批指针重新注册进插件进程的注册表，因此必须带上
// **Kind** —— RWS 的类型只有 Name + Kind（控制器返回已解析的值，不走字节级
// 编解码），丢 Kind 不会 panic，但会让推送通道按错误类型存值。
//
// 按 Name 排序后返回，保证 describe 结果稳定、可逐字比对。
func Types() []*driver.DataType {
	prefix := pluginScope + "."
	names := driver.GetTypeRegistry().Names()
	out := make([]*driver.DataType, 0, len(names))
	for _, full := range names {
		bare, ok := strings.CutPrefix(full, prefix)
		if !ok {
			continue
		}
		// 经 abbLookup 取，复用该包的 sync.Once 缓存（零分配、无锁），
		// 并顺带覆盖"仅在注册表里、但缓存未命中回退查询"的边界。
		dt, ok := abbLookup(bare)
		if !ok || dt == nil {
			continue
		}
		name, ok := strings.CutPrefix(dt.Name, prefix)
		if !ok {
			name = bare // 防御性回退：未带前缀的条目（正常不会出现）
		}
		copied := *dt
		copied.Name = name
		out = append(out, &copied)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
