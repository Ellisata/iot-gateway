// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"fmt"
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// 本文件的主题：**标签名本地就解析不出来**时，受损范围必须收敛到一个点位。
//
// ParseRockwellAddress 只收可打印 ASCII（0x21..0x7E），所以一个**空格**或一个
// **中文**字符就够 —— 把「Line 1.Temp」或中文说明填进地址栏是常见手误。
//
// 修复前的行为是：buildPlan 全量解析、遇第一个非法标签名直接返回 error，于是
// drv.Read 报错 → 采集侧把它当成**设备故障**（incError + reportState(false)）→
// 连续三轮后落一条**断联**报警并推通知。而设备全程在线（IsConnected 为真，重连
// 逻辑根本不启动），坏标签之后的标签也一起被跳过 —— 一个手误把整台设备拖垮。
//
// 现在：解析不出的点位在规划前被摘出去（partitionBadTags），Read 不报错，该点位
// 返回一条 Quality=QualityBadAddress 的记录，由点位质量报警报出「地址无效」。

// badName 含空格 —— ParseRockwellAddress 里 0x20 < 0x21，直接拒。
const badName = "Line 1.Temp"

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

// TestRead_UnparsableTagExcluded 一坏两好：坏的只坏它自己。
func TestRead_UnparsableTagExcluded(t *testing.T) {
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	d.client = &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		if tag != "MotorSpeed" && tag != "Other" {
			return nil, fmt.Errorf("不该读到标签 %q（它本地就解析不出来，一个字节都不该发出去）", tag)
		}
		return mkResp(cipTypeINT, 0x64, 0x00), nil // int16=100
	}}

	addrs := []po.DeviceAddress{
		mkAddr("a1", "MotorSpeed", "int16", "Short"),
		mkAddr("bad", badName, "int16", "Short"),
		mkAddr("a3", "Other", "int16", "Short"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v（一个坏标签名不该让整批失败）", err)
	}
	assertAligned(t, addrs, results)

	// 坏点位：地址无效、无值，但 DataType/Kind 必须照填（下游靠它决定怎么写）。
	assertResult(t, results[1], "bad", "", "int16", driver.QualityBadAddress)
	if results[1].Kind == "" {
		t.Fatalf("坏点位必须带回 Kind, got %q", results[1].Kind)
	}
	assertResult(t, results[0], "a1", "100", "int16", driver.QualityGood)
	assertResult(t, results[2], "a3", "100", "int16", driver.QualityGood)
}

// TestRead_AllUnparsable 一批全坏：不报错、全部地址无效、零次上线。
//
// 这条回归「len(valid)>0 才调 buildPlan」那个守卫 —— 没有它，
// buildPlan 会对空输入报 "no addresses to read"。
func TestRead_AllUnparsable(t *testing.T) {
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	mock := &mockTransport{connected: true}
	d.client = mock

	addrs := []po.DeviceAddress{
		mkAddr("bad1", badName, "int16", "Short"),
		mkAddr("bad2", "温度1", "int16", "Short"), // 非 ASCII
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v（全坏也不该失败）", err)
	}
	assertAligned(t, addrs, results)
	for i, r := range results {
		assertResult(t, r, addrs[i].ID, "", "int16", driver.QualityBadAddress)
		if r.Kind == "" {
			t.Fatalf("第 %d 项必须带回 Kind", i)
		}
	}
	if len(mock.calls) != 0 {
		t.Fatalf("一个可解析的标签名都没有，不该发出任何请求: %+v", mock.calls)
	}
}

// TestPlan_BadRidesTheProcessCacheWithoutTTL 把「bad 挂在进程级缓存上」这件事
// 钉住 —— arrCache 是**进程级、按内容指纹、刻意不设 TTL** 的（见它的注释与
// CHANGELOG），所以这里要证明两件事：
//
//	① bad 随计划一起被缓存下来（重复调用拿到的仍是同一份 bad）；
//	② 名字改对之后**指纹变了**，新计划里那个点位回到 tags 里、bad 清空 ——
//	   因此它不需要任何 TTL 或跨轮持久化机制，也就不会与 arrCache 那条
//	   「别把带 TTL 的每驱动状态混进进程级缓存」的取舍冲突。
func TestPlan_BadRidesTheProcessCacheWithoutTTL(t *testing.T) {
	bad := []po.DeviceAddress{
		mkAddr("a1", "MotorSpeed", "int16", "Short"),
		mkAddr("bad", badName, "int16", "Short"),
	}

	plan, err := getPlan(bad)
	if err != nil {
		t.Fatalf("getPlan = %v", err)
	}
	if plan.bad["bad"] != driver.QualityBadAddress {
		t.Fatalf("plan.bad = %+v, want bad 点位标 %d", plan.bad, driver.QualityBadAddress)
	}
	// bad 的点位不该进任何标签组。
	for _, tag := range plan.tags {
		if tag.Name == badName {
			t.Fatalf("坏标签名进了计划: %+v", plan.tags)
		}
	}

	// ① 同一份输入再问一次：命中缓存，bad 仍在。
	again, err := getPlan(bad)
	if err != nil {
		t.Fatalf("getPlan(2) = %v", err)
	}
	if again.bad["bad"] != driver.QualityBadAddress {
		t.Fatalf("缓存命中后 bad 丢了: %+v", again.bad)
	}

	// ② 把名字改对（只改这一个字段）⇒ 指纹变 ⇒ 新计划，点位回到计划里。
	fixed := []po.DeviceAddress{
		mkAddr("a1", "MotorSpeed", "int16", "Short"),
		mkAddr("bad", "Line1Temp", "int16", "Short"),
	}
	fixedPlan, err := getPlan(fixed)
	if err != nil {
		t.Fatalf("getPlan(fixed) = %v", err)
	}
	if len(fixedPlan.bad) != 0 {
		t.Fatalf("名字改对后 bad 应为空, got %+v", fixedPlan.bad)
	}
	found := false
	for _, tag := range fixedPlan.tags {
		if tag.Name == "Line1Temp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("名字改对后该标签应回到计划里, got %+v", fixedPlan.tags)
	}
}

// TestPartitionBadTags_KeepsOrderAndCount 拆分的纯函数语义。
func TestPartitionBadTags_KeepsOrderAndCount(t *testing.T) {
	addrs := []po.DeviceAddress{
		mkAddr("a", "MotorSpeed", "int16", "Short"),
		mkAddr("b", badName, "int16", "Short"),
		mkAddr("c", "Other", "int16", "Short"),
	}
	valid, bad := partitionBadTags(addrs)

	if len(valid) != 2 || valid[0].ID != "a" || valid[1].ID != "c" {
		t.Fatalf("valid = %+v, want [a c]", valid)
	}
	if len(bad) != 1 || bad["b"] != driver.QualityBadAddress {
		t.Fatalf("bad = %+v, want [b → %d]", bad, driver.QualityBadAddress)
	}
}
