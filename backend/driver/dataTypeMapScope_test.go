// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver_test

import (
	"strings"
	"sync"
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// registerTestScope 注册一个插件 scope，注册内部类型，并返回清理函数。
// scope 名按测试唯一化，避免 TypeRegistry 无注销接口导致的跨测试串扰。
func registerTestScope(t *testing.T, scope, protocol string, types []*driver.DataType, typeMap map[string]string) {
	t.Helper()
	reg := driver.GetTypeRegistry().ForProtocol(scope)
	for _, dt := range types {
		reg.Register(dt)
	}
	if err := driver.RegisterScope(scope, []string{protocol}, typeMap); err != nil {
		t.Fatalf("RegisterScope(%q) 失败: %v", scope, err)
	}
	t.Cleanup(func() { driver.UnregisterScope(scope) })
}

func TestRegisterScopeMakesAllLookupsWork(t *testing.T) {
	const (
		scope    = "testscope_basic"
		protocol = "Test.Plugin.Basic"
	)
	registerTestScope(t, scope, protocol,
		[]*driver.DataType{
			{Name: "bool", Kind: driver.KindBool, Size: 1},
			{Name: "int16", Kind: driver.KindInt, Size: 2},
		},
		map[string]string{"Boolean": "bool", "Short": "int16"},
	)

	if got := driver.ScopeOf(protocol); got != scope {
		t.Errorf("ScopeOf(%q) = %q, want %q", protocol, got, scope)
	}
	// 协议名不区分大小写。
	if got := driver.ScopeOf(strings.ToUpper(protocol)); got != scope {
		t.Errorf("ScopeOf(大写) = %q, want %q", got, scope)
	}
	// 类型名不区分大小写。
	if got, ok := driver.LookupScopeType(scope, "boolean"); !ok || got != "bool" {
		t.Errorf("LookupScopeType(%q, boolean) = %q,%v; want bool,true", scope, got, ok)
	}
	if got, ok := driver.LookupDataType(protocol, "Short"); !ok || got != "int16" {
		t.Errorf("LookupDataType(%q, Short) = %q,%v; want int16,true", protocol, got, ok)
	}

	common, extended := driver.AvailableTypes(scope)
	if len(common) != 2 || common[0] != driver.TypeBoolean || common[1] != driver.TypeShort {
		t.Errorf("AvailableTypes common = %v, want [Boolean Short]", common)
	}
	if len(extended) != 0 {
		t.Errorf("AvailableTypes extended = %v, want 空", extended)
	}

	// NormalizeAddressType：内部名可解析 → 原样返回。
	addr := po.DeviceAddress{DataType: "int16", CommonDataType: "Short"}
	if got := driver.NormalizeAddressType(protocol, addr); got != "int16" {
		t.Errorf("NormalizeAddressType 保留有效内部名 = %q, want int16", got)
	}
	// 内部名漂移 → 用 CommonDataType 修复。
	addr = po.DeviceAddress{DataType: "stale_type", CommonDataType: "Short"}
	if got := driver.NormalizeAddressType(protocol, addr); got != "int16" {
		t.Errorf("NormalizeAddressType 修复漂移 = %q, want int16", got)
	}
}

// TestAvailableTypesGatesOnTypeRegistry 固化既有语义：映射表里有、但 TypeRegistry
// 未注册的类型不展示（避免"映射表有、注册表无"的漂移进入配置界面）。
func TestAvailableTypesGatesOnTypeRegistry(t *testing.T) {
	const (
		scope    = "testscope_gate"
		protocol = "Test.Plugin.Gate"
	)
	// 故意只注册 bool，int16 不注册。
	registerTestScope(t, scope, protocol,
		[]*driver.DataType{{Name: "bool", Kind: driver.KindBool, Size: 1}},
		map[string]string{"Boolean": "bool", "Short": "int16"},
	)

	common, extended := driver.AvailableTypes(scope)
	if len(common) != 1 || common[0] != driver.TypeBoolean {
		t.Errorf("common = %v, want [Boolean]（Short 因未注册应被隐藏）", common)
	}
	if len(extended) != 0 {
		t.Errorf("extended = %v, want 空", extended)
	}

	// 但类型映射本身仍可查询（门禁只在展示层）。
	if got, ok := driver.LookupDataType(protocol, "Short"); !ok || got != "int16" {
		t.Errorf("LookupDataType Short = %q,%v; want int16,true", got, ok)
	}
}

// TestRegisterScopeRejections 覆盖 RegisterScope 的全部入参校验。
//
// 与**已注册** scope / 协议名的冲突不在这里测：那是运行期状态而非入参校验，
// 由 TestRegisterScopeDuplicateScopeRejected、
// TestRegisterScopeProtocolClaimedByAnotherScope 与
// TestRegisterScopeCaseVariantOfRegisteredScopeRejected 分别覆盖。
func TestRegisterScopeRejections(t *testing.T) {
	const scope = "testscope_reject"
	reg := driver.GetTypeRegistry().ForProtocol(scope)
	reg.Register(&driver.DataType{Name: "bool", Kind: driver.KindBool, Size: 1})

	cases := []struct {
		name      string
		scope     string
		protocols []string
		types     map[string]string
		wantSub   string
	}{
		{"空 scope", "", []string{"Test.Plugin.X"}, map[string]string{"Boolean": "bool"}, "scope must not be empty"},
		{"空白 scope", "   ", []string{"Test.Plugin.X"}, map[string]string{"Boolean": "bool"}, "scope must not be empty"},
		{"无协议名", scope, nil, map[string]string{"Boolean": "bool"}, "at least one protocol"},
		{"协议名为空串", scope, []string{""}, map[string]string{"Boolean": "bool"}, "protocol name must not be empty"},
		{"协议名为空白", scope, []string{"  "}, map[string]string{"Boolean": "bool"}, "protocol name must not be empty"},
		{"空类型表", scope, []string{"Test.Plugin.X"}, map[string]string{}, "must not be empty"},
		{"类型名为空", scope, []string{"Test.Plugin.X"}, map[string]string{"": "bool"}, "type name must not be empty"},
		{"内部名为空", scope, []string{"Test.Plugin.X"}, map[string]string{"Boolean": ""}, "empty internal name"},
		{"类型名仅大小写不同", scope, []string{"Test.Plugin.X"}, map[string]string{"Float": "float32", "float": "float64"}, "differ only by case"},
		{"协议名仅大小写不同", scope, []string{"Test.Plugin.X", "test.plugin.x"}, map[string]string{"Boolean": "bool"}, "differ only by case"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := driver.RegisterScope(c.scope, c.protocols, c.types)
			if err == nil {
				t.Fatalf("应被拒绝，但注册成功")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("错误信息 %q 未包含 %q", err.Error(), c.wantSub)
			}
		})
	}
}

