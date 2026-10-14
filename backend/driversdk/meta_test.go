// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driversdk_test

import (
	"strings"
	"testing"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
)

func TestMetaValidate(t *testing.T) {
	base := func() driversdk.Meta {
		return driversdk.Meta{
			APIVersion: driversdk.APIVersion,
			Protocols:  []driversdk.Protocol{{Name: "Test.Plugin"}},
			Scope:      "testplugin",
			Types:      []*driver.DataType{{Name: "bool", Kind: driver.KindBool, Size: 1}},
			TypeMap:    map[string]string{"Boolean": "bool"},
		}
	}

	if err := base().Validate(); err != nil {
		t.Fatalf("合法 Meta 应通过，得到 %v", err)
	}

	cases := []struct {
		name    string
		mutate  func(*driversdk.Meta)
		wantSub string
	}{
		{"版本不匹配", func(m *driversdk.Meta) { m.APIVersion = driversdk.APIVersion + 1 }, "APIVersion"},
		{"协议名为空", func(m *driversdk.Meta) { m.Protocols[0].Name = "" }, "Name"},
		{"scope 为空", func(m *driversdk.Meta) { m.Scope = "" }, "Scope"},
		{"无协议", func(m *driversdk.Meta) { m.Protocols = nil }, "Protocols"},
		{"无类型", func(m *driversdk.Meta) { m.Types = nil }, "Types"},
		{"无类型映射", func(m *driversdk.Meta) { m.TypeMap = nil }, "TypeMap"},
		{"类型名为空", func(m *driversdk.Meta) { m.Types[0].Name = "" }, "Name"},
		{"Kind 为空", func(m *driversdk.Meta) { m.Types[0].Kind = "" }, "Kind"},
		{"悬空映射", func(m *driversdk.Meta) { m.TypeMap["Short"] = "int16" }, "未在 Types 中声明"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := base()
			c.mutate(&m)
			err := m.Validate()
			if err == nil {
				t.Fatal("应校验失败")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("错误信息 %q 未包含 %q", err.Error(), c.wantSub)
			}
		})
	}
}

// TestMetaValidateDiscovery 扫描能力声明的启动期校验。
//
// 形状错了必须在**插件启动时**报错：这些字段错了的表现都是"扫不出设备"，
// 而运维在前端只能看到一句"没有可扫描的协议"，完全指不到根因。
func TestMetaValidateDiscovery(t *testing.T) {
	base := func() driversdk.Meta {
		m := driversdk.Meta{
			APIVersion: driversdk.APIVersion,
			Protocols: []driversdk.Protocol{{
				Name: "Test.Plugin.Scan",
				Discovery: &driver.DiscoverySpec{
					Title:       "扫描",
					DefaultPort: 502,
					TargetMode:  driver.TargetModeUnicast,
					MaxBudgetMs: 6000,
				},
			}},
			Scope:   "testpluginscan",
			Types:   []*driver.DataType{{Name: "bool", Kind: driver.KindBool, Size: 1}},
			TypeMap: map[string]string{"Boolean": "bool"},
		}
		return m
	}

	cases := []struct {
		name    string
		mutate  func(*driversdk.Meta)
		wantSub string
	}{
		{"Title 为空", func(m *driversdk.Meta) { m.Protocols[0].Discovery.Title = "  " }, "Title"},
		{"端口为 0", func(m *driversdk.Meta) { m.Protocols[0].Discovery.DefaultPort = 0 }, "DefaultPort"},
		{"端口越界", func(m *driversdk.Meta) { m.Protocols[0].Discovery.DefaultPort = 70000 }, "DefaultPort"},
		{"TargetMode 非法", func(m *driversdk.Meta) { m.Protocols[0].Discovery.TargetMode = "multicast" }, "TargetMode"},
		{"预算过低", func(m *driversdk.Meta) { m.Protocols[0].Discovery.MaxBudgetMs = 10 }, "MaxBudgetMs"},
		{"预算过高", func(m *driversdk.Meta) { m.Protocols[0].Discovery.MaxBudgetMs = 600000 }, "MaxBudgetMs"},
		{"表单不是合法 JSON", func(m *driversdk.Meta) {
			m.Protocols[0].Discovery.FormJSON = []byte("{不是 JSON")
		}, "FormJSON"},
		// 串口协议的目标是 comPort 而不是 host/port，"扫网段"这套套不上去，
		// 是另一个功能。宁可启动期报错，也不要让一个永远扫不到的协议出现在下拉里。
		{"串口协议声明扫描", func(m *driversdk.Meta) {
			m.Protocols[0].SerialExclusive = true
		}, "SerialExclusive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := base()
			c.mutate(&m)
			err := m.Validate()
			if err == nil {
				t.Fatal("应校验失败")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("错误信息 %q 未包含 %q", err.Error(), c.wantSub)
			}
		})
	}
}

// TestMetaValidateAcceptsNilDiscovery 不声明扫描能力是**正常状态**而非缺陷：
// 8 个既有插件都没有它，任何一个把它们判成非法 Meta 的改动都会让网关
// 一套协议都加载不上。
func TestMetaValidateAcceptsNilDiscovery(t *testing.T) {
	m := driversdk.Meta{
		APIVersion: driversdk.APIVersion,
		Protocols:  []driversdk.Protocol{{Name: "Test.Plugin.Plain"}},
		Scope:      "testpluginplain",
		Types:      []*driver.DataType{{Name: "bool", Kind: driver.KindBool, Size: 1}},
		TypeMap:    map[string]string{"Boolean": "bool"},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("未声明 Discovery 的 Meta 必须合法: %v", err)
	}
}

// ==================== 点位扫描能力声明的校验 ====================

