// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"strings"
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// 本文件的主题：**本地就进不了区间规划**的点位，受损范围必须收敛到它自己。
//
// 两个成因走同一条路，本文件两条都测：
//  ① 地址本地解析不出来（一个字节都没上过线）；
//  ② 配置模式（StartAddress>0）下寄存器地址 < startAddress —— 地址写法没问题，
//     是该点位与 protocol_json 声明的地址区间对不上。
//
// 修复前的行为是：groupAddrsByFC / CalcReadRanges 全量解析、遇①或②直接返回 error，
// 于是 drv.Read 报错 → 采集侧 task.go 判定设备失败（incError + reportState(false)）→
// 连续三轮后落一条**断联**报警并推通知。而那个设备全程在线（IsConnected 为真，
// 重连逻辑根本不启动），失败之后的批次也一起被跳过 —— 一个点位把整台设备拖垮，
// 还把运维引向完全错误的方向。
//
// 现在：这两种点位在计划期被摘出去（partitionBadAddrs），Read 不报错，该点位返回
// 一条 Quality=QualityBadAddress 的记录，由既有的点位质量报警报出「地址无效」并
// 指名到点位；其余点位照常采集，设备保持在线。
//
// 判据是「恰好一条记录、索引对齐、DataType/Kind 非空」—— 最后一条尤其要紧：
// readRanges 的预填骨架是按 plan.groups[fc] 建的，被摘出去的点位不在任何组里，
// 不靠 modbusPlan.bad 补的话 readBatch 末尾按 addrs 重组会产出一条**零值记录**
// （连 DeviceAddressID 都是空的），违反 read.go 里写死的「每个点位恰好一条记录」。

// badName 一个**在所有格式下都解析不出来**的地址：
// tryIECFormat 认前缀 %MW，但 numStr "1x" 过不了 strconv.ParseUint；
// 传统 / 十六进制 / 显式前缀 / 纯数字四种格式也都不成立。
const badName = "%MW1x"

func devAddr(id, name, dataType string) po.DeviceAddress {
	return po.DeviceAddress{ID: id, Name: name, DataType: dataType, CommonDataType: dataType}
}

// assertAligned 断言记录数与输入地址数相等、且逐项按索引对齐（驱动契约）。
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

// TestReadBatch_UnparsableAddressExcluded 一坏两好：坏的只坏它自己。
func TestReadBatch_UnparsableAddressExcluded(t *testing.T) {
	f := newFakeModbus()
	cache := &planCache{}
	cfg := DefaultModbusTcpConfig()
	addrs := []po.DeviceAddress{
		devAddr("p1", "100", "int16"),
		devAddr("bad", badName, "int16"),
		devAddr("p3", "102", "int16"),
	}

	got := runRound(t, f, cache, driver.RangeSig(addrs), addrs, cfg)

	assertAligned(t, addrs, got)

	// 坏点位：地址无效、无值，但 DataType/Kind 必须照填（下游靠它决定怎么写）。
	if b := got[1]; b.Quality != driver.QualityBadAddress || b.Value != "" {
		t.Fatalf("坏点位 want quality=%d 且无值, got quality=%d value=%q",
			driver.QualityBadAddress, b.Quality, b.Value)
	}
	if got[1].DataType != "int16" || got[1].Kind == "" {
		t.Fatalf("坏点位必须带回 DataType/Kind, got dataType=%q kind=%q", got[1].DataType, got[1].Kind)
	}

	// 好点位不受影响：值来自它自己的寄存器（假设备把寄存器地址写成值）。
	if v := got[0]; v.Quality != driver.QualityGood || v.Value != "100" {
		t.Fatalf("p1 want quality=%d value=100, got quality=%d value=%q", driver.QualityGood, v.Quality, v.Value)
	}
	if v := got[2]; v.Quality != driver.QualityGood || v.Value != "102" {
		t.Fatalf("p3 want quality=%d value=102, got quality=%d value=%q", driver.QualityGood, v.Quality, v.Value)
	}

	// 坏地址**一个字节都不该发出去**，好点位确实上了线。
	if calls := f.callsSince(0); len(calls) == 0 {
		t.Fatal("好点位必须真的读设备（假设备一次调用都没收到）")
	}
}

