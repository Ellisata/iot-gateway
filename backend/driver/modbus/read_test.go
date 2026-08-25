package modbus

import (
	"errors"
	"testing"

	goburrowModbus "github.com/goburrow/modbus"

	"iot-gateway/model/po"
)

// mockReadCall 记录一次 mock 读取调用的起始地址与数量
type mockReadCall struct {
	addr uint16
	qty  uint16
}

// TestReadRegisterRanges_FallbackOnGap 验证含空洞的合并区间整段读取失败时，
// 自动降级为严格子段重读，各点位仍能取到数据。
func TestReadRegisterRanges_FallbackOnGap(t *testing.T) {
	// 两个点相距 100（窗口内），中间是空洞
	addrs := []po.DeviceAddress{
		{ID: "p1", Name: "0", DataType: "int16", CommonDataType: "int16"},
		{ID: "p2", Name: "100", DataType: "int16", CommonDataType: "int16"},
	}
	cfg := DefaultModbusTcpConfig()

	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{MergeWindow: 125})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 merged range, got %d", len(ranges))
	}
	if ranges[0].Quantity != 101 {
		t.Fatalf("merged range quantity = %d, want 101 (span 0..100)", ranges[0].Quantity)
	}

	// mock readFn：跨空洞整段(qty 101)失败，单点(qty 1)成功
	var calls []mockReadCall
	readFn := func(addr, qty uint16) ([]byte, error) {
		calls = append(calls, mockReadCall{addr: addr, qty: qty})
		if qty >= 101 {
			return nil, errors.New("illegal data address (gap register)")
		}
		// 单寄存器：大端 int16 = 123
		return []byte{0x00, 0x7B}, nil
	}

	results, err := readRegisterRanges(readFn, addrs, ranges, cfg)
	if err != nil {
		t.Fatalf("fallback should succeed, got: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}

	// 空洞整段只尝试 1 次即降级，不重试；降级后只读实际点位（0 与 100 两个单点）
	fullAttempts := 0
	for _, c := range calls {
		if c.qty >= 101 {
			fullAttempts++
		}
	}
	if fullAttempts != 1 {
		t.Fatalf("full merged range should be attempted exactly once, got %d, calls: %v", fullAttempts, calls)
	}
	if len(calls) != 3 {
		t.Fatalf("want calls [full, strict(0), strict(100)], got: %v", calls)
	}

	got := map[string]string{}
	for _, r := range results {
		got[r.DeviceAddressID] = r.Value
	}
	for _, id := range []string{"p1", "p2"} {
		if got[id] != "123" {
			t.Errorf("point %s value = %q, want %q", id, got[id], "123")
		}
	}
}

// TestReadRegisterRanges_NoFallbackOnDense 验证稠密区间（无空洞）读取失败时不降级，
// 直接返回错误——与 mergeWindow 关闭时的既有行为保持一致。
func TestReadRegisterRanges_NoFallbackOnDense(t *testing.T) {
	addrs := []po.DeviceAddress{
		{ID: "d1", Name: "10", DataType: "int16", CommonDataType: "int16"},
		{ID: "d2", Name: "11", DataType: "int16", CommonDataType: "int16"},
	}
	cfg := DefaultModbusTcpConfig()

	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{MergeWindow: 125})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}
	if len(ranges) != 1 || ranges[0].Quantity != 2 {
		t.Fatalf("want 1 dense range of qty 2, got %v", ranges)
	}

	readFn := func(addr, qty uint16) ([]byte, error) {
		return nil, errors.New("device offline")
	}

	if _, err := readRegisterRanges(readFn, addrs, ranges, cfg); err == nil {
		t.Fatal("dense range read failure should NOT fall back, want error")
	}
}

