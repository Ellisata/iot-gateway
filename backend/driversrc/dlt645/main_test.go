// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"iot-gateway/driver"
	dlt645drv "iot-gateway/driversrc/dlt645/dlt645drv"
)

// TestMetaValid 插件清单必须能通过 SDK 自校验。
//
// 它挡住的是一整类"启动期才失败"的错误：类型表与类型映射对不上、协议没有
// 工厂、Kind 漏填 —— 这些在 CI 里失败远比等插件在网关里被拒绝加载便宜。
func TestMetaValid(t *testing.T) {
	m := meta()
	if err := m.Validate(); err != nil {
		t.Fatalf("Meta.Validate() = %v，插件会被网关拒绝加载", err)
	}
	if len(m.Types) == 0 {
		t.Fatal("Meta.Types 为空，网关的类型下拉与地址校验都会失去依据")
	}
	if len(m.TypeMap) == 0 {
		t.Fatal("Meta.TypeMap 为空")
	}
	// Validate 只看得到 json.RawMessage 非 nil，校验不了表单本身的合法性；
	// 而表单是从 main.go 手写出来的，一个多余逗号就能让前端渲染不出表单。
	for _, p := range m.Protocols {
		if !json.Valid(p.FormJSON) {
			t.Errorf("协议 %q 的 FormJSON 不是合法 JSON", p.Name)
		}
	}
}

// TestFactoriesMatchProtocols 工厂的键必须与协议名逐一对应。
//
// Serve 启动时也会查，但那已经是"插件被网关拉起"之后了；
// 在这里失败能直接定位到 main.go 漏配。
func TestFactoriesMatchProtocols(t *testing.T) {
	fs := factories()
	m := meta()

	for _, p := range m.Protocols {
		factory, ok := fs[p.Name]
		if !ok {
			t.Errorf("协议 %q 没有对应的工厂", p.Name)
			continue
		}
		if d := factory(); d == nil {
			t.Errorf("协议 %q 的工厂返回了 nil", p.Name)
		}
	}
	if len(fs) != len(m.Protocols) {
		t.Errorf("工厂数 %d != 协议数 %d", len(fs), len(m.Protocols))
	}
}

// TestProtocolIdentityPinned 协议名与 scope 必须逐字等于发布时的原值。
//
// 这是本文件里最重要的三个字面量：`device.protocol_id` → `iot_protocol.name`
// → `driver.Register` 是一条纯字符串的链，改任何一个字符都不会有编译期提示，
// 症状是**既有设备被判为「协议不支持」而静默跳过**（collector 只记一行日志），
// 而设备列表、前端下拉、健康检查全都正常 —— 现场可能几周后才察觉。
//
// 对 DL/T 645 还多一层含义：这三个值是从 iot-gateway 的内置驱动原样搬过来的，
// 改动它们会让两个项目之间**无法互相迁移配置**。字面量刻意写死、不从别处派生：
// 它要挡住的正是"有人顺手把名字改得更整齐"。
func TestProtocolIdentityPinned(t *testing.T) {
	cases := []struct{ got, want, what string }{
		{protocolSerial, "DLT645.Serial", "串口协议名"},
		{protocolTCP, "DLT645.TCP", "TCP 协议名"},
		{pluginScope, "dlt645", "类型 scope"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q，发布时的原值是 %q —— 改成别的会让既有设备静默采不到值",
				c.what, c.got, c.want)
		}
	}

	// 提交给网关的 Meta 必须用同一组值（而不是另写一份字面量）。
	m := meta()
	if m.Scope != pluginScope {
		t.Errorf("Meta.Scope = %q, 期望 %q", m.Scope, pluginScope)
	}
	want := map[string]bool{protocolSerial: true, protocolTCP: true}
	for _, p := range m.Protocols {
		if !want[p.Name] {
			t.Errorf("Meta 里出现未预期的协议名 %q", p.Name)
		}
		delete(want, p.Name)
	}
	for name := range want {
		t.Errorf("Meta 缺少协议 %q", name)
	}
}

// TestSerialExclusivePerProtocol 串行独占按协议声明，一个字节都不能错。
//
// 串口协议漏声明 true：同一 485 总线上的并发轮询会把帧交错在一起，
// 症状是「偶发读到别人的值」；TCP 误声明 true：同插件的全部设备白白丢掉并发，
// 慢频率分组会拖住快频率分组的实际采集周期。
func TestSerialExclusivePerProtocol(t *testing.T) {
	want := map[string]bool{
		protocolSerial: true,
		protocolTCP:    false,
	}
	seen := make(map[string]bool, len(want))

	for _, p := range meta().Protocols {
		w, ok := want[p.Name]
		if !ok {
			t.Errorf("出现未预期的协议 %q", p.Name)
			continue
		}
		if p.SerialExclusive != w {
			t.Errorf("协议 %q 的 SerialExclusive = %v, 期望 %v", p.Name, p.SerialExclusive, w)
		}
		if len(p.FormJSON) == 0 {
			t.Errorf("协议 %q 缺少配置表单（FormJSON）", p.Name)
		}
		seen[p.Name] = true
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("协议 %q 不在 Meta.Protocols 中", name)
		}
	}
}