// TestPlanFor_UnparsableGoesToBad 计划期就摘出去：坏点位不在任何功能码组里，
// 但作为 bad 随计划带出来（readBatch 的记录正是从它播种的）。
func TestPlanFor_UnparsableGoesToBad(t *testing.T) {
	cache := &planCache{}
	cfg := DefaultModbusTcpConfig()
	addrs := []po.DeviceAddress{
		devAddr("p1", "100", "int16"),
		devAddr("bad", badName, "int16"),
	}

	plan, err := cache.planFor(driver.RangeSig(addrs), addrs, cfg)
	if err != nil {
		t.Fatalf("planFor = %v（一个坏地址不该让整批规划失败）", err)
	}
	if len(plan.bad) != 1 || plan.bad[0].id != "bad" || plan.bad[0].dataType != "int16" {
		t.Fatalf("bad = %+v, want 恰好 [bad/int16]", plan.bad)
	}
	for fc, g := range plan.groups {
		for _, a := range g {
			if a.ID == "bad" {
				t.Fatalf("坏点位不该进任何功能码组，却出现在 fc=%d", fc)
			}
		}
	}
}

// TestReadBatch_MixedFCWithUnparsable 坏点位必须落在**两个**功能码组之外，
// 且线圈与保持寄存器两组都照常读（分组是 modbus 独有的间接层，本用例专门钉它）。
func TestReadBatch_MixedFCWithUnparsable(t *testing.T) {
	f := newFakeModbus()
	cache := &planCache{}
	cfg := DefaultModbusTcpConfig()
	addrs := []po.DeviceAddress{
		devAddr("c1", "00001", "bool"),  // 传统写法 → FC1 线圈
		devAddr("r1", "40001", "int16"), // 传统写法 → FC3 保持寄存器
		devAddr("bad", badName, "int16"),
	}

	plan, err := cache.planFor(driver.RangeSig(addrs), addrs, cfg)
	if err != nil {
		t.Fatalf("planFor = %v", err)
	}
	if len(plan.groups[FuncCodeReadCoils]) != 1 || len(plan.groups[FuncCodeReadHoldingRegisters]) != 1 {
		t.Fatalf("want 线圈 1 个 + 保持寄存器 1 个, got %+v", plan.groups)
	}

	got, err := readBatch("modbus", f, plan, cache, driver.RangeSig(addrs), addrs, cfg, new(warnSet))
	if err != nil {
		t.Fatalf("readBatch = %v", err)
	}
	assertAligned(t, addrs, got)
	if got[2].Quality != driver.QualityBadAddress {
		t.Fatalf("坏点位 want quality=%d, got %d", driver.QualityBadAddress, got[2].Quality)
	}
	if got[0].Quality != driver.QualityGood || got[1].Quality != driver.QualityGood {
		t.Fatalf("两个功能码组都得照常读, got %d/%d", got[0].Quality, got[1].Quality)
	}
}

// TestPlanFor_AllUnparsable_NoError 一批全坏：不报错、全部地址无效、零次上线。
//
// 两个模式都要过：自动推导模式下 groups 为空、配置模式下 groupAddrsByFC 会写出一个
// **nil 值的键**（read.go 的短路分支），后者若没有 planFor 里的空组守卫，就会撞上
// CalcReadRanges 的 "no addresses to read" —— 而那是「这批一个都读不了」，
// 不是「这批里一个点位都没有」。
func TestPlanFor_AllUnparsable_NoError(t *testing.T) {
	configMode := DefaultModbusTcpConfig()
	configMode.StartAddress = 100
	configMode.Quantity = 10

	for _, tc := range []struct {
		name string
		cfg  *ModbusTcpConfig
	}{
		{"auto", DefaultModbusTcpConfig()},
		{"configMode", configMode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeModbus()
			cache := &planCache{}
			addrs := []po.DeviceAddress{
				devAddr("bad1", badName, "int16"),
				devAddr("bad2", "no-such-address", "int16"),
			}

			plan, err := cache.planFor(driver.RangeSig(addrs), addrs, tc.cfg)
			if err != nil {
				t.Fatalf("planFor = %v（全坏也不该失败）", err)
			}
			got, err := readBatch("modbus", f, plan, cache, driver.RangeSig(addrs), addrs, tc.cfg, new(warnSet))
			if err != nil {
				t.Fatalf("readBatch = %v", err)
			}

			assertAligned(t, addrs, got)
			for i, r := range got {
				if r.Quality != driver.QualityBadAddress {
					t.Fatalf("第 %d 项 want quality=%d, got %d", i, driver.QualityBadAddress, r.Quality)
				}
				if r.DataType != "int16" || r.Kind == "" {
					t.Fatalf("第 %d 项必须带回 DataType/Kind, got %q/%q", i, r.DataType, r.Kind)
				}
			}
			if calls := f.callsSince(0); len(calls) != 0 {
				t.Fatalf("一个可解析的地址都没有，不该发出任何请求, got %+v", calls)
			}
		})
	}
}

