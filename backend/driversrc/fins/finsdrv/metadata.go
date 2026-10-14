// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"sort"

	"iot-gateway/driver"
)

// NewUDP 创建 FINS/UDP（端口 9600）驱动实例（插件工厂用）。
func NewUDP() driver.Driver {
	return newFINSDriver(TransportUDP)
}

// NewTCP 创建 FINS/TCP（含连接握手，供 PLC 自带以太网口）驱动实例（插件工厂用）。
func NewTCP() driver.Driver {
	return newFINSDriver(TransportTCP)
}

// NewSerial 创建 Host Link（FINS over serial，本地串口）驱动实例（插件工厂用）。
func NewSerial() driver.Driver {
	return newFINSDriver(TransportSerial)
}

// NewHostLinkTCP 创建 Host Link over TCP（经串口服务器采集远端 PLC 串口）驱动实例
// （插件工厂用）。
//
// 注意传输层是 TCP 而非串口：协议名叫 "HostLinkTCP"，但网关视角是一条 TCP 长连接，
// 客户端内部已对单次请求+响应持互斥锁，因此 SerialExclusive 为 false
// （见 driver.go 的 SerialExclusive：判据是 transport == TransportSerial）。
// 误接 TransportSerial 会让 RS-485 与实际 TCP 两条路径的语义互换。
func NewHostLinkTCP() driver.Driver {
	return newFINSDriver(TransportHostLinkTCP)
}

// Types 返回上报给网关的插件类型表（driversdk.Meta.Types）。
//
// 上报**全部**已注册类型，而不只是 CommonTypeMap 能到达的那些：
// 网关的 NormalizeAddressType 靠 reg.Get(data_type) 判断地址里存的内部名是否
// 仍然有效 —— 插件漏报一个类型（如 uint16，在 FINS 里没有通用名入口），
// 历史数据里写着它的点位就会被判为"漂移"，按 common_data_type 改写成别的类型
// 并**静默回写库**。值恰好一样，现场只会看到 data_type 自己变了。
//
// 类型清单直读本包的静态缓存（datatypes.go 的 finsTypeCache）：它按 "fins."
// 前缀扫出注册表里本 scope 的全部类型，正是"该包注册进 TypeRegistry 的
// 全部类型"这个问题的唯一直读来源。另写一张 map 字面量必然与注册表漂移。
//
// 每项**整体拷贝** *dt（Decode/Encode/Format 一并带走），只把 Name 换成裸名
// （注册表里存的是带 "fins." 前缀的 "fins.int16"，插件必须上报裸名 "int16"，
// 网关会按 Scope 重新加前缀）。绝不能写成只填 Name/Kind/Size 的瘦身构造：
// driversdk.Serve 会用同一批指针重新注册进插件进程的注册表，瘦身版会把包内
// 缓存的 Decode 换成 nil，解析时调 nil 函数 panic —— 被 worker pool 的 recover
// 吞掉之后，症状是该设备永远采不到值且不留明显痕迹。
//
// 按 Name 排序后返回，保证 describe 结果稳定、可逐字比对。
func Types() []*driver.DataType {
	cache := finsTypeCache()
	out := make([]*driver.DataType, 0, len(cache))
	for bare, dt := range cache {
		if dt == nil {
			// 防御性跳过：finsTypeCache 只在 finsScope.Get 命中时才写入，正常不会为 nil。
			continue
		}
		copied := *dt
		copied.Name = bare
		out = append(out, &copied)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
