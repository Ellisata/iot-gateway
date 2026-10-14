// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"encoding/json"
	"testing"
)

func decodeTestJSON(t *testing.T, s string) any {
	t.Helper()
	root, err := decodeJSON([]byte(s))
	if err != nil {
		t.Fatalf("decodeJSON(%s) 失败: %v", s, err)
	}
	return root
}

// TestStatesOfFallbackChain 五条回退链逐条钉住。
//
// 走不到对应分支的代价是"某类资源在某个固件版本上全部采不到值"，
// 而响应形状本身没有报错。
func TestStatesOfFallbackChain(t *testing.T) {
	cases := []struct {
		name string
		json string
		want int
	}{
		{"标准形态 _embedded._state", `{"_embedded":{"_state":[{"a":1},{"b":2}]}}`, 2},
		{"少一层 _state", `{"_state":[{"a":1}]}`, 1},
		{"非标准嵌套（DFS 深度 ≤3）", `{"data":{"nested":{"_state":[{"a":1}]}}}`, 1},
		{"_embedded 下挂了别的数组", `{"_embedded":{"somethingElse":[{"a":1}]}}`, 1},
		{"没有包装的单资源响应", `{"ctrlstate":"motoron"}`, 1},
		{"根部就是数组", `[{"a":1},{"b":2},{"c":3}]`, 3},
		{"_state 里混有标量（只取对象）", `{"_embedded":{"_state":[{"a":1},42,"x"]}}`, 1},
	}

	for _, c := range cases {
		got := statesOf(decodeTestJSON(t, c.json))
		if len(got) != c.want {
			t.Errorf("%s：statesOf 返回 %d 条，期望 %d 条", c.name, len(got), c.want)
		}
	}
}

func TestDecodeJSONRejectsNonJSON(t *testing.T) {
	// RWS 1.0 在没带 ?json=1 时回 XML —— 这是最常见的配置错误，
	// 错误消息里必须带上响应体片段，才不至于让人去猜"为什么没有值"。
	_, err := decodeJSON([]byte(`<?xml version="1.0"?><html><body>...</body></html>`))
	if err == nil {
		t.Fatal("XML 响应应被拒绝")
	}
	if !isServiceError(err) {
		t.Error("非 JSON 响应是服务级错误：连接本身是好的，不该判成断连")
	}

	if _, err := decodeJSON(nil); err == nil {
		t.Error("空响应体应被拒绝")
	}
	if _, err := decodeJSON([]byte(`{bad json`)); err == nil {
		t.Error("畸形 JSON 应被拒绝")
	}
}

func TestDecodeJSONUsesNumber(t *testing.T) {
	root := decodeTestJSON(t, `{"n": 12345678901234567890}`)
	m, ok := root.(map[string]any)
	if !ok {
		t.Fatal("根应为对象")
	}
	if _, ok := m["n"].(json.Number); !ok {
		t.Errorf("数值应解成 json.Number（避免 float64 往返丢精度），实际 %T", m["n"])
	}
}

// TestFieldOfJudgesByPresenceNotEmptiness 是本文件里最重要的一条。
//
// RWS 用 "" 表示空文本、false 表示逻辑 0、0 表示零值。按"值非空"判缺失会把
// 它们统统当成"字段不存在"并标 Quality=0 —— 那会让一台正常控制器的空信号
// 永远显示为异常。
func TestFieldOfJudgesByPresenceNotEmptiness(t *testing.T) {
	m := map[string]any{"empty": "", "f": false, "zero": json.Number("0"), "nullv": nil}
	for _, k := range []string{"empty", "f", "zero", "nullv"} {
		if _, ok := fieldOf(m, k); !ok {
			t.Errorf("字段 %q 存在（值为零值）却被判为缺失", k)
		}
	}
	if _, ok := fieldOf(m, "absent"); ok {
		t.Error("不存在的字段被判为存在")
	}
}

func TestFieldOfCandidateOrderAndCase(t *testing.T) {
	m := map[string]any{"value": "second", "lvalue": "first"}
	if v, _ := fieldOf(m, "lvalue", "value"); v != "first" {
		t.Errorf("应按候选顺序取第一个命中的，实际 %v", v)
	}
	if v, _ := fieldOf(m, "value", "lvalue"); v != "second" {
		t.Errorf("候选顺序应被尊重，实际 %v", v)
	}

	// 大小写不敏感兜底（同一字段在不同固件上大小写不一致）。
	up := map[string]any{"LValue": "1"}
	if v, ok := fieldOf(up, "lvalue"); !ok || v != "1" {
		t.Errorf("大小写不敏感匹配失败：%v %v", v, ok)
	}
}

