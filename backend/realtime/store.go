// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package realtime 保存采集数据的内存最新值快照，供前端「设备实时数据」页面查询。
//
// 网关本身不落地采集数据 —— 采集结果经 collector.RecordSink 直接交给推送引擎
// 发往 MQTT/时序库，本地没有任何可查的数据源。本包补上这一层：实现
// collector.RecordSink，在采集协程交出每轮结果时顺手覆盖内存中的最新值。
//
// 只存最新值、不存历史：历史趋势应由已推送的时序库承担，而在采集链路上落库
// （PushRecords 是在采集协程里同步调用的）会把 SQLite 写变成轮询周期的瓶颈。
package realtime

import (
	"sync"
	"time"

	"iot-gateway/collector"
)

// Point 单个点位的最新采集值。
type Point struct {
	Name      string    // 点位名（device_address.name）
	Label     string    // 点位说明（device_address.label，如「温度」）
	Value     string    // 最新值（驱动已格式化为字符串）
	DataType  string    // 协议内部类型名
	Kind      string    // 数据类型类别（driver.KindXxx）
	Quality   int       // 采集质量码（driver.QualityXxx，由驱动填充）
	UpdatedAt time.Time // 该值的采集时刻
}

// Store 采集最新值的内存快照。
//
// 并发模型：采集协程写（PushRecords）、HTTP 请求协程读（Lookup），
// 用 RWMutex 保护。写是 O(1) 纯内存操作，绝不阻塞采集。
//
// 生命周期与采集引擎解耦：Store 常驻，不随 Engine.Refresh 重建，
// 因此配置热加载（新增/修改设备点位触发的零中断换任务）不会清空已采集的值，
// 前端页面也就不会每次热加载都闪回「未采集」。
type Store struct {
	mu   sync.RWMutex
	data map[string]map[string]Point // deviceID -> addressID -> 最新值
}

// NewStore 创建空的实时值快照。
func NewStore() *Store {
	return &Store{data: make(map[string]map[string]Point)}
}

// PushRecords 实现 collector.RecordSink：按点位覆盖最新值。
//
// 由采集协程同步调用，故只做内存写、不引入任何 I/O 与锁竞争之外的开销。
func (s *Store) PushRecords(records []collector.CollectedRecord) {
	if len(records) == 0 {
		return
	}
	// 同一批记录来自同一次轮询，共用采集时刻；逐条取 time.Now() 只会放大开销。
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range records {
		byAddr, ok := s.data[r.DeviceID]
		if !ok {
			byAddr = make(map[string]Point)
			s.data[r.DeviceID] = byAddr
		}
		byAddr[r.DeviceAddressID] = Point{
			Name:      r.DeviceAddressName,
			Label:     r.DeviceAddressLabel,
			Value:     r.Value,
			DataType:  r.DataType,
			Kind:      r.Kind,
			Quality:   r.Quality,
			UpdatedAt: now,
		}
	}
}

// Lookup 返回指定设备下这些点位的最新值，键为点位 ID。
//
// 只读请求方点名的点位（而非整台设备的快照），避免为一页 50 条点位复制
// 上万条记录。未采集过的点位（设备刚启用、地址新增后尚未轮询到）不会出现在
// 结果里，由调用方按「未采集」呈现。
//
// 已删除设备的残留记录不会被查询到（查询总是按 deviceID 定点查找），
// 故无需专门回收；其占用上限是「本次运行以来配置过的设备数」，对网关而言可忽略。
func (s *Store) Lookup(deviceID string, addressIDs []string) map[string]Point {
	if len(addressIDs) == 0 {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	byAddr := s.data[deviceID]
	if byAddr == nil {
		return nil
	}
	out := make(map[string]Point, len(addressIDs))
	for _, id := range addressIDs {
		if p, ok := byAddr[id]; ok {
			out[id] = p
		}
	}
	return out
}
