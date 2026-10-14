// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import (
	"strings"
	"testing"
)

// 本文件是**包内白盒**测试，而非常规的 driver_test 外部测试。
//
// 理由：静态基线（scopeTypeMap / scopeLookup）自内置协议全部插件化后已经是**空表**，
// 外部测试再也构造不出"撞基线"的场景 —— 而 RegisterScope 里那两条基线冲突检查
// 仍然在（见 dataTypeMap.go），它们是给"将来若重新引入内置 scope"留的护栏。
// 只有包内测试能临时往基线里塞哨兵条目，把这两条护栏钉住，
// 否则它们会在无人察觉的情况下被删掉，等真的再加内置 scope 时才炸。
//
// 并发说明：Go 的同一包内测试是顺序执行的（全仓无 t.Parallel），
// 且哨兵条目在 t.Cleanup 里还原，不会泄漏到其它用例。

// withBaselineScope 在测试期间往静态基线注入一个哨兵 scope 与协议名。
func withBaselineScope(t *testing.T, scope, protocol string) {
	t.Helper()

	scope = strings.ToLower(scope)
	lowerProto := strings.ToLower(protocol)

	if _, exists := scopeTypeMap[scope]; exists {
		t.Fatalf("基线中已存在 scope %q，哨兵名需要换一个", scope)
	}
	if _, exists := scopeLookup[lowerProto]; exists {
		t.Fatalf("基线中已存在协议名 %q，哨兵名需要换一个", protocol)
	}

	scopeTypeMap[scope] = map[string]string{TypeBoolean: "bool", TypeFloat: "float32"}
	scopeLookup[lowerProto] = scope
	t.Cleanup(func() {
		delete(scopeTypeMap, scope)
		delete(scopeLookup, lowerProto)
	})
}

// TestBaselineScopeBlocksRuntimeRegistration 基线上已有的 scope 与协议名，
// 运行期注册必须被拒 —— 且 scope 的大小写变体同样被拒。
//
// 大小写这一条不是形式主义：类型前缀是经 ForProtocol 小写化后拼出来的，
// "VIBase" 与 "vibase" 落在同一命名空间里；放过去就等于让插件覆盖内置类型，
// 而 TypeRegistry.Register 是覆盖写 —— 症状是"某台内置协议设备永远采不到值"，
// 跟插件本身看不出半点关系。
func TestBaselineScopeBlocksRuntimeRegistration(t *testing.T) {
	withBaselineScope(t, "vitest_baseline_scope", "Vi.Baseline.Proto")

	t.Run("scope 同名被拒", func(t *testing.T) {
		err := RegisterScope("vitest_baseline_scope", []string{"Vi.Plugin.New"}, map[string]string{TypeBoolean: "bool"})
		assertErrContains(t, err, "built-in scope")
	})

	t.Run("scope 大小写变体被拒", func(t *testing.T) {
		for _, scope := range []string{"VITEST_BASELINE_SCOPE", "ViTest_Baseline_Scope"} {
			err := RegisterScope(scope, []string{"Vi.Plugin.New"}, map[string]string{TypeBoolean: "bool"})
			assertErrContains(t, err, "built-in scope")
		}
	})

	t.Run("协议名撞基线被拒", func(t *testing.T) {
		err := RegisterScope("vitest_baseline_other", []string{"vi.baseline.proto"}, map[string]string{TypeBoolean: "bool"})
		assertErrContains(t, err, "built-in protocol")
	})
}

// TestBaselineScopeIsReachableByLookups 基线条目对公开查询接口可见。
func TestBaselineScopeIsReachableByLookups(t *testing.T) {
	const (
		scope    = "vitest_baseline_visible"
		protocol = "Vi.Baseline.Visible"
	)
	withBaselineScope(t, scope, protocol)
	GetTypeRegistry().ForProtocol(scope).Register(&DataType{Name: "bool", Kind: KindBool, Size: 1})

	if got := ScopeOf(protocol); got != scope {
		t.Errorf("ScopeOf(%q) = %q, want %q", protocol, got, scope)
	}
	if got := ScopeOf(strings.ToUpper(protocol)); got != scope {
		t.Errorf("ScopeOf(大写) = %q, want %q", got, scope)
	}
	if got, ok := LookupDataType(protocol, TypeBoolean); !ok || got != "bool" {
		t.Errorf("LookupDataType(%q, %s) = %q,%v; want bool,true", protocol, TypeBoolean, got, ok)
	}
	// 未在哨兵表里的类型查不到。
	if got, ok := LookupDataType(protocol, TypeDouble); ok {
		t.Errorf("LookupDataType(%q, %s) = %q,true; 哨兵表未映射 Double，应为 false", protocol, TypeDouble, got)
	}
}

// TestScopeTypesPrefersBaseline 同一 scope 同时在基线层与扩展层时，基线必须赢。
//
// 正常情况下这不可能发生（RegisterScope 会拒绝，见上一条测试），此处直接
// 构造出这个状态，是为了钉住 scopeTypes 的"先查基线、命中即终结"这条读取规则
// —— 它是"内置协议的类型解析行为永不被插件影响"这句承诺的实现。
func TestScopeTypesPrefersBaseline(t *testing.T) {
	const scope = "vitest_baseline_precedence"
	withBaselineScope(t, scope, "Vi.Baseline.Precedence")

	// 绕过 RegisterScope 的检查，直接往扩展层塞一份"同名但内容不同"的表。
	extMu.Lock()
	extScopes[scope] = map[string]string{TypeDouble: "float64"}
	extMu.Unlock()
	t.Cleanup(func() {
		extMu.Lock()
		delete(extScopes, scope)
		extMu.Unlock()
	})

	if _, ok := LookupScopeType(scope, TypeBoolean); !ok {
		t.Error("应命中基线表的 Boolean")
	}
	if _, ok := LookupScopeType(scope, TypeDouble); ok {
		t.Error("扩展层的 Double 不得可见 —— 基线命中后必须立即终结查询")
	}
}

func assertErrContains(t *testing.T, err error, wantSub string) {
	t.Helper()
	if err == nil {
		t.Fatalf("应被拒绝，但注册成功")
	}
	if !strings.Contains(err.Error(), wantSub) {
		t.Fatalf("错误信息 %q 未包含 %q", err.Error(), wantSub)
	}
}