// TestPlanFor_BelowStartAddressGoesToBad 配置模式下低于 startAddress 的点位同一条路。
//
// 它不是「地址写错了」：格式完全合法，只是与 protocol_json 声明的地址区间对不上。
// 但同样是「不去改配置就永远不会好」，所以归宿相同 —— 只是日志里那句话要说清是
// 哪一种（why 字段就是为这个分的）。
func TestPlanFor_BelowStartAddressGoesToBad(t *testing.T) {
	f := newFakeModbus()
	cache := &planCache{}
	cfg := DefaultModbusTcpConfig()
	cfg.StartAddress = 100
	cfg.Quantity = 10
	addrs := []po.DeviceAddress{
		devAddr("p1", "100", "int16"), // == startAddress，合法
		devAddr("low", "50", "int16"), // < startAddress
	}

	plan, err := cache.planFor(driver.RangeSig(addrs), addrs, cfg)
	if err != nil {
		t.Fatalf("planFor = %v（低于 startAddress 的点位不该让整批规划失败）", err)
	}
	if len(plan.bad) != 1 || plan.bad[0].id != "low" {
		t.Fatalf("bad = %+v, want 恰好 [low]", plan.bad)
	}
	// 两个来源的日志必须能分辨：这句话里得有 startAddress，而不是 "invalid format"。
	if w := plan.bad[0].why; !strings.Contains(w, "startAddress") {
		t.Fatalf("why = %q，应指明是低于 startAddress，不是 invalid format", w)
	}

	got, err := readBatch("modbus", f, plan, cache, driver.RangeSig(addrs), addrs, cfg, new(warnSet))
	if err != nil {
		t.Fatalf("readBatch = %v", err)
	}
	assertAligned(t, addrs, got)
	if b := got[1]; b.Quality != driver.QualityBadAddress || b.Value != "" {
		t.Fatalf("低于 startAddress 的点位 want quality=%d 且无值, got quality=%d value=%q",
			driver.QualityBadAddress, b.Quality, b.Value)
	}
	if got[1].DataType != "int16" || got[1].Kind == "" {
		t.Fatalf("必须带回 DataType/Kind, got dataType=%q kind=%q", got[1].DataType, got[1].Kind)
	}
	// 合法点位照常在配置区间里读到值（假设备把寄存器地址写成值）。
	if v := got[0]; v.Quality != driver.QualityGood || v.Value != "100" {
		t.Fatalf("p1 want quality=%d value=100, got quality=%d value=%q", driver.QualityGood, v.Quality, v.Value)
	}
}

// TestPartitionBadAddrs_KeepsOrderAndData 拆分的纯函数语义：顺序保持、字段带全。
func TestPartitionBadAddrs_KeepsOrderAndData(t *testing.T) {
	addrs := []po.DeviceAddress{
		devAddr("a", "40001", "int16"),
		devAddr("b", badName, "float32"),
		devAddr("c", "%MW100", "int16"),
	}
	valid, bad := partitionBadAddrs(addrs, 0) // startAddress=0 → 只按解析判

	if len(valid) != 2 || valid[0].ID != "a" || valid[1].ID != "c" {
		t.Fatalf("valid = %+v, want [a c]", valid)
	}
	if len(bad) != 1 || bad[0].id != "b" || bad[0].name != badName || bad[0].dataType != "float32" {
		t.Fatalf("bad = %+v, want [b/%s/float32]", bad, badName)
	}
	if !strings.Contains(bad[0].why, "invalid format") {
		t.Fatalf("why = %q，应说明是 invalid format", bad[0].why)
	}
}
