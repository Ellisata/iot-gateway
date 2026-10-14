// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"context"
	"strings"
	"testing"
	"time"
)

// encapTap 认的是库的**文案**，所以这里逐字钉住两句原文：
// 一句是现场那条（0x6F SendRRData 被设备在封装层拒绝），一句是链路真断时
// 库会打的另一种字面 —— 后者必须**截不到**，否则一次真正的网络故障会被
// 解释成"设备在线但拒绝命令"，正是 encapTap 的注释里说的那种认错。
func TestEncapTapObserve(t *testing.T) {
	cases := []struct {
		name   string
		msg    string
		want   uint32
		wantOK bool
	}{
		{
			name:   "现场原文（SendRRData 被拒）",
			msg:    "Operation failed (attempt 1): RRData command failed with status: 0x00000001",
			want:   0x01,
			wantOK: true,
		},
		{
			name:   "同一句挂在别的前缀后面也要认",
			msg:    "goindustrial: Operation failed (attempt 1): RRData command failed with status: 0x00000064",
			want:   0x64,
			wantOK: true,
		},
		{
			name:   "会话不可用（链路真的断了）——不许误认",
			msg:    "Session unavailable (attempt 1): transport connection was reset",
			wantOK: false,
		},
		{
			name:   "i/o timeout 这类传输错误——不许误认",
			msg:    "Operation failed (attempt 1): read tcp 10.0.0.1:5000->10.0.0.2:44818: i/o timeout",
			wantOK: false,
		},
		{
			name:   "标记在但十六进制被截断——不许崩、不许认",
			msg:    "RRData command failed with status: 0x0000",
			wantOK: false,
		},
		{
			name:   "标记在但后面不是十六进制",
			msg:    "RRData command failed with status: 0xZZZZZZZZ",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tap encapTap
			got, ok := tap.observe(tc.msg)
			if ok != tc.wantOK {
				t.Fatalf("observe(%q) ok = %v，期望 %v", tc.msg, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("observe(%q) = 0x%02X，期望 0x%02X", tc.msg, got, tc.want)
			}
			// 认出来就必须能被 take 取走；没认出来就不能留下任何状态
			taken, tok := tap.take()
			if tok != tc.wantOK || (tok && taken != tc.want) {
				t.Fatalf("take() = (0x%02X, %v)，与 observe 的结果不一致", taken, tok)
			}
		})
	}
}

// take 必须清零：一次拒绝不能被拿去解释下一次失败（下一次可能是真的网络故障）。
func TestEncapTapTakeClears(t *testing.T) {
	var tap encapTap
	tap.observe("Operation failed (attempt 1): RRData command failed with status: 0x00000001")

	if st, ok := tap.take(); !ok || st != 0x01 {
		t.Fatalf("第一次 take = (0x%02X, %v)，期望 (0x01, true)", st, ok)
	}
	if st, ok := tap.take(); ok {
		t.Fatalf("第二次 take 仍取到 0x%02X，期望取不到（必须清零）", st)
	}
}

// 桥接器把码翻译成人话之后再输出 —— 那一行是现场唯一能看到真实原因的地方。
func TestBridgeWarnAppendsMeaning(t *testing.T) {
	var tap encapTap
	b := &giLevelBridge{tap: &tap}

	// logf 直写 stderr，这里只验 observe 的效果与描述的措辞存在性：
	// 描述本身由 describeEncapStatus 提供，测试只钉住"0x01 有中文人话"这一条。
	if desc := describeEncapStatus(0x01); !strings.Contains(desc, "不接受") {
		t.Fatalf("describeEncapStatus(0x01) = %q，期望含可读说明", desc)
	}
	if !strings.Contains(describeEncapStatus(0x99), "unknown") {
		t.Fatalf("未知码应回原始十六进制文案，得到 %q", describeEncapStatus(0x99))
	}
	// Warn 走一遍，确保 observe 被真正接上（不 panic、并记录到状态码）
	b.Warn(context.Background(), "Operation failed (attempt %d): %v", 1,
		&wrapErr{msg: "RRData command failed with status: 0x00000001"})
	if st, ok := tap.take(); !ok || st != 0x01 {
		t.Fatalf("Warn 之后 take = (0x%02X, %v)，期望 (0x01, true)", st, ok)
	}
}

// wrapErr 模拟库的 fmt.Errorf("RRData command failed with status: 0x%08X", ...)。
type wrapErr struct{ msg string }

func (e *wrapErr) Error() string { return e.msg }

// 限流的四条不变式。窗口是 60s，用例不等它 —— 直接把时间戳回拨，
// 验的正是"窗口过后要重新露一次头"这条（静音是错的方向，见 warnLimiter 注释）。
func TestWarnLimiter(t *testing.T) {
	l := &warnLimiter{}

	if !l.allow("10.0.0.1:44818", "A") {
		t.Fatal("首句应输出")
	}
	if l.allow("10.0.0.1:44818", "A") {
		t.Fatal("窗口内同一句不该重复输出")
	}
	if !l.allow("10.0.0.1:44818", "B") {
		t.Fatal("换了原因应立即输出（不能等窗口）")
	}
	if !l.allow("10.0.0.2:44818", "B") {
		t.Fatal("另一台设备的同一句话不该被吃掉")
	}

	l.last["10.0.0.1:44818"] = warnEntry{msg: "B", at: time.Now().Add(-2 * warnRepeatInterval)}
	if !l.allow("10.0.0.1:44818", "B") {
		t.Fatal("窗口过后应重新输出一次（设备一直坏不能只报一次）")
	}
}
