// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbus

import (
	"fmt"
	"strconv"
	"testing"

	"iot-gateway/model/po"
)

// parseResult 简化测试断言的辅助结构
type parseResult struct {
	fc   byte
	addr uint16
	ok   bool
}

func TestParsePLCAddress_Traditional(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  parseResult
	}{
		// 标准 5 位
		{"holding register 1", "40001", parseResult{3, 0, true}},
		{"holding register 2", "40002", parseResult{3, 1, true}},
		{"input register 1", "30001", parseResult{4, 0, true}},
		{"coil 1", "00001", parseResult{1, 0, true}},
		{"discrete input 1", "10001", parseResult{2, 0, true}},
		// 6 位（地址 > 9999）
		{"holding register 10001", "410001", parseResult{3, 10000, true}},
		{"holding register 20001", "420001", parseResult{3, 20000, true}},
		{"input register 10001", "310001", parseResult{4, 10000, true}},
		// 非传统格式（会回退到其他格式匹配）
		{"short as plain", "401", parseResult{0, 401, true}},           // 纯数字: addr=401
		{"no prefix as plain", "50001", parseResult{0, 50001, true}},   // 纯数字: addr=50001
		{"no prefix as plain 2", "40000", parseResult{0, 40000, true}}, // 纯数字: addr=40000
		{"non-numeric after prefix", "4abc1", parseResult{0, 0, false}},
		{"all zeros as plain", "00000", parseResult{0, 0, true}}, // 纯数字: addr=0
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc, addr, ok := ParsePLCAddress(tt.input)
			if ok != tt.want.ok || fc != tt.want.fc || addr != tt.want.addr {
				t.Errorf("ParsePLCAddress(%q) = (%d, %d, %v), want (%d, %d, %v)",
					tt.input, fc, addr, ok, tt.want.fc, tt.want.addr, tt.want.ok)
			}
		})
	}
}

func TestParsePLCAddress_ExplicitPrefix(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  parseResult
	}{
		{"holding reg 0", "4x0", parseResult{3, 0, true}},
		{"holding reg 0 (upper X)", "4X0", parseResult{3, 0, true}},
		{"holding reg 1", "4x0001", parseResult{3, 0, true}},
		{"holding reg 10001", "4x10001", parseResult{3, 10000, true}},
		{"input reg 1", "3x0001", parseResult{4, 0, true}},
		// 注意: "0x..." 格式会被 hex 先匹配（见 TestParsePLCAddress_Hex），
		// 线圈的显式前缀应用传统格式 "00001" 或纯数字 "0"。
		{"discrete input 1", "1x0001", parseResult{2, 0, true}},
		// 无效
		{"no x separator", "4-0001", parseResult{0, 0, false}},
		{"x at wrong position", "40x001", parseResult{0, 0, false}},
		{"no number after x", "4x", parseResult{0, 0, false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc, addr, ok := ParsePLCAddress(tt.input)
			if ok != tt.want.ok || fc != tt.want.fc || addr != tt.want.addr {
				t.Errorf("ParsePLCAddress(%q) = (%d, %d, %v), want (%d, %d, %v)",
					tt.input, fc, addr, ok, tt.want.fc, tt.want.addr, tt.want.ok)
			}
		})
	}
}

func TestParsePLCAddress_IEC(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  parseResult
	}{
		{"MW holding reg 0", "%MW0", parseResult{3, 0, true}},
		{"MW holding reg 100", "%MW100", parseResult{3, 100, true}},
		{"IW input reg 100", "%IW100", parseResult{4, 100, true}},
		{"QW holding reg 100", "%QW100", parseResult{3, 100, true}},
		{"MX coil 10", "%MX10", parseResult{1, 10, true}},
		{"IX discrete input 10", "%IX10", parseResult{2, 10, true}},
		{"QX coil 10", "%QX10", parseResult{1, 10, true}},
		// case insensitive
		{"lowercase mw", "%mw100", parseResult{3, 100, true}},
		{"mixed case Iw", "%Iw100", parseResult{4, 100, true}},
		// 无效
		{"no percent", "MW100", parseResult{0, 0, false}},
		{"unknown IEC prefix", "%XX100", parseResult{0, 0, false}},
		{"empty number", "%MW", parseResult{0, 0, false}},
		{"too short", "%M", parseResult{0, 0, false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc, addr, ok := ParsePLCAddress(tt.input)
			if ok != tt.want.ok || fc != tt.want.fc || addr != tt.want.addr {
				t.Errorf("ParsePLCAddress(%q) = (%d, %d, %v), want (%d, %d, %v)",
					tt.input, fc, addr, ok, tt.want.fc, tt.want.addr, tt.want.ok)
			}
		})
	}
}

