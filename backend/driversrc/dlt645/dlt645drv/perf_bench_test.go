// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"fmt"
	"testing"

	"iot-gateway/model/po"
	"iot-gateway/testutil/fake"
)

// benchAddrs 造 n 个点位地址（DI:字节数:小数位），与 loadtest 的地址公式一致。
func benchAddrs(n int) []po.DeviceAddress {
	addrs := make([]po.DeviceAddress, n)
	for i := range addrs {
		addrs[i] = po.DeviceAddress{
			ID:       fmt.Sprintf("id-%d", i),
			Name:     fmt.Sprintf("%08X:4:2", 0x10000000+i),
			DataType: "float",
		}
	}
	return addrs
}

// BenchmarkReadTCP 测「一整批点位读一遍」的耗时：对 5000 点 / maxDIsPerRead=12
// 即 417 帧，用 ns/op ÷ 417 得到**单帧往返成本**——这是 645 容量的唯一自变量
// （帧数 = ⌈点数/maxDIsPerRead⌉，见 loadtest README 的帧数模型）。
func BenchmarkReadTCP(b *testing.B) {
	srv, err := fake.NewDLT645()
	if err != nil {
		b.Fatalf("启动假表失败: %v", err)
	}
	defer srv.Close()

	cfgJSON := fmt.Sprintf(
		`{"host":"127.0.0.1","port":%d,"maxDIsPerRead":12,"interFrameDelayMs":0}`, srv.Port())
	d := newDLT645Driver(TransportTCP)
	if err := d.Connect(cfgJSON); err != nil {
		b.Fatalf("连接失败: %v", err)
	}
	defer d.Close()

	addrs := benchAddrs(5000)
	if _, err := d.Read(addrs); err != nil { // 预热：建规划缓存
		b.Fatalf("预热失败: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := d.Read(addrs)
		if err != nil {
			b.Fatalf("读取失败: %v", err)
		}
		if res[0].Quality != 192 {
			b.Fatalf("首点质量为 %d，期望 192", res[0].Quality)
		}
	}
	b.StopTimer()

	frames := float64(b.N) * float64(frameCount(5000, 12))
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/frames, "ns/frame")
	b.ReportMetric(frames/b.Elapsed().Seconds(), "frames/s")
}

func frameCount(points, perRead int) int {
	return (points + perRead - 1) / perRead
}
