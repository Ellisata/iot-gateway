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
	abbdrv "iot-gateway/driversrc/abb/abbdrv"
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
// 这是本文件里最重要的两个字面量：`device.protocol_id` → `iot_protocol.name`
// → `driver.Register` 是一条纯字符串的链，改任何一个字符都不会有编译期提示，
// 症状是**既有设备被判为「协议不支持」而静默跳过**（collector 只记一行日志），
// 而设备列表、前端下拉、健康检查全都正常 —— 现场可能几周后才察觉。
//
// 字面量刻意写死、不从别处派生：它要挡住的正是"有人顺手把名字改得更整齐"。
func TestProtocolIdentityPinned(t *testing.T) {
	cases := []struct{ got, want, what string }{
		{protocolName, "ABB.RWS", "协议名"},
		{pluginScope, "abb", "类型 scope"},
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
	for _, p := range m.Protocols {
		if p.Name != protocolName {
			t.Errorf("Meta 里的协议名 = %q, 期望 %q", p.Name, protocolName)
		}
	}
}

// TestSerialExclusivePerProtocol 串行独占按协议声明。
//
// RWS 是 HTTP，不存在"总线上的帧被交错"这回事；声明成 true 不会报错，
// 只会让同一插件的所有设备白白丢掉并发。
func TestSerialExclusivePerProtocol(t *testing.T) {
	want := map[string]bool{protocolName: false}
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

// TestFormFieldsMatchConfigStruct 表单里的 field 必须与配置结构的 json tag
// 一一对应。
//
// 表单里的 `field` 就是 Connect 收到的 protocolJSON 的键；两边错开一个字母，
// 症状是"用户填了那个参数，但它根本不生效" —— 没有报错、没有日志，
// 表单看起来一切正常。这类漂移只能靠测试挡。
func TestFormFieldsMatchConfigStruct(t *testing.T) {
	var form struct {
		Rule []struct {
			Field string `json:"field"`
			Title string `json:"title"`
		} `json:"rule"`
	}
	if err := json.Unmarshal([]byte(formRWS), &form); err != nil {
		t.Fatalf("解析表单失败: %v", err)
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
	for _, name := range abbdrv.ConfigFieldNames() {
		configFields[name] = true
	}

	if diff := setDiff(configFields, formFields); len(diff) > 0 {
		t.Errorf("配置字段 %v 没有出现在表单里 —— 用户无法配置它们", diff)
	}
	if diff := setDiff(formFields, configFields); len(diff) > 0 {
		t.Errorf("表单里出现了配置结构没有的字段 %v —— 用户填了也不生效", diff)
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

// TestTypesCoverAllMappedNames 类型表必须覆盖类型映射能到达的每一个内部名。
//
// 网关把 TypeMap 的 value（内部裸名）写进 device_address.data_type，采集时
// 原样回传给驱动的 Read；插件进程里 driversdk.Serve 只会把 Meta.Types 里的项
// 注册进本进程的类型表。所以"TypeMap 里出现了 Types 里没有的类型名"就是
// "该点位取不到 Kind"，而且现场不会有任何编译期提示。
func TestTypesCoverAllMappedNames(t *testing.T) {
	types := abbdrv.Types()
	have := make(map[string]bool, len(types))
	for _, dt := range types {
		have[dt.Name] = true
	}

	for name, internal := range abbdrv.CommonTypeMap() {
		if !have[internal] {
			t.Errorf("通用类型 %q → 内部类型 %q 不在 Types() 里，该点位取不到 Kind", name, internal)
		}
	}

	// 反向也成立：**没有通用名入口**的类型照样要上报，否则网关的
	// NormalizeAddressType 会把历史点位判为"类型漂移"并静默回写库。
	want := []string{"auto", "bool", "float", "int", "string", "time", "uint"}
	got := make([]string, 0, len(types))
	for _, dt := range types {
		got = append(got, dt.Name)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Types() 上报的类型 = %v, 期望 %v", got, want)
	}
}

// TestTypesUseBareNames Types() 每项的 Name 都必须是裸名。
//
// 注册表里存的是带前缀的 "abb.int"，插件必须上报裸名 "int"：
// 网关会给上报的类型名再拼一次 scope 前缀，否则得到 "abb.abb.int"，
// 地址校验与类型下拉双双落空。
func TestTypesUseBareNames(t *testing.T) {
	for _, dt := range abbdrv.Types() {
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
// time 当 string）不会报错，只会让推送通道按错误的表示存值，比 panic 更难发现。
func TestTypeKindsPreserved(t *testing.T) {
	want := map[string]string{
		"auto":   driver.KindString,
		"bool":   driver.KindBool,
		"int":    driver.KindInt,
		"uint":   driver.KindUInt,
		"float":  driver.KindFloat,
		"string": driver.KindString,
		"time":   driver.KindTime,
	}

	got := make(map[string]string, len(want))
	for _, dt := range abbdrv.Types() {
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

// TestFormDefaultsArePresent 表单每一项都必须带默认值。
//
// 这不是装饰：**密码字段缺失**与**显式空密码**在驱动侧是两种语义
// （见 abbdrv/config.go 的 Password 指针），表单不预填的话，
// 用户原样提交就会把出厂默认密码 robotics 清掉。
func TestFormDefaultsArePresent(t *testing.T) {
	var form struct {
		Rule []struct {
			Field string          `json:"field"`
			Value json.RawMessage `json:"value"`
		} `json:"rule"`
	}
	if err := json.Unmarshal([]byte(formRWS), &form); err != nil {
		t.Fatalf("解析表单失败: %v", err)
	}
	for _, r := range form.Rule {
		if len(r.Value) == 0 {
			t.Errorf("表单字段 %q 没有默认值（value）", r.Field)
		}
	}
}
