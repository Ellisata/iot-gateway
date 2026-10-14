// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"iot-gateway/driversrc/modbus/modbusdrv"
)

// TestMetaValid 插件清单必须能通过 SDK 自校验。
//
// 这条测试替代的是旧实现里「从内置注册表派生类型表」这一步隐含的校验：
// 那份派生代码只要有一个类型对不上就会当场报错，改成自带的类型表后，
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
// 这是本文件里最重要的三条字面量：`device.protocol_id` → `iot_protocol.name`
// → `driver.Register` 是一条纯字符串的链，改任何一个字符都不会有编译期提示，
// 症状是**既有设备被判为「协议不支持」而静默跳过**（collector 只记一行日志），
// 而设备列表、前端下拉、健康检查全都正常 —— 现场可能几周后才察觉。
//
// 字面量刻意写死、不从别处派生：它要挡住的正是"有人顺手把名字改得更整齐"。
// 更严格的机器生成版在 pluginhost/testdata/parity/typemap/modbus.json
// （由内置实现生成），pluginhost 的 parity 测试会拿它比对。
func TestProtocolIdentityPinned(t *testing.T) {
	cases := []struct{ got, want, what string }{
		{protocolTCP, "ModBus.TCP", "TCP 协议名"},
		{protocolRTU, "ModBus.RTU", "RTU 协议名"},
		{pluginScope, "modbus", "类型 scope"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q，内置时代的原值是 %q —— 改成别的会让既有设备静默采不到值",
				c.what, c.got, c.want)
		}
	}
}

// TestSerialExclusivePerProtocol 串行独占是按协议而非按插件声明的：
// RTU 独占 RS-485 总线（采集引擎整轮持锁），TCP 不独占。
// 声明错了不会报错，只会让串口上的帧交错或者让 TCP 白白丢掉并发 —— 因此钉住。
func TestSerialExclusivePerProtocol(t *testing.T) {
	for _, p := range meta().Protocols {
		want := p.Name == protocolRTU
		if p.SerialExclusive != want {
			t.Errorf("协议 %q 的 SerialExclusive = %v, 期望 %v", p.Name, p.SerialExclusive, want)
		}
		if len(p.FormJSON) == 0 {
			t.Errorf("协议 %q 缺少配置表单（FormJSON）", p.Name)
		}
	}
}

// TestEveryMappedTypeDecodable 网关会下发的每一个内部类型名，插件都必须能解析。
//
// 网关把 TypeMap 的 value（内部裸名）写进 device_address.data_type，
// 采集时原样回传给驱动的 Read。所以「TypeMap 里出现了插件解析不了的类型名」
// 就是「该点位每轮都采出空值」，而且现场不会有任何编译期提示。
//
// 用 16 字节缓冲覆盖最长类型（float64 8 字节、string 默认跨度 16 字节）。
func TestEveryMappedTypeDecodable(t *testing.T) {
	// 用非零字节：全零缓冲会让 string 解析成空串，测不出"值被正常格式化"。
	raw := bytes.Repeat([]byte{'A'}, 16)

	for name, internal := range modbusdrv.CommonTypeMap() {
		got, err := modbusdrv.ParseReadResult(raw, internal, modbusdrv.ByteOrderBigEndian, modbusdrv.WordOrderBigEndian)
		if err != nil {
			t.Errorf("通用类型 %q → 内部类型 %q 无法解析: %v", name, internal, err)
			continue
		}
		if v := modbusdrv.FormatValue(got, internal); v == "" {
			t.Errorf("通用类型 %q → 内部类型 %q 格式化后为空串", name, internal)
		}
	}
}

// ==================== 表单 ↔ 解析器契约 ====================

// formFields 取出表单 rule 里的字段名集合（并顺带检查 field/title 的完整性）。
func formFields(t *testing.T, raw, which string) map[string]bool {
	t.Helper()
	var form struct {
		Rule []struct {
			Field string `json:"field"`
			Title string `json:"title"`
			Value any    `json:"value"`
		} `json:"rule"`
	}
	if err := json.Unmarshal([]byte(raw), &form); err != nil {
		t.Fatalf("解析 %s 表单失败: %v", which, err)
	}
	if len(form.Rule) == 0 {
		t.Fatalf("%s 表单没有任何字段", which)
	}

	out := make(map[string]bool, len(form.Rule))
	for _, r := range form.Rule {
		if r.Field == "" {
			t.Errorf("%s 表单里有一项没有 field（title=%q）—— 它提交的键无法映射到配置", which, r.Title)
			continue
		}
		if out[r.Field] {
			t.Errorf("%s 表单里的 field %q 重复", which, r.Field)
		}
		if r.Title == "" {
			t.Errorf("%s 表单字段 %q 缺少中文标题", which, r.Field)
		}
		out[r.Field] = true
	}
	return out
}

