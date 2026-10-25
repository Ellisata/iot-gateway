// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

// Modbus 插件（iot-gateway-drivers/modbus）的集成测试。
//
// 这里编译并加载的是**真实的插件源码产物**，不是测试替身 —— 唯一能在
// 测试里替代它的是一个重写了同样逻辑的冒牌货，那样的测试即使插件写错了也会通过。
//
// 内置 driver/modbus 已随插件化一起移除，所以「插件 vs 内置」的逐点位比对
// 不可能再做了。等价性改为与**冻结的黄金基线**比对（见 parity_test.go 的说明）：
// 那份黄金由内置实现在移除前采得，是同一份现场配置应当采出的值的唯一真源。

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/testkit/fake"
	"iot-gateway/collector"
	"iot-gateway/configFile"
	"iot-gateway/model/po"
	"iot-gateway/workerPool"
)

const (
	modbusTCP = "ModBus.TCP"
	modbusRTU = "ModBus.RTU"
	modbusDir = "modbus"
)

// ==================== 通用插件测试基建 ====================
//
// 下面这几个助手是**全部插件测试共用的**：每个协议搬成插件后，
// 自己的测试文件只需声明「驱动名 + scope + 用例名」，
// 剩下的编译、加载、比对都在这里。

// buildPlugin 准备好某个驱动的插件二进制，返回其路径。
//
// 两种取源方式：
//   - 设置了 IOT_DRIVERS_SRC（指向 iot-gateway-drivers 检出目录）时，直接编译本地源码，
//     用于跨仓联调未发布的驱动改动；
//   - 否则在临时模块里 `go get` 已发布的驱动版本（drivers.version）再 `go build`，
//     走 GOPROXY 与校验和，不需要任何源码检出。
func buildPlugin(t *testing.T, name string) string {
	t.Helper()

	outDir := t.TempDir()
	binName := "iot-driver-" + name
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	out := filepath.Join(outDir, binName)

	if src := os.Getenv("IOT_DRIVERS_SRC"); src != "" {
		cmd := exec.Command("go", "build", "-o", out, "./"+name)
		cmd.Dir = src
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("编译插件 %s（源码 %s）失败: %v\n%s", name, src, err, output)
		}
		return out
	}

	// 用一个临时模块解析已发布的驱动源码：
	//   - go install 在设置 GOBIN 时禁止交叉编译；
	//   - go build 需要一个主模块来解析依赖与 go.sum。
	// 临时模块同时满足两者，产物名由 -o 精确控制。
	ver := driversVersion(t)
	buildDir := t.TempDir()
	runGo := func(args ...string) {
		t.Helper()
		cmd := exec.Command("go", args...)
		cmd.Dir = buildDir
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s 失败（可设 IOT_DRIVERS_SRC 指向本地驱动仓库）: %v\n%s",
				strings.Join(args, " "), err, output)
		}
	}
	runGo("mod", "init", "driverbuild")
	runGo("get", "github.com/Ellisata/iot-gateway-drivers/"+name+"@"+ver)
	runGo("build",
		"-ldflags", "-s -w -X main.version="+ver,
		"-o", out,
		"github.com/Ellisata/iot-gateway-drivers/"+name)
	return out
}

// driversVersion 读取仓库根的 drivers.version（构建脚本与插件测试共用的版本真源）。
func driversVersion(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "drivers.version")); err == nil {
			return strings.TrimSpace(string(b))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("向上未找到 drivers.version（从 %s 起）", dir)
		}
		dir = parent
	}
}

// pluginConfigFor 构造指向某目录的插件配置。
// 扫描间隔设成 1 小时：测试自己调 Reload，不给后台 ticker 插手的余地。
func pluginConfigFor(dir string) *configFile.Config {
	return &configFile.Config{
		Plugin: configFile.PluginConfig{
			Enabled:            true,
			Dir:                dir,
			Glob:               "iot-driver-*",
			ScanIntervalMs:     3600000,
			AllowInsecureDir:   true,
			HandshakeTimeoutMs: 15000,
		},
	}
}