func TestParsePLCAddress_Hex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  parseResult
	}{
		{"hex 0x100 => 256", "0x100", parseResult{0, 256, true}},
		{"hex 0XFF => 255", "0XFF", parseResult{0, 255, true}},
		{"hex 0x0 => 0", "0x0", parseResult{0, 0, true}},
		{"hex 0x10 => 16", "0x10", parseResult{0, 16, true}},
		// 无效
		{"no digits after 0x", "0x", parseResult{0, 0, false}},
		{"no 0x prefix", "x100", parseResult{0, 0, false}},
		{"invalid hex chars", "0xGG", parseResult{0, 0, false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc, addr, ok := ParsePLCAddress(tt.input)
			if ok != tt.want.ok || fc != tt.want.fc || addr != tt.want.addr {
				t.Errorf("ParsePLCAddress(%q) = (%d, %d, %v), want (%d, %d, %v)",
					tt.input, fc, addr, ok, tt.want.fc, tt.want.addr, tt.want.ok)
			}
		})
	}
}

func TestParsePLCAddress_PlainNumber(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  parseResult
	}{
		{"zero", "0", parseResult{0, 0, true}},
		{"address 100", "100", parseResult{0, 100, true}},
		{"max uint16", "65535", parseResult{0, 65535, true}},
		// 无效
		{"negative", "-1", parseResult{0, 0, false}},
		{"overflow uint16", "99999", parseResult{0, 0, false}},
		{"non-numeric", "abc", parseResult{0, 0, false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc, addr, ok := ParsePLCAddress(tt.input)
			if ok != tt.want.ok || fc != tt.want.fc || addr != tt.want.addr {
				t.Errorf("ParsePLCAddress(%q) = (%d, %d, %v), want (%d, %d, %v)",
					tt.input, fc, addr, ok, tt.want.fc, tt.want.addr, tt.want.ok)
			}
		})
	}
}

func TestParsePLCAddress_Whitespace(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  parseResult
	}{
		{"leading spaces", "  40001", parseResult{3, 0, true}},
		{"trailing spaces", "40001  ", parseResult{3, 0, true}},
		{"plain with spaces", "  100  ", parseResult{0, 100, true}},
		{"hex with spaces", "  0x100  ", parseResult{0, 256, true}},
		{"iex with spaces", "  %MW100  ", parseResult{3, 100, true}},
		{"empty string", "", parseResult{0, 0, false}},
		{"only spaces", "   ", parseResult{0, 0, false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc, addr, ok := ParsePLCAddress(tt.input)
			if ok != tt.want.ok || fc != tt.want.fc || addr != tt.want.addr {
				t.Errorf("ParsePLCAddress(%q) = (%d, %d, %v), want (%d, %d, %v)",
					tt.input, fc, addr, ok, tt.want.fc, tt.want.addr, tt.want.ok)
			}
		})
	}
}

func TestParsePLCAddress_FormatPriority(t *testing.T) {
	// 测试格式优先级：传统 > 显式前缀 > IEC > 十六进制 > 纯数字
	//
	// "40001" 应该被传统格式匹配，而不是被其它格式截胡
	fc, addr, ok := ParsePLCAddress("40001")
	if !ok || fc != 3 || addr != 0 {
		t.Errorf("ParsePLCAddress(\"40001\") should match traditional format, got (%d, %d, %v)", fc, addr, ok)
	}

	// "4x0001" 应该被显式前缀匹配
	fc, addr, ok = ParsePLCAddress("4x0001")
	if !ok || fc != 3 || addr != 0 {
		t.Errorf("ParsePLCAddress(\"4x0001\") should match explicit prefix, got (%d, %d, %v)", fc, addr, ok)
	}

	// "%MW100" 应该被 IEC 匹配
	fc, addr, ok = ParsePLCAddress("%MW100")
	if !ok || fc != 3 || addr != 100 {
		t.Errorf("ParsePLCAddress(\"%%MW100\") should match IEC format, got (%d, %d, %v)", fc, addr, ok)
	}

	// "0x100" 应该被十六进制匹配（而不是纯数字）
	fc, addr, ok = ParsePLCAddress("0x100")
	if !ok || addr != 256 {
		t.Errorf("ParsePLCAddress(\"0x100\") should match hex format, got (%d, %d, %v)", fc, addr, ok)
	}

	// "100" 应该被纯数字匹配
	fc, addr, ok = ParsePLCAddress("100")
	if !ok || addr != 100 {
		t.Errorf("ParsePLCAddress(\"100\") should match plain number, got (%d, %d, %v)", fc, addr, ok)
	}
}

