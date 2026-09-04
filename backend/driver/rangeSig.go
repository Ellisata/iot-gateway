// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import "iot-gateway/model/po"

// RangeCacheMaxEntries 驱动内区间/规划缓存条数上限。
// 批次集合在任务生命周期内固定（频率组数 × 单组批次数），正常情况下远小于此；
// 超限时清空重建，保证缓存大小有界。
const RangeCacheMaxEntries = 32

// RangeSig 计算点位批次的内容指纹（FNV-1a 64），作为驱动内区间缓存键。
// 区间计算是 (addrs, cfg) 的纯函数，同一设备同一采集分组的批次内容在轮询间不变，
// 用指纹命中缓存可避免每轮重复解析地址与计算区间（点位多时是主要 CPU 开销）。
// FNV-1a 碰撞概率对现实点位集合可忽略；缓存条目数受频率组数 × 批次数约束，
// 驱动实例在配置热刷新时由采集引擎重建，缓存随之失效（驱动 Connect 时也显式清空）。
func RangeSig(addrs []po.DeviceAddress) uint64 {
	const (
		fnvBasis uint64 = 14695981039346656037
		fnvPrime uint64 = 1099511628211
	)
	h := fnvBasis
	for i := range addrs {
		a := &addrs[i]
		for _, c := range a.ID {
			h ^= uint64(c)
			h *= fnvPrime
		}
		h ^= 0xFF
		h *= fnvPrime
		for _, c := range a.Name {
			h ^= uint64(c)
			h *= fnvPrime
		}
		h ^= 0xFF
		h *= fnvPrime
		for _, c := range a.DataType {
			h ^= uint64(c)
			h *= fnvPrime
		}
		h ^= 0xFF
		h *= fnvPrime
	}
	return h
}