// TestRegisterScopeCaseVariantOfRegisteredScopeRejected 两个插件若分别声明
// "Acme" 与 "acme"，它们的类型前缀同为 "acme."：必须只允许先到的那个注册，
// 否则后到的会把前者的类型条目整批覆盖掉（TypeRegistry.Register 是覆盖写），
// 前一个插件的点位会解析失败 —— 两个插件的作者都看不出是自己的问题。
func TestRegisterScopeCaseVariantOfRegisteredScopeRejected(t *testing.T) {
	const protocol = "Test.Plugin.CaseVariant"
	registerTestScope(t, "testscope_casevariant", protocol,
		[]*driver.DataType{{Name: "bool", Kind: driver.KindBool, Size: 1}},
		map[string]string{"Boolean": "bool"},
	)

	for _, scope := range []string{"TESTSCOPE_CASEVARIANT", "TestScope_CaseVariant"} {
		err := driver.RegisterScope(scope, []string{"Test.Plugin.CaseVariant.2"}, map[string]string{"Boolean": "bool"})
		if err == nil {
			t.Fatalf("scope %q 与已注册的 scope 仅大小写不同，必须被拒绝", scope)
		}
		if !strings.Contains(err.Error(), "already registered") {
			t.Errorf("scope %q 的错误信息 %q 未指明已注册", scope, err.Error())
		}
	}
}