func TestScalarString(t *testing.T) {
	cases := []struct {
		in        any
		want      string
		wantValid bool
	}{
		{"x", "x", true},
		{"", "", true},
		{json.Number("12"), "12", true},
		{json.Number("12.50"), "12.50", true},
		{true, "1", true},
		{false, "0", true},
		{nil, "", true},
		{map[string]any{"a": 1}, "", false},
		{[]any{1, 2}, "", false},
	}
	for _, c := range cases {
		got, ok := scalarString(c.in)
		if got != c.want || ok != c.wantValid {
			t.Errorf("scalarString(%#v) = (%q, %v), 期望 (%q, %v)", c.in, got, ok, c.want, c.wantValid)
		}
	}
}

// TestCompoundStringOrder 复合值的字段顺序必须**确定**。
//
// 按 map 遍历顺序输出的话，同一个位姿在不同轮次会格式化成不同字符串 ——
// 时序库里全是"变化点"，排查者看不出到底哪个分量动了。
func TestCompoundStringOrder(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{
			"位姿按 x,y,z,q1..q4 固定顺序",
			map[string]any{"q4": "0", "x": "1", "z": "3", "y": "2", "q1": "4"},
			"x=1;y=2;z=3;q1=4;q4=0",
		},
		{
			"关节角按 rax_1..rex_* 固定顺序",
			map[string]any{"rex_1": "0.1", "rax_2": "2", "rax_1": "1"},
			"rax_1=1;rax_2=2;rex_1=0.1",
		},
		{
			"未知形状按键名排序兜底",
			map[string]any{"b": "2", "a": "1"},
			"a=1;b=2",
		},
		{
			"嵌套复合值递归格式化",
			map[string]any{"x": "1", "y": "2", "extjoint": map[string]any{"eax_b": "0", "eax_a": "0"}},
			"x=1;y=2;extjoint=eax_a=0;eax_b=0",
		},
	}

	for _, c := range cases {
		if got := compoundString(c.in); got != c.want {
			t.Errorf("%s：compoundString = %q, 期望 %q", c.name, got, c.want)
		}
	}

	// 顺序确定性：同一份输入重复格式化结果必须一致。
	m := map[string]any{"q4": "0", "x": "1", "z": "3", "y": "2"}
	first := compoundString(m)
	for i := 0; i < 20; i++ {
		if got := compoundString(m); got != first {
			t.Fatalf("第 %d 次格式化结果不同：%q vs %q", i, got, first)
		}
	}
}

func TestValueOfPerNamespace(t *testing.T) {
	cases := []struct {
		addr string
		st   map[string]any
		want string
		ok   bool
	}{
		{
			"signal:Local/DRV_1/DI1",
			map[string]any{"lvalue": "1", "quality": "1", "name": "DI1"},
			"1", true,
		},
		{
			// 部分固件把 lvalue 叫 value。
			"signal:Local/DRV_1/DI1",
			map[string]any{"value": "1"},
			"1", true,
		},
		{
			"signal:Local/DRV_1/DI1#quality",
			map[string]any{"lvalue": "1", "quality": "1"},
			"1", true,
		},
		{
			// 值存在但为空串 —— 仍然是"取到了"。
			"signal:Local/DRV_1/DI1",
			map[string]any{"lvalue": ""},
			"", true,
		},
		{
			"signal:Local/DRV_1/DI1",
			map[string]any{"name": "DI1"},
			"", false,
		},
		{
			"rapid:T_ROB1/Mod1/nTest",
			map[string]any{"name": "nTest", "type": "num", "value": "23"},
			"23", true,
		},
		{
			// 取分量：值是嵌套对象。
			"rapid:T_ROB1/Mod1/pHome#x",
			map[string]any{"value": map[string]any{"x": "1.5", "y": "2.0", "z": "0"}},
			"1.5", true,
		},
		{
			// 整取复合值：自描述文本。
			"rapid:T_ROB1/Mod1/pHome",
			map[string]any{"value": map[string]any{"x": "1.5", "y": "2.0"}},
			"x=1.5;y=2.0", true,
		},
		{
			"panel:ctrlstate",
			map[string]any{"ctrlstate": "motoron"},
			"motoron", true,
		},
		{
			"motion:ROB_1/joint/rax_1",
			map[string]any{"rax_1": "-0.000000", "rax_2": "0"},
			"-0.000000", true,
		},
		{
			"system:rwversion",
			map[string]any{"rwversion": "7.12.0"},
			"7.12.0", true,
		},
	}

	for _, c := range cases {
		a := mustParse(t, c.addr)
		got, ok := valueOf(c.st, a)
		if ok != c.ok || got != c.want {
			t.Errorf("valueOf(%q, %#v) = (%q, %v), 期望 (%q, %v)", c.addr, c.st, got, ok, c.want, c.ok)
		}
	}
}

