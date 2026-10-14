// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver_test

import (
	"slices"
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// 本文件只测 dataTypeMap 的**机制**：查表规则、展示顺序、回退语义。
//
// 内置协议的**内容**（哪个协议映射哪些类型）已随驱动一起搬进各插件包，
// 由那里的类型表真源 + pluginhost 的 parity 黄金比对守着。在这里断言
// "modbus 有 13 个通用类型" 之类的具体内容，等于在网关侧留一份会漂移的抄件。

// registerOrderScope 注册一个类型映射刻意乱序的 scope：
// 通用类型与扩展类型交错、并混入一个"注册了但没映射"的内部类型。
func registerOrderScope(t *testing.T) (scope, protocol string) {
	t.Helper()
	scope, protocol = "testscope_order", "Test.Order.Protocol"

	reg := driver.GetTypeRegistry().ForProtocol(scope)
	for _, dt := range []*driver.DataType{
		{Name: "bool", Kind: driver.KindBool, Size: 1},
		{Name: "string", Kind: driver.KindString, Size: 0},
		{Name: "int16", Kind: driver.KindInt, Size: 2},
		{Name: "int32", Kind: driver.KindInt, Size: 4},
		{Name: "float32", Kind: driver.KindFloat, Size: 4},
		{Name: "float64", Kind: driver.KindFloat, Size: 8},
		// orphan 未被任何通用名映射：不得出现在 AvailableTypes 里，
		// 但必须能在注册表查到（地址自愈依赖它，见 TestNormalizeAddressType）。
		{Name: "orphan", Kind: driver.KindInt, Size: 2},
	} {
		reg.Register(dt)
	}
	if err := driver.RegisterScope(scope, []string{protocol}, map[string]string{
		"Double":  "float64",
		"Boolean": "bool",
		"String":  "string",
		"Float":   "float32",
		"Real":    "float32",
		"Short":   "int16",
		"Int":     "int32",
	}); err != nil {
		t.Fatalf("RegisterScope: %v", err)
	}
	t.Cleanup(func() { driver.UnregisterScope(scope) })
	return scope, protocol
}

// TestAvailableTypesOrdering 验证展示顺序：通用类型按 CommonDataTypes 的
// 展示序，扩展类型（不在通用列表里的协议专属名）按字母序。
//
// 顺序是配置界面下拉的呈现顺序，改动它没有编译期提示 —— 现场只会看到
// 类型下拉"顺序变了"，而没人会为此报 bug。
func TestAvailableTypesOrdering(t *testing.T) {
	scope, _ := registerOrderScope(t)

	common, extended := driver.AvailableTypes(scope)

	// 通用序取自 CommonDataTypes：Boolean, Date, String, Byte, Char, Short, ...
	wantCommon := []string{driver.TypeBoolean, driver.TypeString, driver.TypeShort, driver.TypeFloat, driver.TypeDouble}
	if !slices.Equal(common, wantCommon) {
		t.Errorf("common = %v, want %v（应按 CommonDataTypes 展示序）", common, wantCommon)
	}
	// 扩展按字母序；orphan 未映射，不得出现。
	wantExtended := []string{"Int", "Real"}
	if !slices.Equal(extended, wantExtended) {
		t.Errorf("extended = %v, want %v（按字母序，且未映射的类型不展示）", extended, wantExtended)
	}
}

// TestAvailableTypesUnknownScope 未知 scope 返回 nil、nil（而非 panic）。
func TestAvailableTypesUnknownScope(t *testing.T) {
	common, extended := driver.AvailableTypes("testscope_never_registered")
	if common != nil || extended != nil {
		t.Errorf("AvailableTypes(未知 scope) = %v,%v, want nil,nil", common, extended)
	}
}

// TestScopeOfUnknownProtocol 未知/空协议名一律返回空串。
// service 与 collector 都靠"空串 = 协议不可用"来判断，返回任何非空值都会
// 让后续查表落到错误的 scope 上。
func TestScopeOfUnknownProtocol(t *testing.T) {
	scope, protocol := registerOrderScope(t)

	if got := driver.ScopeOf(protocol); got != scope {
		t.Errorf("ScopeOf(%q) = %q, want %q", protocol, got, scope)
	}
	if got := driver.ScopeOf("Test.Order.Protocol.Unknown"); got != "" {
		t.Errorf("ScopeOf(未注册) = %q, want 空", got)
	}
	if got := driver.ScopeOf(""); got != "" {
		t.Errorf("ScopeOf(\"\") = %q, want 空", got)
	}
	if got := driver.ScopeOf("  "); got != "" {
		t.Errorf("ScopeOf(空白) = %q, want 空", got)
	}
}

// TestLookupDataTypeUnknownProtocol 未知协议的 LookupDataType 一律 false，
// 不会退化成"查全局注册表碰运气"。
func TestLookupDataTypeUnknownProtocol(t *testing.T) {
	_, protocol := registerOrderScope(t)

	if got, ok := driver.LookupDataType(protocol, "Short"); !ok || got != "int16" {
		t.Errorf("LookupDataType(%q, Short) = %q,%v; want int16,true", protocol, got, ok)
	}
	if got, ok := driver.LookupDataType("Test.Order.Protocol.Unknown", "Short"); ok || got != "" {
		t.Errorf("LookupDataType(未注册协议) = %q,%v; want \"\",false", got, ok)
	}
}

// TestNormalizeAddressType 验证读取路径的类型回退语义（保守，只修坏的）。
//
// 这是"旧库里 data_type 漂移的点位还能不能采到值"的唯一防线：
// 内部名可解析就原样保留，不可解析才用 CommonDataType 重解析，
// 两者都失败则原样返回（让驱动照常报 unsupported data type，而不是静默换成别的类型）。
func TestNormalizeAddressType(t *testing.T) {
	_, protocol := registerOrderScope(t)

	cases := []struct {
		name           string
		protocol       string
		dataType       string
		commonDataType string
		want           string
	}{
		{"有效内部名保留", protocol, "int16", "Short", "int16"},
		{"漂移回退", protocol, "bogus", "Float", "float32"},
		{"扩展类型漂移回退", protocol, "bogus", "Int", "int32"},
		{"未映射但已注册的内部名保留", protocol, "orphan", "", "orphan"},
		{"通用名大小写不敏感", protocol, "bogus", "short", "int16"},
		{"协议未知原样", "Test.Order.Protocol.Unknown", "bogus", "Float", "bogus"},
		{"两者都坏原样", protocol, "bogus", "", "bogus"},
		{"通用名也不认识原样", protocol, "bogus", "Nope", "bogus"},
		{"两者都空原样", protocol, "", "", ""},
	}
	for _, c := range cases {
		addr := po.DeviceAddress{DataType: c.dataType, CommonDataType: c.commonDataType}
		if got := driver.NormalizeAddressType(c.protocol, addr); got != c.want {
			t.Errorf("%s: NormalizeAddressType(%q, {DataType:%q, CommonDataType:%q}) = %q, want %q",
				c.name, c.protocol, c.dataType, c.commonDataType, got, c.want)
		}
	}
}
