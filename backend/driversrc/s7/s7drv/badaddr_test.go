// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package s7

import (
	"fmt"
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// 本文件的主题：**地址本地就解析不出来**时，受损范围必须收敛到一个点位。
//
// 修复前的行为是：CalcS7Ranges 全量解析、遇第一个非法地址直接返回 error，于是
// drv.Read 报错 → 采集侧 task.go 判定设备失败（incError + reportState(false)）→
// 连续三轮后落一条**断联**报警并推通知。而那个设备全程在线（IsConnected 为真，
// 重连逻辑根本不启动），坏地址之后的批次也一起被跳过 —— 一个点位把整台设备拖垮，
// 还把运维引向完全错误的方向。
//
// 现在：解析不出的点位在计划期被摘出去（partitionBadAddrs），Read 不报错，该点位
// 返回一条 Quality=QualityBadAddress 的记录，由既有的点位质量报警报出「地址无效」
// 并指名到点位；其余点位照常采集，设备保持在线。

const badName = "not-an-s7-address!"

func devAddr(id, name, dataType string) po.DeviceAddress {
	return po.DeviceAddress{ID: id, Name: name, DataType: dataType, CommonDataType: dataType}
}

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

// TestReadOnce_UnparsableAddressExcluded 一坏两好：坏的只坏它自己。
func TestReadOnce_UnparsableAddressExcluded(t *testing.T) {
	cfg := DefaultS7Config()
	cfg.MaxGap = 16
	addrs := append(mkDBPoints(0, 2), devAddr("bad", badName, "byte"))

	f := newFakeS7()
	d := &s7Driver{config: cfg}
	results := runRound(t, d, f, driver.RangeSig(addrs), addrs, cfg)

	assertAligned(t, addrs, results)

	// 坏点位：地址无效、无值，但 DataType/Kind 必须照填（下游靠它决定怎么写）。
	b := results[2]
	if b.Quality != driver.QualityBadAddress || b.Value != "" {
		t.Fatalf("坏点位 want quality=%d 且无值, got quality=%d value=%q",
			driver.QualityBadAddress, b.Quality, b.Value)
	}
	if b.DataType != "byte" || b.Kind == "" {
		t.Fatalf("坏点位必须带回 DataType/Kind, got dataType=%q kind=%q", b.DataType, b.Kind)
	}

	// 好点位不受影响（假 PLC 把偏移写成值）。
	for i := 0; i < 2; i++ {
		if want := fmt.Sprintf("%d", i); results[i].Value != want || results[i].Quality != driver.QualityGood {
			t.Fatalf("result[%d] = %+v, want val=%s quality=%d", i, results[i], want, driver.QualityGood)
		}
	}
	if calls := f.callsSince(0); len(calls) == 0 {
		t.Fatal("好点位必须真的读设备（假 PLC 一次调用都没收到）")
	}
}

// TestReadOnce_AllUnparsableNoError 一批全坏：不报错、全部地址无效、零次上线。
//
// 这条同时回归「len(valid)>0 才调 CalcS7Ranges」那个守卫 —— 没有它，
// CalcS7Ranges 会对空输入报 "no addresses to read"，而那是「这批一个都读不了」，
// 不是「这批里一个点位都没有」。
func TestReadOnce_AllUnparsableNoError(t *testing.T) {
	cfg := DefaultS7Config()
	addrs := []po.DeviceAddress{
		devAddr("bad1", badName, "byte"),
		devAddr("bad2", "DB99.DBXx", "byte"),
	}

	f := newFakeS7()
	d := &s7Driver{config: cfg}
	results := runRound(t, d, f, driver.RangeSig(addrs), addrs, cfg)

	assertAligned(t, addrs, results)
	for i, r := range results {
		if r.Quality != driver.QualityBadAddress {
			t.Fatalf("第 %d 项 want quality=%d, got %d", i, driver.QualityBadAddress, r.Quality)
		}
		if r.DataType != "byte" || r.Kind == "" {
			t.Fatalf("第 %d 项必须带回 DataType/Kind, got %q/%q", i, r.DataType, r.Kind)
		}
	}
	if calls := f.callsSince(0); len(calls) != 0 {
		t.Fatalf("一个可解析的地址都没有，不该发出任何请求, got %+v", calls)
	}
}

// TestRangesFor_UnparsableCarriedOnPlan 计划期就摘出去：坏点位不在区间里，
// 但作为 bad 随计划带出来（readOnce 的记录正是从 badFor 播种的）。
//
// 这条同时证明「解析失败」这条**不依赖降级机制**：没有设备确认的坏点 ⇒ 不会调
// storeDegradedPlan ⇒ 计划始终是非降级的，而 badFor 照样要把 bad 交出来。
func TestRangesFor_UnparsableCarriedOnPlan(t *testing.T) {
	cfg := DefaultS7Config()
	cfg.MaxGap = 16
	addrs := append(mkDBPoints(0, 2), devAddr("bad", badName, "byte"))

	f := newFakeS7()
	d := &s7Driver{config: cfg}
	sig := driver.RangeSig(addrs)

	// 先跑一轮（真实路径），再直接问缓存里的计划。
	runRound(t, d, f, sig, addrs, cfg)

	if p := d.rangeCache[sig]; p.degraded {
		t.Fatalf("没有设备确认的坏点，不该产生降级计划: %+v", p)
	}
	bad := d.badFor(sig)
	if bad["bad"] != driver.QualityBadAddress {
		t.Fatalf("badFor = %+v, want bad 点位标 %d", bad, driver.QualityBadAddress)
	}

	// 可解析的两个点位仍在合并区间里。
	ranges, err := d.planFor(sig, addrs, cfg)
	if err != nil {
		t.Fatalf("planFor = %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 merged range over the 2 valid points, got %+v", ranges)
	}
}

// TestPartitionBadAddrs_KeepsOrderAndCount 拆分的纯函数语义。
func TestPartitionBadAddrs_KeepsOrderAndCount(t *testing.T) {
	d := &s7Driver{config: DefaultS7Config()}
	addrs := []po.DeviceAddress{
		devAddr("a", "DB1.DBB0", "byte"),
		devAddr("b", badName, "byte"),
		devAddr("c", "M0.1", "bool"),
	}
	valid, bad := d.partitionBadAddrs(addrs)

	if len(valid) != 2 || valid[0].ID != "a" || valid[1].ID != "c" {
		t.Fatalf("valid = %+v, want [a c]", valid)
	}
	if len(bad) != 1 || bad["b"] != driver.QualityBadAddress {
		t.Fatalf("bad = %+v, want [b → %d]", bad, driver.QualityBadAddress)
	}
}
