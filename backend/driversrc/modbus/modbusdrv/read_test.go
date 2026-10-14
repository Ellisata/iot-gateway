// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"errors"
	"testing"

	goburrowModbus "github.com/goburrow/modbus"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// mockReadCall 记录一次 mock 读取调用的起始地址与数量
type mockReadCall struct {
	addr uint16
	qty  uint16
}

// modbusExc 构造一个 Modbus 异常（设备响应但拒绝请求）。
// code 用 goburrowModbus.ExceptionCode* 常量。
func modbusExc(code byte) error {
	return &goburrowModbus.ModbusError{FunctionCode: 3, ExceptionCode: code}
}

// TestReadRegisterRanges_FallbackOnGap 验证含空洞的合并区间被设备整段拒绝时，
// 自动降级为逐点重读，各点位仍能取到数据。
//
// 假设备回的是 **IllegalDataAddress**（异常码 0x02）而不是一句普通错误：
// 只有地址类异常才代表"设备读不了这个地址"，才会触发降级 —— 普通错误可能是
// 链路问题（见 TestReadRegisterRanges_NoFallbackOnDense）。
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

	// mock readFn：跨空洞整段(qty 101)被设备拒绝，单点(qty 1)成功
	var calls []mockReadCall
	readFn := func(addr, qty uint16) ([]byte, error) {
		calls = append(calls, mockReadCall{addr: addr, qty: qty})
		if qty >= 101 {
			return nil, modbusExc(goburrowModbus.ExceptionCodeIllegalDataAddress)
		}
		// 单寄存器：大端 int16 = 123
		return []byte{0x00, 0x7B}, nil
	}

	results, err := readRegisterRanges(readFn, addrs, ranges, cfg, FuncCodeReadHoldingRegisters, newReadPlanState(nil))
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

// TestReadRegisterRanges_NoFallbackOnDense 非协议错误（链路 / 超时 / 底层 I/O）
// 一律上抛，**不降级**：拿它去推断"哪个地址有问题"没有依据，而按旧代码那条
// "含空洞就降级"的路子，一次断连会先被拆成一堆必然失败的子段请求（白挨一遍），
// 再把错误上抛。
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

	calls := 0
	readFn := func(addr, qty uint16) ([]byte, error) {
		calls++
		return nil, errors.New("device offline")
	}

	if _, err := readRegisterRanges(readFn, addrs, ranges, cfg, FuncCodeReadHoldingRegisters, newReadPlanState(nil)); err == nil {
		t.Fatal("non-protocol error should propagate, want error")
	}
	if calls != 1 {
		t.Errorf("link error should not be degraded into extra probes, calls = %d, want 1", calls)
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

// TestReadRegisterRanges_ProtocolErrDegradesToQualityZero 验证稠密区间被设备以
// IllegalDataAddress 拒绝时，该区间点位标记 Quality=0 并继续
// （而非整台设备读取失败、整轮数据丢弃）。
//
// 两个点位**都**读不到，所以逐点降级的结果与整段标 0 相同 —— 这条用例钉的是
// "不中断整台设备读取"，"只有一个点位坏"的情形见 degrade_test.go。
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
	// → 地址级坏码（拆到单点仍被拒 = 这个寄存器在设备上不存在）
	readFn := func(addr, qty uint16) ([]byte, error) {
		return nil, modbusExc(goburrowModbus.ExceptionCodeIllegalDataAddress)
	}

	results, err := readRegisterRanges(readFn, addrs, ranges, cfg, FuncCodeReadHoldingRegisters, newReadPlanState(nil))
	if err != nil {
		t.Fatalf("protocol error should degrade to quality=0, got error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Quality != driver.QualityBadAddress {
			t.Errorf("point %s quality = %d, want %d (protocol error)", r.DeviceAddressID, r.Quality, driver.QualityBadAddress)
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
			return nil, modbusExc(goburrowModbus.ExceptionCodeIllegalDataAddress)
		}
		return []byte{0x01}, nil
	}

	results, err := readBitRanges(readFn, addrs, ranges, FuncCodeReadCoils, newReadPlanState(nil))
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
