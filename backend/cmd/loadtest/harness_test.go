// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"testing"
	"time"
)

// -serial-baud 折算出的线路时间直接决定测出来的容量，而它是纯算术 ——
// 算错了没有任何现象会提示，只会得到一份看着合理的错误结论。故逐一钉住。
func TestLineTimeAt(t *testing.T) {
	cases := []struct {
		baud, bytes int
		want        time.Duration
	}{
		// 8E1 = 11 位/字节：168 字节 @2400bps = 168×11/2400 s = 770ms
		{2400, 168, 770 * time.Millisecond},
		// 9600bps 是 4 倍速
		{9600, 168, 192500 * time.Microsecond},
		// 115200bps：16.04ms（余数截断）
		{115200, 168, 16041666 * time.Nanosecond},
		// 逐点读（N=1）一帧 36 字节 @2400bps = 165ms
		{2400, 36, 165 * time.Millisecond},
		// 非法输入退化为 0（调用方另有校验，这里保证不产生负数或除零）
		{0, 168, 0},
		{-1, 168, 0},
		{2400, 0, 0},
	}

	for _, c := range cases {
		if got := lineTimeAt(c.baud, c.bytes); got != c.want {
			t.Errorf("lineTimeAt(%d, %d) = %v, want %v", c.baud, c.bytes, got, c.want)
		}
	}
}

// 帧字节数必须随打包数变化：请求 = 12+4N、应答 = 12+N×(4+每标识数据字节)。
func TestDLT645SerialFrameBytes(t *testing.T) {
	cases := []struct {
		dlt645DIs int // 0 = 不带该字段，用驱动默认
		want      int
	}{
		{0, 168},  // 驱动默认 12：24 + 12×12
		{12, 168}, // 显式 12，与默认同值
		{1, 36},   // 逐点读：24 + 4 + 8
		{2, 48},   // 24 + 8 + 16
	}

	for _, c := range cases {
		got := dlt645SerialFrameBytes(Options{DLT645DIs: c.dlt645DIs})
		if got != c.want {
			t.Errorf("dlt645SerialFrameBytes(DLT645DIs=%d) = %d, want %d",
				c.dlt645DIs, got, c.want)
		}
	}
}

// 压测文档（README 的 645 专节）用 2400bps / N=12 举例，给出的是「一帧 168 字节
// ≈ 770ms」。这里把文档里的数字钉在代码上：文档改了代码没改（或反之）会立刻暴露。
func TestDLT645SerialFrameBytesMatchesDocumentedExample(t *testing.T) {
	frames := dlt645SerialFrameBytes(Options{DLT645DIs: 12})
	if frames != 168 {
		t.Fatalf("文档举例为 168 字节/帧，实得 %d", frames)
	}
	if got := lineTimeAt(2400, frames); got != 770*time.Millisecond {
		t.Fatalf("文档举例为 770ms，实得 %v", got)
	}
}

// 只有实现了 serialFrameBytes 的协议才支持 -serial-baud；其余必须显式报错，
// 不能静默按 0 处理（那会让人以为在测串口容量，实际测的还是 loopback）。
func TestSerialBaudSupportedOnlyWhereFrameLayoutKnown(t *testing.T) {
	if adapters["dlt645"].serialFrameBytes == nil {
		t.Error("dlt645 应实现 serialFrameBytes")
	}
	for key, pa := range adapters {
		if key == "dlt645" {
			continue
		}
		if pa.serialFrameBytes != nil {
			t.Errorf("适配器 %q 声明了 serialFrameBytes，但本测试未覆盖其帧布局；"+
				"新增实现时请一并补测试（README 的 -serial-baud 支持列表也要更新）", key)
		}
	}
}
