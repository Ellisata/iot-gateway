// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"testing"

	"iot-gateway/model/po"
)

// mkAddr 构造测试用点位
func mkAddr(id, name, dataType string) po.DeviceAddress {
	return po.DeviceAddress{ID: id, Name: name, DataType: dataType}
}

// cfgWith 返回带指定 MergeWindow/MaxReadWords 的默认配置
func cfgWith(mergeWindow, maxReadWords int) *FINSConfig {
	cfg := DefaultFINSConfig()
	cfg.MergeWindow = uint16(mergeWindow)
	cfg.MaxReadWords = maxReadWords
	return cfg
}

func TestCalcFINSRangesContiguous(t *testing.T) {
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D101", "int16"),
		mkAddr("3", "D102", "int16"),
	}
	ranges, err := CalcFINSRanges(addrs, cfgWith(10, 100))
	if err != nil {
		t.Fatalf("CalcFINSRanges = %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	r := ranges[0]
	if r.Area != AreaDM || r.StartWord != 100 || r.Count != 3 {
		t.Errorf("range = area=0x%02X [%d, count=%d], want DM [100, 3]", r.Area, r.StartWord, r.Count)
	}
	if len(r.AddressMap) != 3 {
		t.Errorf("AddressMap has %d entries, want 3", len(r.AddressMap))
	}
}

func TestCalcFINSRangesGapMerge(t *testing.T) {
	// 间隙 ≤ maxGap：合并为一个区间（跳过空洞）
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D105", "int16"),
	}
	ranges, err := CalcFINSRanges(addrs, cfgWith(10, 100))
	if err != nil {
		t.Fatalf("CalcFINSRanges = %v", err)
	}
	if len(ranges) != 1 || ranges[0].StartWord != 100 || ranges[0].Count != 6 {
		t.Errorf("want 1 merged range [100,6], got %+v", ranges)
	}
}

func TestCalcFINSRangesGapSplit(t *testing.T) {
	// 间隙 > maxGap：拆分为两个区间
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D150", "int16"),
	}
	ranges, err := CalcFINSRanges(addrs, cfgWith(10, 100))
	if err != nil {
		t.Fatalf("CalcFINSRanges = %v", err)
	}
	if len(ranges) != 2 {
		t.Fatalf("want 2 ranges, got %d", len(ranges))
	}
	if ranges[0].StartWord != 100 || ranges[1].StartWord != 150 {
		t.Errorf("starts = [%d, %d], want [100, 150]", ranges[0].StartWord, ranges[1].StartWord)
	}
}

func TestCalcFINSRangesChunk(t *testing.T) {
	// maxReadWords=3：D100..D105 按 3 字分块
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D101", "int16"),
		mkAddr("3", "D102", "int16"),
		mkAddr("4", "D103", "int16"),
		mkAddr("5", "D104", "int16"),
		mkAddr("6", "D105", "int16"),
	}
	ranges, err := CalcFINSRanges(addrs, cfgWith(10, 3))
	if err != nil {
		t.Fatalf("CalcFINSRanges = %v", err)
	}
	if len(ranges) != 2 {
		t.Fatalf("want 2 chunked ranges, got %d", len(ranges))
	}
	if ranges[0].StartWord != 100 || ranges[0].Count != 3 ||
		ranges[1].StartWord != 103 || ranges[1].Count != 3 {
		t.Errorf("chunks = [%d,%d] [%d,%d], want [100,3] [103,3]",
			ranges[0].StartWord, ranges[0].Count, ranges[1].StartWord, ranges[1].Count)
	}
}

func TestCalcFINSRangesBitSharesWord(t *testing.T) {
	// bool 点位占所在字，与同字 int 点位合并进同一区间
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D100.05", "bool"),
		mkAddr("3", "D101", "int16"),
	}
	ranges, err := CalcFINSRanges(addrs, cfgWith(10, 100))
	if err != nil {
		t.Fatalf("CalcFINSRanges = %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	r := ranges[0]
	if r.StartWord != 100 || r.Count != 2 {
		t.Errorf("range = [%d, count=%d], want [100, 2]", r.StartWord, r.Count)
	}
	if len(r.AddressMap) != 3 {
		t.Errorf("AddressMap has %d entries, want 3", len(r.AddressMap))
	}
	// 验证 bool 点位映射到所在字
	ba, ok := r.AddressMap["2"]
	if !ok || ba.Word != 100 || !ba.IsBit() || ba.Bit != 5 {
		t.Errorf("bool address map = %+v, want word=100 bit=5", ba)
	}
}

func TestCalcFINSRangesMultiWordType(t *testing.T) {
	// int32 占 2 字，区间末端延伸覆盖完整数据
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int32"),
		mkAddr("2", "D102", "int16"),
	}
	ranges, err := CalcFINSRanges(addrs, cfgWith(10, 100))
	if err != nil {
		t.Fatalf("CalcFINSRanges = %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	if ranges[0].StartWord != 100 || ranges[0].Count != 3 {
		t.Errorf("range = [%d, count=%d], want [100, 3]", ranges[0].StartWord, ranges[0].Count)
	}
}

func TestCalcFINSRangesCrossArea(t *testing.T) {
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "CIO100", "int16"),
	}
	ranges, err := CalcFINSRanges(addrs, cfgWith(10, 100))
	if err != nil {
		t.Fatalf("CalcFINSRanges = %v", err)
	}
	if len(ranges) != 2 {
		t.Fatalf("want 2 ranges (different areas), got %d", len(ranges))
	}
	areas := map[finsArea]bool{}
	for _, r := range ranges {
		areas[r.Area] = true
	}
	if !areas[AreaDM] || !areas[AreaCIO] {
		t.Errorf("areas = %+v, want DM and CIO", areas)
	}
}

func TestCalcFINSRangesEmpty(t *testing.T) {
	if _, err := CalcFINSRanges(nil, cfgWith(10, 100)); err == nil {
		t.Fatalf("expected error for empty addresses")
	}
}

func TestCalcFINSRangesInvalidAddress(t *testing.T) {
	addrs := []po.DeviceAddress{mkAddr("1", "BANANA", "int16")}
	if _, err := CalcFINSRanges(addrs, cfgWith(10, 100)); err == nil {
		t.Fatalf("expected error for invalid address")
	}
}