// TestFormsMatchConfigStruct 表单里的 field 必须与驱动实际读取的配置键一一对应。
//
// 表单里的 `field` 就是 Connect 收到的 protocolJSON 的键；两边错开一个字母，
// 症状是"用户填了那个参数，但它根本不生效" —— 没有报错、没有日志，
// 表单看起来一切正常。这类漂移只能靠测试挡。
//
// 两份表单**分别**比对：串口参数（comPort 等）与以太网参数（host/port）没有交集，
// 用一份并集去比对，抄错的那一份就看不出来了。
func TestFormsMatchConfigStruct(t *testing.T) {
	forms := map[string]string{
		protocolSerial: formSerial,
		protocolTCP:    formTCP,
	}
	transports := map[string]string{
		protocolSerial: dlt645drv.TransportSerial,
		protocolTCP:    dlt645drv.TransportTCP,
	}

	for proto, raw := range forms {
		t.Run(proto, func(t *testing.T) {
			var form struct {
				Rule []struct {
					Field string `json:"field"`
					Title string `json:"title"`
				} `json:"rule"`
			}
			if err := json.Unmarshal([]byte(raw), &form); err != nil {
				t.Fatalf("解析表单失败: %v", err)
			}
			if len(form.Rule) == 0 {
				t.Fatal("表单没有任何字段")
			}

			formFields := make(map[string]bool, len(form.Rule))
			for _, r := range form.Rule {
				if r.Field == "" {
					t.Errorf("表单里有一项没有 field（title=%q）—— 它提交的键无法映射到配置", r.Title)
					continue
				}
				if formFields[r.Field] {
					t.Errorf("表单里的 field %q 重复", r.Field)
				}
				formFields[r.Field] = true
				if r.Title == "" {
					t.Errorf("表单字段 %q 缺少中文标题", r.Field)
				}
			}

			configFields := make(map[string]bool)
			for _, name := range dlt645drv.ConfigFieldNames(transports[proto]) {
				configFields[name] = true
			}

			if diff := setDiff(configFields, formFields); len(diff) > 0 {
				t.Errorf("配置字段 %v 没有出现在表单里 —— 用户无法配置它们", diff)
			}
			if diff := setDiff(formFields, configFields); len(diff) > 0 {
				t.Errorf("表单里出现了配置结构没有的字段 %v —— 用户填了也不生效", diff)
			}
		})
	}
}

func setDiff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// TestFormDefaultsArePresent 表单每一项都必须带默认值。
//
// 本驱动的配置解析靠「字段缺席 → 用驱动默认值」兜底，而表单一旦把某个字段
// 渲染成空，用户看到的就是「不填会怎样」无从知晓 —— 更糟的是有几个默认值
// 反直觉（parity 默认 E 而非仓库通用的 N、baudRate 默认 2400、
// 串口 preambleBytes 默认 4 而 TCP 默认 0）。把驱动实际会用的值填进表单，
// 提交与不提交的行为才一致。
//
// 无默认值的唯一例外是 host：串口服务器的地址必须由现场填，
// 预填一个 127.0.0.1 只会让人原样提交然后对着超时发呆。
func TestFormDefaultsArePresent(t *testing.T) {
	exempt := map[string]bool{"host": true}

	for _, raw := range []string{formSerial, formTCP} {
		var form struct {
			Rule []struct {
				Field string          `json:"field"`
				Value json.RawMessage `json:"value"`
			} `json:"rule"`
		}
		if err := json.Unmarshal([]byte(raw), &form); err != nil {
			t.Fatalf("解析表单失败: %v", err)
		}
		for _, r := range form.Rule {
			if exempt[r.Field] {
				if len(r.Value) != 0 {
					t.Errorf("字段 %q 应留空由现场填写，却带了默认值 %s", r.Field, r.Value)
				}
				continue
			}
			if len(r.Value) == 0 {
				t.Errorf("表单字段 %q 没有默认值（value）", r.Field)
			}
		}
	}
}

