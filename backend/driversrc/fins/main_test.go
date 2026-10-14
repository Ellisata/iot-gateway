// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"iot-gateway/driver"
	finsdrv "iot-gateway/driversrc/fins/finsdrv"
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
	// 而四份表单里三份是从 migrations/008 手工抠出来的、一份是新写的，
	// 一个多余逗号就能让前端渲染不出表单。这里补上这道校验。
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

// ==================== 两项扫描能力 ====================

// TestProbersMatchDiscovery 声明了 Discovery 的协议必须有对应的 prober，反之亦然。
//
// driversdk.Serve 启动时也查这一条，但那时插件已经被网关拉起、失败表现为
// "整个插件的四个协议一起不可用"；在 CI 里失败能直接定位到 main.go 漏配。
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
		t.Fatal("FINS 应声明局域网扫描能力，否则设备扫描页的协议下拉里没有欧姆龙")
	}
}

// TestDiscoveryOnlyOnEthernet 只有**直连 PLC 的两种以太网传输**能声明设备扫描。
//
// 两边的理由完全不同，所以两边都要钉：
//
//   - Serial（串口）：driversdk.Meta.Validate **明文禁止**串口协议声明 Discovery
//     （目标模型是 host/port，表达不了 comPort），声明了插件启动即失败；
//   - HostLinkTCP：契约上允许，但它的 host 填的是**串口服务器**而不是 PLC ——
//     扫一个网段只会扫出一堆串口服务器，一键建出来的设备大概率指向一台
//     后面并没有 PLC 的服务器。这类"能扫到但建出来不能用"的结果比扫不到更糟。
func TestDiscoveryOnlyOnEthernet(t *testing.T) {
	want := map[string]bool{
		protocolUDP:         true,
		protocolTCP:         true,
		protocolSerial:      false,
		protocolHostLinkTCP: false,
	}
	seen := make(map[string]bool, len(want))
	for _, p := range meta().Protocols {
		w, ok := want[p.Name]
		if !ok {
			t.Errorf("出现未预期的协议 %q", p.Name)
			continue
		}
		seen[p.Name] = true
		if got := p.Discovery != nil; got != w {
			t.Errorf("协议 %q 声明 Discovery = %v，期望 %v", p.Name, got, w)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("协议 %q 不在 Meta.Protocols 中", name)
		}
	}
}

// TestDiscoverySpecsAreUnicast 两个以太网协议的扫描声明取值必须对得上现场事实。
//
// 字节布局写错、端口写错都会让扫描"静默地扫不到设备"——那是本功能最难查的一类
// 故障（网关侧看起来一切正常）。所以这里把几个关键取值逐字钉住。
func TestDiscoverySpecsAreUnicast(t *testing.T) {
	for name, spec := range map[string]*driver.DiscoverySpec{
		protocolUDP: finsdrv.DiscoveryUDP(),
		protocolTCP: finsdrv.DiscoveryTCP(),
	} {
		if spec.DefaultPort != 9600 {
			t.Errorf("协议 %q 的默认端口 = %d，期望 9600", name, spec.DefaultPort)
		}
		if spec.TargetMode != driver.TargetModeUnicast {
			t.Errorf("协议 %q 的目标模式 = %q，期望 %q",
				name, spec.TargetMode, driver.TargetModeUnicast)
		}
		if len(spec.FormJSON) == 0 || !json.Valid(spec.FormJSON) {
			t.Errorf("协议 %q 的扫描表单缺失或不是合法 JSON —— "+
				"没有它前端会渲染出一个空表单，用户无从配置超时"+
				"（而超时是扫描速度唯一的调节点）", name)
		}
		if spec.Title == "" {
			t.Errorf("协议 %q 的扫描声明缺少 Title", name)
		}
	}
	if err := meta().Validate(); err != nil {
		t.Fatalf("Meta 校验失败（含扫描声明）: %v", err)
	}
}

// TestPointFindersMatchPointDiscovery 与 TestProbersMatchDiscovery 同构。
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
		t.Fatal("FINS 应声明点位扫描能力，否则设备地址页不会出现「自动发现点位」按钮")
	}

	// 默认类型必须是**通用类型名**（TypeMap 的 key）：写成内部裸名 "word"
	// 会被 Meta.Validate 拒掉，而那条报错指向的是 TypeMap，不容易一眼看出。
	pd := finsdrv.PointDiscovery()
	if pd.DefaultCommonDataType != driver.TypeWord {
		t.Errorf("DefaultCommonDataType = %q，期望 %q", pd.DefaultCommonDataType, driver.TypeWord)
	}
	if _, ok := meta().TypeMap[pd.DefaultCommonDataType]; !ok {
		t.Errorf("DefaultCommonDataType %q 不是 TypeMap 的键 —— "+
			"它必须是通用类型名，否则前端下拉的初值是空的", pd.DefaultCommonDataType)
	}
	if !json.Valid(pd.FormJSON) {
		t.Error("点位扫描表单不是合法 JSON")
	}
}