// TestCalcReadRanges_StringSpanExtendsRange 验证 string 点位按 stringLen 折算寄存器跨度，
// 区间末端延伸到覆盖其完整数据（不再因落在区间末尾被静默判 Quality=0）。
func TestCalcReadRanges_StringSpanExtendsRange(t *testing.T) {
	addrs := []po.DeviceAddress{
		{ID: "s1", Name: "10", DataType: "string", CommonDataType: "string"},
	}
	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{MergeWindow: 125, StringLen: 16})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	// string 16 字节 = 8 个寄存器：范围应覆盖 10..17
	if ranges[0].StartAddress != 10 || ranges[0].Quantity != 8 {
		t.Fatalf("range = [%d, qty %d), want [10, qty 8)", ranges[0].StartAddress, ranges[0].Quantity)
	}
}

// TestReadRegisterRanges_ProtocolErrDegradesToQualityZero 验证稠密区间读取失败为协议异常时，
// 该区间点位标记 Quality=0 并继续（而非整台设备读取失败、整轮数据丢弃）。
func TestReadRegisterRanges_ProtocolErrDegradesToQualityZero(t *testing.T) {
	addrs := []po.DeviceAddress{
		{ID: "d1", Name: "10", DataType: "int16", CommonDataType: "int16"},
		{ID: "d2", Name: "11", DataType: "int16", CommonDataType: "int16"},
	}
	cfg := DefaultModbusTcpConfig()
	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{MergeWindow: 125})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}

	// 协议异常：设备响应但拒绝该地址（IllegalDataAddress）
	readFn := func(addr, qty uint16) ([]byte, error) {
		return nil, &goburrowModbus.ModbusError{FunctionCode: 3, ExceptionCode: 2}
	}

	results, err := readRegisterRanges(readFn, addrs, ranges, cfg)
	if err != nil {
		t.Fatalf("protocol error should degrade to quality=0, got error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Quality != 0 {
			t.Errorf("point %s quality = %d, want 0 (protocol error)", r.DeviceAddressID, r.Quality)
		}
	}
}

// TestGroupAddrsByFC_Mixed 验证线圈与保持寄存器混合点位被按功能码拆分，
// 而不是整组报错。
func TestGroupAddrsByFC_Mixed(t *testing.T) {
	addrs := []po.DeviceAddress{
		{ID: "r1", Name: "40001", DataType: "int16"}, // 保持寄存器 FC3
		{ID: "c1", Name: "00001", DataType: "bool"},  // 线圈 FC1
		{ID: "p1", Name: "100", DataType: "int16"},   // 纯数字 → 归入 cfg.FunctionCode
	}
	cfg := DefaultModbusTcpConfig() // FunctionCode 默认 3

	groups, err := groupAddrsByFC(addrs, cfg)
	if err != nil {
		t.Fatalf("groupAddrsByFC failed: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("want 2 fc groups (1 and 3), got %d", len(groups))
	}
	if len(groups[FuncCodeReadCoils]) != 1 || groups[FuncCodeReadCoils][0].ID != "c1" {
		t.Fatalf("fc1 group = %+v, want [c1]", groups[FuncCodeReadCoils])
	}
	if len(groups[FuncCodeReadHoldingRegisters]) != 2 {
		t.Fatalf("fc3 group = %+v, want [r1 p1]", groups[FuncCodeReadHoldingRegisters])
	}
}

// TestReadBitRanges_FallbackOnGap 验证位类型（FC1/FC2）同样的空洞降级路径。
func TestReadBitRanges_FallbackOnGap(t *testing.T) {
	addrs := []po.DeviceAddress{
		{ID: "b1", Name: "0", DataType: "bool", CommonDataType: "bool"},
		{ID: "b2", Name: "100", DataType: "bool", CommonDataType: "bool"},
	}

	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{MergeWindow: 125})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 merged bit range, got %d", len(ranges))
	}

	readFn := func(addr, qty uint16) ([]byte, error) {
		if qty >= 101 {
			return nil, errors.New("illegal data address (gap)")
		}
		return []byte{0x01}, nil
	}

	results, err := readBitRanges(readFn, addrs, ranges)
	if err != nil {
		t.Fatalf("fallback should succeed, got: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Value != "1" {
			t.Errorf("point %s value = %q, want \"1\"", r.DeviceAddressID, r.Value)
		}
	}
}