// startPluginManager 编译并加载 srcDir 下的插件，返回 Manager 与库。
//
// reload 传 nil 时用一个只记录调用的 reloader；需要观察「是否触发了采集重建」
// 的调用方自己传（见 upgrade_test.go）。
func startPluginManager(t *testing.T, srcDir string, reload Reloader) (*Manager, *gorm.DB) {
	t.Helper()

	if reload == nil {
		reload = &fakeReloader{}
	}
	bin := buildPlugin(t, srcDir)
	db := newTestDB(t)

	m := NewManager(db, pluginConfigFor(filepath.Dir(bin)), reload)
	t.Cleanup(m.Stop)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return m, db
}

// runParityThroughPlugin 用插件驱动复现某几条用例的黄金基线。
//
// 这是每个插件搬完之后的主要验收点：同一份 testdata/parity/addresses.json、
// 同一个假 PLC、同一批点位，唯一变的是驱动实现来自插件进程。
func runParityThroughPlugin(t *testing.T, srcDir, scope string, caseNames []string) {
	t.Helper()

	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	startPluginManager(t, srcDir, nil)
	cases := loadParityCases(t)

	for _, name := range caseNames {
		t.Run(name, func(t *testing.T) {
			c, ok := cases[name]
			if !ok {
				t.Fatalf("用例 %q 不在 addresses.json 里", name)
			}
			if c.Scope != scope {
				t.Fatalf("用例 %q 的 scope 是 %q，本插件预计是 %q —— 用例与插件对不上号", name, c.Scope, scope)
			}

			d, err := driver.Create(c.Protocol)
			if err != nil {
				t.Fatalf("创建插件驱动 %s 失败: %v", c.Protocol, err)
			}
			defer d.Close()

			srv := startParityFake(t, c.Fake)
			if err := d.Connect(parityProtocolJSON(t, c, srv.Port())); err != nil {
				t.Fatalf("连接假 PLC 失败: %v", err)
			}

			assertGoldenMatches(t, name, readParity(t, d, c))
		})
	}

	assertProtocolsMatchGolden(t, scope, registeredProtocols(scope))
	assertTypeMapMatchesGolden(t, scope)
}

// ==================== Modbus 插件测试 ====================

// TestModbusPluginRegistersBothProtocols 一个插件提供两个协议、共用类型表，
// 且 TCP/RTU 的 SerialExclusive 各自独立 —— 这正是内置 Modbus 的形状。
func TestModbusPluginRegistersBothProtocols(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}
	m, db := startPluginManager(t, modbusDir, nil)
	waitForDriverRegistered(t, modbusTCP)
	waitForDriverRegistered(t, modbusRTU)

	// Status 应报告一个插件、两个协议。
	st := m.Status()
	if len(st) != 1 {
		t.Fatalf("Status 条目数 = %d, want 1", len(st))
	}
	if len(st[0].Protocols) != 2 {
		t.Fatalf("插件协议数 = %d, want 2 (%v)", len(st[0].Protocols), st[0].Protocols)
	}
	if st[0].Scope != "modbus" {
		t.Errorf("scope = %q, want modbus", st[0].Scope)
	}

	// SerialExclusive 必须按协议独立：RTU 独占串行总线，TCP 不独占。
	// 搞反会让 RTU 总线上出现交错的帧，或让 TCP 白白串行化。
	cases := []struct {
		protocol string
		want     bool
	}{
		{modbusTCP, false},
		{modbusRTU, true},
	}
	for _, c := range cases {
		d, err := driver.Create(c.protocol)
		if err != nil {
			t.Fatalf("Create(%s): %v", c.protocol, err)
		}
		se, ok := d.(driver.SerialExclusive)
		if !ok {
			t.Fatalf("%s 应实现 driver.SerialExclusive", c.protocol)
		}
		if got := se.SerialExclusive(); got != c.want {
			t.Errorf("%s SerialExclusive() = %v, want %v", c.protocol, got, c.want)
		}
	}

	// 两个协议各自建立 iot_protocol 行（前端下拉才能分别选到）。
	for _, name := range []string{modbusTCP, modbusRTU} {
		var row po.IotProtocol
		if err := db.Where("name = ?", name).First(&row).Error; err != nil {
			t.Errorf("协议 %s 的 iot_protocol 行未建立: %v", name, err)
			continue
		}
		if row.FormJSON == "" || row.FormJSON == "{}" {
			t.Errorf("协议 %s 的配置表单为空", name)
		}
	}

	// TCP 与 RTU 的配置表单必须不同（一个是 host/port，一个是 comPort）。
	var tcpRow, rtuRow po.IotProtocol
	_ = db.Where("name = ?", modbusTCP).First(&tcpRow).Error
	_ = db.Where("name = ?", modbusRTU).First(&rtuRow).Error
	if tcpRow.FormJSON == rtuRow.FormJSON {
		t.Error("TCP 与 RTU 的配置表单不应相同（FormJSON 是按协议而非按插件的）")
	}
}

