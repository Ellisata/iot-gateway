// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"iot-gateway/driver"
	opcuadrv "iot-gateway/driversrc/opcua/opcuadrv"
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
	// 而表单是从 migrations/013 手工抠出来的，一个多余逗号就能让前端
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

// TestProtocolIdentityPinned 协议名与 scope 必须逐字等于内置时代的原值。
//
// 这是本文件里最重要的两个字面量：`device.protocol_id` → `iot_protocol.name`
// → `driver.Register` 是一条纯字符串的链，改任何一个字符都不会有编译期提示，
// 症状是**既有设备被判为「协议不支持」而静默跳过**（collector 只记一行日志），
// 而设备列表、前端下拉、健康检查全都正常 —— 现场可能几周后才察觉。
//
// 字面量刻意写死、不从别处派生：它要挡住的正是"有人顺手把名字改得更整齐"。
// 更严格的机器生成版在 pluginhost/testdata/parity/typemap/opcua.json
// （由内置实现生成），pluginhost 的 parity 测试会拿它比对。
func TestProtocolIdentityPinned(t *testing.T) {
	cases := []struct{ got, want, what string }{
		{protocolName, "OPC.UA", "协议名"},
		{pluginScope, "opcua", "类型 scope"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q，内置时代的原值是 %q —— 改成别的会让既有设备静默采不到值",
				c.what, c.got, c.want)
		}
	}
}

// TestSerialExclusivePerProtocol 串行独占是按协议而非按插件声明的。
//
// OPC UA 只有一个协议、走 TCP 长连接（endpoint 的 scheme 固定为 opc.tcp://），
// 底层是并发安全的 SecureChannel，不独占串行总线。声明成 true 不会报错，
// 只会让同一插件的所有设备白白丢掉并发 —— 因此钉住。
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

// TestScanCapabilitiesDeclared 两个协议的扫描能力声明必须与现场事实对得上。
//
// OPC UA 只有一个协议、只走 opc.tcp 长连接，所以两项能力**都**应当声明 ——
// 与 MC 的"串口只能声明一半"（Discovery 被 Meta.Validate 明文禁止挂在串口
// 协议上）是对照组。这里逐字钉住，因为漏声明不会报任何错：协议只是不出现在
// 扫描页的下拉里，现场看起来就像"这个功能还没做"。
func TestScanCapabilitiesDeclared(t *testing.T) {
	seen := false
	for _, p := range meta().Protocols {
		if p.Name != protocolName {
			continue
		}
		seen = true
		if p.Discovery == nil {
			t.Errorf("协议 %q 未声明 Discovery，它不会出现在局域网扫描页的协议下拉里", p.Name)
		}
		if p.PointDiscovery == nil {
			t.Errorf("协议 %q 未声明 PointDiscovery，设备地址页不会出现「自动发现点位」按钮", p.Name)
		}
	}
	if !seen {
		t.Fatalf("协议 %q 不在 Meta.Protocols 中", protocolName)
	}
}

// TestProbersMatchDiscovery 声明了 Discovery 的协议必须有对应的 prober。
//
// driversdk.Serve 会在启动时做同样的检查，但那时已经是「插件被网关拉起」了，
// 失败的形态是插件进程起不来、协议整个消失；在这里失败能直接定位到 main.go 漏配。
func TestProbersMatchDiscovery(t *testing.T) {
	ps := probers()
	for _, p := range meta().Protocols {
		if p.Discovery == nil {
			continue
		}
		if _, ok := ps[p.Name]; !ok {
			t.Errorf("协议 %q 声明了 Discovery 但没有对应的 prober —— 插件启动即失败", p.Name)
		}
	}
	for name := range ps {
		if !hasProtocol(name) {
			t.Errorf("prober %q 不在 Meta.Protocols 中", name)
		}
	}
}

