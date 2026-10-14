// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

// 欧姆龙两个插件的**扫描能力**验收：声明能穿过进程边界被网关读到，
// 以及探测真的能在真实插件进程上跑通。
//
// 为什么单开一份而不是全靠驱动包里的测试：驱动包的测试直接调 `ProbeUDP` /
// `Probe`，跳过了整条 `pluginwire` → `driversdk.handleProbe` → prober 的链路。
// 那条链路上任何一处对不上（方法的载荷形状、预算单位、选项的 string/RawMessage
// 转换）都不会在驱动包测试里暴露 —— 而它恰恰是"插件能跑"与"网关能用"之间的全部
// 距离。fixture 插件的 `TestProbeTargetsEndToEnd` 验证了链路本身，
// 这里补的是"这两个**真实**插件的声明与实现确实接上了"。

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/testutil/fake"
)

// fakeTarget 把一个 "host:port" 地址转成探测目标。
func fakeTarget(t *testing.T, addr string) driver.ScanTarget {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("拆分地址 %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("端口 %q 不是数字: %v", portStr, err)
	}
	return driver.ScanTarget{Host: host, Port: port}
}

// TestFinsPluginScanCapabilities FINS 的两项扫描能力必须无损过线，
// 且**只挂在两个以太网协议上**。
//
// 串口按契约不得声明 Discovery（driversdk.Meta.Validate 会拒），
// HostLinkTCP 连的是串口服务器而不是 PLC —— 扫一个网段只会扫出一堆串口服务器。
// 这两条在驱动包的 main_test.go 里已逐条断言，这里验证的是它们在**线协议上**
// 仍然是这个形状（漏一个字段、错一个端口，前端就会渲染出错的表单或回填错的端口）。
func TestFinsPluginScanCapabilities(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}
	m, _ := startPluginManager(t, "./driversrc/fins", nil)

	want := map[string]bool{
		"Omron.FINS.UDP":         true,
		"Omron.FINS.TCP":         true,
		"Omron.FINS.Serial":      false,
		"Omron.FINS.HostLinkTCP": false,
	}
	seen := map[string]bool{}

	for _, pd := range m.DiscoverySpecs() {
		w, ok := want[pd.Protocol]
		if !ok {
			continue // 别的插件的协议（本用例只关心 FINS 这四个）
		}
		seen[pd.Protocol] = true
		if !w {
			t.Errorf("协议 %q 不该声明设备扫描", pd.Protocol)
			continue
		}
		if pd.Spec.DefaultPort != 9600 {
			t.Errorf("协议 %q 的默认端口 = %d，期望 9600", pd.Protocol, pd.Spec.DefaultPort)
		}
		if pd.Spec.TargetMode != driver.TargetModeUnicast {
			t.Errorf("协议 %q 的目标模式 = %q，期望 unicast（FINS 的扫描是逐主机单播）",
				pd.Protocol, pd.Spec.TargetMode)
		}
		if len(pd.Spec.FormJSON) == 0 {
			t.Errorf("协议 %q 的扫描表单没过来", pd.Protocol)
		}
	}
	for name, w := range want {
		if w && !seen[name] {
			t.Errorf("协议 %q 声明了设备扫描却没出现在 DiscoverySpecs 里 —— "+
				"它不会出现在扫描页的协议下拉中", name)
		}
	}

	// 点位扫描：同样只给两个以太网协议。
	seenPoints := map[string]bool{}
	for _, pd := range m.PointDiscoverySpecs() {
		if _, ok := want[pd.Protocol]; !ok {
			continue
		}
		seenPoints[pd.Protocol] = true
		if pd.Spec.DefaultCommonDataType != driver.TypeWord {
			t.Errorf("协议 %q 的 DefaultCommonDataType = %q，期望 %q",
				pd.Protocol, pd.Spec.DefaultCommonDataType, driver.TypeWord)
		}
		if pd.Spec.MaxPoints <= 0 {
			t.Errorf("协议 %q 的 MaxPoints = %d，必须为正", pd.Protocol, pd.Spec.MaxPoints)
		}
	}
	for _, name := range []string{"Omron.FINS.UDP", "Omron.FINS.TCP"} {
		if !seenPoints[name] {
			t.Errorf("协议 %q 声明了点位扫描却没出现在 PointDiscoverySpecs 里 —— "+
				"设备地址页不会出现「自动发现点位」按钮", name)
		}
	}
	if seenPoints["Omron.FINS.Serial"] || seenPoints["Omron.FINS.HostLinkTCP"] {
		t.Error("串口与 Host Link over TCP 的会话复用路径尚未验证，不该声明点位扫描")
	}
}