// TestModbusPluginUnregistersAllProtocols Stop 必须把插件的**全部**协议都摘掉。
// 只摘第一个是这类多值循环最典型的漏写，而症状（残留协议仍可 Create、
// 但底层进程已死）会以"设备一直连不上"的形式表现出来。
func TestModbusPluginUnregistersAllProtocols(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}
	m, _ := startPluginManager(t, modbusDir, nil)
	waitForDriverRegistered(t, modbusTCP)

	// 前置：两个协议都在，且类型 scope 可用。
	for _, p := range []string{modbusTCP, modbusRTU} {
		if _, err := driver.Create(p); err != nil {
			t.Fatalf("前置条件不成立，%s 应可用: %v", p, err)
		}
	}
	if got := driver.ScopeOf(modbusTCP); got != "modbus" {
		t.Fatalf("前置条件：scope = %q", got)
	}

	m.Stop()

	for _, p := range []string{modbusTCP, modbusRTU} {
		if _, err := driver.Create(p); err == nil {
			t.Errorf("Stop 后 %s 仍可创建（协议未注销）", p)
		}
		if got := driver.ScopeOf(p); got != "" {
			t.Errorf("Stop 后 %s 的 scope 仍为 %q（类型映射未注销）", p, got)
		}
	}
	// 类型 scope 整体摘除。
	if _, ok := driver.LookupDataType(modbusTCP, "Float"); ok {
		t.Error("Stop 后插件类型映射应已注销")
	}
}

// TestModbusPluginParity 插件驱动必须逐点位复现内置实现冻结下来的黄金基线。
//
// 这是"同一份现场配置在原内置版与现插件版下采出相同的值"这条承诺的验收点。
func TestModbusPluginParity(t *testing.T) {
	runParityThroughPlugin(t, modbusDir, "modbus", []string{"modbus-tcp"})
}

