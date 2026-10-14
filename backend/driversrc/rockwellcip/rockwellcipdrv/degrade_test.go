// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"testing"

	"github.com/iceisfun/goindustrial/protocol/ethernetip/cip"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// fakeArrayDevice 假数组设备，按**真实语义**应答：请求路径带起始下标，返回该起点起
// count 个元素；**覆盖了不存在的下标就整块拒绝** —— 设备拒绝的是整个请求，不会逐元素
// 回一个失败。因此一个下标写错，整块元素一起没有值（这正是本组用例针对的形态）。
//
// status 决定拒绝时回什么状态码：用例据此区分"地址问题"（可据此确认元素不存在）
// 与"可恢复的设备状态"（不能据此确认任何东西）。
func fakeArrayDevice(t *testing.T, base string, calls *[]string,
	missing map[int]bool, status cip.USINT, transient bool) func(string, uint16) ([]byte, error) {

	t.Helper()
	return func(tag string, count uint16) ([]byte, error) {
		if calls != nil {
			*calls = append(*calls, fmt.Sprintf("%s x%d", tag, count))
		}
		name, off, ok := splitArrayIndex(tag)
		if !ok || name != base {
			return nil, fmt.Errorf("请求路径必须带起始下标（设备只按路径定位元素），got %q", tag)
		}
		for i := 0; i < int(count); i++ {
			if missing[off+i] {
				return nil, cip.Error{Status: status}
			}
		}
		if transient {
			return nil, cip.Error{Status: status}
		}
		buf := make([]byte, int(count)*2)
		for i := 0; i < int(count); i++ {
			binary.LittleEndian.PutUint16(buf[i*2:], uint16((off+i)*10)) // 值 = 下标 * 10
		}
		return mkResp(cipTypeINT, buf...), nil
	}
}

// mkArrayPoints 造 n 个 base[0..n-1] 的 int16 点位。
func mkArrayPoints(base string, n int) []po.DeviceAddress {
	addrs := make([]po.DeviceAddress, 0, n)
	for i := 0; i < n; i++ {
		addrs = append(addrs, mkAddr(fmt.Sprintf("a%d", i), fmt.Sprintf("%s[%d]", base, i), "int16", "Short"))
	}
	return addrs
}

// TestReadArrayChunkRefusalIsolatesElement 一块数组里有一个下标不存在时，设备会
// **整块拒绝** Read Tag Elements —— 此时只有那一个元素 Quality=0，同块其它元素照常取值。
//
// 这正是修复前的形态：整块（最多 125 个元素）一起被标 quality=0，
// 而设备其实只是不认识 S[3] 这一个下标。
func TestReadArrayChunkRefusalIsolatesElement(t *testing.T) {
	var calls []string
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	d.client = &mockTransport{connected: true,
		readElems: fakeArrayDevice(t, "S", &calls, map[int]bool{3: true}, cip.StatusPathDestinationUnknown, false),
	}

	results, err := d.Read(mkArrayPoints("S", 6))
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if len(results) != 6 {
		t.Fatalf("want 6 results, got %d", len(results))
	}
	for i, r := range results {
		if i == 3 {
			if r.Value != "" || r.Quality != driver.QualityBadAddress {
				t.Errorf("S[3]（设备上不存在）= %+v, want val=\"\" quality=%d", r, driver.QualityBadAddress)
			}
			if r.DataType != "int16" || r.Kind == "" {
				t.Errorf("S[3] 读不到时 DataType/Kind 仍须回填，got %+v", r)
			}
			// 质量码必须是 QualityBadAddress：这一块被拒的是**地址类**状态且对半拆到单元素仍被拒 =
			// 这个下标在设备上不存在（永久，不自愈）。标成 comms 会让一次"数组少一个
			// 元素"每轮都被当成可自愈的通信抖动，点位级报警永远等不到那个定性。
			if r.Quality != driver.QualityBadAddress {
				t.Errorf("S[3] Quality = %d, want %d", r.Quality, driver.QualityBadAddress)
			}
			continue
		}
		want := strconv.Itoa(i * 10)
		if r.Value != want || r.Quality != 192 {
			t.Errorf("S[%d] = %+v, want val=%s quality=192", i, r, want)
		}
		// 健在的兄弟元素照常取值，且**不得**带任何成因：不变量"正常即无成因"。
		// 一旦它们陪着坏元素一起被标了成因，下游会把整块数组报成异常。
		if r.Quality != driver.QualityGood {
			t.Errorf("S[%d] 质量码不是 QualityGood，got %d", i, r.Quality)
		}
	}

	// 整块先被拒一次，随后对半拆到把 S[3] 单独摘出来
	wantCalls := []string{"S[0] x6", "S[0] x3", "S[3] x3", "S[3] x1", "S[4] x2"}
	if len(calls) != len(wantCalls) {
		t.Fatalf("请求 = %v, want %v", calls, wantCalls)
	}
	for i := range calls {
		if calls[i] != wantCalls[i] {
			t.Errorf("请求[%d] = %q, want %q", i, calls[i], wantCalls[i])
		}
	}
}