// TestOmronCipPluginScanCapabilities CIP 只声明设备扫描，且目标模式是 Both。
//
// `TargetMode=both` 是这个协议扫得到设备的关键：广播是 ListIdentity 的标准用法
// （一条数据报覆盖整个网段），逐主机单播是禁广播的受管网络上的兜底。
// 只声明其中一个，现场都会出现"扫不到"而看不出原因的情形。
func TestOmronCipPluginScanCapabilities(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}
	m, _ := startPluginManager(t, "./driversrc/omroncip", nil)

	var found bool
	for _, pd := range m.DiscoverySpecs() {
		if pd.Protocol != "Omron.CIP" {
			continue
		}
		found = true
		if pd.Spec.DefaultPort != 44818 {
			t.Errorf("默认端口 = %d，期望 44818", pd.Spec.DefaultPort)
		}
		if pd.Spec.TargetMode != driver.TargetModeBoth {
			t.Errorf("目标模式 = %q，期望 both", pd.Spec.TargetMode)
		}
		if len(pd.Spec.FormJSON) == 0 {
			t.Error("扫描表单没过来")
		}
	}
	if !found {
		t.Fatal("Omron.CIP 的扫描能力没穿过进程边界 —— 扫描页不会列出它")
	}

	// CIP 刻意不做点位扫描：它的点位是符号名，不是可枚举的地址空间。
	for _, pd := range m.PointDiscoverySpecs() {
		if pd.Protocol == "Omron.CIP" {
			t.Error("Omron.CIP 不该声明点位扫描（见 omroncip/main.go 里的说明）")
		}
	}
}

// TestFinsProbeEndToEnd 真实的 FINS 插件进程探测真实的 FINS/UDP 假 PLC。
//
// 走完整链路：网关 → pluginwire → driversdk.handleProbe → finsdrv.ProbeUDP。
// 断言到"设备与型号都正确回来"这一级 —— 型号来自扫描过程中额外发的
// 控制器数据读取（0x0501），它能不能过线只有这条用例说了算。
func TestFinsProbeEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}
	srv, err := fake.NewFINSUDP()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	m, _ := startPluginManager(t, "./driversrc/fins", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	reply, err := m.ProbeTargets(ctx, "Omron.FINS.UDP", nil, fakeTarget(t, srv.Addr()))
	if err != nil {
		t.Fatalf("ProbeTargets: %v", err)
	}
	if len(reply.Devices) != 1 {
		t.Fatalf("应发现 1 台设备，实际 %d 台: %+v", len(reply.Devices), reply.Devices)
	}
	d := reply.Devices[0]
	if d.Model != "CJ2M-CPU31" {
		t.Errorf("型号 = %q，期望 CJ2M-CPU31 —— 控制器数据读取这一步没走过线，"+
			"或型号解析在插件进程里坏了", d.Model)
	}
	if d.ProtocolJSON == "" {
		t.Error("连接配置为空，一键建设备会建出一台连不上的设备")
	}
	if d.UnitID != 1 {
		t.Errorf("UnitID = %d，期望 1（127.0.0.1 的 FINS 节点号）", d.UnitID)
	}
}