// TestTypesCoverAllMappedNames 类型表必须覆盖类型映射能到达的每一个内部名。
//
// 网关把 TypeMap 的 value（内部裸名）写进 device_address.data_type，采集时
// 原样回传给驱动的 Read；插件进程里 driversdk.Serve 只会把 Meta.Types 里的项
// 注册进本进程的类型表。所以"TypeMap 里出现了 Types 里没有的类型名"就是
// "该点位取不到 Kind"，而且现场不会有任何编译期提示。
func TestTypesCoverAllMappedNames(t *testing.T) {
	types := dlt645drv.Types()
	have := make(map[string]bool, len(types))
	for _, dt := range types {
		have[dt.Name] = true
	}

	for name, internal := range dlt645drv.CommonTypeMap() {
		if !have[internal] {
			t.Errorf("通用类型 %q → 内部类型 %q 不在 Types() 里，该点位取不到 Kind", name, internal)
		}
	}

	// 反向也成立：**没有通用名入口**的类型照样要上报，否则网关的
	// NormalizeAddressType 会把历史点位判为"类型漂移"并静默回写库。
	want := []string{"bcd", "bool", "datetime", "float", "int", "lbcd", "string", "uint"}
	got := make([]string, 0, len(types))
	for _, dt := range types {
		got = append(got, dt.Name)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Types() 上报的类型 = %v, 期望 %v", got, want)
	}
}

// TestAllCommonTypesMapped 13 个通用类型必须全部有映射。
//
// 少映射一个，前端类型下拉就缺一项，而 NormalizeAddressType 会退化成
// "找不到就保留原值"，把本该被纠正的漂移放过去。
func TestAllCommonTypesMapped(t *testing.T) {
	tm := dlt645drv.CommonTypeMap()
	for _, name := range driver.CommonDataTypes {
		internal, ok := tm[name]
		if !ok || internal == "" {
			t.Errorf("通用类型 %q 没有映射到内部类型（前端下拉会缺这一项）", name)
		}
	}

	// 与内置 DL/T 645 的 dlt645Types 逐项一致：两边类型语义不同会让同一份
	// 现场配置在两个入口下采出不同的值。
	want := map[string]string{
		"Boolean": "bool", "Date": "datetime", "String": "string",
		"Byte": "uint", "Char": "int", "Short": "int", "Word": "uint",
		"DWord": "uint", "Long": "int", "Float": "float", "Double": "float",
		"BCD": "bcd", "LBCD": "lbcd",
	}
	for name, internal := range want {
		if got := tm[name]; got != internal {
			t.Errorf("类型映射 %s = %q, want %q（必须与内置实现一致）", name, got, internal)
		}
	}
	if len(tm) != len(want) {
		t.Errorf("类型映射项数 %d != %d（本协议应无扩展类型）", len(tm), len(want))
	}
}

// TestTypesUseBareNames Types() 每项的 Name 都必须是裸名。
//
// 注册表里存的是带前缀的 "dlt645.float"，插件必须上报裸名 "float"：
// 网关会给上报的类型名再拼一次 scope 前缀，否则得到 "dlt645.dlt645.float"，
// 地址校验与类型下拉双双落空。
func TestTypesUseBareNames(t *testing.T) {
	for _, dt := range dlt645drv.Types() {
		if dt == nil {
			t.Error("Types() 中出现 nil 项")
			continue
		}
		if strings.Contains(dt.Name, ".") {
			t.Errorf("类型 %q 的 Name 带了 scope 前缀，网关会再拼一次得到无效名", dt.Name)
		}
	}
}

// TestTypeKindsPreserved 上报的每一项都必须带上 Kind。
//
// driversdk.Serve 会把 Types 里的指针**重新注册**进插件进程的注册表，
// 于是"只填 Name 的瘦身构造"会把 Kind 清成空字符串。填错 Kind（例如把
// datetime 当 string）不会报错，只会让推送通道按错误的表示存值，比 panic 更难发现。
func TestTypeKindsPreserved(t *testing.T) {
	want := map[string]string{
		"bool":     driver.KindBool,
		"int":      driver.KindInt,
		"uint":     driver.KindUInt,
		"float":    driver.KindFloat,
		"bcd":      driver.KindUInt,
		"lbcd":     driver.KindUInt,
		"string":   driver.KindString,
		"datetime": driver.KindTime,
	}

	got := make(map[string]string, len(want))
	for _, dt := range dlt645drv.Types() {
		got[dt.Name] = dt.Kind
	}
	if len(got) != len(want) {
		t.Errorf("上报类型数 %d != 期望的 %d", len(got), len(want))
	}
	for name, kind := range want {
		if k, ok := got[name]; !ok {
			t.Errorf("类型 %q 未被上报", name)
		} else if k != kind {
			t.Errorf("类型 %q 的 Kind = %q, 期望 %q（Kind 错了会让推送通道按错误类型存值）",
				name, k, kind)
		}
	}
}

