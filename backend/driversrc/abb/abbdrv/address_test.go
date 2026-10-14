// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// mustParse 解析地址，失败即终止。
func mustParse(t *testing.T, s string) *parsedAddr {
	t.Helper()
	a, err := parseAddress(s)
	if err != nil {
		t.Fatalf("parseAddress(%q) 意外失败: %v", s, err)
	}
	return a
}

func TestParseAddressNamespaces(t *testing.T) {
	cases := []struct {
		in   string
		want parsedAddr
	}{
		// ---- signal ----
		{"signal:Local/DRV_1/DI1", parsedAddr{
			ns: nsSignal, signalNet: "Local", signalUnit: "DRV_1", signalName: "DI1", field: "lvalue"}},
		{"signal:DRV_1/DI1", parsedAddr{
			ns: nsSignal, signalUnit: "DRV_1", signalName: "DI1", field: "lvalue"}},
		{"signal:DI1", parsedAddr{
			ns: nsSignal, signalName: "DI1", field: "lvalue"}},
		// ;state 是 RWS 的"只取值"后缀，语义与不带它相同，解析时剥掉。
		{"signal:Local/DRV_1/DI1;state", parsedAddr{
			ns: nsSignal, signalNet: "Local", signalUnit: "DRV_1", signalName: "DI1", field: "lvalue"}},
		// 命名空间大小写不敏感。
		{"SIGNAL:Local/DRV_1/DI1", parsedAddr{
			ns: nsSignal, signalNet: "Local", signalUnit: "DRV_1", signalName: "DI1", field: "lvalue"}},
		// 显式字段选择器覆盖默认的 lvalue。
		{"signal:Local/DRV_1/DI1#quality", parsedAddr{
			ns: nsSignal, signalNet: "Local", signalUnit: "DRV_1", signalName: "DI1", field: "quality"}},

		// ---- rapid ----
		{"rapid:T_ROB1/Module1/nTest", parsedAddr{
			ns: nsRapid, rapidTask: "T_ROB1", rapidModule: "Module1", rapidSymbol: "nTest"}},
		// 可选的 RAPID 段（原生路径里就有），剥掉。
		{"rapid:RAPID/T_ROB1/Module1/nTest", parsedAddr{
			ns: nsRapid, rapidTask: "T_ROB1", rapidModule: "Module1", rapidSymbol: "nTest"}},

		// ---- panel ----
		{"panel:ctrlstate", parsedAddr{ns: nsPanel, panelRes: "ctrlstate", field: "ctrlstate"}},
		// ctrl-state 与 ctrlstate 等价（URL 里有连字符，字段名没有）。
		{"panel:ctrl-state", parsedAddr{ns: nsPanel, panelRes: "ctrlstate", field: "ctrlstate"}},
		{"panel:opmode", parsedAddr{ns: nsPanel, panelRes: "opmode", field: "opmode"}},
		{"panel:speedratio", parsedAddr{ns: nsPanel, panelRes: "speedratio", field: "speedratio"}},

		// ---- motion ----
		{"motion:ROB_1/joint/rax_1", parsedAddr{
			ns: nsMotion, mechUnit: "ROB_1", motionRes: motionResJoint, field: "rax_1"}},
		// 省略机械单元时回退 ROB_1。
		{"motion:joint/rax_1", parsedAddr{
			ns: nsMotion, mechUnit: defaultMechUnit, motionRes: motionResJoint, field: "rax_1"}},
		// 不带字段 = 整组复合值。
		{"motion:ROB_1/joint", parsedAddr{
			ns: nsMotion, mechUnit: "ROB_1", motionRes: motionResJoint}},
		{"motion:joint", parsedAddr{
			ns: nsMotion, mechUnit: defaultMechUnit, motionRes: motionResJoint}},
		{"motion:ROB_1/cartesian/x", parsedAddr{
			ns: nsMotion, mechUnit: "ROB_1", motionRes: motionResCartesian, field: "x"}},
		// robtarget / jointtarget 是 RWS 的原生资源名，同样接受。
		{"motion:ROB_1/robtarget/x", parsedAddr{
			ns: nsMotion, mechUnit: "ROB_1", motionRes: motionResCartesian, field: "x"}},
		{"motion:ROB_1/jointtarget/rax_2", parsedAddr{
			ns: nsMotion, mechUnit: "ROB_1", motionRes: motionResJoint, field: "rax_2"}},
		// 示教器上显示的是 rax1 风格，两种写法都接受并归一化。
		{"motion:ROB_1/joint/rax1", parsedAddr{
			ns: nsMotion, mechUnit: "ROB_1", motionRes: motionResJoint, field: "rax_1"}},
		{"motion:ROB_1/joint/rex6", parsedAddr{
			ns: nsMotion, mechUnit: "ROB_1", motionRes: motionResJoint, field: "rex_6"}},

		// ---- system ----
		{"system:rwversion", parsedAddr{ns: nsSystem, systemField: "rwversion", field: "rwversion"}},
		{"system:RWSysID", parsedAddr{ns: nsSystem, systemField: "rwsysid", field: "rwsysid"}},
	}

	for _, c := range cases {
		got := mustParse(t, c.in)
		if !reflect.DeepEqual(*got, c.want) {
			t.Errorf("parseAddress(%q)\n got  %+v\n want %+v", c.in, *got, c.want)
		}
	}
}

