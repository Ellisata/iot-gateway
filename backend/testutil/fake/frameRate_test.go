// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"fmt"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// 这两条表征测试钉住 DL/T 645 容量模型的两个自变量——
// 「帧间延时按帧计费」与「帧数 = ceil(点数 / maxDIsPerRead)」。
//
// 它们不是回归测试而是**测量前提**：两者任一丝默失效，容量评估结论会整体失真
// （前者决定单设备吞吐上限，后者决定点数与帧数的换算），且不会以任何形式报错。

// TestDLT645InterFrameDelayPerFrame 帧间延时按帧计费：
// 一次 N 帧的读取，耗时下界就是 N × 帧间延时，与链路速率、并发设备数都无关。
//
// 这是本协议最容易配错的一项——现场为「兼容慢表」把 interFrameDelayMs 调大，
// 却不知道它同时把单设备吞吐上限压到 1000/delay 帧每秒：
// 默认 30ms → 33 帧/s，若 maxDIsPerRead 保持默认 1（1 帧/点），单表就只有 33 点/s。
func TestDLT645InterFrameDelayPerFrame(t *testing.T) {
	srv, err := NewDLT645()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	const (
		frames  = 10 // 120 点 / maxDIs=12
		delayMS = 20
	)
	addrs := dltAddrs(120)

	d, err := driver.Create("DLT645.TCP")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Connect(fmt.Sprintf(
		`{"host":"127.0.0.1","port":%d,"maxDIsPerRead":12,"interFrameDelayMs":%d}`,
		srv.Port(), delayMS)); err != nil {
		t.Fatal(err)
	}

	d.Read(addrs) // 预热
	before := DLT645ReadFrames()
	start := time.Now()
	if _, err := d.Read(addrs); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if got := DLT645ReadFrames() - before; got != frames {
		t.Fatalf("帧数 = %d, want %d（120 点 / maxDIsPerRead=12）", got, frames)
	}
	if floor := time.Duration(frames*delayMS) * time.Millisecond; elapsed < floor {
		t.Errorf("耗时 %v 低于 %d 帧 × %dms = %v 的下界——帧间延时没有按帧生效",
			elapsed, frames, delayMS, floor)
	}
}

// TestDLT645FrameCountIsCeil 帧数 = ceil(点数 / maxDIsPerRead)，与地址是否相邻无关。
//
// 645 的读命令逐数据标识寻址，规范里没有「区间/连续块」概念，
// 因此**批量读是本协议唯一的帧数杠杆**，且上限受规范约束（单请求数据域 ≤ 200 字节）。
func TestDLT645FrameCountIsCeil(t *testing.T) {
	srv, err := NewDLT645()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	cases := []struct {
		points, maxDIs, want int
	}{
		{120, 1, 120},
		{120, 4, 30},
		{120, 12, 10},
		{100, 12, 9}, // 尾部不足一组也要发一帧
	}
	for _, c := range cases {
		d, err := driver.Create("DLT645.TCP")
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Connect(fmt.Sprintf(
			`{"host":"127.0.0.1","port":%d,"maxDIsPerRead":%d,"interFrameDelayMs":0}`,
			srv.Port(), c.maxDIs)); err != nil {
			t.Fatal(err)
		}

		addrs := dltAddrs(c.points)
		d.Read(addrs) // 预热
		before := DLT645ReadFrames()
		if _, err := d.Read(addrs); err != nil {
			t.Fatal(err)
		}
		if got := DLT645ReadFrames() - before; got != int64(c.want) {
			t.Errorf("%d 点 / maxDIs=%d：帧数 = %d, want %d", c.points, c.maxDIs, got, c.want)
		}
		d.Close()
	}
}

// dltAddrs 生成 n 个厂商私有数据标识点位（4 字节 / 2 位小数，与假表的数据长度约定一致）。
func dltAddrs(n int) []po.DeviceAddress {
	addrs := make([]po.DeviceAddress, n)
	for i := range addrs {
		addrs[i] = po.DeviceAddress{
			ID:       fmt.Sprintf("a%d", i),
			Name:     fmt.Sprintf("%08X:4:2", 0x06000000+i),
			DataType: "float",
		}
	}
	return addrs
}