// TestOmronCipProbeEndToEnd 真实的 CIP 插件进程探测真实的 ListIdentity 假设备。
//
// 广播模式在这里用单播目标模拟（假设备监听在 127.0.0.1，用广播地址打不到它），
// 因此这条用例验证的是**帧的构造与解析穿过进程边界仍然正确**；
// 广播目标的多设备收集由 omroncipdrv 的单元测试覆盖。
func TestOmronCipProbeEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}
	srv, err := fake.NewOmronCIPDiscovery(fake.OmronNJ501("127.0.0.1", 44818))
	if err != nil {
		t.Fatalf("起假设备: %v", err)
	}
	defer srv.Close()

	m, _ := startPluginManager(t, "./driversrc/omroncip", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	reply, err := m.ProbeTargets(ctx, "Omron.CIP", nil, fakeTarget(t, srv.Addr()))
	if err != nil {
		t.Fatalf("ProbeTargets: %v", err)
	}
	if len(reply.Devices) != 1 {
		t.Fatalf("应发现 1 台设备，实际 %d 台: %+v", len(reply.Devices), reply.Devices)
	}
	d := reply.Devices[0]
	if d.Host != "127.0.0.1" {
		t.Errorf("Host = %q，期望 127.0.0.1", d.Host)
	}
	if d.Model != "NJ501-1300" {
		t.Errorf("型号 = %q，期望 NJ501-1300 —— 产品名在过线时丢了，"+
			"或 Identity 项的解析在插件进程里坏了", d.Model)
	}
	if d.Vendor != "Omron" {
		t.Errorf("Vendor = %q，期望 Omron（ListIdentity 的厂商字段是设备自述的）", d.Vendor)
	}
}

// TestRockwellCipPluginScanCapabilities Rockwell 两项扫描能力都要无损过线。
//
// 这个插件与 Omron.CIP 的差异值得在这里被看见：同样是 EtherNet/IP、同样
// TargetMode=both，但 Rockwell **还**声明了点位扫描（原生枚举），
// 而 Omron.CIP 刻意没有。两条能力各查一遍，是因为它们在网关侧走的是两张独立的
// 能力表（DiscoverySpecs / PointDiscoverySpecs），漏一张的表现不同：前者是
// 协议下拉里没有它，后者是地址页的按钮置灰。
func TestRockwellCipPluginScanCapabilities(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}
	m, _ := startPluginManager(t, "./driversrc/rockwellcip", nil)

	var found bool
	for _, pd := range m.DiscoverySpecs() {
		if pd.Protocol != "Rockwell.CIP" {
			continue
		}
		found = true
		if pd.Spec.DefaultPort != 44818 {
			t.Errorf("默认端口 = %d，期望 44818", pd.Spec.DefaultPort)
		}
		if pd.Spec.TargetMode != driver.TargetModeBoth {
			t.Errorf("目标模式 = %q，期望 both", pd.Spec.TargetMode)
		}
		if len(pd.Spec.FormJSON) == 0 {
			t.Error("扫描表单没过来")
		}
	}
	if !found {
		t.Fatal("Rockwell.CIP 的扫描能力没穿过进程边界 —— 扫描页不会列出它")
	}

	var foundPoints bool
	for _, pd := range m.PointDiscoverySpecs() {
		if pd.Protocol != "Rockwell.CIP" {
			continue
		}
		foundPoints = true
		if pd.Spec.Title == "" || pd.Spec.Description == "" {
			t.Error("点位扫描的标题/说明没过来 —— 弹窗里最要紧的就是那段性质说明")
		}
		// 默认类型必须是**通用类型名**（TypeMap 的键），不是内部裸名。
		if pd.Spec.DefaultCommonDataType != driver.TypeLong {
			t.Errorf("DefaultCommonDataType = %q，期望 %q",
				pd.Spec.DefaultCommonDataType, driver.TypeLong)
		}
		if pd.Spec.MaxPoints <= 0 || pd.Spec.MaxBudgetMs <= 0 {
			t.Errorf("MaxPoints/MaxBudgetMs = %d/%d，都必须为正",
				pd.Spec.MaxPoints, pd.Spec.MaxBudgetMs)
		}
	}
	if !foundPoints {
		t.Fatal("Rockwell.CIP 的点位扫描能力没穿过进程边界 —— 地址页不会出现「自动发现点位」按钮")
	}
}

