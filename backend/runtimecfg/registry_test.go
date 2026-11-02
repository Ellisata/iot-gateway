// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package runtimecfg

import (
	"reflect"
	"sort"
	"testing"

	"iot-gateway/configFile"
)

// excludedBools 是**有意识地**不纳入动态管理的布尔配置项。
//
// 测试会遍历 configFile.Config 的每个布尔叶子，要求它要么已在 registry.go
// 登记，要么在这里写明理由。这样新增一个开关时，「要不要让它可动态修改」
// 会变成一次必须做出的决定，而不是悄悄漏掉。
var excludedBools = map[string]string{
	"plugin.disableProtocolRowProvisioning": "影响插件加载时对 iot_protocol 行的供给，改后需重新加载插件；v1 不纳入",
	"plugin.allowInsecureDir":               "插件目录权限校验，属安全敏感启动项，不提供页面开关",
	"plugin.disableHotUpgrade":              "插件热升级开关，改动需重新扫描目录；v1 不纳入",
}

func TestRegistryCoversEveryBoolConfig(t *testing.T) {
	bools := map[string]bool{}
	collectBoolLeaves(reflect.TypeOf(configFile.Config{}), "", bools)

	managed := map[string]bool{}
	for _, spec := range All() {
		managed[string(spec.Key)] = true
		// 本测试只守布尔叶子；枚举项（int）由 TestRegistrySpecsAreWellFormed
		// 校验选项，并由 TestManagerEnumDefaultUpdateReset 验证默认值来源。
		if spec.Type != ValueTypeBool {
			continue
		}
		if !bools[string(spec.Key)] {
			t.Errorf("注册项 %s 在 configFile.Config 里不存在或不是布尔字段", spec.Key)
		}
	}

	var uncovered, stale []string
	for key := range bools {
		if !managed[key] {
			if _, ok := excludedBools[key]; !ok {
				uncovered = append(uncovered, key)
			}
		}
	}
	for key := range excludedBools {
		if !bools[key] {
			stale = append(stale, key)
		}
		if managed[key] {
			t.Errorf("%s 同时出现在注册表与排除表里", key)
		}
	}
	sort.Strings(uncovered)
	sort.Strings(stale)

	if len(uncovered) > 0 {
		t.Errorf("以下布尔配置既未登记也不在排除表里（在 runtimecfg/registry.go 登记，或在 excludedBools 写明理由）:\n%v", uncovered)
	}
	if len(stale) > 0 {
		t.Errorf("排除表里的 key 已不存在于 Config，删掉这些陈旧条目: %v", stale)
	}
}

func TestRegistrySpecsAreWellFormed(t *testing.T) {
	cfg := &configFile.Config{}
	for _, spec := range All() {
		switch spec.Type {
		case ValueTypeBool:
			if len(spec.Options) != 0 {
				t.Errorf("%s 是布尔项，不应带 Options", spec.Key)
			}
		case ValueTypeEnum:
			if len(spec.Options) == 0 {
				t.Errorf("%s 是枚举项，必须至少有一个 Option", spec.Key)
			}
			seen := map[int]bool{}
			for _, o := range spec.Options {
				if o.LabelKey == "" {
					t.Errorf("%s 的枚举项 %d 缺少 LabelKey", spec.Key, o.Value)
				}
				if seen[o.Value] {
					t.Errorf("%s 的枚举值 %d 重复", spec.Key, o.Value)
				}
				seen[o.Value] = true
			}
		default:
			t.Errorf("%s 的类型 %q 未实现", spec.Key, spec.Type)
		}
		if spec.Group == "" || spec.GroupLabelKey == "" || spec.LabelKey == "" || spec.DescKey == "" {
			t.Errorf("%s 缺少分组或文案 key", spec.Key)
		}
		if spec.Base == nil {
			t.Errorf("%s 缺少默认值读取函数", spec.Key)
			continue
		}
		// 默认值本身由 default.yaml 决定，这里只校验可调用且动态类型与 Type 一致。
		v := spec.Base(cfg)
		switch spec.Type {
		case ValueTypeBool:
			if _, ok := v.(bool); !ok {
				t.Errorf("%s 的 Base 返回 %T，期望 bool", spec.Key, v)
			}
		case ValueTypeEnum:
			if _, ok := v.(int); !ok {
				t.Errorf("%s 的 Base 返回 %T，期望 int", spec.Key, v)
			}
		}
	}

	// 生效方式钉住：改错一个会让「立即生效」变成需要重启却看不出来。
	wantMode := map[Key]ApplyMode{
		KeyDiscoveryEnabled:         ApplyHotAction,
		KeyDeviceScanEnabled:        ApplyHotRead,
		KeyPushChangeOnly:           ApplyHotRead,
		KeyPushKeepaliveIntervalSec: ApplyHotRead,
		KeyPluginEnabled:            ApplyRestart,
		KeyAgentApiEnabled:          ApplyHotRead,
		KeyControlEnabled:           ApplyHotRead,
	}
	for key, want := range wantMode {
		spec, ok := Lookup(key)
		if !ok {
			t.Errorf("注册表缺少 %s", key)
			continue
		}
		if spec.Mode != want {
			t.Errorf("%s 的生效方式为 %q，期望 %q", key, spec.Mode, want)
		}
	}
}

// collectBoolLeaves 按 mapstructure 标签拼出点分路径，收集所有布尔叶子。
func collectBoolLeaves(typ reflect.Type, prefix string, out map[string]bool) {
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.PkgPath != "" { // 未导出字段
			continue
		}
		tag := f.Tag.Get("mapstructure")
		if tag == "" {
			continue
		}
		key := tag
		if prefix != "" {
			key = prefix + "." + tag
		}
		ft := f.Type
		switch ft.Kind() {
		case reflect.Bool:
			out[key] = true
		case reflect.Struct:
			collectBoolLeaves(ft, key, out)
		}
	}
}
