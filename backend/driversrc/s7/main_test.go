// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"iot-gateway/driver"
	s7drv "iot-gateway/driversrc/s7/s7drv"
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

// TestProbersMatchDiscovery 声明了局域网扫描能力的协议必须有对应的 prober。
//
// 与 TestFactoriesMatchProtocols 同源：driversdk.Serve 启动时也会查，
// 但那时插件已经被网关拉起了；在这里失败能直接定位到 main.go 漏配。
//
// 反方向那条同样重要：一个没有 Discovery 声明的 prober 永远不会被调用
// （网关只按自己看到的能力派发），留着它只会让人以为"这个协议能扫"。
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
				"（漏配会让插件**启动即失败**，整个插件的所有协议一起不可用）", p.Name)
		}
	}
	for name := range ps {
		if !declared[name] {
			t.Errorf("协议 %q 有 prober 但没声明 Discovery，它永远不会被调用", name)
		}
	}
	if len(declared) == 0 {
		t.Fatal("S7 应声明局域网扫描能力，否则设备扫描页的协议下拉里没有西门子")
	}
}

// TestDiscoverySpecPassesValidation 扫描声明的取值范围必须能过 Meta.Validate ——
// 越界会让**整个插件**在网关侧加载失败（不是只有扫描功能不可用）。
//
// Meta.Validate 已在 TestMetaValid 里整体调过一次，这里补的是取值本身：
// 默认端口是不是 ISO-on-TCP 的 102、表单有没有给。
func TestDiscoverySpecPassesValidation(t *testing.T) {
	if err := meta().Validate(); err != nil {
		t.Fatalf("Meta 校验失败（含扫描声明）: %v", err)
	}
	d := s7drv.Discovery()
	if d.DefaultPort != 102 {
		t.Errorf("默认端口 = %d，期望 102（ISO-TSAP）", d.DefaultPort)
	}
	if d.TargetMode != driver.TargetModeUnicast {
		t.Errorf("目标模式 = %q，期望 %q —— S7 没有广播型探测手段",
			d.TargetMode, driver.TargetModeUnicast)
	}
	if len(d.FormJSON) == 0 {
		t.Error("必须给扫描表单：没有它前端会渲染出一个空表单，" +
			"用户无从配置机架/槽位（S7 最常见的一条扫不到的原因）")
	}
}

// TestPointFindersMatchPointDiscovery 与 TestProbersMatchDiscovery 同构：
// 声明了点位扫描能力的协议必须有对应的 finder，反之亦然。
//
// 两个方向都要查：漏配一个会让插件**启动即失败**（整个插件的所有协议一起不可用），
// 而多配一个 finder 永远不会被调用，留着只会让人以为"这个协议能点扫"。
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
				"（漏配会让插件**启动即失败**）", p.Name)
		}
	}
	for name := range fs {
		if !declared[name] {
			t.Errorf("协议 %q 有 finder 但没声明 PointDiscovery，它永远不会被调用", name)
		}
	}
	if len(declared) == 0 {
		t.Fatal("S7 应声明点位扫描能力，否则设备地址页不会出现「自动发现点位」按钮")
	}

	// 默认类型必须是**通用类型名**（TypeMap 的 key）：写成内部裸名 "word"
	// 会被 Meta.Validate 拒掉，而那条报错指向的是 TypeMap，不容易一眼看出。
	pd := s7drv.PointDiscovery()
	if pd.DefaultCommonDataType != driver.TypeWord {
		t.Errorf("DefaultCommonDataType = %q，期望 %q", pd.DefaultCommonDataType, driver.TypeWord)
	}
	if _, ok := meta().TypeMap[pd.DefaultCommonDataType]; !ok {
		t.Errorf("DefaultCommonDataType %q 不是 TypeMap 的键 —— "+
			"它必须是通用类型名，否则前端下拉的初值是空的", pd.DefaultCommonDataType)
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
// 更严格的机器生成版在 pluginhost/testdata/parity/typemap/s7.json
// （由内置实现生成），pluginhost 的 parity 测试会拿它比对。
func TestProtocolIdentityPinned(t *testing.T) {
	cases := []struct{ got, want, what string }{
		{protocolName, "Siemens.S7", "协议名"},
		{pluginScope, "s7", "类型 scope"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q，内置时代的原值是 %q —— 改成别的会让既有设备静默采不到值",
				c.what, c.got, c.want)
		}
	}
}

// TestSerialExclusivePerProtocol S7 只有 ISO-on-TCP 一种传输，不独占串行总线。
//
// 声明错了不会报错，只会让该插件的设备白白丢掉并发（声明 true）或者
// 在未来的串口传输上与别的设备交错帧（漏声明）—— 因此钉住。
func TestSerialExclusivePerProtocol(t *testing.T) {
	// 期望逐字写死，不从协议名派生：这条测试要钉住的正是"哪个协议该独占"。
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

// TestEveryMappedTypeDecodable 网关会下发的每一个内部类型名，插件都必须能解析。
//
// 网关把 TypeMap 的 value（内部裸名）写进 device_address.data_type，
// 采集时原样回传给驱动的 Read。所以「TypeMap 里出现了插件解析不了的类型名」
// 就是「该点位每轮都采出空值」，而且现场不会有任何编译期提示。
//
// 用 16 字节缓冲覆盖最长类型（lreal 8 字节、string 默认跨度 16 字节）。
func TestEveryMappedTypeDecodable(t *testing.T) {
	// 用非零字节：全零缓冲会让 string 解析成空串，测不出"值被正常格式化"。
	raw := bytes.Repeat([]byte{'A'}, 16)

	// BitOffset = -1 表示"字设备、无位访问"——零值 S7Address 的 BitOffset 是 0，
	// 会被 IsBit() 判成位访问而走取位分支，测不到常规类型解码路径。
	addr := s7drv.S7Address{BitOffset: -1}

	for name, internal := range s7drv.CommonTypeMap() {
		got, err := s7drv.ParseS7Value(raw, addr, internal)
		if err != nil {
			t.Errorf("通用类型 %q → 内部类型 %q 无法解析: %v", name, internal, err)
			continue
		}
		if v := s7drv.FormatS7Value(got, internal); v == "" {
			t.Errorf("通用类型 %q → 内部类型 %q 格式化后为空串", name, internal)
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
//   - Name 带 "s7." 前缀：网关会给上报的类型名再拼一次 scope 前缀，
//     得到 "s7.s7.int"，地址校验与类型下拉双双落空。
func TestTypesCarryDecode(t *testing.T) {
	types := s7drv.Types()
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