func TestDeclaredTypeOf(t *testing.T) {
	a := mustParse(t, "signal:L/D/S")
	if got := declaredTypeOf(map[string]any{"type": "DI"}, a); got != "DI" {
		t.Errorf("declaredTypeOf = %q, 期望 DI", got)
	}
	if got := declaredTypeOf(map[string]any{"signaltype": "AI"}, a); got != "AI" {
		t.Errorf("应接受 signaltype 别名，实际 %q", got)
	}
	if got := declaredTypeOf(map[string]any{"lvalue": "1"}, a); got != "" {
		t.Errorf("没有类型字段时应返回空串，实际 %q", got)
	}

	// panel / motion / system 不声明类型（它们的语义是固定的）。
	p := mustParse(t, "panel:ctrlstate")
	if got := declaredTypeOf(map[string]any{"type": "DI"}, p); got != "" {
		t.Errorf("panel 不该取类型字段，实际 %q", got)
	}

	r := mustParse(t, "rapid:T/M/S")
	if got := declaredTypeOf(map[string]any{"value-type": "num"}, r); got != "num" {
		t.Errorf("rapid 的 value-type 别名未命中：%q", got)
	}
}

func TestIsSimulated(t *testing.T) {
	if !isSimulated(map[string]any{"lstate": "simulated"}) {
		t.Error("lstate=simulated 应被识别")
	}
	if !isSimulated(map[string]any{"l-state": "Simulated"}) {
		t.Error("别名与大小写都应被识别")
	}
	if isSimulated(map[string]any{"lstate": "not simulated"}) {
		t.Error("not simulated 不是仿真")
	}
	if isSimulated(map[string]any{"lvalue": "1"}) {
		t.Error("没有 lstate 字段时不应判为仿真")
	}
}

// TestStateIndexDropsAmbiguousKeys 歧义键必须被剔除。
//
// 同一个裸名（如 DI1）出现在两个设备上时，随便选一个的表现是
// "把 B 设备的值记到 A 点位上" —— 时序库里看不出任何异常，
// 而走一次单点 GET 只是多花一个请求。
func TestStateIndexDropsAmbiguousKeys(t *testing.T) {
	ix := newStateIndex()
	a := map[string]any{"lvalue": "a"}
	b := map[string]any{"lvalue": "b"}

	ix.add("DI1", a)
	if _, ok := ix.lookup("DI1"); !ok {
		t.Fatal("唯一的键应可查到")
	}
	ix.add("DI1", b) // 第二个条目写入同一个键 → 歧义
	if _, ok := ix.lookup("DI1"); ok {
		t.Error("歧义键不该可查到（宁可多一次单点 GET，也不能读错值）")
	}

	// 空键不参与索引。
	ix.add("", a)
	if _, ok := ix.lookup(""); ok {
		t.Error("空键不该可查到")
	}
}

// TestBuildSignalIndexKeys 列表里的三级后缀键都要建：
// 地址允许写成 name / unit/name / net/unit/name 三种粒度。
func TestBuildSignalIndexKeys(t *testing.T) {
	ix := buildSignalIndex([]map[string]any{
		{"name": "DI1", "unitnm": "DRV_1", "network": "Local", "lvalue": "1"},
		{"name": "DI2", "unitnm": "DRV_1", "network": "Local", "lvalue": "0"},
	})

	for _, key := range []string{"Local/DRV_1/DI1", "DRV_1/DI1", "DI1", "Local/DRV_1/DI2"} {
		if _, ok := ix.lookup(key); !ok {
			t.Errorf("键 %q 应可查到", key)
		}
	}
	// 两个信号 × 三级键，数量不多不少。
	if ix.len() != 6 {
		t.Errorf("索引条目数 = %d, 期望 6（两个信号 × 三级键）", ix.len())
	}

	// 网络段缺失时只建两级键，不应产生以 "/" 开头的怪键。
	ix2 := buildSignalIndex([]map[string]any{{"name": "DI1", "unitnm": "DRV_1"}})
	for _, key := range []string{"DRV_1/DI1", "DI1"} {
		if _, ok := ix2.lookup(key); !ok {
			t.Errorf("键 %q 应可查到", key)
		}
	}
	if _, ok := ix2.lookup("/DRV_1/DI1"); ok {
		t.Error("不该产生以斜杠开头的键")
	}

	// 没有 name 的条目直接跳过（建不出可用的键）。
	if ix3 := buildSignalIndex([]map[string]any{{"unitnm": "DRV_1"}}); ix3.len() != 0 {
		t.Errorf("缺 name 的条目应被跳过，实际索引里有 %d 条", ix3.len())
	}
}