// TestModbusPluginCollectsThroughEngine 走完整采集链路（collector → RPC → 插件），
// 验证插件驱动能被采集引擎正常调度、点位全部采到、协议名正确透传。
//
// 与 TestModbusPluginParity 的分工：后者直连驱动验**协议语义**，
// 这条验**网关侧的接入**（任务编排、锁粒度、记录上报）。
func TestModbusPluginCollectsThroughEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	srv, err := fake.NewModbusTCP()
	if err != nil {
		t.Fatalf("启动假 Modbus 从站: %v", err)
	}
	t.Cleanup(srv.Close)

	bin := buildPlugin(t, modbusDir)
	db := newTestDB(t)
	for _, m := range []any{&po.Device{}, &po.DeviceAddress{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	pool := workerPool.NewWorkerPool(4, 64)
	pool.Start()
	sink := &recordingSink{}
	engine := collector.NewEngine(db, pool, sink, noopStateSink{}, nil, nil)

	m := NewManager(db, pluginConfigFor(filepath.Dir(bin)), engine)
	t.Cleanup(m.Stop)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, modbusTCP)

	// 插件的 iot_protocol 行由 Manager 依据 Meta 自动建立。
	var proto po.IotProtocol
	if err := db.Where("name = ?", modbusTCP).First(&proto).Error; err != nil {
		t.Fatalf("插件协议行应由 Manager 自动建立: %v", err)
	}

	dev := po.Device{
		Name:         "插件设备",
		ProtocolID:   proto.ID,
		ProtocolJSON: fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"unitId":1,"timeoutMs":3000}`, srv.Port()),
		Status:       1,
	}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("创建设备: %v", err)
	}

	// 连续 + 空洞 + 多寄存器混合：覆盖区间合并与多帧读取。
	specs := []struct {
		reg      int
		internal string
		common   string
	}{
		{40001, "int16", "Short"},
		{40002, "int16", "Short"},
		{40003, "int32", "Long"},    // 占 2 寄存器
		{40008, "float32", "Float"}, // 空洞 → 触发区间合并
		{40020, "uint16", "Word"},
	}
	addrs := make([]po.DeviceAddress, 0, len(specs))
	for _, s := range specs {
		a := po.DeviceAddress{
			Name:           strconv.Itoa(s.reg),
			Label:          fmt.Sprintf("插件设备-%d", s.reg),
			DeviceID:       dev.ID,
			CommonDataType: s.common,
			DataType:       s.internal,
			ScanFrequency:  200,
			Status:         1,
		}
		if err := db.Create(&a).Error; err != nil {
			t.Fatalf("创建地址 %d: %v", s.reg, err)
		}
		addrs = append(addrs, a)
	}

	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	t.Cleanup(engine.Stop)

	waitFor(t, 20*time.Second, "全部点位采到", func() bool {
		return len(sink.snapshot()) >= len(addrs)
	})

	got := make(map[string]collector.CollectedRecord)
	for _, r := range sink.snapshot() {
		got[r.DeviceAddressID] = r
	}
	for _, a := range addrs {
		r, ok := got[a.ID]
		if !ok {
			t.Errorf("地址 %s 缺采集记录", a.Name)
			continue
		}
		if r.Quality != driver.QualityGood {
			t.Errorf("地址 %s（%s）Quality = %d, want %d", a.Name, a.DataType, r.Quality, driver.QualityGood)
		}
		if r.DataType != a.DataType {
			t.Errorf("地址 %s DataType = %q, want %q", a.Name, r.DataType, a.DataType)
		}
		// 协议名透传：这是区分"记录来自插件链路"的唯一标记。
		if r.Protocol != modbusTCP {
			t.Errorf("地址 %s Protocol = %q, want %q", a.Name, r.Protocol, modbusTCP)
		}
		if r.DeviceAddressName != a.Name {
			t.Errorf("地址名透传有误：%q, want %q", r.DeviceAddressName, a.Name)
		}
	}
}

// recordingStateSink 记录每台设备的每次采集状态上报，供"设备有没有被判离线"
// 这类断言使用（noopStateSink 恰恰把这个后果丢掉了）。
type recordingStateSink struct {
	mu     sync.Mutex
	states map[string][]bool
}

func (s *recordingStateSink) ReportDevicePoll(deviceID, _ string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = make(map[string][]bool)
	}
	s.states[deviceID] = append(s.states[deviceID], ok)
}

// sawFailure 报告该设备是否被上报过任何一次失败。
func (s *recordingStateSink) sawFailure(deviceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ok := range s.states[deviceID] {
		if !ok {
			return true
		}
	}
	return false
}

// TestModbusPluginBadAddressDoesNotOfflineDevice 一个**本地就解析不出来**的点位
// 地址不得让设备被判离线 —— 这是现场那条日志的回归，走完整链路（collector →
// RPC → 真实插件产物 → 假从站）。
//
// 修复前的形态：日志一行
//
//	collector: read device X failed: modbus: calc address ranges failed:
//	modbus: address "%MW1x" (id=…) invalid format
//
// 随后这台设备被判**离线**。链路是：插件返回 error → 采集侧只看得见一个 err、
// 分不清"地址写错了"与"网线被拔了" → incError + reportState(false) → 连续三轮后
// 落一条断联报警。而设备**全程在线**（IsConnected 为真，重连逻辑根本不启动），
// 坏地址之后的批次也一起被跳过。
//
// 断言刻意落在**离线状态接收者**上（不是日志、不是质量码）：那才是用户看到的那
// 个后果。坏地址只该让那一个点位成为 bad-address，设备状态自始至终是在线。
func TestModbusPluginBadAddressDoesNotOfflineDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	srv, err := fake.NewModbusTCP()
	if err != nil {
		t.Fatalf("启动假 Modbus 从站: %v", err)
	}
	t.Cleanup(srv.Close)

	bin := buildPlugin(t, modbusDir)
	db := newTestDB(t)
	for _, m := range []any{&po.Device{}, &po.DeviceAddress{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	pool := workerPool.NewWorkerPool(4, 64)
	pool.Start()
	sink := &recordingSink{}
	state := &recordingStateSink{}
	engine := collector.NewEngine(db, pool, sink, state, nil, nil)

	m := NewManager(db, pluginConfigFor(filepath.Dir(bin)), engine)
	t.Cleanup(m.Stop)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, modbusTCP)

	var proto po.IotProtocol
	if err := db.Where("name = ?", modbusTCP).First(&proto).Error; err != nil {
		t.Fatalf("插件协议行应由 Manager 自动建立: %v", err)
	}

	dev := po.Device{
		Name:         "坏地址设备",
		ProtocolID:   proto.ID,
		ProtocolJSON: fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"unitId":1,"timeoutMs":3000}`, srv.Port()),
		Status:       1,
	}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("创建设备: %v", err)
	}

	// 两个可解析的地址夹着**一个本地解析不出来的**（%MW1x：tryIECFormat 认前缀
	// %MW，但 "1x" 过不了 ParseUint）。
	specs := []struct {
		name     string
		internal string
		common   string
	}{
		{"40001", "int16", "Short"},
		{"%MW1x", "int16", "Short"},
		{"40003", "int16", "Short"},
	}
	addrs := make([]po.DeviceAddress, 0, len(specs))
	for _, s := range specs {
		a := po.DeviceAddress{
			Name:           s.name,
			Label:          fmt.Sprintf("坏地址设备-%s", s.name),
			DeviceID:       dev.ID,
			CommonDataType: s.common,
			DataType:       s.internal,
			ScanFrequency:  200,
			Status:         1,
		}
		if err := db.Create(&a).Error; err != nil {
			t.Fatalf("创建地址 %s: %v", s.name, err)
		}
		addrs = append(addrs, a)
	}

	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	t.Cleanup(engine.Stop)

	// 三个点位都该有记录（坏的也在内 —— 「addrs 里每个点位恰好一条记录」是契约）。
	waitFor(t, 20*time.Second, "三个点位都有记录", func() bool {
		return len(sink.snapshot()) >= len(addrs)
	})

	got := make(map[string]collector.CollectedRecord)
	for _, r := range sink.snapshot() {
		got[r.DeviceAddressID] = r
	}
	for i, a := range addrs {
		r, ok := got[a.ID]
		if !ok {
			t.Errorf("地址 %q 缺采集记录", a.Name)
			continue
		}
		if r.DataType != a.DataType {
			t.Errorf("地址 %q DataType = %q, want %q（坏地址也必须带回类型）", a.Name, r.DataType, a.DataType)
		}
		if i == 1 {
			if r.Quality != driver.QualityBadAddress {
				t.Errorf("坏地址 %q Quality = %d, want %d（地址无效）", a.Name, r.Quality, driver.QualityBadAddress)
			}
			continue
		}
		if r.Quality != driver.QualityGood {
			t.Errorf("地址 %q Quality = %d, want %d（同批好点位不受影响）", a.Name, r.Quality, driver.QualityGood)
		}
	}

	// 这条是用例的主题：设备**从来没有**被上报为采集失败。
	if state.sawFailure(dev.ID) {
		t.Errorf("设备 %q 被上报过采集失败 —— 一个坏地址把设备判成了离线", dev.Name)
	}
}