// TestPointFindersMatchPointDiscovery 声明了 PointDiscovery 的协议必须有对应的 finder。
// 理由同上（driversdk.Serve 的启动校验）。
func TestPointFindersMatchPointDiscovery(t *testing.T) {
	fs := finders()
	for _, p := range meta().Protocols {
		if p.PointDiscovery == nil {
			continue
		}
		if _, ok := fs[p.Name]; !ok {
			t.Errorf("协议 %q 声明了 PointDiscovery 但没有对应的 finder —— 插件启动即失败", p.Name)
		}
	}
	for name := range fs {
		if !hasProtocol(name) {
			t.Errorf("finder %q 不在 Meta.Protocols 中", name)
		}
	}
}

// hasProtocol 该协议名是否出现在 Meta 里。
func hasProtocol(name string) bool {
	for _, p := range meta().Protocols {
		if p.Name == name {
			return true
		}
	}
	return false
}

// TestScanSpecsMatchFieldReality 两项扫描声明的取值必须对得上现场事实。
//
// 端口写错会让扫描"静默地扫不到设备"——那是本功能最难查的一类故障
// （网关侧看起来一切正常）。所以这里把几个关键取值逐字钉住。
func TestScanSpecsMatchFieldReality(t *testing.T) {
	d := opcuadrv.Discovery()
	if d.DefaultPort != 4840 {
		t.Errorf("扫描默认端口 = %d，期望 4840（OPC UA 的注册端口，也是 LDS 的标准端口）",
			d.DefaultPort)
	}
	if d.TargetMode != driver.TargetModeUnicast {
		t.Errorf("目标模式 = %q，期望 %q —— 本插件没有 UDP/组播传输，"+
			"广播目标只会变成朝子网广播地址连 TCP", d.TargetMode, driver.TargetModeUnicast)
	}
	if d.Title == "" || d.Description == "" {
		t.Error("扫描声明缺少 Title 或 Description —— 它是用户理解判据的唯一来源")
	}
	if len(d.FormJSON) == 0 || !json.Valid(d.FormJSON) {
		t.Error("扫描表单缺失或不是合法 JSON —— 没有它前端会渲染出一个空表单，" +
			"用户无从配置超时（而超时是扫描速度唯一的调节点）")
	}
	if d.MaxBudgetMs < 500 || d.MaxBudgetMs > 60000 {
		t.Errorf("Discovery.MaxBudgetMs = %d，超出 Meta.Validate 的 [500,60000]", d.MaxBudgetMs)
	}

	p := opcuadrv.PointDiscovery()
	if p.Title == "" || p.Description == "" {
		t.Error("点扫声明缺少 Title 或 Description —— 它是用户理解「列出的是什么」的唯一来源")
	}
	if len(p.FormJSON) == 0 || !json.Valid(p.FormJSON) {
		t.Error("点扫表单缺失或不是合法 JSON")
	}
	if p.MaxPoints < 1 || p.MaxPoints > 20000 {
		t.Errorf("MaxPoints = %d，超出 Meta.Validate 的 [1,20000]", p.MaxPoints)
	}
	if p.MaxBudgetMs < 500 || p.MaxBudgetMs > 30000 {
		t.Errorf("PointDiscovery.MaxBudgetMs = %d，超出 Meta.Validate 的 [500,30000]", p.MaxBudgetMs)
	}
	// DefaultCommonDataType 必须是 TypeMap 的**键**（通用类型名）：
	// 写成内部裸名（"int64"）会被 Meta.Validate 拒掉，而那时插件已经起不来了。
	if _, ok := opcuadrv.CommonTypeMap()[p.DefaultCommonDataType]; !ok {
		t.Errorf("DefaultCommonDataType = %q 不是 TypeMap 的键（通用类型名）",
			p.DefaultCommonDataType)
	}
}