// pointBase 一个声明了点位扫描能力的 Meta。
//
// TypeMap 里同时有 "Boolean" 与 "Short" 两个**通用名**，而它们的 value
// （"bool"/"int16"）是内部裸名 —— DefaultCommonDataType 必须写前者，
// 写成后者是这条校验最容易踩的坑。
func pointBase() driversdk.Meta {
	return driversdk.Meta{
		APIVersion: driversdk.APIVersion,
		Protocols: []driversdk.Protocol{{
			Name: "Test.Plugin.Point",
			PointDiscovery: &driver.PointDiscoverySpec{
				Title:                 "扫描寄存器范围",
				DefaultCommonDataType: "Short",
				MaxBudgetMs:           6000,
				MaxPoints:             500,
			},
		}},
		Scope: "testpluginpoint",
		Types: []*driver.DataType{
			{Name: "bool", Kind: driver.KindBool, Size: 1},
			{Name: "int16", Kind: driver.KindInt, Size: 2},
		},
		TypeMap: map[string]string{"Boolean": "bool", "Short": "int16"},
	}
}

// TestMetaValidatePointDiscovery 点位扫描声明的启动期校验。
//
// 与 TestMetaValidateDiscovery 同源：这些字段错了的表现都是"点开按钮发现扫不了"，
// 前端只能给出一句"该协议不支持"，完全指不到根因 —— 宁可插件启动就失败。
func TestMetaValidatePointDiscovery(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*driversdk.Meta)
		wantSub string
	}{
		{"Title 为空", func(m *driversdk.Meta) {
			m.Protocols[0].PointDiscovery.Title = "  "
		}, "Title"},
		{"预算过低", func(m *driversdk.Meta) {
			m.Protocols[0].PointDiscovery.MaxBudgetMs = 100
		}, "MaxBudgetMs"},
		{"预算过高", func(m *driversdk.Meta) {
			m.Protocols[0].PointDiscovery.MaxBudgetMs = 60000
		}, "MaxBudgetMs"},
		{"MaxPoints 为 0", func(m *driversdk.Meta) {
			m.Protocols[0].PointDiscovery.MaxPoints = 0
		}, "MaxPoints"},
		{"MaxPoints 过大", func(m *driversdk.Meta) {
			m.Protocols[0].PointDiscovery.MaxPoints = 99999
		}, "MaxPoints"},
		{"DefaultCommonDataType 为空", func(m *driversdk.Meta) {
			m.Protocols[0].PointDiscovery.DefaultCommonDataType = ""
		}, "DefaultCommonDataType"},
		// 写成内部裸名（value）而不是通用名（key）是最容易犯的错：前端下拉
		// 只会选中一个不存在的项，不报任何错。
		{"DefaultCommonDataType 写成内部名", func(m *driversdk.Meta) {
			m.Protocols[0].PointDiscovery.DefaultCommonDataType = "int16"
		}, "DefaultCommonDataType"},
		{"表单不是合法 JSON", func(m *driversdk.Meta) {
			m.Protocols[0].PointDiscovery.FormJSON = []byte("{不是 JSON")
		}, "FormJSON"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := pointBase()
			c.mutate(&m)
			err := m.Validate()
			if err == nil {
				t.Fatal("应校验失败")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("错误信息 %q 未包含 %q", err.Error(), c.wantSub)
			}
		})
	}
}

// TestValidateAllowsSerialPointDiscovery 点位扫描**必须**允许串口协议声明。
//
// 与 TestMetaValidateDiscovery 里那条「串口协议声明扫描 ⇒ 拒绝」成对，钉的是
// 两个校验函数之间**刻意的不对称**：局域网扫描的目标是"网段里的一个 IP"，
// 串口无关；而点位扫描的目标是**一台已存在的设备**，串口设备同样有"它有哪些
// 点位"这个问题，且串口恰是会话复用最重要的场景。
//
// 反例同样要钉住：真有人把两个 validate 合并成一份（或"顺手统一"成两边都禁），
// 串口设备的点扫会**静默消失**，前端只是一个永远灰着的按钮。
func TestValidateAllowsSerialPointDiscovery(t *testing.T) {
	m := pointBase()
	m.Protocols[0].SerialExclusive = true
	if err := m.Validate(); err != nil {
		t.Fatalf("串口独占协议声明 PointDiscovery 必须合法: %v", err)
	}

	// 同一个协议同时声明 Discovery 与 PointDiscovery 才是非法的那一半 ——
	// SerialExclusive 挡的是 Discovery，不是 PointDiscovery。
	m.Protocols[0].Discovery = &driver.DiscoverySpec{
		Title: "扫描", DefaultPort: 502, TargetMode: driver.TargetModeUnicast, MaxBudgetMs: 6000,
	}
	err := m.Validate()
	if err == nil {
		t.Fatal("串口协议声明 Discovery 仍应被拒")
	}
	if !strings.Contains(err.Error(), "SerialExclusive") {
		t.Fatalf("错误信息 %q 未点明 SerialExclusive", err.Error())
	}
}

// TestMetaValidateAcceptsNilPointDiscovery 不声明点位扫描同样是**正常状态**。
//
// 与 TestMetaValidateAcceptsNilDiscovery 同一条理由：全部既有插件（以及
// ModBus 之外的 8 个）都没有它，任何把它们判成非法 Meta 的改动都会让网关
// 一套协议都加载不上。
func TestMetaValidateAcceptsNilPointDiscovery(t *testing.T) {
	m := pointBase()
	m.Protocols[0].PointDiscovery = nil
	if err := m.Validate(); err != nil {
		t.Fatalf("未声明 PointDiscovery 的 Meta 必须合法: %v", err)
	}
}