// TestModbusPluginScanProbe 用**真实的 Modbus 插件产物**跑一次扫描探测。
//
// 与 TestModbusPluginParity（协议语义）、TestModbusPluginCollectsThroughEngine
// （网关侧接入）三者互补：这条验的是**扫描链路** —— 网关把目标交给插件进程、
// 插件在自己的协议实现里探测、结果穿过帧 RPC 回到网关。
// 这条链路上任何一层把参数或结果丢一半，都只有在真实二进制上才看得出来。
func TestModbusPluginScanProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	// 假 PLC 只认 1 号与 5 号站，且应答读设备标识（FC43/14）。
	srv, err := fake.NewModbusTCPWithUnitIDs(1, 5)
	if err != nil {
		t.Fatalf("启动假 Modbus 从站: %v", err)
	}
	t.Cleanup(srv.Close)

	bin := buildPlugin(t, modbusDir)
	db := newTestDB(t)
	m := NewManager(db, pluginConfigFor(filepath.Dir(bin)), &fakeReloader{})
	t.Cleanup(m.Stop)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, modbusTCP)

	// 插件必须声明了扫描能力，否则前端根本不会把它列出来。
	specs := m.DiscoverySpecs()
	found := false
	for _, s := range specs {
		if s.Protocol == modbusTCP {
			found = true
			if s.Spec.DefaultPort != 502 {
				t.Errorf("默认端口 = %d，期望 502", s.Spec.DefaultPort)
			}
			if len(s.Spec.FormJSON) == 0 {
				t.Error("扫描表单没随 Meta 过线，前端会渲染出一个空表单")
			}
		}
	}
	if !found {
		t.Fatalf("%s 应声明扫描能力，实际 %+v", modbusTCP, specs)
	}
	// 串口协议不能声明扫描能力（Meta.Validate 也会拒），这里顺带钉住。
	for _, s := range specs {
		if s.Protocol == modbusRTU {
			t.Error("ModBus.RTU 是串口协议，不该声明局域网扫描能力")
		}
	}

	host, portStr, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatalf("解析假 PLC 地址: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("解析端口: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reply, err := m.ProbeTargets(ctx, modbusTCP,
		[]byte(`{"unitIds":"1,3,5"}`),
		driver.ScanTarget{Host: host, Port: port})
	if err != nil {
		t.Fatalf("ProbeTargets: %v", err)
	}

	units := map[int]bool{}
	for _, d := range reply.Devices {
		units[d.UnitID] = true
		// 插件给出的连接配置必须能直接建成设备：协议名与端口都要对得上。
		if d.Host != host || d.Port != port {
			t.Errorf("设备地址 = %s:%d，期望 %s:%d", d.Host, d.Port, host, port)
		}
		if d.ProtocolJSON == "" {
			t.Error("探测结果必须自带可用的连接配置")
		}
	}
	if !units[1] || !units[5] {
		t.Errorf("1 号与 5 号站都应答，实际 %v", units)
	}
	if units[3] {
		t.Errorf("3 号站静默丢弃，不该被报成设备（实际 %v）", units)
	}
}