// -- CalcReadRanges tests --------------------------------------------------

func TestCalcReadRanges_Auto(t *testing.T) {
	// 自动推导：不连续地址被聚类为多个独立段
	addrs := []po.DeviceAddress{
		{ID: "a1", Name: "40001"},
		{ID: "a2", Name: "40003"},
		{ID: "a3", Name: "411115"},
	}

	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}

	if len(ranges) != 3 {
		t.Fatalf("got %d ranges, want 3 (three non-contiguous addresses)", len(ranges))
	}

	// 第一段：a1 @ 0
	if ranges[0].StartAddress != 0 {
		t.Errorf("ranges[0].StartAddress = %d, want 0", ranges[0].StartAddress)
	}
	if ranges[0].Quantity != 1 {
		t.Errorf("ranges[0].Quantity = %d, want 1", ranges[0].Quantity)
	}
	if ranges[0].AddressMap["a1"] != 0 {
		t.Errorf("ranges[0].AddressMap[a1] = %d, want 0", ranges[0].AddressMap["a1"])
	}
	if len(ranges[0].AddressMap) != 1 {
		t.Errorf("ranges[0] has %d addresses, want 1", len(ranges[0].AddressMap))
	}

	// 第二段：a2 @ 2
	if ranges[1].StartAddress != 2 {
		t.Errorf("ranges[1].StartAddress = %d, want 2", ranges[1].StartAddress)
	}
	if ranges[1].Quantity != 1 {
		t.Errorf("ranges[1].Quantity = %d, want 1", ranges[1].Quantity)
	}
	if ranges[1].AddressMap["a2"] != 2 {
		t.Errorf("ranges[1].AddressMap[a2] = %d, want 2", ranges[1].AddressMap["a2"])
	}

	// 第三段：a3 @ 11114
	if ranges[2].StartAddress != 11114 {
		t.Errorf("ranges[2].StartAddress = %d, want 11114", ranges[2].StartAddress)
	}
	if ranges[2].Quantity != 1 {
		t.Errorf("ranges[2].Quantity = %d, want 1", ranges[2].Quantity)
	}
	if ranges[2].AddressMap["a3"] != 11114 {
		t.Errorf("ranges[2].AddressMap[a3] = %d, want 11114", ranges[2].AddressMap["a3"])
	}
}

func TestCalcReadRanges_ContiguousMerge(t *testing.T) {
	// 连续地址合并为单个段
	addrs := []po.DeviceAddress{
		{ID: "c1", Name: "10"},
		{ID: "c2", Name: "11"},
		{ID: "c3", Name: "12"},
	}

	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}

	if len(ranges) != 1 {
		t.Fatalf("got %d ranges, want 1 (addresses are contiguous)", len(ranges))
	}

	if ranges[0].StartAddress != 10 {
		t.Errorf("StartAddress = %d, want 10", ranges[0].StartAddress)
	}
	if ranges[0].Quantity != 3 {
		t.Errorf("Quantity = %d, want 3 (10-12)", ranges[0].Quantity)
	}
	if ranges[0].AddressMap["c1"] != 10 {
		t.Errorf("AddressMap[c1] = %d, want 10", ranges[0].AddressMap["c1"])
	}
	if ranges[0].AddressMap["c2"] != 11 {
		t.Errorf("AddressMap[c2] = %d, want 11", ranges[0].AddressMap["c2"])
	}
	if ranges[0].AddressMap["c3"] != 12 {
		t.Errorf("AddressMap[c3] = %d, want 12", ranges[0].AddressMap["c3"])
	}
}

