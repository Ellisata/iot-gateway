// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// 本文件的主题：**本地就进不了区间规划**的点位，受损范围必须收敛到它自己。
//
// 两个成因走同一条路，本文件两条都测：
//  ① 地址本地解析不出来（一个字节都没上过线）；
//  ② 单点位跨度就超过 maxReadWords（地址写得对，但声明的长度放不进单请求，
//     而 FINS 一次请求的数据须连续、不能拆块）。
//
// 修复前的行为是：CalcFINSRanges 全量解析、遇①或②直接返回 error，于是 drv.Read 报错
// → 采集侧 task.go 判定设备失败（incError + reportState(false)）→ 连续三轮后落一条
// **断联**报警并推通知。而那个设备全程在线（IsConnected 为真，重连逻辑根本不启动），
// 失败之后的批次也一起被跳过 —— 一个点位把整台设备拖垮。
//
// 现在：这两种点位在计划期被摘出去（partitionBadAddrs），Read 不报错，该点位返回一条
// Quality=QualityBadAddress 的记录，由既有的点位质量报警报出「地址无效」并指名到点位；
// 其余点位照常采集，设备保持在线。

const badName = "BANANA"

func assertAligned(t *testing.T, addrs []po.DeviceAddress, got []driver.ReadResult) {
	t.Helper()
	if len(got) != len(addrs) {
		t.Fatalf("want %d records (一个点位一条), got %d: %+v", len(addrs), len(got), got)
	}
	for i := range addrs {
		if got[i].DeviceAddressID != addrs[i].ID {
			t.Fatalf("第 %d 项 ID = %q，want %q（顺序错位）", i, got[i].DeviceAddressID, addrs[i].ID)
		}
	}
}

// TestRead_UnparsableAddressExcluded 一坏两好：坏的只坏它自己。
func TestRead_UnparsableAddressExcluded(t *testing.T) {
	cfg := cfgWith(10, 100)
	f := newFakeFINS()
	d := &finsDriver{config: cfg, client: f}
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("bad", badName, "int16"),
		mkAddr("2", "D102", "int16"),
	}

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v（一个坏地址不该让整批失败）", err)
	}
	assertAligned(t, addrs, results)

	// 坏点位：地址无效、无值，但 DataType/Kind 必须照填（下游靠它决定怎么写）。
	b := results[1]
	if b.Quality != driver.QualityBadAddress || b.Value != "" {
		t.Fatalf("坏点位 want quality=%d 且无值, got quality=%d value=%q",
			driver.QualityBadAddress, b.Quality, b.Value)
	}
	if b.DataType != "int16" || b.Kind == "" {
		t.Fatalf("坏点位必须带回 DataType/Kind, got dataType=%q kind=%q", b.DataType, b.Kind)
	}

	// 好点位不受影响（假设备把字地址写进值里，偏移算错会立刻对不上）。
	if v := results[0]; v.Value != "100" || v.Quality != driver.QualityGood {
		t.Fatalf("D100 = %+v, want val=100 quality=%d", v, driver.QualityGood)
	}
	if v := results[2]; v.Value != "102" || v.Quality != driver.QualityGood {
		t.Fatalf("D102 = %+v, want val=102 quality=%d", v, driver.QualityGood)
	}
	if calls := f.callsSince(0); len(calls) == 0 {
		t.Fatal("好点位必须真的读设备（假设备一次调用都没收到）")
	}
}

// TestRead_AllExcluded 一批全不可用：不报错、全部地址无效、零次上线。
//
// 三种成因混在一起（地址解析不出 ×2、单点位跨度超限 ×1），它们走的是同一条路。
// 这条同时回归「len(valid)>0 才调 CalcFINSRanges」那个守卫 —— 没有它，
// CalcFINSRanges 会对空输入报 "no addresses to read"。
func TestRead_AllExcluded(t *testing.T) {
	cfg := cfgWith(10, 1) // maxReadWords=1：一个 2 字的 int32 放不下
	f := newFakeFINS()
	d := &finsDriver{config: cfg, client: f}
	addrs := []po.DeviceAddress{
		mkAddr("bad1", badName, "int16"),
		mkAddr("bad2", "CIO0.99.99", "int16"),
		mkAddr("big", "D200", "int32"),
	}

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v（全坏也不该失败）", err)
	}
	assertAligned(t, addrs, results)
	for i, r := range results {
		if r.Quality != driver.QualityBadAddress {
			t.Fatalf("第 %d 项 want quality=%d, got %d", i, driver.QualityBadAddress, r.Quality)
		}
		if r.DataType != addrs[i].DataType || r.Kind == "" {
			t.Fatalf("第 %d 项必须带回 DataType/Kind, got %q/%q", i, r.DataType, r.Kind)
		}
	}
	if calls := f.callsSince(0); len(calls) != 0 {
		t.Fatalf("一个可用的地址都没有，不该发出任何请求, got %+v", calls)
	}
}