// TestModbusPluginProbeSkipsNonModbus 502 端口上跑着别的服务时不该误报。
func TestModbusPluginProbeSkipsNonModbus(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
			_ = conn.Close()
		}
	}()

	bin := buildPlugin(t, modbusDir)
	db := newTestDB(t)
	m := NewManager(db, pluginConfigFor(filepath.Dir(bin)), &fakeReloader{})
	t.Cleanup(m.Stop)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, modbusTCP)

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reply, err := m.ProbeTargets(ctx, modbusTCP, nil, driver.ScanTarget{Host: host, Port: port})
	if err != nil {
		t.Fatalf("对端不是 Modbus 不是错误: %v", err)
	}
	if len(reply.Devices) != 0 {
		t.Fatalf("不该把非 Modbus 服务报成设备，实际 %+v", reply.Devices)
	}
}

// TestModbusPluginDiscoverPoints 用**真实的 Modbus 插件产物**跑一次点位范围扫描。
//
// 与 TestModbusPluginScanProbe（局域网探测）成对：那条验"网段里谁在"，
// 这条验"这台设备上有哪些点位"。它同时钉住三件只有真实二进制才验得到的事：
//
//   - 结果确实穿过了帧 RPC（Pluginwire 的载荷编码、进程边界）；
//   - 生成的地址写法能被**该协议自己**的解析器还原（这里落回 LookupDataType
//     所依赖的同一套网关侧契约）；
//   - 空洞被如实标成"读不到"，而不是整段丢弃或整段回零。
func TestModbusPluginDiscoverPoints(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	// 假 PLC：[100,103] 与 [110,111] 两段存在，中间的 104-109 是空洞；
	// 100 与 103 上还配了非零值。
	srv, err := fake.NewModbusTCPWithOption(
		fake.WithModbusRegisters(map[uint16]uint16{100: 4321, 103: 65535}),
		fake.WithModbusSpan(100, 103),
		fake.WithModbusSpan(110, 111),
	)
	if err != nil {
		t.Fatalf("启动假 Modbus 从站: %v", err)
	}
	t.Cleanup(srv.Close)

	bin := buildPlugin(t, modbusDir)
	db := newTestDB(t)
	m := NewManager(db, pluginConfigFor(filepath.Dir(bin)), &fakeReloader{})
	t.Cleanup(m.Stop)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, modbusTCP)

	// TCP 与 RTU 都该声明点扫能力 —— 与 Discovery 只挂 TCP 正相反。
	// 串口设备同样有"它有哪些点位"这个问题，而且串口恰是会话复用最重要的场景。
	declared := map[string]bool{}
	for _, s := range m.PointDiscoverySpecs() {
		declared[s.Protocol] = true
		if len(s.Spec.FormJSON) == 0 {
			t.Errorf("%s 的扫描表单没随 Meta 过线，前端会渲染出一个空参数区", s.Protocol)
		}
	}
	for _, p := range []string{modbusTCP, modbusRTU} {
		if !declared[p] {
			t.Errorf("%s 应声明点位扫描能力", p)
		}
	}

	host, portStr, _ := net.SplitHostPort(srv.Addr())
	port, _ := strconv.Atoi(portStr)
	protocolJSON := fmt.Sprintf(`{"host":%q,"port":%d,"unitId":1}`, host, port)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := m.DiscoverPoints(ctx, modbusTCP, protocolJSON,
		[]byte(`{"functionCode":3,"startAddress":100,"count":12}`))
	if err != nil {
		t.Fatalf("DiscoverPoints: %v", err)
	}

	if res.Scanned != 12 {
		t.Fatalf("Scanned = %d，期望 12（范围内每个地址都要有结论）", res.Scanned)
	}
	if res.Readable+res.Unreadable != res.Scanned {
		t.Fatalf("不变量被破坏：readable %d + unreadable %d != scanned %d",
			res.Readable, res.Unreadable, res.Scanned)
	}
	if res.Readable != 6 || res.Unreadable != 6 {
		t.Fatalf("可读 %d / 读不到 %d，期望 6/6（104-109 是空洞）—— "+
			"整段失败却没降级，症状是范围里一半地址被静默丢弃",
			res.Readable, res.Unreadable)
	}
	if res.AssumedCommonDataType != "Word" {
		t.Errorf("假设类型 = %q，期望 Word", res.AssumedCommonDataType)
	}

	got := map[string]string{}
	for _, p := range res.Points {
		got[p.Name] = p.Value
	}
	// 地址写法必须是 IEC 形式，且非零值要一路对得上。
	if got["%MW100"] != "4321" {
		t.Errorf("%%MW100 的值 = %q，期望 4321", got["%MW100"])
	}
	if got["%MW103"] != "65535" {
		t.Errorf("%%MW103 的值 = %q，期望 65535", got["%MW103"])
	}
	for _, bad := range []string{"%MW104", "%MW109"} {
		if _, ok := got[bad]; ok {
			t.Errorf("空洞里的 %s 不该出现在结果里", bad)
		}
	}

	// 插件声明的默认类型必须能在网关侧解析成内部类型 —— 前端拿它初始化下拉，
	// 而它最终会写进 device_address.common_data_type。
	if _, ok := driver.LookupDataType(modbusTCP, "Word"); !ok {
		t.Error(`driver.LookupDataType("ModBus.TCP","Word") 应命中：` +
			"插件缺了这个映射的话，扫描结果建点位时每一条都会被判\"协议不支持该数据类型\"")
	}
}
