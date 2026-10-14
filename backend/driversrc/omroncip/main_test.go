// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"iot-gateway/driver"
	omroncipdrv "iot-gateway/driversrc/omroncip/omroncipdrv"
)

// TestMetaValid 插件清单必须能通过 SDK 自校验。
//
// 这条测试替代的是旧实现里「从内置注册表派生类型表」这一步隐含的校验：
// 那份派生代码只要有一个类型对不上就会当场报错，改成插件自带类型表后，
// 校验点就落在这里 —— 类型表与类型映射对不上、协议没有工厂，
// 都应该在 CI 里失败，而不是等插件在网关里启动时才失败。
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
	// Validate 不校验 FormJSON 的合法性（它只看得到 json.RawMessage 非 nil），
	// 而表单是从 migrations/008 手工抠出来的，一个多余逗号就能让前端
	// 渲染不出表单。这里补上这道校验。
	for _, p := range m.Protocols {
		if !json.Valid(p.FormJSON) {
			t.Errorf("协议 %q 的 FormJSON 不是合法 JSON", p.Name)
		}
	}
}

// TestFactoriesMatchProtocols 工厂的键必须与协议名逐一对应。
//
// driversdk.Serve 会在启动时做同样的检查，但那时已经是「插件被网关拉起」了；
// 在这里失败能直接定位到 main.go 漏配，而不必去翻插件进程的 stderr。
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

// ==================== 扫描能力 ====================

// TestProbersMatchDiscovery 声明了 Discovery 的协议必须有对应的 prober，反之亦然。
//
// driversdk.Serve 启动时也查这一条，但那时插件已经被网关拉起、失败表现为
// "这个插件唯一的协议一起不可用"；在 CI 里失败能直接定位到 main.go 漏配。
func TestProbersMatchDiscovery(t *testing.T) {
	ps := probers()
	declared := make(map[string]bool)
	for _, p := range meta().Protocols {
		if p.Discovery == nil {
			continue
		}
		declared[p.Name] = true
		if _, ok := ps[p.Name]; !ok {
			t.Errorf("协议 %q 声明了 Discovery 却没有对应的 prober"+
				"（漏配会让插件**启动即失败**）", p.Name)
		}
	}
	for name := range ps {
		if !declared[name] {
			t.Errorf("协议 %q 有 prober 但没声明 Discovery，它永远不会被调用", name)
		}
	}
	if len(declared) == 0 {
		t.Fatal("Omron.CIP 应声明局域网扫描能力，否则设备扫描页的协议下拉里没有它")
	}
}

// TestDiscoverySpecUsesBothTargetModes 广播与逐主机两种目标都要扫。
//
// 只声明 broadcast 时，禁广播的受管网络上症状是"扫描正常结束但一台设备都没有"，
// 而用户完全无从判断是网络还是配置；只声明 unicast 则退化成 254 次数据报往返。
// 这个组合是刻意的，因此逐字钉住。
func TestDiscoverySpecUsesBothTargetModes(t *testing.T) {
	d := omroncipdrv.Discovery()
	if d.DefaultPort != 44818 {
		t.Errorf("默认端口 = %d，期望 44818（EtherNet/IP 显式报文）", d.DefaultPort)
	}
	if d.TargetMode != driver.TargetModeBoth {
		t.Errorf("目标模式 = %q，期望 %q —— ListIdentity 是广播型协议，"+
			"但只靠广播在禁它的网络上会一台都扫不到", d.TargetMode, driver.TargetModeBoth)
	}
	if len(d.FormJSON) == 0 || !json.Valid(d.FormJSON) {
		t.Error("扫描表单缺失或不是合法 JSON —— 没有它前端会渲染出一个空表单")
	}
	if d.Title == "" {
		t.Error("扫描声明缺少 Title")
	}
	if err := meta().Validate(); err != nil {
		t.Fatalf("Meta 校验失败（含扫描声明）: %v", err)
	}
}

// TestNoPointDiscovery CIP **刻意不实现点位扫描**。
//
// CIP 的点位是符号名（MotorSpeed、Recipe.Setpoint），不是可枚举的地址空间，
// Modbus/S7/FINS 那种"从某个字起扫 N 个字"的契约形状落不下来；要枚举标签只能走
// 另一条服务（Symbol Object，类 0x6B）。
//
// **别把 Rockwell.CIP 当成反例**：同一个类号、同一条服务，它做了点扫，因为
// Logix 那份实例属性表的布局在本仓库里有一份已验证的第三方实现可依据
// （见 main.go 里那段说明）。欧姆龙 NJ/NX 走的是另一套对象模型，
// 那份依据对不上，也没有真机核过。
//
// 这条测试钉的是"别顺手加上一个没验证过的能力"：能力一旦声明出去，
// 前端就会出现一个按钮，而按下去的结果是返回一份空清单 ——
// 用户会从"一个标签都没有"得出"这台设备没有点位"的结论。
func TestNoPointDiscovery(t *testing.T) {
	for _, p := range meta().Protocols {
		if p.PointDiscovery != nil {
			t.Errorf("协议 %q 声明了 PointDiscovery，但本插件没有（也不该有）"+
				"对应的实现 —— 见 main.go 里那段说明", p.Name)
		}
	}
}