// TestBuildSignalIndexKeysFromPath 具名字段缺失的固件（RobotWare 8.1.1 的信号
// 列表就不带 network / unitnm）也要建出三级键，路径从 _title 里取。
//
// 没有这条，索引里只剩裸名键：写全限定路径的点位永远命中不了批量列表，
// 每轮白烧一次单点 GET。
func TestBuildSignalIndexKeysFromPath(t *testing.T) {
	// 真实形态：RW 8.1.1 的列表条目，路径只在 _title 里。
	ix := buildSignalIndex([]map[string]any{
		{"_title": "IntBus/IoPanel/Auto", "name": "Auto", "type": "DO", "lvalue": "1"},
	})
	for _, key := range []string{"IntBus/IoPanel/Auto", "IoPanel/Auto", "Auto"} {
		if _, ok := ix.lookup(key); !ok {
			t.Errorf("键 %q 应可查到", key)
		}
	}
	if ix.len() != 3 {
		t.Errorf("索引条目数 = %d, 期望 3（三级键，不重复建）", ix.len())
	}

	// 无网络/设备前缀的信号（_title 就是裸名）只建一级键。
	ixBare := buildSignalIndex([]map[string]any{
		{"_title": "GOFA_EStop", "name": "GOFA_EStop", "type": "DI", "lvalue": "0"},
	})
	if ixBare.len() != 1 {
		t.Errorf("裸条目索引数 = %d, 期望 1", ixBare.len())
	}
	if _, ok := ixBare.lookup("GOFA_EStop"); !ok {
		t.Error("键 \"GOFA_EStop\" 应可查到")
	}

	// _title 缺失时回退到 _links.self.href（相对路径与完整 URL 都要吃下）。
	ixHref := buildSignalIndex([]map[string]any{
		{"name": "DI1", "_links": map[string]any{"self": map[string]any{
			"href": "https://127.0.0.1:80/rw/iosystem/signals/Local/DRV_1/DI1",
		}}},
	})
	for _, key := range []string{"Local/DRV_1/DI1", "DRV_1/DI1", "DI1"} {
		if _, ok := ixHref.lookup(key); !ok {
			t.Errorf("键 %q 应可查到", key)
		}
	}
}

// TestBuildSignalIndexIgnoresUnrelatedPath 路径末段与 name 对不上的条目
// 不参与建键：_title 在别的资源上是别的东西，采信它等于把值记到别的点位上。
func TestBuildSignalIndexIgnoresUnrelatedPath(t *testing.T) {
	ix := buildSignalIndex([]map[string]any{
		{"_title": "ctrlstate", "name": "DI1", "lvalue": "1"},
		{"_title": "Local/DRV_1/DI2/extra", "name": "DI2", "lvalue": "0"},
	})
	if ix.len() != 2 {
		t.Errorf("索引条目数 = %d, 期望 2（只建裸名键）", ix.len())
	}
	for _, key := range []string{"ctrlstate/DI1", "DI1", "Local/DRV_1/DI2/extra", "DI2"} {
		_, ok := ix.lookup(key)
		if want := key == "DI1" || key == "DI2"; ok != want {
			t.Errorf("键 %q 可查 = %v, 期望 %v", key, ok, want)
		}
	}
}

// TestBuildSignalIndexPathKeysStillAmbiguous 路径补齐出的键同样受歧义剔除约束：
// 两个设备下的同名信号，裸名键必须被剔除，全限定键各自保留。
func TestBuildSignalIndexPathKeysStillAmbiguous(t *testing.T) {
	ix := buildSignalIndex([]map[string]any{
		{"_title": "IntBus/IoPanel/Auto", "name": "Auto", "lvalue": "1"},
		{"_title": "IntBus/EPanel/Auto", "name": "Auto", "lvalue": "0"},
	})
	if _, ok := ix.lookup("Auto"); ok {
		t.Error("歧义裸名键不该可查到（宁可多一次单点 GET，也不能读错值）")
	}
	for _, key := range []string{"IntBus/IoPanel/Auto", "IntBus/EPanel/Auto"} {
		if _, ok := ix.lookup(key); !ok {
			t.Errorf("键 %q 应可查到", key)
		}
	}
}
