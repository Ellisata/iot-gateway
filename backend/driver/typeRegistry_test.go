// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver_test

import (
	"testing"

	"iot-gateway/driver"
)

// 本文件只测 TypeRegistry / ProtocolScope 的**解析机制**：前缀、大小写、
// 未命中回退。各协议注册了哪些类型、各自的 Kind 是什么，已随驱动搬进插件包，
// 由那里的类型表真源与 pluginhost 的 parity 黄金比对守着。

const (
	kindScope = "testscope_kinds"
	// shadowType 在两个地方各注册一份：scope 内一份、全局裸名一份（Kind/Size 不同），
	// 用来验证 ProtocolScope.Get 优先命中带前缀的那个。
	//
	// 刻意不用 "bool"/"int16" 这类常见名：全局裸名会污染同测试二进制里的其它用例
	// （任何 scope 查不到的类型都会回退到它），而真实协议从不注册全局裸名
	// （见 driver/s7/datatypes.go 的同名告诫）。用一个只属于本文件的生僻名，
	// 才能既测到回退语义又不制造现实里不存在的状态。
	shadowType     = "testonlyshadow"
	globalOnlyType = "testonlyglobal"
)

func init() {
	reg := driver.GetTypeRegistry()
	reg.ForProtocol(kindScope).Register(&driver.DataType{Name: "bool", Kind: driver.KindBool, Size: 1})
	reg.ForProtocol(kindScope).Register(&driver.DataType{Name: "int16", Kind: driver.KindInt, Size: 2})
	reg.ForProtocol(kindScope).Register(&driver.DataType{Name: "float32", Kind: driver.KindFloat, Size: 4})
	reg.ForProtocol(kindScope).Register(&driver.DataType{Name: shadowType, Kind: driver.KindInt, Size: 2})
	// 与 scope 内同名（含前缀后不同键）的全局裸名。
	reg.Register(&driver.DataType{Name: shadowType, Kind: driver.KindString, Size: 99})
	reg.Register(&driver.DataType{Name: globalOnlyType, Kind: driver.KindTime, Size: 1})
}

// TestProtocolScopeKindOf 验证裸名经 scope 前缀解析可命中，且大小写不敏感 ——
// 推送通道的类型化完全依赖这条映射（Kind 决定时序库的存储表示）。
func TestProtocolScopeKindOf(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"bool", driver.KindBool},
		{"int16", driver.KindInt},
		{"float32", driver.KindFloat},
		{"INT16", driver.KindInt}, // 大小写不敏感
		{"Int16", driver.KindInt}, // 混合大小写
		{"nope", ""},              // 未注册 → 空串，调用方回退为数值推断
		{"", ""},                  // 空名不 panic
	}
	scoped := driver.GetTypeRegistry().ForProtocol(kindScope)
	for _, c := range cases {
		if got := scoped.KindOf(c.name); got != c.want {
			t.Errorf("KindOf(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestProtocolScopePrefersScopedType 同名类型在 scope 内与全局都存在时，
// 必须命中 scope 内的那个。
//
// 这条不是细节：各协议都会注册自己的 "bool"（Kind/Size/解码方式都可能不同），
// 若回退到全局裸名，多个协议会共用同一份类型定义 —— 症状是「某个协议的值
// 按另一个协议的规则解码」，而两边作者都看不出是自己的问题。
func TestProtocolScopePrefersScopedType(t *testing.T) {
	scoped := driver.GetTypeRegistry().ForProtocol(kindScope)

	dt, ok := scoped.Get(shadowType)
	if !ok {
		t.Fatalf("Get(%q) 未命中", shadowType)
	}
	if dt.Kind != driver.KindInt || dt.Size != 2 {
		t.Errorf("Get(%q) = {Kind:%q Size:%d}, want {Kind:%q Size:2}（应命中 scope 内的定义，而非全局裸名）",
			shadowType, dt.Kind, dt.Size, driver.KindInt)
	}
}

// TestProtocolScopeFallsBackToGlobal 未登录在本 scope 的裸名会回退到全局查找。
//
// 这是 ProtocolScope.Get 的既有语义（见其注释），也是各驱动包"不注册全局裸名"
// 这条纪律存在的原因：一旦有协议往全局注册了裸名，所有协议的同名查询都会命中它。
// 钉住它，是为了让改动这条回退的行为必须先改这条测试。
func TestProtocolScopeFallsBackToGlobal(t *testing.T) {
	scoped := driver.GetTypeRegistry().ForProtocol("testscope_no_such_type")

	dt, ok := scoped.Get(globalOnlyType)
	if !ok {
		t.Fatalf("Get(%q) 应回退命中全局裸名", globalOnlyType)
	}
	if dt.Kind != driver.KindTime {
		t.Errorf("Get(%q).Kind = %q, want %q", globalOnlyType, dt.Kind, driver.KindTime)
	}
}

// TestProtocolScopeRegisterAddsPrefix 注册时自动加 "{scope}." 前缀 ——
// 否则不同协议的同名类型会互相覆盖（Register 是覆盖写）。
func TestProtocolScopeRegisterAddsPrefix(t *testing.T) {
	reg := driver.GetTypeRegistry()
	scoped := reg.ForProtocol(kindScope)

	dt, ok := reg.Get(kindScope + "." + "int16")
	if !ok {
		t.Fatalf("注册后应能以 %q 查到", kindScope+".int16")
	}
	if dt != scoped.MustGet("int16") {
		t.Error("带前缀查找与 scope 查找应命中同一条目")
	}
	if dt.Name != kindScope+".int16" {
		t.Errorf("注册后条目 Name = %q, want %q（应带前缀）", dt.Name, kindScope+".int16")
	}
}