// TestProtocolIdentityPinned 协议名与 scope 必须逐字等于内置时代的原值。
//
// 这是本文件里最重要的两条字面量：`device.protocol_id` → `iot_protocol.name`
// → `driver.Register` 是一条纯字符串的链，改任何一个字符都不会有编译期提示，
// 症状是**既有设备被判为「协议不支持」而静默跳过**（collector 只记一行日志），
// 而设备列表、前端下拉、健康检查全都正常 —— 现场可能几周后才察觉。
//
// 字面量刻意写死、不从别处派生：它要挡住的正是"有人顺手把名字改得更整齐"。
func TestProtocolIdentityPinned(t *testing.T) {
	cases := []struct{ got, want, what string }{
		{protocolName, "Omron.CIP", "协议名"},
		{pluginScope, "cip", "类型 scope"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q，内置时代的原值是 %q —— 改成别的会让既有设备静默采不到值",
				c.what, c.got, c.want)
		}
	}
}

// TestSerialExclusivePerProtocol CIP 不独占任何串行总线（只有 EtherNet/IP
// 显式报文，TCP 长连接，内部线程安全），声明错了不会报错，只会白白丢掉并发 ——
// 因此钉住。
func TestSerialExclusivePerProtocol(t *testing.T) {
	// 期望逐字写死，不从协议名派生：这条测试要钉住的正是"该协议不该独占"。
	want := map[string]bool{
		protocolName: false,
	}
	seen := make(map[string]bool, len(want))

	for _, p := range meta().Protocols {
		if w, ok := want[p.Name]; ok {
			if p.SerialExclusive != w {
				t.Errorf("协议 %q 的 SerialExclusive = %v, 期望 %v", p.Name, p.SerialExclusive, w)
			}
			seen[p.Name] = true
		} else {
			t.Errorf("出现未预期的协议 %q", p.Name)
		}
		if len(p.FormJSON) == 0 {
			t.Errorf("协议 %q 缺少配置表单（FormJSON）", p.Name)
		}
	}

	// 协议必须出现：漏了就不会有人检查它的独占性与表单。
	for name := range want {
		if !seen[name] {
			t.Errorf("协议 %q 不在 Meta.Protocols 中", name)
		}
	}
}

// TestTypesCarryDecode Types() 上报的每一项都必须带解码函数、且 Name 是裸名。
//
// 这两条都直接决定设备能不能采到值：
//
//   - Decode 为 nil：driversdk.Serve 会把 Types 里的指针**重新注册**进插件进程的
//     注册表，瘦身构造（只填 Name/Kind/Size）会把包内缓存的 Decode 换成 nil，
//     解析时调 nil 函数 panic，被 worker pool 的 recover 吞掉，该设备永远采不到值；
//   - Name 带 "cip." 前缀：网关会给上报的类型名再拼一次 scope 前缀，
//     得到 "cip.cip.int16"，地址校验与类型下拉双双落空。
func TestTypesCarryDecode(t *testing.T) {
	types := omroncipdrv.Types()
	if len(types) == 0 {
		t.Fatal("Types() 返回空列表")
	}
	names := make([]string, 0, len(types))
	for _, dt := range types {
		names = append(names, dt.Name)
	}
	t.Logf("Types() 共上报 %d 个类型: %v", len(types), names)
	for _, dt := range types {
		if dt == nil {
			t.Error("Types() 中出现 nil 项")
			continue
		}
		if dt.Decode == nil {
			t.Errorf("类型 %q 的 Decode 为 nil，该类型下所有点位都会解析失败", dt.Name)
		}
		if strings.Contains(dt.Name, ".") {
			t.Errorf("类型 %q 的 Name 带了 scope 前缀，网关会再拼一次得到无效名", dt.Name)
		}
	}
}

// TestEveryMappedTypeDecodable 网关会下发的每一个内部类型名，插件都必须能解析。
//
// 网关把 TypeMap 的 value（内部裸名）写进 device_address.data_type，
// 采集时原样回传给驱动的 Read。所以「TypeMap 里出现了插件解析不了的类型名」
// 就是「该点位每轮都采出空值」，而且现场不会有任何编译期提示。
//
// 用 16 字节缓冲覆盖最长类型（float64 8 字节、string 默认跨度 80 字节但缓冲
// 短于它也无妨 —— 解码按实际长度截断，测的是"能解析且格式化非空"）。
func TestEveryMappedTypeDecodable(t *testing.T) {
	// 用非零字节：全零缓冲会让 bool 解析成 false、string 解析成空串，
	// 测不出"值被正常格式化"。
	raw := bytes.Repeat([]byte{'A'}, 16)
	cfg := omroncipdrv.DefaultCIPConfig()

	for name, internal := range omroncipdrv.CommonTypeMap() {
		got, err := omroncipdrv.ParseCIPValue(raw, internal, cfg)
		if err != nil {
			t.Errorf("通用类型 %q → 内部类型 %q 无法解析: %v", name, internal, err)
			continue
		}
		if v := omroncipdrv.FormatCIPValue(got, internal); v == "" {
			t.Errorf("通用类型 %q → 内部类型 %q 格式化后为空串", name, internal)
		}
	}
}