// TestTypesCoverAllMappedNames 类型表必须覆盖类型映射能到达的每一个内部名。
//
// 网关把 TypeMap 的 value（内部裸名）写进 device_address.data_type，采集时原样
// 回传给驱动的 Read；插件进程里 driversdk.Serve 又只会把 Meta.Types 里的项注册
// 进本进程的类型表。所以「TypeMap 里出现了 Types 里没有的类型名」就是
// 「该点位取不到 Kind」，而且现场不会有任何编译期提示。
//
// 反向也必须成立：**没有通用名入口**的类型照样要上报。gateway 的
// NormalizeAddressType 靠 reg.Get(data_type) 判断地址里存的内部名是否仍然有效，
// 漏报一个（OPC UA 的 int32/uint64/byteString 就没有 CommonDataTypes 里的入口）
// 会让历史点位被判为"类型漂移"、按 common_data_type 改写后**静默回写库**。
func TestTypesCoverAllMappedNames(t *testing.T) {
	types := opcuadrv.Types()
	have := make(map[string]bool, len(types))
	for _, dt := range types {
		have[dt.Name] = true
	}

	for name, internal := range opcuadrv.CommonTypeMap() {
		if !have[internal] {
			t.Errorf("通用类型 %q → 内部类型 %q 不在 Types() 里，该点位取不到 Kind", name, internal)
		}
	}

	// 这些类型没有通用名入口，因此上面那个循环碰不到它们 —— 单独钉住，
	// 保证"上报全部已注册类型"而不是"上报 TypeMap 能到达的那些"。
	for _, bare := range []string{"int32", "uint64", "byteString"} {
		if !have[bare] {
			t.Errorf("类型 %q 未被上报：它没有通用名入口，漏报会让历史点位被判为类型漂移", bare)
		}
	}
}

// TestTypesUseBareNames Types() 每项的 Name 都必须是裸名。
//
// 注册表里存的是带前缀的 "opcua.int16"，插件必须上报裸名 "int16"：
// 网关会给上报的类型名再拼一次 scope 前缀，否则得到
// "opcua.opcua.int16"，地址校验与类型下拉双双落空。
func TestTypesUseBareNames(t *testing.T) {
	types := opcuadrv.Types()
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
		if strings.Contains(dt.Name, ".") {
			t.Errorf("类型 %q 的 Name 带了 scope 前缀，网关会再拼一次得到无效名", dt.Name)
		}
	}
}

// TestTypeKindsPreserved Types() 上报的每一项都必须带上注册时的 Kind。
//
// 这条替代其它协议的 TestTypesCarryDecode：OPC UA 的类型本来就只有 Name + Kind
// （服务器返回原生值，不走字节级 Decode/Encode），所以这里丢的不是解析函数
// 而是 Kind。driversdk.Serve 会把 Types 里的指针**重新注册**进插件进程的注册表，
// 于是"只填 Name 的瘦身构造"会把 Kind 清成空字符串：
//
//   - Meta.Validate 会在 Kind 为空时拒绝加载（种情况能在启动期拦住）；
//   - 但**填错** Kind（例如把 datetime 当 string）不会报错，只会让推送通道
//     按错误的表示存值（时序库字段类型、JSON 序列化形态），比 panic 更难发现。
//
// 所以抽查的这几项要与 types.go 里 init() 注册的 Kind 逐字一致。
func TestTypeKindsPreserved(t *testing.T) {
	want := map[string]string{
		"bool":       driver.KindBool,
		"byte":       driver.KindUInt,
		"char":       driver.KindInt,
		"int16":      driver.KindInt,
		"uint16":     driver.KindUInt,
		"int32":      driver.KindInt,
		"uint32":     driver.KindUInt,
		"int64":      driver.KindInt,
		"uint64":     driver.KindUInt,
		"float32":    driver.KindFloat,
		"float64":    driver.KindFloat,
		"string":     driver.KindString,
		"byteString": driver.KindString,
		"datetime":   driver.KindTime,
	}

	got := make(map[string]string, len(want))
	for _, dt := range opcuadrv.Types() {
		got[dt.Name] = dt.Kind
	}
	if len(got) != len(want) {
		t.Errorf("上报类型数 %d != 已注册类型数 %d（types.go 的 init() 注册 14 个）",
			len(got), len(want))
	}
	for name, kind := range want {
		k, ok := got[name]
		if !ok {
			t.Errorf("类型 %q 未被上报", name)
			continue
		}
		if k != kind {
			t.Errorf("类型 %q 的 Kind = %q, 期望 %q（Kind 丢了会让推送通道按错误类型存值）",
				name, k, kind)
		}
	}
}
