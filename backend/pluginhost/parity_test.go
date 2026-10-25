// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ellisata/iot-gateway-drivers/testutil/fake"
	"github.com/Ellisata/iot-gateway-drivers/testutil/parity"
	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/model/po"
)

// ==================== 行为等价的共享基建 ====================
//
// 协议驱动从内置实现搬成进程外插件后，"同一份现场配置必须采出相同的值"这条
// 承诺失去了对照物 —— 仓库里不再有第二份实现。等价性因此从「跨实现」改为
// 「跨时间」：
//
//	阶段 0  用**内置驱动** + 假 PLC 跑一遍 testdata/parity/addresses.json，
//	        把逐点位的 Value/DataType/Kind/Quality 冻进 <case>.golden.json；
//	阶段 2+ 用**插件驱动**跑同一份 addresses.json，与黄金逐项比对。
//
// 黄金文件的读写格式定义在 testutil/parity（普通包），因为 driversrc/ 下的
// 插件（package main）也要读同一批文件。本文件是消费侧的**驱动侧**基建：
// 拉起假 PLC、把共享输入渲染成协议 JSON、跑一轮 Read。
//
// 覆盖面：帧格式、地址解析、区间规划、类型映射、格式化，外加插件侧的
// driversdk 帧编解码与 client.go 的 RPC 链路（内置版没有这一段）。
//
// **已知局限**：testutil/fake 的假 PLC 一律回定长全零数据，所以除 OPC UA 外
// 各协议的 Value 恒为 "0"/空串 —— 黄金比对能抓住"帧没对上、索引错位、Quality
// 掉了、DataType/Kind 变了"，抓不住"字节序或解码算式改了"。后者由随实现一起
// 搬进插件包的 5800 行单测（parser_test / frame_test / driver_test 等）守着，
// 它们与实现同目录、同时被搬，覆盖不会丢。
//
// **一处手工编辑**：opcua.golden.json 的 "missing" 条目由 quality 0 改为 1。
// 那一点（ns=1;i=777）服务器回 BadNodeIdUnknown，内置实现在阶段 0 记的是裸
// Quality=0；后来采集质量成因并入 Quality（driver.QualityXxx 的坏子码）之后，
// 同一个事实改由 QualityBadAddress=1 表达 —— **信息相同、编码不同**，故改的是
// 编码而不是放宽了断言。这是整批黄金里唯一被动过的一条（其余 47 处仍是 192，
// 假 PLC 只回零值），改动理由见 CHANGELOG。

// parityCase 一条等价性用例的输入。
type parityCase struct {
	Protocol  string          `json:"protocol"`
	Scope     string          `json:"scope"`
	Fake      string          `json:"fake"`
	Connect   map[string]any  `json:"connect"`
	Addresses []parityAddress `json:"addresses"`
}

// parityAddress 是 po.DeviceAddress 的投影。
//
// 只留驱动真正读得到的三个字段 —— 与 RPC 层给插件的投影一致
// （见 docs/driver-plugins.md §3.3），这样内置版与插件版收到的输入逐字相同。
type parityAddress struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	DataType string `json:"dataType"`
}

// parityDataDir 黄金文件与共享输入的目录。
func parityDataDir(t *testing.T) string {
	t.Helper()

	dir, err := parity.DataDir()
	if err != nil {
		t.Fatalf("定位 parity 数据目录失败: %v", err)
	}
	return dir
}

// loadParityCases 读取共享输入，返回 case 名 → 用例。
func loadParityCases(t *testing.T) map[string]parityCase {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(parityDataDir(t), "addresses.json"))
	if err != nil {
		t.Fatalf("读取 parity 输入失败: %v", err)
	}

	// 先解成 RawMessage 再逐个解码：文件头有 "_comment" 这类说明字段，
	// 形状与用例不同，直接解进 map[string]parityCase 会在这里炸掉。
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("解析 parity 输入失败: %v", err)
	}

	cases := make(map[string]parityCase, len(entries))
	for name, body := range entries {
		if strings.HasPrefix(name, "_") {
			continue
		}
		var c parityCase
		if err := json.Unmarshal(body, &c); err != nil {
			t.Fatalf("解析 parity 用例 %q 失败: %v", name, err)
		}
		cases[name] = c
	}
	if len(cases) == 0 {
		t.Fatal("parity 输入为空")
	}
	return cases
}