// TestRegisterScopeIsAtomic 校验失败时不得留下部分生效的状态。
func TestRegisterScopeIsAtomic(t *testing.T) {
	const (
		scope    = "testscope_atomic"
		protocol = "Test.Plugin.Atomic"
	)
	reg := driver.GetTypeRegistry().ForProtocol(scope)
	reg.Register(&driver.DataType{Name: "bool", Kind: driver.KindBool, Size: 1})

	// 先让另一个 scope 认领一个协议名，再让本次注册"第一个协议名合法、
	// 第二个已被认领"——整次注册必须失败，且第一个协议名不得残留。
	const taken = "Test.Plugin.AtomicTaken"
	registerTestScope(t, "testscope_atomic_holder", taken,
		[]*driver.DataType{{Name: "bool", Kind: driver.KindBool, Size: 1}},
		map[string]string{"Boolean": "bool"},
	)

	err := driver.RegisterScope(scope, []string{protocol, taken}, map[string]string{"Boolean": "bool"})
	if err == nil {
		t.Fatal("含已被认领的协议名时应整体失败")
	}
	if got := driver.ScopeOf(protocol); got != "" {
		t.Errorf("失败后 ScopeOf(%q) = %q, 应为空（无部分生效）", protocol, got)
	}
	if _, ok := driver.LookupScopeType(scope, "Boolean"); ok {
		t.Error("失败后 scope 不应可查（无部分生效）")
	}
}

func TestRegisterScopeDuplicateScopeRejected(t *testing.T) {
	const scope = "testscope_dup"
	reg := driver.GetTypeRegistry().ForProtocol(scope)
	reg.Register(&driver.DataType{Name: "bool", Kind: driver.KindBool, Size: 1})

	if err := driver.RegisterScope(scope, []string{"Test.Plugin.Dup1"}, map[string]string{"Boolean": "bool"}); err != nil {
		t.Fatalf("首次注册失败: %v", err)
	}
	t.Cleanup(func() { driver.UnregisterScope(scope) })

	if err := driver.RegisterScope(scope, []string{"Test.Plugin.Dup2"}, map[string]string{"Boolean": "bool"}); err == nil {
		t.Fatal("重复注册同一 scope 应被拒绝")
	}
}

// TestRegisterScopeProtocolClaimedByAnotherScope 协议名只能归属一个 scope。
func TestRegisterScopeProtocolClaimedByAnotherScope(t *testing.T) {
	const (
		scopeA = "testscope_claimA"
		scopeB = "testscope_claimB"
		proto  = "Test.Plugin.Claimed"
	)
	for _, s := range []string{scopeA, scopeB} {
		driver.GetTypeRegistry().ForProtocol(s).Register(&driver.DataType{Name: "bool", Kind: driver.KindBool, Size: 1})
	}
	if err := driver.RegisterScope(scopeA, []string{proto}, map[string]string{"Boolean": "bool"}); err != nil {
		t.Fatalf("注册 A 失败: %v", err)
	}
	t.Cleanup(func() { driver.UnregisterScope(scopeA) })

	if err := driver.RegisterScope(scopeB, []string{proto}, map[string]string{"Boolean": "bool"}); err == nil {
		t.Fatal("同一协议名被第二个 scope 认领应被拒绝")
	}
}

func TestUnregisterScopeRevertsLookups(t *testing.T) {
	const (
		scope    = "testscope_unreg"
		protocol = "Test.Plugin.Unreg"
	)
	reg := driver.GetTypeRegistry().ForProtocol(scope)
	reg.Register(&driver.DataType{Name: "bool", Kind: driver.KindBool, Size: 1})
	if err := driver.RegisterScope(scope, []string{protocol}, map[string]string{"Boolean": "bool"}); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	if !driver.UnregisterScope(scope) {
		t.Fatal("UnregisterScope 应返回 true")
	}
	if driver.UnregisterScope(scope) {
		t.Fatal("重复 UnregisterScope 应返回 false")
	}

	if got := driver.ScopeOf(protocol); got != "" {
		t.Errorf("注销后 ScopeOf = %q, want 空", got)
	}
	if _, ok := driver.LookupDataType(protocol, "Boolean"); ok {
		t.Error("注销后 LookupDataType 应返回 false")
	}
	if common, extended := driver.AvailableTypes(scope); common != nil || extended != nil {
		t.Errorf("注销后 AvailableTypes = %v,%v, want nil,nil", common, extended)
	}

	// 归零后协议名可被重新认领（这是插件"删文件再放回"的路径）。
	t.Cleanup(func() { driver.UnregisterScope(scope) })
	if err := driver.RegisterScope(scope, []string{protocol}, map[string]string{"Boolean": "bool"}); err != nil {
		t.Fatalf("注销后重新注册应成功: %v", err)
	}
}

