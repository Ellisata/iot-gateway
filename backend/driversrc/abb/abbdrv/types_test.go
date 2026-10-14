// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"testing"

	"iot-gateway/driver"
)

func TestTypesBareNamesSortedAndKindPreserved(t *testing.T) {
	types := Types()
	if len(types) == 0 {
		t.Fatal("Types() 返回空列表")
	}

	var prev string
	for _, dt := range types {
		if dt == nil {
			t.Fatal("Types() 里出现 nil")
		}
		// 网关会给上报的类型名再拼一次 scope 前缀，带了前缀会得到
		// "abb.abb.int" —— 地址校验与类型下拉双双落空。
		if containsRune(dt.Name, '.') {
			t.Errorf("类型 %q 带了 scope 前缀", dt.Name)
		}
		if dt.Kind == "" {
			t.Errorf("类型 %q 的 Kind 为空 —— 推送通道会按错误类型存值", dt.Name)
		}
		if prev != "" && dt.Name < prev {
			t.Errorf("类型未按名称排序：%q 出现在 %q 之后", dt.Name, prev)
		}
		prev = dt.Name
	}

	// Kind 逐字比对：改一个 Kind 就等于改推送通道的存储表示。
	want := map[string]string{
		"auto":   driver.KindString,
		"bool":   driver.KindBool,
		"int":    driver.KindInt,
		"uint":   driver.KindUInt,
		"float":  driver.KindFloat,
		"string": driver.KindString,
		"time":   driver.KindTime,
	}
	got := make(map[string]string, len(types))
	for _, dt := range types {
		got[dt.Name] = dt.Kind
	}
	if len(got) != len(want) {
		t.Errorf("上报 %d 个类型，期望 %d 个：%v", len(got), len(want), got)
	}
	for name, kind := range want {
		if k, ok := got[name]; !ok {
			t.Errorf("类型 %q 未被上报", name)
		} else if k != kind {
			t.Errorf("类型 %q 的 Kind = %q, 期望 %q", name, k, kind)
		}
	}
}

// TestTypeMapCoversAllTargets TypeMap 的每个 value 都必须能在 Types 里找到。
// 悬空映射会被插件加载拒掉（driversdk.Meta.Validate），这里提前在插件自带的
// 测试里抓住它，而不是等网关那边报"插件被拒绝加载"。
func TestTypeMapCoversAllTargets(t *testing.T) {
	have := make(map[string]bool)
	for _, dt := range Types() {
		have[dt.Name] = true
	}
	for common, internal := range CommonTypeMap() {
		if !have[internal] {
			t.Errorf("通用类型 %q → 内部类型 %q 不在 Types() 里", common, internal)
		}
	}
}

// TestTypeMapOmitsUnmappedCommonTypes RWS 没有字节/字符寄存器与 BCD 信号，
// 这几项刻意不映射 —— 下拉里少一项，好过多一项选了也取不到值的。
func TestTypeMapOmitsUnmappedCommonTypes(t *testing.T) {
	m := CommonTypeMap()
	for _, name := range []string{driver.TypeByte, driver.TypeChar, driver.TypeBCD, driver.TypeLBCD} {
		if _, ok := m[name]; ok {
			t.Errorf("类型 %q 不该被映射：RWS 没有对应概念", name)
		}
	}
	// Auto 必须存在 —— 它是本协议的主推类型。
	if m["Auto"] != "auto" {
		t.Errorf("Auto → %q, 期望 auto", m["Auto"])
	}
}

func TestTypeKind(t *testing.T) {
	// 裸名与带 scope 前缀的名都要能解析（网关回传的 data_type 可能是后者）。
	for _, name := range []string{"int", "abb.int"} {
		if got := typeKind(name); got != driver.KindInt {
			t.Errorf("typeKind(%q) = %q, 期望 %q", name, got, driver.KindInt)
		}
	}
	if got := typeKind("nonexistent"); got != "" {
		t.Errorf("未注册类型应返回空串，实际 %q", got)
	}
}