// startParityFake 按 case 的 fake 字段启动对应假 PLC。
func startParityFake(t *testing.T, name string) *fake.Server {
	t.Helper()

	var (
		srv *fake.Server
		err error
	)
	switch name {
	case "modbus-tcp":
		srv, err = fake.NewModbusTCP()
	case "mc3e":
		srv, err = fake.NewMC3E()
	case "s7":
		srv, err = fake.NewS7()
	case "omron-cip":
		srv, err = fake.NewCIP()
	case "rockwell-cip":
		srv, err = fake.NewCIPAB()
	case "fins-tcp":
		srv, err = fake.NewFINSTCP()
	case "opcua":
		srv, err = fake.NewOpcUa()
	default:
		t.Fatalf("未知的假 PLC %q —— 请同时在 startParityFake 与 addresses.json 的说明里登记", name)
	}
	if err != nil {
		t.Fatalf("启动假 PLC %s 失败: %v", name, err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// parityProtocolJSON 把 case 的 connect 渲染成协议 JSON，port=0 时填入假服务器端口。
func parityProtocolJSON(t *testing.T, c parityCase, port int) string {
	t.Helper()

	conn := make(map[string]any, len(c.Connect))
	for k, v := range c.Connect {
		conn[k] = v
	}
	if p, ok := conn["port"]; ok {
		if f, ok := p.(float64); ok && f == 0 {
			conn["port"] = port
		}
	}
	raw, err := json.Marshal(conn)
	if err != nil {
		t.Fatalf("渲染协议 JSON 失败: %v", err)
	}
	return string(raw)
}

// deviceAddresses 把投影还原成 po.DeviceAddress（其余字段留零值）。
func (c parityCase) deviceAddresses() []po.DeviceAddress {
	out := make([]po.DeviceAddress, len(c.Addresses))
	for i, a := range c.Addresses {
		out[i] = po.DeviceAddress{ID: a.ID, Name: a.Name, DataType: a.DataType}
	}
	return out
}

// readParity 跑一轮采集并投影成可比对结果。
//
// 顺便校验驱动契约（见 docs/driver-plugins.md §3.2）：数量与 ID 必须与 addrs
// 逐项对应。不校验的话，一个"少回一个点位"的实现会让比对在错位的数组上
// 逐项比较，报出一堆看不出根因的 value 不符。
func readParity(t *testing.T, d driver.Driver, c parityCase) []parity.Result {
	t.Helper()

	addrs := c.deviceAddresses()
	got, err := d.Read(sdkAddrs(addrs))
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if len(got) != len(addrs) {
		t.Fatalf("Read 返回 %d 项，请求了 %d 项（驱动契约要求索引一一对应）", len(got), len(addrs))
	}

	out := make([]parity.Result, len(got))
	for i, r := range got {
		if r.DeviceAddressID != addrs[i].ID {
			t.Fatalf("第 %d 项 ID = %q，请求的是 %q（顺序错位；网关会退回按 ID 查名称，但海量点位下会明显变慢）",
				i, r.DeviceAddressID, addrs[i].ID)
		}
		out[i] = parity.Result{
			ID:       r.DeviceAddressID,
			Value:    r.Value,
			DataType: r.DataType,
			Kind:     r.Kind,
			Quality:  r.Quality,
		}
	}
	return out
}

// assertGoldenMatches 逐点位比对实际结果与黄金。
func assertGoldenMatches(t *testing.T, caseName string, got []parity.Result) {
	t.Helper()

	g, err := parity.LoadGolden(caseName)
	if err != nil {
		t.Fatalf("%v\n（黄金基线由内置实现在阶段 0 生成，内置驱动删除后就再也生成不出来了）", err)
	}
	for _, d := range parity.CompareResults(got, g.Results) {
		t.Error(d)
	}
}

// registeredProtocols 返回当前注册在 scope 下的全部协议名。
//
// driver 只导出「协议名 → scope」（ScopeOf）与「全部协议名」（Registered），
// 没有反查接口 —— 反向索引由这里现场构造，省得为一条测试加一个生产 API。
func registeredProtocols(scope string) []string {
	var out []string
	for _, protocol := range driver.Registered() {
		if driver.ScopeOf(protocol) == scope {
			out = append(out, protocol)
		}
	}
	return out
}

// assertTypeMapMatchesGolden 比对当前类型表与黄金 —— 即前端类型下拉的完整内容。
//
// 这是"类型映射漏项"最直接的防线：driver.AvailableTypes 会因为
// "映射表里有、注册表里没有"而**静默隐藏**一个类型，前端下拉少一项、
// 日志里一个字都没有（见 driver/dataTypeMap.go 的 AvailableTypes 注释）。
func assertTypeMapMatchesGolden(t *testing.T, scope string) {
	t.Helper()

	want, err := parity.LoadTypeMap(scope)
	if err != nil {
		t.Fatalf("%v", err)
	}
	common, extended := driver.AvailableTypes(scope)

	if !parity.EqualStrings(common, want.Common) {
		t.Errorf("scope %s 的通用类型 = %v，黄金 %v\n"+
			"（前端类型下拉会少项/多项，且不会有任何日志）", scope, common, want.Common)
	}
	if !parity.EqualStrings(extended, want.Extended) {
		t.Errorf("scope %s 的扩展类型 = %v，黄金 %v", scope, extended, want.Extended)
	}
}

// assertProtocolsMatchGolden 比对协议名与 scope —— 契约里最容易抄错、
// 抄错后症状最隐蔽的一环。
//
// 协议名写错一个字符，用它的设备会被 collector 判为「协议不支持」而**静默跳过**
// （只有一行日志），前端协议下拉、设备列表、健康检查全都正常。
func assertProtocolsMatchGolden(t *testing.T, scope string, protocols []string) {
	t.Helper()

	want, err := parity.LoadTypeMap(scope)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if want.Scope != scope {
		t.Errorf("黄金文件记录的 scope 是 %q，本次用的却是 %q", want.Scope, scope)
	}
	got, wantP := parity.SortedCopy(protocols), parity.SortedCopy(want.Protocols)
	if !parity.EqualStrings(got, wantP) {
		t.Errorf("scope %s 的协议名 = %v，黄金 %v\n"+
			"（多/少/错一个字符都会让既有设备静默采不到值）", scope, got, wantP)
	}
}
