// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"sort"
	"strings"

	"iot-gateway/driver"
)

// NewDriver 创建 OPC UA 驱动实例（插件工厂用）。
//
// 转调 newOpcUaDriver()（driver.go）而不是在这里重复构造：newClient 是可注入
// 的客户端工厂字段，默认值 newRealClient 由那个构造函数填 —— 直接写
// &opcuaDriver{} 会让 Connect 调用 nil 函数 panic。与内置时代 driver.go 的
// init() 里 driver.Register 的写法保持同一处真源。
func NewDriver() driver.Driver {
	return newOpcUaDriver()
}

// Types 返回上报给网关的插件类型表（driversdk.Meta.Types）。
//
// 上报**全部**已注册类型，而不只是 CommonTypeMap 能到达的那些：
// 网关的 NormalizeAddressType 靠 reg.Get(data_type) 判断地址里存的内部名是否
// 仍然有效 —— 插件漏报一个类型（如 int32/uint64/byteString，在 OPC UA 里没有
// 通用名入口），历史数据里写着它的点位就会被判为"漂移"，按 common_data_type
// 改写成别的类型并**静默回写库**。值恰好一样，现场只会看到 data_type 自己变了。
//
// 类型清单直读本包的 sync.Once 缓存（types.go 的 opcuaLookup）：它按 "opcua."
// 前缀扫出注册表里本 scope 的全部类型，正是"该包注册进 TypeRegistry 的全部类型"
// 这个问题的唯一直读来源。另写一张 map 字面量必然与注册表漂移。
//
// 每项**整体拷贝** *dt，只把 Name 换成裸名（注册表里存的是带 "opcua." 前缀的
// "opcua.int16"，插件必须上报裸名 "int16"，网关会按 Scope 重新加前缀）。
//
// 裸名取自 *dt.Name（而非 Names() 返回的键）：TypeRegistry 的 map 键是
// strings.ToLower 过的，Names() 于是把 byteString 折成 "bytestring"，
// 上报出去就与 types.go 里注册的原始 token 对不上（网关侧虽大小写不敏感，
// 但 describe 会把它当成另一个类型名，逐字比对必然漂移）。dt.Name 保留了
// 注册时的原始大小写，剥掉前缀才是真正的"裸名"。
//
// driversdk.Serve 会用同一批指针重新注册进插件进程的注册表，因此这里必须带上
// **Kind**：OPC UA 的类型本来就只有 Name + Kind，没有 Decode/Encode/Format
// （服务器返回原生值，无需字节级编解码），丢 Kind 不会 panic，但会让推送通道
// 按错误类型存值 —— 比 panic 更难发现。绝不能写成只填 Name 的瘦身构造。
//
// 按 Name 排序后返回，保证 describe 结果稳定、可逐字比对。
func Types() []*driver.DataType {
	prefix := protocolName + "."
	names := driver.GetTypeRegistry().Names()
	out := make([]*driver.DataType, 0, len(names))
	for _, full := range names {
		key, ok := strings.CutPrefix(full, prefix)
		if !ok {
			continue
		}
		// 经 opcuaLookup 取，直接复用该包的 sync.Once 缓存（零分配、无锁），
		// 并顺带覆盖"仅在注册表里、但缓存未命中回退查询"的边界。
		dt, ok := opcuaLookup(key)
		if !ok || dt == nil {
			continue
		}
		bare, ok := strings.CutPrefix(dt.Name, prefix)
		if !ok {
			// 防御性回退：未带前缀的条目（正常不会出现在本 scope 的扫描里）。
			bare = key
		}
		copied := *dt
		copied.Name = bare
		out = append(out, &copied)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