// TestResolveKindAuto 是本文件的核心：auto 只信资源声明的 RWS 类型，
// 绝不按值推断。
//
// 按值推断必然出错的场景是模拟量恒为 0/1 时被推断成 bool —— 这类错误只会
// 在时序库里表现为"字段类型变了"，比 panic 更难发现。
func TestResolveKindAuto(t *testing.T) {
	cases := []struct {
		addr     string
		declared string
		want     string
	}{
		// ---- signal：按信号自身声明 ----
		{"signal:L/D/DI1", "DI", driver.KindBool},
		{"signal:L/D/DO1", "DO", driver.KindBool},
		{"signal:L/D/AI1", "AI", driver.KindFloat},
		{"signal:L/D/AO1", "AO", driver.KindFloat},
		{"signal:L/D/GI1", "GI", driver.KindInt},
		{"signal:L/D/GO1", "GO", driver.KindInt},
		{"signal:L/D/di1", "di", driver.KindBool}, // 大小写不敏感
		{"signal:L/D/X1", "", driver.KindString},  // 声明缺失 → 保守兜底
		{"signal:L/D/X1", "WEIRD", driver.KindString},

		// ---- signal：字段决定语义，与信号类型无关 ----
		{"signal:L/D/DI1#quality", "DI", driver.KindInt},
		{"signal:L/D/DI1#lstate", "DI", driver.KindString},
		{"signal:L/D/DI1#phstate", "DI", driver.KindString},
		{"signal:L/D/DI1#type", "DI", driver.KindString},

		// ---- rapid ----
		{"rapid:T/M/n", "num", driver.KindFloat},
		{"rapid:T/M/d", "dnum", driver.KindFloat},
		{"rapid:T/M/b", "bool", driver.KindBool},
		{"rapid:T/M/s", "string", driver.KindString},
		{"rapid:T/M/p", "robtarget", driver.KindString},  // 复合值整取 = 文本
		{"rapid:T/M/p#x", "robtarget", driver.KindFloat}, // 已知全数值复合类型的分量
		{"rapid:T/M/j#rax_1", "jointtarget", driver.KindFloat},
		{"rapid:T/M/x#a", "mysteryType", driver.KindString}, // 未知复合类型的分量
		{"rapid:T/M/n#a", "num", driver.KindString},         // 标量类型没有分量

		// ---- panel ----
		{"panel:speedratio", "", driver.KindInt},
		{"panel:ctrlstate", "", driver.KindString},
		{"panel:opmode", "", driver.KindString},

		// ---- motion ----
		{"motion:ROB_1/joint/rax_1", "", driver.KindFloat},
		{"motion:ROB_1/cartesian/x", "", driver.KindFloat},
		{"motion:ROB_1/joint/extjoint", "", driver.KindString}, // 复合分量
		{"motion:ROB_1/joint", "", driver.KindString},          // 整组

		// ---- system ----
		{"system:rwversion", "", driver.KindString},
	}

	for _, c := range cases {
		a := mustParse(t, c.addr)
		if got := resolveKind(a, typeAuto, c.declared); got != c.want {
			t.Errorf("resolveKind(%q, auto, %q) = %q, 期望 %q", c.addr, c.declared, got, c.want)
		}
	}
}

// TestResolveKindConfiguredTypeWins 用户显式配置的类型永远优先 ——
// 包括"用户选错了"。显式选择是运维的最后手段，驱动不该越过它去猜。
func TestResolveKindConfiguredTypeWins(t *testing.T) {
	a := mustParse(t, "signal:L/D/DI1") // auto 会判成 bool
	if got := resolveKind(a, "int", "DI"); got != driver.KindInt {
		t.Errorf("配置了 int 时应返回 int，实际 %q", got)
	}
	if got := resolveKind(a, "float", "DI"); got != driver.KindFloat {
		t.Errorf("配置了 float 时应返回 float，实际 %q", got)
	}
	// 配置的类型未注册（历史数据漂移）→ 落回 auto 规则，而不是返回空 Kind。
	if got := resolveKind(a, "no-such-type", "DI"); got != driver.KindBool {
		t.Errorf("未注册的配置类型应落回 auto 规则，实际 %q", got)
	}
}

