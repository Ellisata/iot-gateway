// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"fmt"
	"testing"

	"iot-gateway/testutil/fake"
)

// 帧数是 645 容量的唯一自变量（帧数 = ⌈点数 / maxDIsPerRead⌉），压测报告的容量表、
// 「提波特率的收益递减」结论都建立在它上面。这里直接数 buildPlan 实际产出的组数，
// 把公式钉在实现上 —— 而不是让公式自己证明自己。
//
// 期望值手算：500 点 → 41 组满 12 + 1 组 8；2000 点 → 166×12 + 8；5000 点 → 416×12 + 8。
// 三者点位数对 12 取余都恰好是 8，故末组都是 8 个点位。
func TestPlanGroupCountMatchesFrameFormula(t *testing.T) {
	cases := []struct {
		points, perRead int
		wantGroups      int
		wantLastGroup   int
	}{
		{500, 12, 42, 8},
		{2000, 12, 167, 8},
		{5000, 12, 417, 8},
		{500, 1, 500, 1}, // 逐点读：帧数 = 点数
		{24, 12, 2, 12},  // 正好整除，末组满
		{36, 12, 3, 12},
		{1, 12, 1, 1},
	}

	for _, c := range cases {
		cfg := DefaultDLT645Config(TransportTCP)
		cfg.PreambleBytes = 0
		cfg.MaxDIsPerRead = c.perRead

		plan := buildPlan(benchAddrs(c.points), cfg)

		if len(plan.groups) != c.wantGroups {
			t.Errorf("%d 点（每帧 %d 个标识）分了 %d 帧，want %d",
				c.points, c.perRead, len(plan.groups), c.wantGroups)
		}
		if got := len(plan.groups[len(plan.groups)-1].idx); got != c.wantLastGroup {
			t.Errorf("%d 点（每帧 %d 个标识）末帧含 %d 个点位，want %d",
				c.points, c.perRead, got, c.wantLastGroup)
		}

		// 分组必须无重无漏地覆盖全部点位 —— 帧数与末帧大小都对、中间却漏点，
		// 是「容量表看着对、现场却少采」的典型形态。
		seen := make([]bool, c.points)
		for _, g := range plan.groups {
			for _, i := range g.idx {
				if seen[i] {
					t.Fatalf("%d 点：点位 %d 被分进多帧", c.points, i)
				}
				seen[i] = true
			}
		}
		for i, ok := range seen {
			if !ok {
				t.Fatalf("%d 点：点位 %d 没有落进任何帧", c.points, i)
			}
		}
	}
}

// 与压测报告的容量表对上：报告写「5000 点 / 12 = 417 帧」，实现必须给出同一个数。
func TestPlanFramesMatchLoadtestReport(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.MaxDIsPerRead = 12

	if got := len(buildPlan(benchAddrs(5000), cfg).groups); got != 417 {
		t.Fatalf("5000 点分了 %d 帧，压测报告按 417 帧折算线路时间", got)
	}
}

// 端到端数一遍真实线路上的帧：假表逐帧计数，读 500 个点位应当正好收到 42 帧。
//
// 上面两个测试数的是 buildPlan 的组数，这个数的是**真的发出去的报文**——
// 组数与实际帧数之间还隔着 exchange（异常应答降级重读、后续帧放弃等）都可能多发帧，
// 只有数报文才覆盖得到。
func TestReadWireFrameCountMatchesFormula(t *testing.T) {
	srv, err := fake.NewDLT645()
	if err != nil {
		t.Fatalf("启动假表失败: %v", err)
	}
	defer srv.Close()

	d := newDLT645Driver(TransportTCP)
	if err := d.Connect(fmt.Sprintf(
		`{"host":"127.0.0.1","port":%d,"maxDIsPerRead":12,"interFrameDelayMs":0}`, srv.Port())); err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer d.Close()

	const points, wantFrames = 500, 42

	before := fake.DLT645ReadFrames()
	results, err := d.Read(benchAddrs(points))
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	got := fake.DLT645ReadFrames() - before

	if got != wantFrames {
		t.Errorf("线路上实际发出 %d 帧，want %d（%d 点 ÷ 每帧 12 个标识，向上取整）",
			got, wantFrames, points)
	}
	// 帧数对不上时，点位是否全部取到值是最能说明问题的旁证。
	for i := range results {
		if results[i].Quality != 192 {
			t.Fatalf("点位 %d Quality = %d，应有 %d 点全部取到值",
				i, results[i].Quality, points)
		}
	}
}
