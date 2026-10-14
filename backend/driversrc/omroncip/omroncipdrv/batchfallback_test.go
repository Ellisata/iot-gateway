// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"iot-gateway/driver"
	cipcore "iot-gateway/driversrc/omroncip/cipcore"
	"iot-gateway/model/po"
)

// singleServiceErrorEcho 设备拒绝整个 0x0A 请求时的应答形态：
// 不是多服务响应（0x8A | 服务数 | 子响应…），而是一个**单服务**的错误回显 ——
// 0xCC 是 0x4C|0x80（Data Table Read 的应答位），后面跟保留字节、通用状态与
// 附加状态字数。设备等于在说"我没听懂 0x0A，按 0x4C 的格式给你回个错"。
//
// 真机上是哪台设备、回的是 0xCC 还是别的状态码，我没有可验证的样本；这组用例
// 钉住的不是某个具体状态码，而是**任何不成其为多服务应答的应答都不得把设备判离线**。
var singleServiceErrorEcho = []byte{0xCC, 0x00, 0x04, 0x00}

// TestParseMultipleReadMarksBatchUnusable 把"应答不是多服务响应"这条线协议事实
// 钉到 errBatchUnusable 哨兵上。driver 侧的判断依赖它，而它是纯字节可测的。
func TestParseMultipleReadMarksBatchUnusable(t *testing.T) {
	sizes := []int{2, 2, 2}

	t.Run("单服务错误回显", func(t *testing.T) {
		_, err := parseMultipleDataTableRead(singleServiceErrorEcho, sizes)
		if err == nil {
			t.Fatal("期望报错，得到 nil")
		}
		if !isBatchUnusable(err) {
			t.Errorf("err = %v，期望带上 errBatchUnusable 哨兵", err)
		}
	})

	t.Run("应答截断", func(t *testing.T) {
		// 0x8A + 服务数 2，随后什么都没有
		_, err := parseMultipleDataTableRead([]byte{0x8A, 0x02}, sizes[:2])
		if err == nil {
			t.Fatal("期望报错，得到 nil")
		}
		if !isBatchUnusable(err) {
			t.Errorf("err = %v，期望带上 errBatchUnusable 哨兵", err)
		}
	})

	t.Run("空应答", func(t *testing.T) {
		_, err := parseMultipleDataTableRead(nil, sizes)
		if err == nil || !isBatchUnusable(err) {
			t.Errorf("err = %v，期望带上 errBatchUnusable 哨兵", err)
		}
	})

	t.Run("合规的多服务响应不贴哨兵", func(t *testing.T) {
		// 0x8A | 服务数 2 | [0xCC 00 00 | INT 码 LE | 数据(2B)] x2
		resp := []byte{
			0x8A, 0x02,
			0xCC, 0x00, 0x00, 0xC3, 0x00, 0x64, 0x00,
			0xCC, 0x00, 0x00, 0xC3, 0x00, 0x65, 0x00,
		}
		results, err := parseMultipleDataTableRead(resp, sizes[:2])
		if err != nil {
			t.Fatalf("合规响应不该报错：%v", err)
		}
		if len(results) != 2 || results[0].Err != nil || results[1].Err != nil {
			t.Fatalf("results = %+v，期望两条成功结果", results)
		}
		if string(results[0].Data) != "\x64\x00" || string(results[1].Data) != "\x65\x00" {
			t.Errorf("数据切片错位：% X / % X", results[0].Data, results[1].Data)
		}
	})

	t.Run("链路错误不贴哨兵", func(t *testing.T) {
		if isBatchUnusable(fmt.Errorf("cip: read failed: EOF")) {
			t.Error("链路错误被误判为批量不可用")
		}
		if isBatchUnusable(nil) {
			t.Error("nil 被误判为批量不可用")
		}
	})
}

// batchRejectingDevice 一个恒拒 0x0A 批量的假设备：任意批量请求都收到
// "单服务错误回显"，逐标签单读则正常返回值 100。
func batchRejectingDevice(readFn func(tag string, code uint16) ([]byte, error)) *mockCIPTransport {
	return &mockCIPTransport{
		connected: true,
		readTagsFn: func(specs []TagSpec) ([]TagResult, error) {
			sizes := make([]int, len(specs))
			for i := range specs {
				sizes[i] = 2
			}
			return parseMultipleDataTableRead(singleServiceErrorEcho, sizes)
		},
		readFn: readFn,
	}
}