// TestRockwellCipProbeEndToEnd 真实的 Rockwell 插件进程探测真实的 ListIdentity 假设备。
//
// 这条用例同时把 `cipcore` 的**跨插件目录复用**验到底：帧的构造发生在
// rockwellcipdrv/probe.go，而它 import 的是 omroncip/cipcore —— 两者只要有一边
// 的构建出问题，这里就跑不起来。
func TestRockwellCipProbeEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}
	srv, err := fake.NewRockwellCIPDiscovery(fake.RockwellL83E("127.0.0.1", 44818))
	if err != nil {
		t.Fatalf("起假设备: %v", err)
	}
	defer srv.Close()

	m, _ := startPluginManager(t, "./driversrc/rockwellcip", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	reply, err := m.ProbeTargets(ctx, "Rockwell.CIP", nil, fakeTarget(t, srv.Addr()))
	if err != nil {
		t.Fatalf("ProbeTargets: %v", err)
	}
	if len(reply.Devices) != 1 {
		t.Fatalf("应发现 1 台设备，实际 %d 台: %+v", len(reply.Devices), reply.Devices)
	}
	d := reply.Devices[0]
	if d.Host != "127.0.0.1" {
		t.Errorf("Host = %q，期望 127.0.0.1", d.Host)
	}
	if d.Model != "1756-L83E" {
		t.Errorf("型号 = %q，期望 1756-L83E —— 产品名在过线时丢了，"+
			"或 Identity 项的解析在插件进程里坏了", d.Model)
	}
	if d.Vendor != "Allen-Bradley" {
		t.Errorf("Vendor = %q，期望 Allen-Bradley（ListIdentity 的厂商字段是设备自述的）", d.Vendor)
	}
	// 一键建设备用的连接配置必须能被插件自己的配置解析器吃下 —— 这条在驱动包里
	// 用 ParseRockwellConfig 直接断言过，这里再确认它能原样过线。
	if !strings.Contains(d.ProtocolJSON, `"host":"127.0.0.1"`) {
		t.Errorf("连接配置 = %s，期望含 host=127.0.0.1", d.ProtocolJSON)
	}
}

// TestRockwellCipPointScanEndToEnd 点位扫描（原生枚举）在真实插件进程上跑通。
//
// **这是本功能唯一能证明整条链路接上了的用例**：驱动包里的
// TestScanPointsAgainstFakePLC 直接调 ScanPoints，跳过了
// pluginwire 的载荷形状、选项的 string/RawMessage 转换、临时实例的建连与收尾、
// 以及 `defer drv.Close()` 把枚举 goroutine 解开这一步。任何一处对不上，
// 症状都是"点一下按钮什么也没发生"。
//
// 顺带钉住那条跨端约定：本协议不读采样值，所以 AssumedCommonDataType 必须为空
// （前端据它隐藏「显示 0 值」开关），而每一行都要带自己的逐点类型。
func TestRockwellCipPointScanEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}
	srv, err := fake.NewCIPABWithTags(
		fake.CIPTag{Name: "MotorSpeed", Type: fake.CIPTypeDINT},
		fake.CIPTag{Name: "Temperature", Type: fake.CIPTypeREAL},
		fake.CIPTag{Name: "Run", Type: fake.CIPTypeBOOL},
		fake.CIPTag{Name: "BigCounter", Type: fake.CIPTypeLINT}, // 无通用类型 → 跳过
	)
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	m, _ := startPluginManager(t, "./driversrc/rockwellcip", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	protocolJSON := `{"host":"127.0.0.1","port":` + strconv.Itoa(srv.Port()) + `}`
	res, err := m.DiscoverPoints(ctx, "Rockwell.CIP", protocolJSON, nil)
	if err != nil {
		t.Fatalf("DiscoverPoints: %v", err)
	}

	if res.Scanned != 4 || res.Readable != 3 || res.Unreadable != 1 {
		t.Errorf("计数 = scanned %d / readable %d / unreadable %d，期望 4 / 3 / 1",
			res.Scanned, res.Readable, res.Unreadable)
	}
	if res.AssumedCommonDataType != "" {
		t.Errorf("AssumedCommonDataType = %q，期望空（本协议不读采样值，"+
			"前端据它隐藏「显示 0 值」开关）", res.AssumedCommonDataType)
	}

	got := map[string]string{}
	for _, p := range res.Points {
		got[p.Name] = p.CommonDataType
		if p.Value != "" {
			t.Errorf("点位 %q 带了采样值 %q —— 本协议应该只列标签与类型", p.Name, p.Value)
		}
	}
	want := map[string]string{
		"MotorSpeed":  driver.TypeLong,
		"Temperature": driver.TypeFloat,
		"Run":         driver.TypeBoolean,
	}
	for name, common := range want {
		if got[name] != common {
			t.Errorf("点位 %q 的逐点类型 = %q，期望 %q（实得清单：%v）",
				name, got[name], common, got)
		}
	}
}