// TestReadArrayChunkTransientStatusKeepsChunk 非地址类状态（0x02 资源不可用）说明不了
// **哪个元素**不存在，因此**不得**触发降级、也不得确认任何元素：
//
//	① 只发那一次整块请求（逐个去问只会白挨一遍，还可能把好元素误判成不存在）；
//	② 整块保持 quality=0，下一轮照旧整块重试（不缓存任何"坏元素"结论）。
func TestReadArrayChunkTransientStatusKeepsChunk(t *testing.T) {
	var calls []string
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	d.client = &mockTransport{connected: true,
		readElems: fakeArrayDevice(t, "S", &calls, map[int]bool{}, cip.StatusResourceUnavailable, true),
	}

	results, err := d.Read(mkArrayPoints("S", 6))
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	for i, r := range results {
		if r.Value != "" {
			t.Errorf("S[%d] = %+v, want 空值（本轮取不到值）", i, r)
		}
		// 只能到通信级：非地址类状态（资源不可用 / 部分传输…）说明不了**哪个元素**
		// 不存在，所以既不降级也不确认任何点位，整块保持 0 下轮重试。标成 address 会把
		// 一次"设备忙"误报成"整块元素永久缺失"，那是不可逆的错误定性。
		if r.Quality != driver.QualityBadComms {
			t.Errorf("S[%d] Quality = %d, want %d", i, r.Quality, driver.QualityBadComms)
		}
	}
	if len(calls) != 1 {
		t.Errorf("非地址类状态不该触发降级，发了 %d 次请求：%v", len(calls), calls)
	}
}

// TestReadArrayChunkRefusalRecovers 设备侧把缺的元素补上之后，下一轮整块就正常了
// （块级降级不缓存任何结论，所以不存在"摘掉了就回不来"）。
func TestReadArrayChunkRefusalRecovers(t *testing.T) {
	var calls []string
	missing := map[int]bool{3: true}
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	d.client = &mockTransport{connected: true,
		readElems: func(tag string, count uint16) ([]byte, error) {
			return fakeArrayDevice(t, "S", &calls, missing, cip.StatusPathDestinationUnknown, false)(tag, count)
		},
	}
	addrs := mkArrayPoints("S", 6)

	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("first Read error: %v", err)
	}
	delete(missing, 3) // 现场把数组补到 6 个元素

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read after recovery error: %v", err)
	}
	for i, r := range results {
		want := strconv.Itoa(i * 10)
		if r.Value != want || r.Quality != 192 {
			t.Errorf("恢复后 S[%d] = %+v, want val=%s quality=192", i, r, want)
		}
		// 块级降级不缓存任何结论，恢复后整块重新读通 —— 质量码必须回到 QualityGood，
		// 不留上一轮的坏码（否则下游会拿着一条正常值配一个"地址不存在"的定性，自相矛盾）。
		if r.Quality != driver.QualityGood {
			t.Errorf("恢复后 S[%d] 质量码不是 QualityGood，got %d", i, r.Quality)
		}
	}
}

