// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import (
	"slices"
	"testing"
)

// TestOperatorsForKind 钉住「比较符 × 数据类型」矩阵 —— 它是配置校验（service）、
// 采集判定（alarm）与前端下拉（经 /protocol/getDataTypes 的 kindByType）共用的唯一真源。
// 三处任何一处自行改动语义，都会在这里暴露。
func TestOperatorsForKind(t *testing.T) {
	numeric := []string{OpRange, OpGt, OpGe, OpLt, OpLe, OpEq, OpNe}
	tests := []struct {
		kind string
		want []string
	}{
		{KindInt, numeric},
		{KindUInt, numeric},
		{KindFloat, numeric},
		{KindBool, []string{OpEq, OpNe}},
		{KindString, []string{OpEq, OpNe, OpIn}},
		// 时间类型刻意不支持：那类需求其实比的是"多久没更新"，不是配置边界
		{KindTime, nil},
		// 未知/空 Kind 一律不支持 —— 认不出来就不放行，别配出一个永不生效的开关
		{"", nil},
		{"unknown", nil},
	}
	for _, c := range tests {
		t.Run(c.kind, func(t *testing.T) {
			got := OperatorsForKind(c.kind)
			if !slices.Equal(got, c.want) {
				t.Fatalf("OperatorsForKind(%q) = %v，期望 %v", c.kind, got, c.want)
			}
		})
	}
}

// TestOperatorsForKindReturnsCopy 返回的切片被改写不得影响后续调用 ——
// 共享内部切片会让"某个协议突然少了个比较符"，且极难查。
func TestOperatorsForKindReturnsCopy(t *testing.T) {
	first := OperatorsForKind(KindFloat)
	if len(first) == 0 {
		t.Fatal("float 应有可用比较符")
	}
	first[0] = "tampered"

	second := OperatorsForKind(KindFloat)
	if second[0] == "tampered" {
		t.Fatal("OperatorsForKind 返回了内部切片，被调用方改写后污染了真源")
	}
}

// TestOperatorAllowed 与 OperatorsForKind 必须同源（一个说可用、另一个说不可用就是 bug）。
func TestOperatorAllowed(t *testing.T) {
	tests := []struct {
		kind, op string
		want     bool
	}{
		{KindFloat, OpRange, true},
		{KindFloat, OpIn, false}, // in 只对文本有意义
		{KindBool, OpEq, true},
		{KindBool, OpGt, false}, // 布尔没有区间/大小可言
		{KindBool, OpRange, false},
		{KindString, OpIn, true},
		{KindString, OpGt, false}, // 文本不做大小比较
		{KindTime, OpEq, false},
		{"", OpEq, false},
	}
	for _, c := range tests {
		if got := OperatorAllowed(c.kind, c.op); got != c.want {
			t.Fatalf("OperatorAllowed(%q, %q) = %v，期望 %v", c.kind, c.op, got, c.want)
		}
	}
}

func TestIsNumericKind(t *testing.T) {
	for _, k := range []string{KindInt, KindUInt, KindFloat} {
		if !IsNumericKind(k) {
			t.Fatalf("%s 应为数值类别", k)
		}
	}
	for _, k := range []string{KindBool, KindString, KindTime, "", "unknown"} {
		if IsNumericKind(k) {
			t.Fatalf("%s 不应为数值类别", k)
		}
	}
}
