// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"fmt"
	"strconv"
	"testing"

	"iot-gateway/model/po"
)

// benchAddrs 生成 n 个连续 D 字地址点位（int16），模拟典型采集批次。
func benchAddrs(n int) []po.DeviceAddress {
	addrs := make([]po.DeviceAddress, n)
	for i := 0; i < n; i++ {
		addrs[i] = po.DeviceAddress{
			ID:       "addr-" + strconv.Itoa(i),
			Name:     fmt.Sprintf("D%d", 100+i),
			DataType: "int16",
		}
	}
	return addrs
}

// BenchmarkCalcMCRanges 区间计算的纯函数成本（2000 点典型批次）。
// 这是无缓存路径；实际 Read 命中驱动内指纹缓存后本函数仅在首轮执行。
func BenchmarkCalcMCRanges(b *testing.B) {
	addrs := benchAddrs(2000)
	cfg := DefaultMCConfig()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := CalcMCRanges(addrs, cfg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDriverReadRangeCached 驱动 Read 全链路：区间已缓存，
// 每轮仅剩传输层 I/O（模拟）与逐点解码，是实际轮询的热路径。
func BenchmarkDriverReadRangeCached(b *testing.B) {
	cfg := DefaultMCConfig()
	mt := &mockTransport{fn: func(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
		return make([]byte, points*2), nil
	}}
	d := &mcDriver{config: cfg, client: mt}
	addrs := benchAddrs(2000)
	if _, err := d.Read(addrs); err != nil { // 预热：计算并缓存区间
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.Read(addrs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMcLookup 类型元数据查询（缓存命中，零分配无锁）。
func BenchmarkMcLookup(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = mcLookup("float32")
	}
}

// BenchmarkMcTypeWords 类型占字数计算（区间计算内逐点调用）。
func BenchmarkMcTypeWords(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = mcTypeWords("int32", 16)
	}
}