// TestProtocolIdentityPinned 协议名与 scope 必须逐字等于内置时代的原值。
//
// 这是本文件里最重要的五条字面量：`device.protocol_id` → `iot_protocol.name`
// → `driver.Register` 是一条纯字符串的链，改任何一个字符都不会有编译期提示，
// 症状是**既有设备被判为「协议不支持」而静默跳过**（collector 只记一行日志），
// 而设备列表、前端下拉、健康检查全都正常 —— 现场可能几周后才察觉。
//
// 字面量刻意写死、不从别处派生：它要挡住的正是"有人顺手把名字改得更整齐"。
// 注意 HostLinkTCP 曾经在文档里被写成 Omron.Net.FINS.* 一类的旧名，
// 注册名一律以内置实现里的 Omron.FINS.* 为准。
func TestProtocolIdentityPinned(t *testing.T) {
	cases := []struct{ got, want, what string }{
		{protocolUDP, "Omron.FINS.UDP", "UDP 协议名"},
		{protocolTCP, "Omron.FINS.TCP", "TCP 协议名"},
		{protocolSerial, "Omron.FINS.Serial", "Serial 协议名"},
		{protocolHostLinkTCP, "Omron.FINS.HostLinkTCP", "HostLinkTCP 协议名"},
		{pluginScope, "fins", "类型 scope"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q，内置时代的原值是 %q —— 改成别的会让既有设备静默采不到值",
				c.what, c.got, c.want)
		}
	}
}

// TestSerialExclusivePerProtocol 串行独占是按协议而非按插件声明的：
// **只有 Omron.FINS.Serial 独占串行总线**（采集引擎整轮持设备锁）。
//
// 其余三个都是 false，HostLinkTCP 尤其容易被误判 —— 协议名里带 "Serial/HostLink"，
// 但它走的是 TCP 长连接（串口服务器），客户端内部已持锁，与 FINS/TCP 同级。
// 搞反会让 RS-485 上出现交错的帧（偶发 CRC 错、跳变值），是最难排查的一类现场故障；
// 反过来给 TCP 加独占只是白白丢掉并发。
func TestSerialExclusivePerProtocol(t *testing.T) {
	// 期望逐字写死，不从协议名派生：这条测试要钉住的正是"哪个协议该独占"。
	want := map[string]bool{
		protocolUDP:         false,
		protocolTCP:         false,
		protocolSerial:      true,
		protocolHostLinkTCP: false,
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
		seen[p.Name] = true

		if len(p.FormJSON) == 0 {
			t.Errorf("协议 %q 缺少配置表单（FormJSON）", p.Name)
			continue
		}
		if !json.Valid(p.FormJSON) {
			t.Errorf("协议 %q 的 FormJSON 不是合法 JSON", p.Name)
		}
	}

	// 四个协议都必须出现：漏一个就不会有人检查它的独占性与表单。
	for name := range want {
		if !seen[name] {
			t.Errorf("协议 %q 不在 Meta.Protocols 中", name)
		}
	}
}

// TestFormsAreDistinct 表单是按协议而非按插件的：一份抄给两个协议，
// 就意味着其中一个协议的现场配置填不对（选项与协议不符）。
//
// 这条测试对本插件尤其必要：UDP/TCP/HostLinkTCP 三份表单在内置时代的播种脚本里
// 只有 form-create 的内部 id 不同，把冗余字段去掉后就是逐字节相同的三份，
// 一旦真被复制粘贴，除了这条测试没有任何地方会报错。
func TestFormsAreDistinct(t *testing.T) {
	protos := meta().Protocols
	for i := 0; i < len(protos); i++ {
		for j := i + 1; j < len(protos); j++ {
			if bytes.Equal(protos[i].FormJSON, protos[j].FormJSON) {
				t.Errorf("协议 %q 与 %q 的配置表单完全相同（FormJSON 是按协议而非按插件的）",
					protos[i].Name, protos[j].Name)
			}
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
//   - Name 带 "fins." 前缀：网关会给上报的类型名再拼一次 scope 前缀，
//     得到 "fins.fins.int16"，地址校验与类型下拉双双落空。
func TestTypesCarryDecode(t *testing.T) {
	types := finsdrv.Types()
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
// 用 16 字节缓冲覆盖最长类型（float64 8 字节、date 4 字节、string 默认跨度 16 字节）。
func TestEveryMappedTypeDecodable(t *testing.T) {
	// 用非零字节：全零缓冲会让 string 解析成空串、bool 解析成 false，
	// 测不出"值被正常格式化"。
	raw := bytes.Repeat([]byte{'A'}, 16)

	// Bit = -1 表示"字设备、无位访问"——零值 FINSAddress 的 Bit 是 0，
	// 会被 IsBit() 判成位访问而走取位分支，测不到常规类型解码路径。
	addr := finsdrv.FINSAddress{Bit: -1}
	cfg := finsdrv.DefaultFINSConfig()

	for common, internal := range finsdrv.CommonTypeMap() {
		got, err := finsdrv.ParseFINSValue(raw, addr, internal, cfg)
		if err != nil {
			t.Errorf("通用类型 %q → 内部类型 %q 无法解析: %v", common, internal, err)
			continue
		}
		if v := finsdrv.FormatFINSValue(got, internal); v == "" {
			t.Errorf("通用类型 %q → 内部类型 %q 格式化后为空串", common, internal)
		}
	}
}