// TestFormsMatchConfigStruct 表单里的每个字段都必须是解析器认识的键。
//
// 反方向（解析器认识、表单没暴露）**刻意不在此断言**：mergeWindow / stringLen
// 属于这种情况，它们有合理默认值、不必端到现场；而把这条做成双向等于会连带
// 要求改动 TCP 表单，超出本次范围。真正要紧的那一处由
// TestSerialParamsAreConfigurable 单独钉死。
//
// 本测试要挡住的症状是"用户填了那个参数，但它根本不生效" —— 没有报错、没有
// 日志，表单看起来一切正常。ModBus.RTU 的 baudRate/dataBits/stopBits/parity
// 曾经整整缺了一轮（表单没有、解析器也没声明），根源就是缺这条测试。
func TestFormsMatchConfigStruct(t *testing.T) {
	forms := map[string]string{protocolTCP: formTCP, protocolRTU: formRTU}
	transports := map[string]string{protocolTCP: "TCP", protocolRTU: "RTU"}

	for proto, raw := range forms {
		t.Run(proto, func(t *testing.T) {
			valid := make(map[string]bool)
			for _, name := range modbusdrv.ConfigFieldNames(transports[proto]) {
				valid[name] = true
			}
			for field := range formFields(t, raw, proto) {
				if !valid[field] {
					t.Errorf("表单字段 %q 不在配置结构体里 —— 用户填了也不生效", field)
				}
			}
		})
	}
}

// TestSerialParamsAreConfigurable ModBus.RTU 的串口链路参数必须**既在表单里、
// 又被解析器读取**。
//
// 这条是上面那条的子集断言，故意写死字段清单：即使将来有人给 ConfigFieldNames
// 开了例外、或把某个字段从表单里摘掉，这一条也绕不过去。
// 参照 iot-gateway 的 TestMigrationRTUFormExposesSerialParams。
func TestSerialParamsAreConfigurable(t *testing.T) {
	got := formFields(t, formRTU, protocolRTU)
	for _, want := range []string{"comPort", "baudRate", "dataBits", "stopBits", "parity", "timeoutMs"} {
		if !got[want] {
			t.Errorf("ModBus.RTU 表单缺少串口字段 %q —— 非默认链路的设备将无法配置", want)
		}
	}

	// 端口必须是自由文本框：写死的 select 选项覆盖不了现场端口号
	// （COM7、/dev/ttyUSB1 …），而前端并未给 select 开 filterable/allow-create。
	var form struct {
		Rule []struct {
			Field string `json:"field"`
			Type  string `json:"type"`
		} `json:"rule"`
	}
	if err := json.Unmarshal([]byte(formRTU), &form); err != nil {
		t.Fatalf("解析表单失败: %v", err)
	}
	for _, r := range form.Rule {
		if r.Field == "comPort" && r.Type != "input" {
			t.Errorf("comPort 的控件类型 = %q，必须是 input（自由文本）", r.Type)
		}
	}
}

// TestFormDefaultsArePresent 串口表单每一项都必须带默认值。
//
// 本驱动的解析靠"字段缺席 → 用驱动默认值"兜底，表单一旦把某个字段渲染成空，
// 用户就看不出"不填会发生什么"；而把解析器实际会用的值填进表单，
// 提交与不提交的行为才一致（照 dlt645/main.go 的做法）。
// 只对串口表单断言：TCP 表单本次不动，它上面的既有字段没有默认值。
func TestFormDefaultsArePresent(t *testing.T) {
	var form struct {
		Rule []struct {
			Field string          `json:"field"`
			Value json.RawMessage `json:"value"`
		} `json:"rule"`
	}
	if err := json.Unmarshal([]byte(formRTU), &form); err != nil {
		t.Fatalf("解析表单失败: %v", err)
	}
	for _, r := range form.Rule {
		if len(r.Value) == 0 {
			t.Errorf("ModBus.RTU 表单字段 %q 没有默认值（value）", r.Field)
		}
	}
}

// TestProbersMatchDiscovery 声明了扫描能力的协议必须有对应的 prober，
// 反之 prober 也必须落在声明了能力的协议上。
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
			t.Errorf("协议 %q 声明了 Discovery 却没有对应的 prober", p.Name)
		}
	}
	for name := range ps {
		if !declared[name] {
			t.Errorf("协议 %q 有 prober 但没声明 Discovery，它永远不会被调用", name)
		}
	}
}