func int16OK(tag string, code uint16) ([]byte, error) {
	if code != cipTypeINT {
		return nil, fmt.Errorf("类型码 = 0x%04X, want INT 0x%04X", code, cipTypeINT)
	}
	return []byte{0x64, 0x00}, nil // int16 = 100
}

func mkThreeTags() []po.DeviceAddress {
	return []po.DeviceAddress{
		mkCIPAddr("a1", "T1", "int16", "Short"),
		mkCIPAddr("a2", "T2", "int16", "Short"),
		mkCIPAddr("a3", "T3", "int16", "Short"),
	}
}

// TestDriverReadBatchRejectedFallsBackToSingleReads 整批被设备拒绝时，本轮的
// **数据要当场救回来**：同批标签逐个单读取值，而不是整批置 0 等下轮。
//
// 修复前的形态：整批点位 Quality=0（数据丢失一整轮），且此后每一轮都先赔一次
// 注定失败的批量请求。更早的形态更糟 —— 把协议级拒绝当链路错误上抛，
// 整台设备被判离线、反复重连，而 PLC 一直应答得好好的。
func TestDriverReadBatchRejectedFallsBackToSingleReads(t *testing.T) {
	d := newCIPDriver()
	cfg := DefaultCIPConfig()
	cfg.MaxTagsPerRequest = 3
	d.config = cfg
	mock := batchRejectingDevice(int16OK)
	d.client = mock

	results, err := d.Read(mkThreeTags())
	if err != nil {
		t.Fatalf("物流层拒绝不该让 Read 报错，got: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}
	for i := range results {
		assertResult(t, results[i], fmt.Sprintf("a%d", i+1), "100", "int16", 192)
	}
	if mock.batchCalls != 1 {
		t.Errorf("batchCalls = %d, want 1（批量只该试一次）", mock.batchCalls)
	}
	if mock.singleCalls != 3 {
		t.Errorf("singleCalls = %d, want 3（整批的 3 个标签逐个单读）", mock.singleCalls)
	}
	if !d.batchUnusable() {
		t.Error("整批被拒后应记下「本设备批量不可用」")
	}
}

// TestDriverReadBatchHintSkipsNextRound 静默期内后续几轮**不再试批量**，
// 直接逐标签单读 —— 这是"记住这台设备不吃批量"的全部意义：
// 否则每轮都要先赔一次失败的批量往返（点位多时是整轮的网络时间）。
func TestDriverReadBatchHintSkipsNextRound(t *testing.T) {
	d := newCIPDriver()
	cfg := DefaultCIPConfig()
	cfg.MaxTagsPerRequest = 3
	d.config = cfg
	mock := batchRejectingDevice(int16OK)
	d.client = mock
	addrs := mkThreeTags()

	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("first Read error: %v", err)
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("second Read error: %v", err)
	}
	if mock.batchCalls != 1 {
		t.Errorf("batchCalls = %d, want 1（静默期内不该再发批量请求）", mock.batchCalls)
	}
	if mock.singleCalls != 6 {
		t.Errorf("singleCalls = %d, want 6（两轮各 3 次单读）", mock.singleCalls)
	}
	for i := range results {
		assertResult(t, results[i], fmt.Sprintf("a%d", i+1), "100", "int16", 192)
	}
}

// TestDriverReadBatchHintExpiresAndRetries 静默期到期后**自动重试一次批量**：
// 现场把设备修好之后不必重启网关即可恢复批量读；若仍失败，重新进入静默期。
func TestDriverReadBatchHintExpiresAndRetries(t *testing.T) {
	d := newCIPDriver()
	cfg := DefaultCIPConfig()
	cfg.MaxTagsPerRequest = 3
	d.config = cfg
	mock := batchRejectingDevice(int16OK)
	d.client = mock
	addrs := mkThreeTags()

	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("first Read error: %v", err)
	}

	// 把静默期推到过去，等价于 TTL 到期（不引时钟注入：字段同包可见）
	d.batchMu.Lock()
	d.batchBadUntil = time.Now().Add(-time.Second)
	d.batchMu.Unlock()

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read after expiry error: %v", err)
	}
	if mock.batchCalls != 2 {
		t.Errorf("batchCalls = %d, want 2（到期后应重试一次批量）", mock.batchCalls)
	}
	for i := range results {
		assertResult(t, results[i], fmt.Sprintf("a%d", i+1), "100", "int16", 192)
	}
	if !d.batchUnusable() {
		t.Error("重试仍失败，应重新进入静默期")
	}
}

