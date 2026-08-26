package mitsubishi

import (
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

func mkAddr(id, name, dataType string) po.DeviceAddress {
	return po.DeviceAddress{ID: id, Name: name, DataType: dataType, CommonDataType: dataType}
}

func mcCfg(maxGap, maxWords, stringLen int) *MCConfig {
	cfg := DefaultMCConfig()
	cfg.MaxGap = maxGap
	cfg.MaxReadWords = maxWords
	cfg.StringLen = stringLen
	return cfg
}

// TestCalcMCRangesMerge 相邻区间按 maxGap 合并。
func TestCalcMCRangesMerge(t *testing.T) {
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D108", "int16"), // gap = 7 ≤ 8，合并
	}
	ranges, err := CalcMCRanges(addrs, mcCfg(8, 100, 16))
	if err != nil {
		t.Fatalf("CalcMCRanges = %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	r := ranges[0]
	if r.BitMode || r.Start != 100 || r.Points != 9 || len(r.AddressMap) != 2 {
		t.Errorf("range = %+v, want [100, points 9) 2 points", r)
	}
}

// TestCalcMCRangesNoMerge gap 超限不合并。
func TestCalcMCRangesNoMerge(t *testing.T) {
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D200", "int16"), // gap 99 > 8
	}
	ranges, err := CalcMCRanges(addrs, mcCfg(8, 100, 16))
	if err != nil {
		t.Fatalf("CalcMCRanges = %v", err)
	}
	if len(ranges) != 2 {
		t.Fatalf("want 2 ranges, got %d", len(ranges))
	}
	if ranges[0].Start != 100 || ranges[1].Start != 200 {
		t.Errorf("range starts = %d, %d", ranges[0].Start, ranges[1].Start)
	}
}

// TestCalcMCRangesMultiWord 多字类型跨度延伸（float32 占 2 字）。
func TestCalcMCRangesMultiWord(t *testing.T) {
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "float32"), // 100-101
		mkAddr("2", "D102", "int16"),   // 102，gap=0 合并
	}
	ranges, err := CalcMCRanges(addrs, mcCfg(8, 100, 16))
	if err != nil {
		t.Fatalf("CalcMCRanges = %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	r := ranges[0]
	if r.Start != 100 || r.Points != 3 { // [100, 103)
		t.Errorf("range = [%d, points %d), want [100, 3)", r.Start, r.Points)
	}
	// float32 点位跨度 2 字
	if addr, ok := r.AddressMap["1"]; !ok || addr.SpanWords != 2 || addr.Word != 100 {
		t.Errorf("addr 1 = %+v", addr)
	}
}

// TestCalcMCRangesBitMode bool 位设备走位模式。
func TestCalcMCRangesBitMode(t *testing.T) {
	addrs := []po.DeviceAddress{
		mkAddr("1", "M10", "bool"),
		mkAddr("2", "M12", "bool"), // gap 1 ≤ 8，合并
	}
	ranges, err := CalcMCRanges(addrs, mcCfg(8, 100, 16))
	if err != nil {
		t.Fatalf("CalcMCRanges = %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	r := ranges[0]
	if !r.BitMode || r.Start != 10 || r.Points != 3 {
		t.Errorf("range = %+v, want bit mode [10, points 3)", r)
	}
}

// TestCalcMCRangesSeparateDevices 不同设备分组。
func TestCalcMCRangesSeparateDevices(t *testing.T) {
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "M10", "bool"),
		mkAddr("3", "W100", "int16"),
	}
	ranges, err := CalcMCRanges(addrs, mcCfg(8, 100, 16))
	if err != nil {
		t.Fatalf("CalcMCRanges = %v", err)
	}
	if len(ranges) != 3 { // D 字 + M 位 + W 字
		t.Fatalf("want 3 ranges, got %d", len(ranges))
	}
}

// TestCalcMCRangesBitDeviceWordAlign 位设备字访问读头对齐到 16 位边界（位地址）。
func TestCalcMCRangesBitDeviceWordAlign(t *testing.T) {
	// M10(floor/16→0) + M4(floor/16→0)：对齐到同一读头 0，合并
	addrs := []po.DeviceAddress{
		mkAddr("1", "M10", "int16"),
		mkAddr("2", "M4", "int16"),
	}
	ranges, err := CalcMCRanges(addrs, mcCfg(8, 100, 16))
	if err != nil {
		t.Fatalf("CalcMCRanges = %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	r := ranges[0]
	if r.BitMode || r.Start != 0 || r.Points != 1 {
		t.Errorf("range = %+v, want word mode [0, points 1)", r)
	}
	if addr := r.AddressMap["1"]; addr.Word != 0 || addr.SpanWords != 1 {
		t.Errorf("addr 1 = %+v, want Word=0", addr)
	}

	// M16 对齐到 16（第 2 个字），与 M0 读头 gap 15 > 8 → 两个区间
	addrs = []po.DeviceAddress{
		mkAddr("1", "M16", "int16"),
		mkAddr("2", "M0", "int16"),
	}
	ranges, err = CalcMCRanges(addrs, mcCfg(8, 100, 16))
	if err != nil {
		t.Fatalf("CalcMCRanges = %v", err)
	}
	if len(ranges) != 2 {
		t.Fatalf("want 2 ranges, got %d", len(ranges))
	}
	if ranges[0].Start != 0 || ranges[1].Start != 16 {
		t.Errorf("range starts = %d, %d, want 0, 16", ranges[0].Start, ranges[1].Start)
	}
	if addr := ranges[1].AddressMap["1"]; addr.Word != 16 {
		t.Errorf("M16 aligned word = %d, want 16", addr.Word)
	}
}

// TestCalcMCRangesWordDeviceBool 字设备 bool 默认取 bit 0。
func TestCalcMCRangesWordDeviceBool(t *testing.T) {
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "bool"),
		mkAddr("2", "D100.15", "bool"),
	}
	ranges, err := CalcMCRanges(addrs, mcCfg(8, 100, 16))
	if err != nil {
		t.Fatalf("CalcMCRanges = %v", err)
	}
	if len(ranges) != 1 || ranges[0].BitMode {
		t.Fatalf("want 1 word range, got %d", len(ranges))
	}
	// D100 bool → Bit 0；D100.15 bool → Bit 15；同字合并
	if a1 := ranges[0].AddressMap["1"]; a1.Bit != 0 {
		t.Errorf("D100 bool bit = %d, want 0", a1.Bit)
	}
	if a2 := ranges[0].AddressMap["2"]; a2.Bit != 15 {
		t.Errorf("D100.15 bit = %d, want 15", a2.Bit)
	}
}

// TestCalcMCRangesChunk 单区间点数超 maxReadWords 分块。
func TestCalcMCRangesChunk(t *testing.T) {
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D101", "int16"),
		mkAddr("3", "D102", "int16"),
	}
	ranges, err := CalcMCRanges(addrs, mcCfg(8, 2, 16)) // maxWords=2
	if err != nil {
		t.Fatalf("CalcMCRanges = %v", err)
	}
	if len(ranges) != 2 {
		t.Fatalf("want 2 ranges, got %d", len(ranges))
	}
	if ranges[0].Start != 100 || ranges[0].Points != 2 {
		t.Errorf("range[0] = %+v, want [100, points 2)", ranges[0])
	}
	if ranges[1].Start != 102 || ranges[1].Points != 1 {
		t.Errorf("range[1] = %+v, want [102, points 1)", ranges[1])
	}
}