// TestPlanFor_BadAddrsInPlan 计划期就摘出去：坏点位不在区间里，但作为 bad 随计划带出。
//
// 同时证明「解析失败」这条**不依赖降级机制**：不会被 storeDegradedPlan 覆盖掉。
func TestPlanFor_BadAddrsInPlan(t *testing.T) {
	cfg := cfgWith(10, 100)
	f := newFakeFINS()
	d := &finsDriver{config: cfg, client: f}
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("bad", badName, "int16"),
	}
	sig := driver.RangeSig(addrs)

	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("Read = %v", err)
	}
	if p := d.rangeCache[sig]; p.degraded {
		t.Fatalf("没有设备确认的坏点，不该产生降级计划: %+v", p)
	}
	if bad := d.badFor(sig); bad["bad"] != driver.QualityBadAddress {
		t.Fatalf("badFor = %+v, want bad 点位标 %d", bad, driver.QualityBadAddress)
	}
}

// TestRead_SpanTooLargeExcluded 跨度放不下的点位与解析失败同一条路：只坏它自己。
//
// 它**不是**解析失败：地址写得对，只是该点位声明的长度放不进单请求，而 FINS 一次
// 请求的数据须连续、不能拆块。上下游共用 range.go 的 finsSpanWords 判据，
// 所以「多大算放不下」只有一个答案（不是两条路径各算一遍）。
func TestRead_SpanTooLargeExcluded(t *testing.T) {
	cfg := cfgWith(10, 1) // maxReadWords=1：一个 2 字的 int32 放不下
	f := newFakeFINS()
	d := &finsDriver{config: cfg, client: f}
	addrs := []po.DeviceAddress{
		mkAddr("big", "D100", "int32"),
		mkAddr("ok", "D110", "int16"),
	}

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v（跨度超限的点位不该让整批失败）", err)
	}
	assertAligned(t, addrs, results)

	b := results[0]
	if b.Quality != driver.QualityBadAddress || b.Value != "" {
		t.Fatalf("超限点位 want quality=%d 且无值, got quality=%d value=%q",
			driver.QualityBadAddress, b.Quality, b.Value)
	}
	if b.DataType != "int32" || b.Kind == "" {
		t.Fatalf("超限点位必须带回 DataType/Kind, got dataType=%q kind=%q", b.DataType, b.Kind)
	}
	// 好点位照常读到值：超限点位不在任何区间里，它的跨度也没把 D110 所在区间撑爆。
	if v := results[1]; v.Value != "110" || v.Quality != driver.QualityGood {
		t.Fatalf("D110 = %+v, want val=110 quality=%d", v, driver.QualityGood)
	}
}

// TestPartitionBadAddrs_KeepsOrderAndCount 拆分的纯函数语义：两个来源都摘出去。
func TestPartitionBadAddrs_KeepsOrderAndCount(t *testing.T) {
	cfg := cfgWith(10, 1) // maxReadWords=1：int32 放不下
	d := &finsDriver{config: cfg}
	addrs := []po.DeviceAddress{
		mkAddr("a", "D100", "int16"),
		mkAddr("b", badName, "int16"),  // 解析失败
		mkAddr("c", "CIO0.00", "bool"), // 位点位，占 1 字，不受上限影响
		mkAddr("d", "D200", "int32"),   // 跨度超限
	}
	valid, bad := d.partitionBadAddrs(addrs, cfg)

	if len(valid) != 2 || valid[0].ID != "a" || valid[1].ID != "c" {
		t.Fatalf("valid = %+v, want [a c]", valid)
	}
	if len(bad) != 2 || bad["b"] != driver.QualityBadAddress || bad["d"] != driver.QualityBadAddress {
		t.Fatalf("bad = %+v, want [b d → %d]", bad, driver.QualityBadAddress)
	}
}