// runRound 跑**驱动真实的一轮采集**（rockwellDriver.Read），只把 client 换成假设备。
//
// 与 s7 用例里的同名助手同一用意：刻意不在测试里重写那个循环 —— 重写的版本与生产
// 代码是两份实现，用例量到的是测试自己那版。"标量 tag 被拒时按状态分类"这类行为
// 一旦和驱动里写的不同，测试反而会替错的实现背书。
func runRound(t *testing.T, d *rockwellDriver, addrs []po.DeviceAddress) []driver.ReadResult {
	t.Helper()
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}
	return results
}

// TestReadScalarTagStatusQuality 标量 tag（不带 `[下标]`，故走 0x4C 普通读取而非数组
// 分块那条路）被设备拒绝时，成因必须按 CIP 状态分到 address 或 comms。
//
// 这条分支此前**完全没被覆盖**：上面三个用例全部驱动数组块路径，标量 tag 一律不分类
// （见 driver.go 里"修掉一处不对称"那段注释）。本用例把那处行为变更钉死：
//
//   - 路径目标未知（0x05）：设备明说"这个 tag/路径我这儿没有" → address
//     （永久，不自愈）—— 标成 comms 会让一次"标签名写错"永远只报通信级；
//   - 资源不可用（0x02）：整条 tag 被拒但说明不了是不是地址问题 → comms
//     （下轮重试）—— 标成 address 会把一次暂时的设备忙报成"测点永久坏了"。
//
// 两种判错都是现场直接可见的告警噪音，所以两侧都必须钉住。
func TestReadScalarTagStatusQuality(t *testing.T) {
	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	d.client = &mockTransport{connected: true, readFn: func(tag string) ([]byte, error) {
		switch tag {
		case "Missing": // 标签名写错：设备明说路径目标未知
			return nil, cip.Error{Status: cip.StatusPathDestinationUnknown}
		case "Busy": // 设备忙：非地址类状态，说明不了地址是否存在
			return nil, cip.Error{Status: cip.StatusResourceUnavailable}
		default:
			return mkResp(cipTypeINT, 0x64, 0x00), nil
		}
	}}

	addrs := []po.DeviceAddress{
		mkAddr("a1", "Good", "int16", "Short"),
		mkAddr("a2", "Missing", "int16", "Short"),
		mkAddr("a3", "Busy", "int16", "Short"),
	}
	results := runRound(t, d, addrs)
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}

	// 正常标量 tag：取值且不变量"正常即无成因"成立
	if results[0].Value != "100" || results[0].Quality != 192 {
		t.Errorf("Good = %+v, want val=100 quality=192", results[0])
	}
	if results[0].Quality != driver.QualityGood {
		t.Errorf("Good 质量码不是 QualityGood，got %d", results[0].Quality)
	}

	// 地址类状态 → address
	if results[1].Value != "" || results[1].Quality != driver.QualityBadAddress {
		t.Errorf("Missing = %+v, want val=\"\" quality=%d", results[1], driver.QualityBadAddress)
	}
	if results[1].Quality != driver.QualityBadAddress {
		t.Errorf("Missing（路径目标未知）Quality = %d, want %d",
			results[1].Quality, driver.QualityBadAddress)
	}

	// 非地址类状态 → comms
	if results[2].Value != "" || results[2].Quality != driver.QualityBadComms {
		t.Errorf("Busy = %+v, want val=\"\" quality=%d", results[2], driver.QualityBadComms)
	}
	if results[2].Quality != driver.QualityBadComms {
		t.Errorf("Busy（资源不可用）Quality = %d, want %d",
			results[2].Quality, driver.QualityBadComms)
	}
}