func TestCalcReadRanges_NonContiguous(t *testing.T) {
	// 多种 name 格式混合，自动推导，非连续地址分段
	addrs := []po.DeviceAddress{
		{ID: "e1", Name: "40001"},  // 传统 → addr=0
		{ID: "e2", Name: "0x100"},  // hex  → addr=256
		{ID: "e3", Name: "%MW500"}, // IEC  → addr=500
		{ID: "e4", Name: "1000"},   // 纯数字 → addr=1000
	}

	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}

	if len(ranges) != 4 {
		t.Fatalf("got %d ranges, want 4 (four non-contiguous addresses)", len(ranges))
	}

	// 每个地址单独一段，地址和 Quantity 一一对应
	type wantRange struct {
		startAddr uint16
		quantity  uint16
		id        string
		addr      uint16
	}
	wants := []wantRange{
		{0, 1, "e1", 0},
		{256, 1, "e2", 256},
		{500, 1, "e3", 500},
		{1000, 1, "e4", 1000},
	}
	for i, w := range wants {
		if ranges[i].StartAddress != w.startAddr {
			t.Errorf("ranges[%d].StartAddress = %d, want %d", i, ranges[i].StartAddress, w.startAddr)
		}
		if ranges[i].Quantity != w.quantity {
			t.Errorf("ranges[%d].Quantity = %d, want %d", i, ranges[i].Quantity, w.quantity)
		}
		if ranges[i].AddressMap[w.id] != w.addr {
			t.Errorf("ranges[%d].AddressMap[%q] = %d, want %d", i, w.id, ranges[i].AddressMap[w.id], w.addr)
		}
	}
}

func TestCalcReadRange_Auto_SingleAddress(t *testing.T) {
	addrs := []po.DeviceAddress{
		{ID: "x1", Name: "40001"},
	}

	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("got %d ranges, want 1", len(ranges))
	}
	if ranges[0].StartAddress != 0 {
		t.Errorf("StartAddress = %d, want 0", ranges[0].StartAddress)
	}
	if ranges[0].Quantity != 1 {
		t.Errorf("Quantity = %d, want 1", ranges[0].Quantity)
	}
}

func TestCalcReadRange_Config(t *testing.T) {
	// 配置模式：cfgStartAddress > 0 时使用配置值
	addrs := []po.DeviceAddress{
		{ID: "b1", Name: "100"},
		{ID: "b2", Name: "102"},
	}

	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{StartAddress: 100, Quantity: 10})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("got %d ranges, want 1 (config mode uses single range)", len(ranges))
	}
	ar := ranges[0]
	if ar.StartAddress != 100 {
		t.Errorf("StartAddress = %d, want 100", ar.StartAddress)
	}
	if ar.Quantity != 10 {
		t.Errorf("Quantity = %d, want 10", ar.Quantity)
	}
	if ar.AddressMap["b1"] != 100 {
		t.Errorf("AddressMap[b1] = %d, want 100", ar.AddressMap["b1"])
	}
	if ar.AddressMap["b2"] != 102 {
		t.Errorf("AddressMap[b2] = %d, want 102", ar.AddressMap["b2"])
	}
}

func TestCalcReadRange_Config_AddrBelowStart(t *testing.T) {
	// 配置模式：name 解析出的地址 < cfgStartAddress → 报错
	addrs := []po.DeviceAddress{
		{ID: "c1", Name: "50"},
	}

	_, err := CalcReadRanges(addrs, CalcReadRangeOptions{StartAddress: 100, Quantity: 10})
	if err == nil {
		t.Fatal("expected error for address < startAddress, got nil")
	}
}

func TestCalcReadRange_EmptyAddrs(t *testing.T) {
	_, err := CalcReadRanges([]po.DeviceAddress{}, CalcReadRangeOptions{})
	if err == nil {
		t.Fatal("expected error for empty addrs, got nil")
	}
}

func TestCalcReadRange_InvalidName(t *testing.T) {
	addrs := []po.DeviceAddress{
		{ID: "d1", Name: "invalid!"},
	}

	_, err := CalcReadRanges(addrs, CalcReadRangeOptions{})
	if err == nil {
		t.Fatal("expected error for invalid name, got nil")
	}
}