// TestCalcMCRangesString string 按 stringLen 字数折算。
func TestCalcMCRangesString(t *testing.T) {
	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "string"),
	}
	ranges, err := CalcMCRanges(addrs, mcCfg(8, 100, 4)) // stringLen=4 字
	if err != nil {
		t.Fatalf("CalcMCRanges = %v", err)
	}
	r := ranges[0]
	if r.Start != 100 || r.Points != 4 {
		t.Errorf("range = %+v, want [100, points 4)", r)
	}
	if a := r.AddressMap["1"]; !a.IsString || a.SpanWords != 4 {
		t.Errorf("addr = %+v", a)
	}
}

// TestCalcMCRangesErrors 非法地址/超限报错。
func TestCalcMCRangesErrors(t *testing.T) {
	if _, err := CalcMCRanges(nil, mcCfg(8, 100, 16)); err == nil {
		t.Errorf("expected empty addresses error")
	}
	if _, err := CalcMCRanges([]po.DeviceAddress{mkAddr("1", "NOPE", "int16")}, mcCfg(8, 100, 16)); err == nil {
		t.Errorf("expected invalid address error")
	}
	// 单点位跨度超 maxReadWords 报错
	if _, err := CalcMCRanges([]po.DeviceAddress{mkAddr("1", "D100", "float64")}, mcCfg(8, 1, 16)); err == nil {
		t.Errorf("expected span exceeds maxReadWords error")
	}
}

// TestRangeSig 指纹稳定且能区分不同批次（作为区间缓存键的基础）。
func TestRangeSig(t *testing.T) {
	a := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D100.5", "bool"),
	}

	// 同内容（不同切片实例）→ 相同指纹
	if got := driver.RangeSig([]po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D100.5", "bool"),
	}); got != driver.RangeSig(a) {
		t.Errorf("same content should produce same signature")
	}
	// 顺序不同 → 指纹不同（批次顺序在轮询间稳定，不影响正确性）
	if driver.RangeSig(a) == driver.RangeSig([]po.DeviceAddress{
		mkAddr("2", "D100.5", "bool"),
		mkAddr("1", "D100", "int16"),
	}) {
		t.Errorf("different order should produce different signature")
	}
	// 内容不同（地址或类型变化）→ 指纹不同
	if driver.RangeSig(a) == driver.RangeSig([]po.DeviceAddress{
		mkAddr("1", "D101", "int16"),
		mkAddr("2", "D100.5", "bool"),
	}) {
		t.Errorf("different address should produce different signature")
	}
	if driver.RangeSig(a) == driver.RangeSig([]po.DeviceAddress{
		mkAddr("1", "D100", "uint16"),
		mkAddr("2", "D100.5", "bool"),
	}) {
		t.Errorf("different data type should produce different signature")
	}
}