// TestDriverReadBatchLinkErrorStillPropagates 批量路径上的**链路错误**照旧上抛：
// 只有"设备答了话但不认批量"才回退，读超时/写失败仍是设备离线信号。
//
// 这条区分是整个修复的支点：少了它，"回退"就变成了掩盖断连。
func TestDriverReadBatchLinkErrorStillPropagates(t *testing.T) {
	d := newCIPDriver()
	cfg := DefaultCIPConfig()
	cfg.MaxTagsPerRequest = 3
	d.config = cfg
	d.client = &mockCIPTransport{connected: true,
		readTagsFn: func(specs []TagSpec) ([]TagResult, error) {
			return nil, fmt.Errorf("cip: read failed: EOF")
		},
		readFn: int16OK,
	}

	if _, err := d.Read(mkThreeTags()); err == nil {
		t.Fatal("链路错误必须上抛，由采集引擎判设备离线")
	}
	if d.batchUnusable() {
		t.Error("链路错误不得被记成「批量不可用」")
	}
}

// TestDriverReadFallbackKeepsTagRejectionLocal 回退阶段的语义必须与常规单读完全
// 一致：设备拒绝某一个标签只让该标签的点位 Quality=0，其余照常取值。
//
// 这正是真机 2026-09-29 的形状（10.62.100.16 上的 Recipe.Setpoint 回 0x05）——
// 只不过那次是"设备拒绝"被当成"设备离线"，一个写错的点名字让整台设备的
// 所有点位一起失去数据。
func TestDriverReadFallbackKeepsTagRejectionLocal(t *testing.T) {
	d := newCIPDriver()
	cfg := DefaultCIPConfig()
	cfg.MaxTagsPerRequest = 3
	d.config = cfg
	d.client = batchRejectingDevice(func(tag string, code uint16) ([]byte, error) {
		if tag == "T2" {
			return nil, cipcore.NewGeneralStatusError(0x05)
		}
		return int16OK(tag, code)
	})

	results, err := d.Read(mkThreeTags())
	if err != nil {
		t.Fatalf("单个标签被拒不该让 Read 报错，got: %v", err)
	}
	assertResult(t, results[0], "a1", "100", "int16", driver.QualityGood)
	assertResult(t, results[1], "a2", "", "int16", driver.QualityBadAddress)
	assertResult(t, results[2], "a3", "100", "int16", driver.QualityGood)
}

// TestDriverReadFallbackLinkErrorPropagates 回退走到一半链路断了 → 照旧上抛。
// "回退"救的是协议级拒绝，不是断连。
func TestDriverReadFallbackLinkErrorPropagates(t *testing.T) {
	d := newCIPDriver()
	cfg := DefaultCIPConfig()
	cfg.MaxTagsPerRequest = 3
	d.config = cfg
	d.client = batchRejectingDevice(func(tag string, code uint16) ([]byte, error) {
		if tag == "T2" {
			return nil, errors.New("cip: read failed: connection reset")
		}
		return int16OK(tag, code)
	})

	if _, err := d.Read(mkThreeTags()); err == nil {
		t.Fatal("回退途中的链路错误必须上抛")
	}
}

// TestDriverConnectClearsBatchHint 重新建连后静默期作废：设备换了、配置改了，
// 没有理由继续认定"这台设备不吃批量"。
func TestDriverConnectClearsBatchHint(t *testing.T) {
	d := newCIPDriver()
	d.markBatchUnusable(errors.New("stub"))
	if !d.batchUnusable() {
		t.Fatal("markBatchUnusable 未生效")
	}
	d.clearBatchHint()
	if d.batchUnusable() {
		t.Error("clearBatchHint 未作废静默期")
	}
}