// TestRTUDeniesDiscovery 串口协议不能声明扫描能力。
//
// 扫描的目标模型是 host/port，表达不了 comPort；串口上"总线上有哪些站号"
// 是另一个功能。driversdk.Meta.Validate 会拒绝这个组合（启动即失败），
// 因此这里钉住它不会被误加回来。
func TestRTUDeniesDiscovery(t *testing.T) {
	for _, p := range meta().Protocols {
		if p.Name == protocolRTU && p.Discovery != nil {
			t.Error("ModBus.RTU 是串口协议，不能声明局域网扫描能力")
		}
	}
}

// TestDiscoverySpecPassesValidation 扫描声明的取值范围必须能过 Meta.Validate ——
// 越界会让**整个插件**在网关侧加载失败（不是只有扫描功能不可用）。
func TestDiscoverySpecPassesValidation(t *testing.T) {
	if err := meta().Validate(); err != nil {
		t.Fatalf("Meta 校验失败（含扫描声明）: %v", err)
	}
	d := modbusdrv.Discovery()
	if d.DefaultPort != 502 {
		t.Errorf("默认端口 = %d，期望 502", d.DefaultPort)
	}
	if len(d.FormJSON) == 0 {
		t.Error("必须给扫描表单：没有它前端会渲染出一个空表单，用户无从配置站号与超时")
	}
}

// ==================== 点位扫描 ====================

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
			t.Errorf("协议 %q 声明了 PointDiscovery 却没有对应的 finder", p.Name)
		}
	}
	for name := range fs {
		if !declared[name] {
			t.Errorf("协议 %q 有 finder 但没声明 PointDiscovery，它永远不会被调用", name)
		}
	}
}

// TestPointDiscoveryAllowedOnSerial 点位扫描**必须**对串口协议开放。
//
// 这条与 TestRTUDeniesDiscovery 成对，钉的是那条刻意的不对称：局域网扫描
// 拒绝串口（目标是 host/port，表达不了 comPort），点位扫描则相反 ——
// 目标是**一台已存在的设备**，串口设备同样有"它有哪些点位"这个问题，
// 而且串口恰是会话复用最重要的场景（回落临时实例会抢占串口句柄打断采集）。
//
// 反例也一并钉住：真有谁"顺手统一"成两边都禁，RTU 的点位扫描会静默消失，
// 而前端只会看到一个 disabled 的按钮。
func TestPointDiscoveryAllowedOnSerial(t *testing.T) {
	sawSerial := false
	for _, p := range meta().Protocols {
		if p.PointDiscovery == nil {
			t.Errorf("协议 %q 应当声明点位扫描能力", p.Name)
			continue
		}
		if p.SerialExclusive {
			sawSerial = true
		}
	}
	if !sawSerial {
		t.Error("没有任何串口协议声明点位扫描 —— 串口恰是该能力最重要的场景")
	}
	if err := meta().Validate(); err != nil {
		t.Fatalf("Meta 校验失败（含点位扫描声明）: %v", err)
	}
}

// TestPointDiscoverySpecPassesValidation 点扫声明的取值范围必须能过 Meta.Validate。
//
// 越界会让**整个插件**加载失败（不是只有点扫不可用），失败得很响但也很难定位。
func TestPointDiscoverySpecPassesValidation(t *testing.T) {
	d := modbusdrv.PointDiscovery()
	if len(d.FormJSON) == 0 {
		t.Error("必须给点扫表单：没有它前端会渲染出一个空表单，用户无从指定功能区与范围")
	}
	if d.Title == "" || d.Description == "" {
		t.Error("Title 与 Description 都是用户理解本功能性质的来源，不能为空")
	}
	// DefaultCommonDataType 必须是**通用类型名**（TypeMap 的 key）。写成内部裸名
	// "word" 会被 Meta.Validate 拒掉 —— 上面那条 Validate 调用已经覆盖，
	// 这里再显式钉一次字面量，免得有人把它当成"内部名"改掉。
	if d.DefaultCommonDataType != "Word" {
		t.Errorf("DefaultCommonDataType = %q，期望通用类型名 \"Word\"", d.DefaultCommonDataType)
	}
	if _, ok := modbusdrv.CommonTypeMap()[d.DefaultCommonDataType]; !ok {
		t.Errorf("%q 不是 TypeMap 的键", d.DefaultCommonDataType)
	}
}