func TestParseAddressNativePathEqualsDialectOne(t *testing.T) {
	// 方言二（可粘贴的 RWS 原生路径）必须归一化成与方言一**逐字段相同**的结果。
	// 不一致的症状是"同一个点位换个写法就采不到了"。
	pairs := [][2]string{
		{"/rw/iosystem/signals/Local/DRV_1/DI1", "signal:Local/DRV_1/DI1"},
		{"/rw/iosystem/signals/Local/DRV_1/DI1;state", "signal:Local/DRV_1/DI1"},
		{"/rw/iosystem/signals/DRV_1/DI1", "signal:DRV_1/DI1"},
		{"/rw/rapid/symbol/data/RAPID/T_ROB1/Module1/nTest", "rapid:T_ROB1/Module1/nTest"},
		{"/rw/rapid/symbol/T_ROB1/Module1/nTest", "rapid:T_ROB1/Module1/nTest"},
		{"/rw/panel/ctrl-state", "panel:ctrlstate"},
		{"/rw/panel/opmode", "panel:opmode"},
		{"/rw/motionsystem/mechunits/ROB_1/jointtarget", "motion:ROB_1/joint"},
		{"/rw/motionsystem/mechunits/ROB_1/robtarget", "motion:ROB_1/cartesian"},
		{"/rw/motionsystem/mechunits/ROB_1/cartesian", "motion:ROB_1/cartesian"},
		{"/rw/system#rwversion", "system:rwversion"},
	}

	for _, p := range pairs {
		native := mustParse(t, p[0])
		dialect := mustParse(t, p[1])
		if !reflect.DeepEqual(*native, *dialect) {
			t.Errorf("两种写法解析结果不一致：\n %s → %+v\n %s → %+v", p[0], *native, p[1], *dialect)
		}
	}
}

// TestSignalSemicolonSuffixStripped ";" 之后的一切都是资源属性后缀，一律截断。
//
// ABB 的信号名只允许字母数字与下划线，所以 ";" 不可能出现在名字里；
// 而放行未知后缀（如用户手滑写了 ;lvalue 之外的拼写）的代价是每轮一次
// 必然 404 的往返。截断让这类写法退化为"多打了一截"，而不是采不到值。
func TestSignalSemicolonSuffixStripped(t *testing.T) {
	for _, in := range []string{
		"signal:Local/DRV_1/DI1;state",
		"signal:Local/DRV_1/DI1;lvalue",
		"signal:Local/DRV_1/DI1;anything",
	} {
		a := mustParse(t, in)
		if a.signalName != "DI1" {
			t.Errorf("parseAddress(%q) 的信号名 = %q, 期望 DI1（分号后的后缀应被截断）", in, a.signalName)
		}
	}
}

// TestNativePathCarriesFieldThrough 原生路径带 #字段 时同样生效。
func TestNativePathCarriesFieldThrough(t *testing.T) {
	a := mustParse(t, "/rw/iosystem/signals/Local/DRV_1/DI1#quality")
	if a.field != "quality" {
		t.Errorf("field = %q, 期望 quality", a.field)
	}
	if a.signalNet != "Local" || a.signalUnit != "DRV_1" || a.signalName != "DI1" {
		t.Errorf("信号路径解析错误：%+v", *a)
	}
}

// TestBareAddressRejected 是本文件里最重要的一条。
//
// 裸地址（无命名空间前缀）**必须**被拒绝。`Local/DRV_1/DI1` 与 rapid 的
// `task/module/symbol` 在语法上完全同形，任何"没前缀就当 IO 信号"的回退都是
// 在猜；猜错的症状是**静默读到另一个资源的值** —— 推送通道只按 Kind 存数，
// 从时序库里完全看不出异常。
func TestBareAddressRejected(t *testing.T) {
	for _, in := range []string{
		"Local/DRV_1/DI1",
		"DI1",
		"T_ROB1/Module1/nTest",
		"ROB_1/joint/rax_1",
		"rw/iosystem/signals/Local/DRV_1/DI1", // 少了开头的斜杠
	} {
		_, err := parseAddress(in)
		var pe *parseError
		if !errors.As(err, &pe) {
			t.Errorf("parseAddress(%q) 应返回 *parseError，实际 %v", in, err)
		}
	}
}