// TestPointFindersMatchPointDiscovery 声明与实现必须一一对应。
//
// 两个方向都要断：声明了却没有 finder ⇒ 插件**启动即失败**（driversdk 的同一条
// 校验，"每个协议都必须有工厂"是它的兄弟）；有 finder 却没声明 ⇒ 那个实现永远
// 不会被调用，谁按它写测试都是白写。与 mitsubishi / fins 的同名测试同构。
func TestPointFindersMatchPointDiscovery(t *testing.T) {
	fs := finders()

	declared := make(map[string]bool)
	for _, p := range meta().Protocols {
		if p.PointDiscovery == nil {
			continue
		}
		declared[p.Name] = true
		if _, ok := fs[p.Name]; !ok {
			t.Errorf("协议 %q 声明了 PointDiscovery 却没有对应的 finder"+
				"（driversdk.Serve 会直接拒绝启动）", p.Name)
		}
	}
	for name := range fs {
		if !declared[name] {
			t.Errorf("协议 %q 有 finder 但没声明 PointDiscovery，它永远不会被调用", name)
		}
	}
	if len(declared) == 0 {
		t.Fatal("一个协议都没声明 PointDiscovery —— 是删漏了还是这个测试空转了？")
	}
}

// TestPointDiscoverySpecFields 声明里几个决定前端行为的取值。
//
// DefaultCommonDataType 必须是 TypeMap 的 **key**（通用类型名）：写成内部裸名
// （"float"）会被 driversdk.Meta.Validate 拒掉，而写成 TypeMap 里没有的名字则是
// "下拉框默认选中项是空的" —— 用户得自己挑一次，不报任何错。
func TestPointDiscoverySpecFields(t *testing.T) {
	m := meta()
	if err := m.Validate(); err != nil {
		t.Fatalf("meta().Validate(): %v", err)
	}

	for _, p := range m.Protocols {
		d := p.PointDiscovery
		if d == nil {
			t.Fatalf("协议 %q 没有声明 PointDiscovery", p.Name)
		}
		if _, ok := m.TypeMap[d.DefaultCommonDataType]; !ok {
			t.Errorf("协议 %q 的 DefaultCommonDataType = %q 不是 TypeMap 的键（应为通用类型名）",
				p.Name, d.DefaultCommonDataType)
		}
		if d.Title == "" || d.Description == "" {
			t.Errorf("协议 %q 的 PointDiscovery 缺 Title 或 Description"+
				"（后者是用户理解本功能性质的主要来源）", p.Name)
		}
		if !json.Valid(d.FormJSON) {
			t.Errorf("协议 %q 的 PointDiscovery.FormJSON 不是合法 JSON", p.Name)
		}
	}

	// 串口与 TCP 的声明必须**逐字段相同**：扫描走的地址解析、字典与解码都与
	// 传输层无关，两边的表单、预算、上限因此没有理由分叉（分叉了就会出现
	// "同一台表换个接法扫出来的东西不一样"，而两边都不报错）。
	// 比**值**不比指针：PointDiscovery() 每次返回一份新的，共享同一个指针会让
	// 一方被改动时另一方跟着变，而声明本该是只读的。
	ps := meta().Protocols
	a, b := ps[0].PointDiscovery, ps[1].PointDiscovery
	if !reflect.DeepEqual(a, b) {
		t.Errorf("两个协议的点位扫描声明不一致：串口 %+v，TCP %+v", a, b)
	}
	if a == b {
		t.Error("两个协议共享了同一个声明指针 —— 声明是只读的，各持一份更安全")
	}
}

// TestNoDeviceScan **两个协议都不得声明 Discovery（局域网设备扫描）**。
//
// 这是有意的留白，不是漏配，理由写在 meta() 的那段注释里：
//   - 串口按契约不得声明（目标模型是 host/port，表达不了 comPort），
//     driversdk.Meta.Validate 会拒 —— 所以这里只是把另一条不变式一起钉住。
//   - TCP 连的是串口服务器 / DTU，与 Omron.FINS.HostLinkTCP 同形，FINS 也刻意
//     不声明。DL/T 645-2007 确有广播「读通信地址」(13H → 93H) 能枚举总线上的表，
//     但那是"总线上有哪些表"，不是"网段里有哪些主机"——是另一个功能，且 1997 版
//     的广播语义无依据、无真机可验证。
//
// 钉住它的意义：能力一旦声明出去前端就会出现扫描按钮，而按下去的结果没人验证过。
// 与 omroncip 的 TestNoPointDiscovery 是同一条"把有意的不做变成会红的测试"。
func TestNoDeviceScan(t *testing.T) {
	for _, p := range meta().Protocols {
		if p.Discovery != nil {
			t.Errorf("协议 %q 声明了 Discovery —— 本插件有意不做设备扫描，"+
				"要做请先补上 prober 并说明为什么值得（见 meta() 的注释）", p.Name)
		}
	}
}