// ==================== 三菱 MC ====================

// TestMitsubishiMcPluginScanCapabilities 三菱的两项扫描能力必须无损过线，
// 且**Discovery 只挂在以太网协议上、PointDiscovery 两个协议都挂**。
//
// 这条不对称是本插件与 FINS 的一处刻意差异（那边对串口/Host Link 都不声明）：
// MC 的串口会话语义是齐的 —— mcDriver 实现了 driver.ConnectionMatcher，
// 「测试连接」走的就是同一条复用路径，点扫复用的也是采集路径上一直在用的
// 那份 4C 帧传输。这里验证的是它在**线协议上**仍然是这个形状（漏一个字段、
// 错一个端口，前端就会渲染出错的表单或回填错的端口）。
func TestMitsubishiMcPluginScanCapabilities(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}
	m, _ := startPluginManager(t, "./driversrc/mitsubishi", nil)

	want := map[string]bool{
		"Mitsubishi.MC.TCP":    true,  // 两项能力
		"Mitsubishi.MC.Serial": false, // 只声明点扫，不声明设备扫描
	}

	seen := map[string]bool{}
	for _, pd := range m.DiscoverySpecs() {
		w, ok := want[pd.Protocol]
		if !ok {
			continue // 别的插件的协议
		}
		seen[pd.Protocol] = true
		if !w {
			t.Errorf("协议 %q 不该声明设备扫描（串口的 Discovery 被 Meta.Validate 明文禁止）", pd.Protocol)
			continue
		}
		if pd.Spec.DefaultPort != 5007 {
			t.Errorf("协议 %q 的默认端口 = %d，期望 5007", pd.Protocol, pd.Spec.DefaultPort)
		}
		if pd.Spec.TargetMode != driver.TargetModeUnicast {
			t.Errorf("协议 %q 的目标模式 = %q，期望 unicast", pd.Protocol, pd.Spec.TargetMode)
		}
		if len(pd.Spec.FormJSON) == 0 {
			t.Errorf("协议 %q 的扫描表单没过来", pd.Protocol)
		}
	}
	if !seen["Mitsubishi.MC.TCP"] {
		t.Error("Mitsubishi.MC.TCP 声明了设备扫描却没出现在 DiscoverySpecs 里 —— " +
			"它不会出现在扫描页的协议下拉中")
	}

	// 点位扫描：两个协议都要有（串口现场正是最需要批量录入点位的一类）。
	seenPoints := map[string]bool{}
	for _, pd := range m.PointDiscoverySpecs() {
		if _, ok := want[pd.Protocol]; !ok {
			continue
		}
		seenPoints[pd.Protocol] = true
		if pd.Spec.DefaultCommonDataType != driver.TypeWord {
			t.Errorf("协议 %q 的 DefaultCommonDataType = %q，期望 %q",
				pd.Protocol, pd.Spec.DefaultCommonDataType, driver.TypeWord)
		}
		if pd.Spec.Title == "" || len(pd.Spec.FormJSON) == 0 {
			t.Errorf("协议 %q 的点扫声明缺 Title 或表单", pd.Protocol)
		}
	}
	for name := range want {
		if !seenPoints[name] {
			t.Errorf("协议 %q 声明了点位扫描却没出现在 PointDiscoverySpecs 里 —— "+
				"设备地址页不会出现「自动发现点位」按钮", name)
		}
	}
}

