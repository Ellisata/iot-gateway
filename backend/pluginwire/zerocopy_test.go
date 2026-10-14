// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginwire

// 零拷贝字符串的**内存驻留**代价实测。
//
// 零拷贝省下的每一次复制，代价都是：返回的字符串盯住的是**整个载荷缓冲**，
// 而不是它自己那几个字节。这条测试把这个代价量出来，因为它决定了这个优化
// 能不能上生产 —— 网关的采集结果会带着这些字符串一路流到 push 队列
//（collector.toRecords 直接赋值、不复制，见 collector/task.go 的 toRecords），
// 而 push 队列里可以同时躺着成千上万条记录。
//
// 跑法（两遍，对照 zeroCopyStrings 的开关）：
//
//	go test ./pluginwire -run TestZeroCopyRetentionReport -v

import (
	"fmt"
	"runtime"
	"testing"
)

// replyPayload 造一份形状与真实 ReadReply 相同的载荷：
// session + connected + 结果数 + N×(id,value,dataType,kind,quality)。
func replyPayload(points int) []byte {
	e := NewEncoder(64 * points)
	e.Uvarint(1) // session
	e.Bool(true) // connected
	e.Uvarint(uint64(points))
	for j := 0; j < points; j++ {
		e.String(fmt.Sprintf("0192f3a1-0000-7000-8000-%012d", j))
		e.String("1234.5")
		e.String("int16")
		e.String("int")
		e.Varint(192)
	}
	return e.Bytes()
}

// decodeKeepingFirst 解析整帧，但**只保留第一条结果的 value**，其余全部丢弃。
// 这正是网关的真实形态：push 队列最终只可能留住其中若干条。
func decodeKeepingFirst(p []byte) string {
	d := NewDecoder(p)
	d.Uvarint() // session
	d.Bool()    // connected
	n := d.Uvarint()

	var first string
	for j := uint64(0); j < n; j++ {
		// 每次读取都必须显式丢弃：留一个悬空的结果会让 vet 报
		// "call not used"，更麻烦的是编译器有权把这些读取整个删掉，
		// 那样量到的就不是解码器的行为而是它的缺席。
		_ = d.String() // id
		v := d.String()
		_ = d.String() // dataType
		_ = d.String() // kind
		_ = d.Varint() // quality
		if j == 0 {
			first = v
		}
	}
	if d.Err() != nil {
		panic("解析失败: " + d.Err().Error())
	}
	return first
}

// TestZeroCopyRetentionReport 量并打印：每留住一条记录，堆上要多钉住多少字节。
//
// 期望（也是这条测试要固化的结论）：
//   - 零拷贝开启：每帧 ≈ 整帧载荷大小 —— 留一条 6 字节的值，钉住整块 50 KB；
//   - 零拷贝关闭：每帧 ≈ 那一条记录自身，约几十字节。
func TestZeroCopyRetentionReport(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过内存驻留实测")
	}

	const frames = 64
	const points = 1000

	// 基线必须在**分配载荷之前**量。放到之后量的话，被钉住的载荷已经在
	// baseline 里了，驻留会表现为"增量为零"而不是一个正数 —— 看上去像没有代价。
	runtime.GC()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	payloads := make([][]byte, frames)
	for i := range payloads {
		payloads[i] = replyPayload(points)
	}
	frameBytes := len(payloads[0])

	// 每解析完一帧就把载荷引用断开（置 nil），这样"还剩多少被引用"完全
	// 取决于解码器交出去的字符串。
	//
	// kept 用**指针**收住每个字符串：只把它们塞进切片，编译器有权认定
	// 除首个之外的元素都是死存储而整段删掉（实测第一版就是这么被抹掉的，
	// 量到的驻留是负的）。取地址后每个元素都可达，抹不掉。
	kept := make([]*string, 0, frames)
	for i, p := range payloads {
		v := decodeKeepingFirst(p)
		kept = append(kept, &v)
		payloads[i] = nil
	}

	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	perFrame := retained / frames

	// 累计长度：既防优化，也顺带确认真的留住了内容。
	total := 0
	for _, s := range kept {
		total += len(*s)
	}

	t.Logf("zeroCopyStrings=%v", zeroCopyStrings)
	t.Logf("载荷/帧 = %d B，保留 %d 帧，每帧只留住 1 条值（共 %d 字节实际内容）",
		frameBytes, frames, total)
	t.Logf("堆驻留总量 = %d B（%.2f MB），每帧 = %d B（载荷的 %.0f%%）",
		retained, float64(retained)/(1<<20), perFrame,
		float64(perFrame)*100/float64(frameBytes))
}