// TestRegisteredScopeUnaffectedByOtherScope 一个 scope 的查询结果不受其它
// scope 注册的影响 —— 每个插件跑在自己的 scope 里，互不串扰。
func TestRegisteredScopeUnaffectedByOtherScope(t *testing.T) {
	const (
		stableScope    = "testscope_isolation_stable"
		stableProtocol = "Test.Plugin.Isolation.Stable"
	)
	registerTestScope(t, stableScope, stableProtocol,
		[]*driver.DataType{{Name: "bool", Kind: driver.KindBool, Size: 1}},
		map[string]string{"Boolean": "bool"},
	)
	// 再注册一个不相干的 scope。
	registerTestScope(t, "testscope_isolation_other", "Test.Plugin.Isolation.Other",
		[]*driver.DataType{{Name: "float32", Kind: driver.KindFloat, Size: 4}},
		map[string]string{"Float": "float32"},
	)

	if got := driver.ScopeOf(stableProtocol); got != stableScope {
		t.Errorf("ScopeOf(%q) = %q, want %q", stableProtocol, got, stableScope)
	}
	if got, ok := driver.LookupDataType(stableProtocol, "Boolean"); !ok || got != "bool" {
		t.Errorf("LookupDataType(%q, Boolean) = %q,%v; want bool,true", stableProtocol, got, ok)
	}
	// 未注册协议仍返回空。
	if got := driver.ScopeOf("Test.Plugin.NotRegistered"); got != "" {
		t.Errorf("未注册协议 ScopeOf = %q, want 空", got)
	}
}

// TestScopeLayerConcurrentAccess 在 -race 下验证注册/注销与查询并发安全。
//
// 内置基线清空后，全部 scope 都来自扩展层，这条路径从"插件专属"变成了
// 唯一路径 —— 并发安全不再是加分项而是底线。
//
// stableScope 全程只读，与 racyScope 的反复注册/注销并发，模拟
// 「一个插件在热升级，另一个插件的设备正在采集」这个真实时序。
func TestScopeLayerConcurrentAccess(t *testing.T) {
	const (
		scope    = "testscope_race"
		protocol = "Test.Plugin.Race"

		stableScope    = "testscope_race_stable"
		stableProtocol = "Test.Plugin.Race.Stable"
	)
	driver.GetTypeRegistry().ForProtocol(scope).Register(&driver.DataType{Name: "bool", Kind: driver.KindBool, Size: 1})
	registerTestScope(t, stableScope, stableProtocol,
		[]*driver.DataType{
			{Name: "bool", Kind: driver.KindBool, Size: 1},
			{Name: "float32", Kind: driver.KindFloat, Size: 4},
		},
		map[string]string{"Boolean": "bool", "Float": "float32"},
	)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			_ = driver.RegisterScope(scope, []string{protocol}, map[string]string{"Boolean": "bool"})
		}()
		go func() {
			defer wg.Done()
			driver.UnregisterScope(scope)
		}()
		go func() {
			defer wg.Done()
			_ = driver.ScopeOf(protocol)
			_, _ = driver.LookupDataType(protocol, "Boolean")
			driver.AvailableTypes(scope)
			driver.NormalizeAddressType(protocol, po.DeviceAddress{DataType: "bool", CommonDataType: "Boolean"})
		}()
		go func() {
			defer wg.Done()
			// 只读 scope 与扩展层写入并发，验证查询路径不因此出错。
			_ = driver.ScopeOf(stableProtocol)
			_, _ = driver.LookupDataType(stableProtocol, "Float")
			driver.AvailableTypes(stableScope)
		}()
	}
	wg.Wait()
}
