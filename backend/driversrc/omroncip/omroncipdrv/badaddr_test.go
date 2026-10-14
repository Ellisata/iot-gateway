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
// ParseCIPAddress 只收可打印 ASCII（0x21..0x7E），所以一个**空格**或一个**中文**
// 字符就够 —— 把「Line 1.Temp」或中文说明填进地址栏是常见手误。
//
// 修复前的行为是：CalcCIPTags 全量解析、遇第一个非法标签名直接返回 error，于是
// drv.Read 报错 → 采集侧把它当成**设备故障**（incError + reportState(false)）→
// 连续三轮后落一条**断联**报警并推通知。而设备全程在线（IsConnected 为真，重连
// 逻辑根本不启动），坏标签之后的标签也一起被跳过 —— 一个手误把整台设备拖垮。
//
// 现在：解析不出的点位在规划前被摘出去（partitionBadTags），Read 不报错，该点位
// 返回一条 Quality=QualityBadAddress 的记录，由点位质量报警报出「地址无效」。

// badName 含空格 —— ParseCIPAddress 里 0x20 < 0x21，直接拒。
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
	d := newCIPDriver()
	d.config = DefaultCIPConfig()
	d.config.MaxTagsPerRequest = 0 // 单读路径
	d.client = &mockCIPTransport{connected: true, readFn: func(tag string, code uint16) ([]byte, error) {
		if tag != "D100" && tag != "D102" {
			return nil, fmt.Errorf("不该读到标签 %q（它本地就解析不出来，一个字节都不该发出去）", tag)
		}
		return []byte{0x64, 0x00}, nil // int16=100
	}}

	addrs := []po.DeviceAddress{
		mkCIPAddr("a1", "D100", "int16", "Short"),
		mkCIPAddr("bad", badName, "int16", "Short"),
		mkCIPAddr("a3", "D102", "int16", "Short"),
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
	// 好点位不受影响。
	assertResult(t, results[0], "a1", "100", "int16", driver.QualityGood)
	assertResult(t, results[2], "a3", "100", "int16", driver.QualityGood)
}

// TestRead_AllUnparsable 一批全坏：不报错、全部地址无效、零次上线。
//
// 这条回归「len(valid)>0 才调 CalcCIPTags」那个守卫 —— 没有它，
// CalcCIPTags 会对空输入报 "no addresses to read"。
func TestRead_AllUnparsable(t *testing.T) {
	d := newCIPDriver()
	d.config = DefaultCIPConfig()
	d.config.MaxTagsPerRequest = 0
	transport := &mockCIPTransport{connected: true}
	d.client = transport

	addrs := []po.DeviceAddress{
		mkCIPAddr("bad1", badName, "int16", "Short"),
		mkCIPAddr("bad2", "温度1", "int16", "Short"), // 非 ASCII
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
	if transport.singleCalls != 0 || transport.batchCalls != 0 {
		t.Fatalf("一个可解析的标签名都没有，不该发出任何请求 (single=%d batch=%d)",
			transport.singleCalls, transport.batchCalls)
	}
}

// TestPartitionBadTags_KeepsOrderAndCount 拆分的纯函数语义。
func TestPartitionBadTags_KeepsOrderAndCount(t *testing.T) {
	addrs := []po.DeviceAddress{
		mkCIPAddr("a", "D100", "int16", "Short"),
		mkCIPAddr("b", badName, "int16", "Short"),
		mkCIPAddr("c", "MotorSpeed", "int16", "Short"),
	}
	valid, bad := partitionBadTags(addrs)

	if len(valid) != 2 || valid[0].ID != "a" || valid[1].ID != "c" {
		t.Fatalf("valid = %+v, want [a c]", valid)
	}
	if len(bad) != 1 || bad["b"] != driver.QualityBadAddress {
		t.Fatalf("bad = %+v, want [b → %d]", bad, driver.QualityBadAddress)
	}
}