// TestMitsubishiMcProbeEndToEnd 真实的三菱插件进程探测真实的 3E 帧假 PLC。
//
// 驱动包里的 TestProbeTCPFindsDevice 直接调 ProbeTCP，跳过了 pluginwire 的载荷
// 形状、预算单位与选项的 string/RawMessage 转换 —— 而那段链路任何一处对不上，
// 症状都是"扫描页上一个设备也扫不出来"。
func TestMitsubishiMcProbeEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}
	srv, err := fake.NewMC3E()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	m, _ := startPluginManager(t, "./driversrc/mitsubishi", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	reply, err := m.ProbeTargets(ctx, "Mitsubishi.MC.TCP", nil, fakeTarget(t, srv.Addr()))
	if err != nil {
		t.Fatalf("ProbeTargets: %v", err)
	}
	if len(reply.Devices) != 1 {
		t.Fatalf("应发现 1 台设备，实际 %d 台: %+v", len(reply.Devices), reply.Devices)
	}
	d := reply.Devices[0]
	if d.Host != "127.0.0.1" {
		t.Errorf("Host = %q，期望 127.0.0.1", d.Host)
	}
	// 一键建设备用的连接配置必须能被插件自己的配置解析器吃下。
	if !strings.Contains(d.ProtocolJSON, `"host":"127.0.0.1"`) {
		t.Errorf("连接配置 = %s，期望含 host=127.0.0.1", d.ProtocolJSON)
	}
	// MC 没有任何设备标识读取手段，型号/厂商必须留空 —— 而"发现了设备"不依赖它们。
	if d.Vendor != "" || d.Model != "" {
		t.Errorf("Vendor/Model = %q/%q，MC 读不出设备标识，应留空", d.Vendor, d.Model)
	}
}

// TestMitsubishiMcPointScanEndToEnd 点位扫描在真实插件进程上跑通。
//
// **这是本功能唯一能证明整条链路接上了的用例**：驱动包里的
// TestScanPointsValueMatchesName 直接调 scanWith，跳过了 pluginwire 的载荷形状、
// 选项的 string/RawMessage 转换、临时实例的建连与收尾（defer drv.Close()）。
// 任何一处对不上，症状都是"点一下「自动发现点位」什么也没发生"。
//
// 假 PLC（testutil/fake 的 MC3E）对任意地址一律回成功 + 全 0，因此扫出来的
// 每个点位都是"读到了、值为 0"。这正是要断言的那件事：零值点位**照样列出来**
// （插件侧不做零值过滤，过滤放在前端的一枚开关）。
func TestMitsubishiMcPointScanEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}
	srv, err := fake.NewMC3E()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	m, _ := startPluginManager(t, "./driversrc/mitsubishi", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	protocolJSON := `{"host":"127.0.0.1","port":` + strconv.Itoa(srv.Port()) + `}`
	res, err := m.DiscoverPoints(ctx, "Mitsubishi.MC.TCP", protocolJSON,
		json.RawMessage(`{"device":"D","start":"100","width":"word","count":10}`))
	if err != nil {
		t.Fatalf("DiscoverPoints: %v", err)
	}

	if res.Scanned != 10 || res.Readable != 10 || res.Unreadable != 0 {
		t.Errorf("计数 = scanned %d / readable %d / unreadable %d，期望 10 / 10 / 0",
			res.Scanned, res.Readable, res.Unreadable)
	}
	if res.Truncated || res.Incomplete {
		t.Errorf("10 个点位不该触到任何上限: truncated=%v incomplete=%v", res.Truncated, res.Incomplete)
	}
	// 字粒度必须报出 Word：前端据它标注"当前值按 X 渲染"。
	if res.AssumedCommonDataType != driver.TypeWord {
		t.Errorf("AssumedCommonDataType = %q，期望 %q", res.AssumedCommonDataType, driver.TypeWord)
	}
	if len(res.Points) != 10 {
		t.Fatalf("返回 %d 个点位，期望 10", len(res.Points))
	}
	// 地址那一列是**只读**的，用户直接拿它建点位 —— 写法必须与前端
	// addressSyntax.js 里 mc 的示例（D100）一致。
	if res.Points[0].Name != "D100" || res.Points[9].Name != "D109" {
		t.Errorf("首尾地址 = %q / %q，期望 D100 / D109", res.Points[0].Name, res.Points[9].Name)
	}
	// 全 0 的设备上**照样每个点位都列出来**：插件侧过滤会让用户从一份空列表
	// 得出"这里没有点位"，与"没扫到"是同型的错误。
	for _, p := range res.Points {
		if p.Value != "" || p.Note == "" {
			t.Errorf("点位 %q = %q / %q，期望零值点位（Value 空 + Note）",
				p.Name, p.Value, p.Note)
		}
	}
}