func TestFormatValueBool(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1", "1"}, {"0", "0"},
		{"true", "1"}, {"TRUE", "1"}, {"True", "1"},
		{"false", "0"}, {"FALSE", "0"},
		{"on", "1"}, {"off", "0"},
		{"yes", "1"}, {"no", "0"},
		// 认不出来就原样返回 —— 编造一个 "0" 比留下解析失败更糟。
		{"maybe", "maybe"},
		{"", ""},
		{" 1 ", "1"},
	}
	for _, c := range cases {
		if got := formatValue(c.in, driver.KindBool); got != c.want {
			t.Errorf("formatValue(%q, bool) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestFormatValueInteger(t *testing.T) {
	cases := []struct{ in, want string }{
		{"12", "12"},
		// RWS 不区分书写形式，整型点位也会回小数形式；
		// 直接透传会让通道的 ParseInt 失败并降级到浮点列。
		{"12.000000", "12"},
		{"-5", "-5"},
		{"+7", "7"},
		{"007", "7"},
		{"0", "0"},
		// 非整数原样保留（通道自己决定怎么办）。
		{"1.5", "1.5"},
		{"abc", "abc"},
		{"", ""},
	}
	for _, c := range cases {
		if got := formatValue(c.in, driver.KindInt); got != c.want {
			t.Errorf("formatValue(%q, int) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestFormatValueFloatPreservesRaw 浮点值一律原样透传。
//
// 做 float64 往返会把 "-0.000000" 变成 "-0"，白白丢掉控制器给出的精度信息。
func TestFormatValueFloatPreservesRaw(t *testing.T) {
	for _, in := range []string{"-0.000000", "1.500000", "0", "-0", "1e-7"} {
		if got := formatValue(in, driver.KindFloat); got != in {
			t.Errorf("formatValue(%q, float) = %q, 期望原样透传", in, got)
		}
	}
}

// TestNormalizeDecimalOnlyForNumericStrings 本地化小数点只在整串形如
// "-?\d+,\d+" 时才替换 —— 否则会误伤含逗号的文本值。
func TestNormalizeDecimalOnlyForNumericStrings(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1,5", "1.5"},
		{"-1,5", "-1.5"},
		{"0,000000", "0.000000"},
		// 以下都不该被改：
		{"waiting, paused", "waiting, paused"},
		{"1,", "1,"},
		{",5", ",5"},
		{"1,2,3", "1,2,3"},
		{"a,5", "a,5"},
		{"1.5", "1.5"},
	}
	for _, c := range cases {
		if got := normalizeDecimal(c.in); got != c.want {
			t.Errorf("normalizeDecimal(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestFormatValueStringNeverRewritesText 字符串类型不做任何数值化改写 ——
// 含逗号的文本值在 string Kind 下必须逐字保留。
func TestFormatValueStringNeverRewritesText(t *testing.T) {
	for _, in := range []string{"1,5", "waiting, paused", "-0.000000"} {
		if got := formatValue(in, driver.KindString); got != in {
			t.Errorf("formatValue(%q, string) = %q, 期望原样", in, got)
		}
	}
}

func TestCommonTypeMapReturnsCopy(t *testing.T) {
	m := CommonTypeMap()
	m["Boolean"] = "tampered"
	if CommonTypeMap()["Boolean"] == "tampered" {
		t.Error("CommonTypeMap 应返回副本，调用方修改不该影响包内真源")
	}
}

func TestLookupCommonType(t *testing.T) {
	if v, ok := LookupCommonType("Float"); !ok || v != "float" {
		t.Errorf("LookupCommonType(Float) = (%q, %v)", v, ok)
	}
	// 历史配置里可能存着别的写法。
	if v, ok := LookupCommonType("float"); !ok || v != "float" {
		t.Errorf("大小写不敏感查找失败：(%q, %v)", v, ok)
	}
	if _, ok := LookupCommonType("Nope"); ok {
		t.Error("未命中的通用类型不该返回 true")
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