func TestParseAddressErrors(t *testing.T) {
	cases := []struct{ in, why string }{
		{"", "空地址"},
		{"   ", "只有空白"},
		{"foo:bar", "未知命名空间"},
		{"signal:", "signal 缺信号名"},
		{"signal:a/b/c/d", "signal 段数过多"},
		{"rapid:T_ROB1/Module1", "rapid 段数不足"},
		{"rapid:T_ROB1", "rapid 只有一段"},
		{"rapid:", "rapid 空"},
		{"panel:", "panel 缺资源名"},
		{"panel:unknown", "panel 白名单外的资源"},
		{"motion:", "motion 空"},
		{"motion:ROB_1/joint/rax_9", "motion 白名单外的字段"},
		{"motion:ROB_1/joint/q1", "关节资源不接受笛卡尔字段"},
		{"motion:ROB_1/cartesian/rax_1", "笛卡尔资源不接受关节字段"},
		{"motion:ROB_1/speed/x", "motion 未知资源"},
		{"motion:ROB_1", "motion 缺资源"},
		{"system:", "system 缺字段名"},
		{"signal:DI1#", "# 后为空"},
		{"#quality", "# 前为空"},
		{"/rw/unknown/thing", "未识别的原生路径"},
		{"/rw/system", "原生 system 路径缺字段"},
		{"/rw/iosystem/signals", "原生信号路径缺信号名"},
		{"/rw", "只有 rw 根"},
	}

	for _, c := range cases {
		_, err := parseAddress(c.in)
		var pe *parseError
		if !errors.As(err, &pe) {
			t.Errorf("parseAddress(%q)（%s）应返回 *parseError，实际 %v", c.in, c.why, err)
		}
	}
}

// TestParseErrorIsNotServiceError parseError 必须能被单独识别出来：
// 驱动据此"不发请求、直接 Quality=0"，而服务级错误是要发请求的。
func TestParseErrorIsNotServiceError(t *testing.T) {
	_, err := parseAddress("foo:bar")
	if err == nil {
		t.Fatal("期望错误")
	}
	if isServiceError(err) {
		t.Error("parseError 不该被当成服务级错误（它压根没发请求）")
	}
	if !isFatal(err) {
		// parseError 不是 isFatal 的目标 —— Read 在计划阶段就把它们分流了，
		// 走到 isFatal 说明分类漏了一处。
		t.Log("parseError 未被 isFatal 判为致命（符合预期：它在计划阶段就被分流）")
	}
}

func TestSignalPathAndRapidPath(t *testing.T) {
	cases := []struct{ in, signal, rapid string }{
		{"signal:Local/DRV_1/DI1", "Local/DRV_1/DI1", ""},
		{"signal:DRV_1/DI1", "DRV_1/DI1", ""},
		{"signal:DI1", "DI1", ""},
	}
	for _, c := range cases {
		a := mustParse(t, c.in)
		if got := a.signalPath(); got != c.signal {
			t.Errorf("%s signalPath = %q, 期望 %q", c.in, got, c.signal)
		}
	}

	for _, in := range []string{"rapid:T_ROB1/Module1/nTest", "rapid:RAPID/T_ROB1/Module1/nTest"} {
		a := mustParse(t, in)
		if got := a.rapidPath(); got != "T_ROB1/Module1/nTest" {
			t.Errorf("%s rapidPath = %q, 期望 T_ROB1/Module1/nTest", in, got)
		}
	}
}

func TestResourcePath(t *testing.T) {
	cfg := DefaultConfig()
	cases := []struct{ in, want string }{
		{"signal:Local/DRV_1/DI1", "/rw/iosystem/signals/Local/DRV_1/DI1"},
		{"rapid:T_ROB1/Module1/nTest", "/rw/rapid/symbol/data/RAPID/T_ROB1/Module1/nTest"},
		{"panel:ctrlstate", "/rw/panel/ctrl-state"},
		{"panel:opmode", "/rw/panel/opmode"},
		{"panel:speedratio", "/rw/panel/speedratio"},
		{"motion:ROB_1/joint/rax_1", "/rw/motionsystem/mechunits/ROB_1/jointtarget"},
		{"motion:ROB_1/cartesian/x", "/rw/motionsystem/mechunits/ROB_1/robtarget"},
		{"system:rwversion", "/rw/system"},
	}
	for _, c := range cases {
		a := mustParse(t, c.in)
		got, err := resourcePath(a, cfg)
		if err != nil {
			t.Fatalf("resourcePath(%q) 失败: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("resourcePath(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}

	// cartesianSource=cartesian 时走 /cartesian 并带上坐标系查询串。
	cfg2 := DefaultConfig()
	cfg2.CartesianSource = "cartesian"
	a := mustParse(t, "motion:ROB_1/cartesian/x")
	got, err := resourcePath(a, cfg2)
	if err != nil {
		t.Fatalf("resourcePath 失败: %v", err)
	}
	for _, want := range []string{"/rw/motionsystem/mechunits/ROB_1/cartesian?", "tool=tool0", "wobj=wobj0", "coordinate=Base"} {
		if !strings.Contains(got, want) {
			t.Errorf("cartesian 路径 %q 缺少 %q", got, want)
		}
	}
}

// TestEscapePathKeepsSeparators 信号名里的特殊字符要逐段转义，
// 但 "/" 必须原样保留为路径分隔符。
func TestEscapePathKeepsSeparators(t *testing.T) {
	got := escapePath("Local/DRV 1/DI#1")
	want := "Local/DRV%201/DI%231"
	if got != want {
		t.Errorf("escapePath = %q, 期望 %q", got, want)
	}
}