// -- MergeWindow 窗口合并 tests --------------------------------------------------

func TestCalcReadRanges_MergeWindow(t *testing.T) {
	// 散点 + 窗口 125：把跨度 ≤125 的邻近点并入同一区间，减少往返次数
	addrs := []po.DeviceAddress{
		{ID: "m1", Name: "0"},
		{ID: "m2", Name: "100"},
		{ID: "m3", Name: "200"},
		{ID: "m4", Name: "300"},
		{ID: "m5", Name: "400"},
	}

	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{MergeWindow: 125})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}

	// 期望 3 段：[0,100]、[200,300]、[400,400]
	if len(ranges) != 3 {
		t.Fatalf("got %d ranges, want 3 (window merge)", len(ranges))
	}
	wants := []struct {
		start, qty uint16
		ids        []string
	}{
		{0, 101, []string{"m1", "m2"}},
		{200, 101, []string{"m3", "m4"}},
		{400, 1, []string{"m5"}},
	}
	for i, w := range wants {
		if ranges[i].StartAddress != w.start {
			t.Errorf("ranges[%d].StartAddress = %d, want %d", i, ranges[i].StartAddress, w.start)
		}
		if ranges[i].Quantity != w.qty {
			t.Errorf("ranges[%d].Quantity = %d, want %d", i, ranges[i].Quantity, w.qty)
		}
		if len(ranges[i].AddressMap) != len(w.ids) {
			t.Errorf("ranges[%d] has %d points, want %d", i, len(ranges[i].AddressMap), len(w.ids))
		}
		for _, id := range w.ids {
			if _, ok := ranges[i].AddressMap[id]; !ok {
				t.Errorf("ranges[%d] missing point %q", i, id)
			}
		}
	}
}

func TestCalcReadRanges_MergeWindow_NoRegressionOnDenseBlock(t *testing.T) {
	// 稠密块 10..20 + 孤立点 300：稠密块保持完整，不被窗口拆小
	addrs := []po.DeviceAddress{
		{ID: "d1", Name: "10"},
		{ID: "d2", Name: "300"},
	}
	for i := 11; i <= 20; i++ {
		addrs = append(addrs, po.DeviceAddress{ID: fmt.Sprintf("d%d", i), Name: strconv.Itoa(i)})
	}

	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{MergeWindow: 125})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}

	if len(ranges) != 2 {
		t.Fatalf("got %d ranges, want 2 (dense block + isolated point)", len(ranges))
	}
	if ranges[0].StartAddress != 10 || ranges[0].Quantity != 11 {
		t.Errorf("dense block = [%d, qty %d], want [10, qty 11] (must not be split)",
			ranges[0].StartAddress, ranges[0].Quantity)
	}
	if len(ranges[0].AddressMap) != 11 {
		t.Errorf("dense block has %d points, want 11", len(ranges[0].AddressMap))
	}
	if ranges[1].StartAddress != 300 || ranges[1].Quantity != 1 {
		t.Errorf("isolated point range = [%d, qty %d], want [300, qty 1]",
			ranges[1].StartAddress, ranges[1].Quantity)
	}
}

func TestCalcReadRanges_MergeWindow_LargeWindow(t *testing.T) {
	// 窗口足够大（400 ≥ 最大跨度）→ 全部并入单区间
	addrs := []po.DeviceAddress{
		{ID: "m1", Name: "0"},
		{ID: "m2", Name: "100"},
		{ID: "m3", Name: "200"},
		{ID: "m4", Name: "300"},
		{ID: "m5", Name: "400"},
	}

	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{MergeWindow: 400})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}

	if len(ranges) != 1 {
		t.Fatalf("got %d ranges, want 1", len(ranges))
	}
	if ranges[0].StartAddress != 0 || ranges[0].Quantity != 401 {
		t.Errorf("range = [%d, qty %d], want [0, qty 401]", ranges[0].StartAddress, ranges[0].Quantity)
	}
	if len(ranges[0].AddressMap) != 5 {
		t.Errorf("range has %d points, want 5", len(ranges[0].AddressMap))
	}
}

