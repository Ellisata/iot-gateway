// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push_test

// push 队列的**驻留峰值**实测：队列里躺着批次时，插件侧零拷贝交出来的字符串
// 会把多少帧载荷一起钉在堆上。
//
// 为什么要在 push 这一层量：字符串赋值只复制头（指针+长度），不复制字节，
// 所以 collector.toRecords 的 `Value: r.Value` 不切断别名，别名一路传进
// outbox 的批次里。采集侧看到的是"解码快了一倍"，代价却记在这里。
//
// 两种形态对照：
//
//	alias —— 记录直接带插件解码出来的字符串（零拷贝的现状）
//	clone —— 记录建立时 strings.Clone 一次（等价于放弃零拷贝的收益）
//
// 两种存活形态（它们的结论**方向相反**，必须分开量）：
//
//	whole  —— 整帧的记录都进了队列（outbox 的真实形态：批次整进整出）
//	single —— 一帧只活下来一条记录（某条记录被长期扣住的那种形态）
//
// 跑法：go test ./push -run TestOutboxRetentionReport -v

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"iot-gateway/collector"
	"iot-gateway/push"
)

// 现场「5000 点/台」量级：帧载荷约 280 KB，足以让驻留项盖过测量噪声；
// 记录项约 760 KB。两者同量级，才看得出谁大谁小。
const (
	retentionBatches = 100
	retentionPoints  = 5000
)

type connStub bool

func (c connStub) Connected() bool { return bool(c) }

func sampleReply(n int) driversdk.ReadReply {
	res := make([]driver.ReadResult, n)
	for i := range res {
		res[i] = driver.ReadResult{
			DeviceAddressID: fmt.Sprintf("0192f3a1-0000-7000-8000-%012d", i),
			Value:           fmt.Sprintf("%d.5", 1000+i),
			DataType:        "int16",
			Kind:            driver.KindInt,
			Quality:         192,
		}
	}
	return driversdk.ReadReply{Results: res, Connected: true}
}

// toRecords 逐字段照抄 collector.toRecords 的赋值方式。
//
// keep 限定保留前几条记录，用来构造「一帧只剩一条」的形态；
// clone 为真时把 4 个来源字段各自复制一份 —— 这正是"在记录边界切断别名"
// 所需的代价，也正是零拷贝省掉的那几次分配。
func toRecords(reply *driversdk.ReadReply, clone bool, keep int) []collector.CollectedRecord {
	n := len(reply.Results)
	if keep > 0 && keep < n {
		n = keep
	}
	out := make([]collector.CollectedRecord, 0, n)
	for i := 0; i < n; i++ {
		r := &reply.Results[i]
		rec := collector.CollectedRecord{
			DeviceID:           "dev-1",
			DeviceName:         "一号线-注塑机",
			DeviceAddressID:    r.DeviceAddressID,
			DeviceAddressName:  fmt.Sprintf("point_%d", i),
			DeviceAddressLabel: "温度",
			Value:              r.Value,
			DataType:           r.DataType,
			Kind:               r.Kind,
			Protocol:           "ModBus.TCP",
			Quality:            r.Quality,
		}
		if clone {
			rec.DeviceAddressID = strings.Clone(rec.DeviceAddressID)
			rec.Value = strings.Clone(rec.Value)
			rec.DataType = strings.Clone(rec.DataType)
			rec.Kind = strings.Clone(rec.Kind)
		}
		out = append(out, rec)
	}
	return out
}

// measure 把 retentionBatches 个批次灌进 outbox（下游不消费，队列一路填满），
// 返回堆驻留增量与每批次均值。
//
// 载荷缓冲在解码之后立刻断开引用（置 nil），所以还剩多少被引用完全取决于
// 记录里的字符串是否别名它。
func measure(t *testing.T, clone bool, keep int) (total, perBatch int64) {
	t.Helper()

	runtime.GC()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	// 连通 + 未启用缓存：批次留在内存队列里，这是驻留的上界形态。
	// 断连走 spool 时会落盘并随即释放，驻留窗口短得多。
	o := push.NewOutbox(push.OutboxConfig{
		Tag: "retention", ID: "c", Name: "c",
		Conn: connStub(true), QueueSize: 4096,
	})
	o.Start()

	payloads := make([][]byte, retentionBatches)
	for i := range payloads {
		r := sampleReply(retentionPoints)
		payloads[i] = r.EncodeBinary()
	}
	frameBytes := len(payloads[0])

	for i, p := range payloads {
		var reply driversdk.ReadReply
		if err := reply.DecodeBinary(p); err != nil {
			t.Fatalf("DecodeBinary: %v", err)
		}
		o.Enqueue(push.PushBatch{
			DeviceID:    fmt.Sprintf("dev-%d", i%8),
			CollectedAt: "2026-09-15 10:00:00",
			Records:     toRecords(&reply, clone, keep),
		})
		payloads[i] = nil // 断开对载荷的引用，只留记录里的字符串
	}
	payloads = nil

	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	total = int64(after.HeapAlloc) - int64(before.HeapAlloc)
	perBatch = total / retentionBatches

	t.Logf("  %-7s clone=%-5v  载荷 %d B/帧  →  驻留 %7d B/批（合计 %.1f MB）",
		scopeName(keep), clone, frameBytes, perBatch, float64(total)/(1<<20))

	// 收尾：排空队列，让下一次测量从干净基线开始。
	for len(o.Batches()) > 0 {
		<-o.Batches()
	}
	o.Close()
	runtime.KeepAlive(o)
	return total, perBatch
}

func scopeName(keep int) string {
	if keep == 1 {
		return "single"
	}
	return "whole"
}

// TestOutboxRetentionReport 对照量出「零拷贝在 push 队列上的驻留代价」。
//
// 期望：两种存活形态的结论方向相反，这正是这条实测存在的意义。
func TestOutboxRetentionReport(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过 push 队列驻留实测")
	}

	// whole：整帧记录都进队列（outbox 的真实形态，批次整进整出）
	t.Logf("【whole】每帧 %d 条记录全部进队列", retentionPoints)
	aliasWhole, _ := measure(t, false, 0)
	runtime.GC()
	cloneWhole, _ := measure(t, true, 0)
	runtime.GC()

	// single：一帧只活下来一条记录
	t.Logf("【single】每帧只活下来 1 条记录")
	aliasSingle, _ := measure(t, false, 1)
	runtime.GC()
	cloneSingle, _ := measure(t, true, 1)

	t.Logf("—— 结论 ——")
	t.Logf("whole （整批存活）：零拷贝 %d B/批，克隆 %d B/批，差 %+d B/批",
		aliasWhole/retentionBatches, cloneWhole/retentionBatches,
		(aliasWhole-cloneWhole)/retentionBatches)
	t.Logf("single（仅一条存活）：零拷贝 %d B/批，克隆 %d B/批，差 %+d B/批",
		aliasSingle/retentionBatches, cloneSingle/retentionBatches,
		(aliasSingle-cloneSingle)/retentionBatches)
}