func TestCalcReadRanges_MergeWindow_Boundary(t *testing.T) {
	// 边界：跨度 == 窗口则合并，> 窗口则分段
	merge := []po.DeviceAddress{
		{ID: "a", Name: "0"},
		{ID: "b", Name: "125"},
	}
	split := []po.DeviceAddress{
		{ID: "a", Name: "0"},
		{ID: "b", Name: "126"},
	}

	ranges, err := CalcReadRanges(merge, CalcReadRangeOptions{MergeWindow: 125})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}
	if len(ranges) != 1 || ranges[0].Quantity != 126 {
		t.Errorf("span 125 should merge into 1 range of qty 126, got %d ranges qty=%d",
			len(ranges), ranges[0].Quantity)
	}

	ranges, err = CalcReadRanges(split, CalcReadRangeOptions{MergeWindow: 125})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}
	if len(ranges) != 2 {
		t.Errorf("span 126 should split into 2 ranges, got %d", len(ranges))
	}
}

func TestCalcReadRanges_MergeWindow_ConfigModeIgnored(t *testing.T) {
	// 配置模式（StartAddress > 0）：MergeWindow 忽略，使用配置的起始/数量
	addrs := []po.DeviceAddress{
		{ID: "c1", Name: "100"},
		{ID: "c2", Name: "102"},
	}

	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{
		StartAddress: 100,
		Quantity:     10,
		MergeWindow:  5,
	})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("got %d ranges, want 1 (config mode)", len(ranges))
	}
	if ranges[0].StartAddress != 100 || ranges[0].Quantity != 10 {
		t.Errorf("range = [%d, qty %d], want [100, qty 10]", ranges[0].StartAddress, ranges[0].Quantity)
	}
	if ranges[0].AddressMap["c1"] != 100 || ranges[0].AddressMap["c2"] != 102 {
		t.Errorf("config mode address map wrong: %v", ranges[0].AddressMap)
	}
}

// -- mergeWindow 配置解析 tests --------------------------------------------------

func TestParseModbusTcpConfig_MergeWindow(t *testing.T) {
	// 默认 125
	cfg, err := ParseModbusTcpConfig(`{}`)
	if err != nil {
		t.Fatalf("ParseModbusTcpConfig failed: %v", err)
	}
	if cfg.MergeWindow != defaultMergeWindow {
		t.Errorf("default MergeWindow = %d, want %d", cfg.MergeWindow, defaultMergeWindow)
	}

	// 显式覆盖（原生 JSON 数值）
	cfg, err = ParseModbusTcpConfig(`{"mergeWindow":2000}`)
	if err != nil {
		t.Fatalf("ParseModbusTcpConfig failed: %v", err)
	}
	if cfg.MergeWindow != 2000 {
		t.Errorf("MergeWindow = %d, want 2000", cfg.MergeWindow)
	}

	// 非数字字符串：原生类型解析直接失败（旧的全字符串格式已迁移为原生类型）
	_, err = ParseModbusTcpConfig(`{"mergeWindow":"abc"}`)
	if err == nil {
		t.Fatal("ParseModbusTcpConfig should fail on non-numeric mergeWindow")
	}
}

func TestParseModbusRTUConfig_MergeWindow(t *testing.T) {
	// 默认 125
	cfg, err := ParseModbusRTUConfig(`{}`)
	if err != nil {
		t.Fatalf("ParseModbusRTUConfig failed: %v", err)
	}
	if cfg.MergeWindow != defaultMergeWindow {
		t.Errorf("RTU default MergeWindow = %d, want %d", cfg.MergeWindow, defaultMergeWindow)
	}

	// 显式覆盖（原生 JSON 数值）
	cfg, err = ParseModbusRTUConfig(`{"mergeWindow":2000}`)
	if err != nil {
		t.Fatalf("ParseModbusRTUConfig failed: %v", err)
	}
	if cfg.MergeWindow != 2000 {
		t.Errorf("RTU MergeWindow = %d, want 2000", cfg.MergeWindow)
	}

	// 非数字字符串：原生类型解析直接失败（旧的全字符串格式已迁移为原生类型）
	_, err = ParseModbusRTUConfig(`{"mergeWindow":"abc"}`)
	if err == nil {
		t.Fatal("ParseModbusRTUConfig should fail on non-numeric mergeWindow")
	}
}
