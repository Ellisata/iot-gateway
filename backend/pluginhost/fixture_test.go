// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

// 子进程集成测试用的夹具插件。
//
// 通过 os.Args[0] 重执行技巧：把测试二进制复制成插件文件名后再执行它，
// 由 TestMain 依据环境变量分派到 runFixturePlugin。这样无需额外的构建步骤，
// 也不需要测试时假设工具链位置，就能用真实的 exec.Command 路径跑通全流程。
//
// 夹具驱动用 protocolJSON 里的 mode 字段控制行为，覆盖自愈相关场景：
//
//	{"mode":"crash"}   Read 时直接退进程（模拟插件崩溃）
//	{"mode":"hang"}    Read 时永久阻塞（模拟插件卡死但进程还活着）

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"iot-gateway/driveradapter"
	"iot-gateway/model/po"
)

// sdkAddrs / sdkAddr 把测试里的数据库模型地址转成驱动契约地址。
func sdkAddrs(addrs []po.DeviceAddress) []driver.DeviceAddress {
	return driveradapter.FromPOs(addrs)
}

func sdkAddr(a po.DeviceAddress) driver.DeviceAddress {
	return driveradapter.FromPO(a)
}

// envFixture 标记子进程以夹具插件身份启动。取值：
//
//	1        正常插件
//	badmeta  Meta 非法，Serve 立即返回错误后退出（模拟握手失败）
//	sleep    启动后不提供任何服务，用于模拟"握手永远不返回"
const envFixture = "IOTGW_PLUGIN_FIXTURE"

// envFixtureTag 给 Read 的返回值加前缀，用来判断「这条数据是哪个版本采出来的」。
//
// 热升级测试没有第二个可执行文件：**新版本的行为差异全部由环境变量承载**
// （本变量与 envFixtureVariant），磁盘上的文件变化只负责让指纹与 sha 变化。
// 两者是独立的两件事 —— 正是 upgrade 里「先比指纹、再比 sha」要区分的。
const envFixtureTag = "IOTGW_FIXTURE_TAG"

// envFixtureVariant 让夹具以不同的 Meta 启动，覆盖热升级中「注册面变化」的各条分支：
//
//	""          基线
//	addtype     多一个数据类型 + TypeMap 条目（只动类型表）
//	renamescope 换 scope（只动类型 scope）
//	addproto    多一个协议名（必须重建采集任务）
//	serial      翻转 SerialExclusive（必须重建采集任务）
//	discovery   给协议挂上扫描能力（只动 Discovery）
//	discovery2  同 discovery 但 Title/预算不同，用于验证「热升级后扫描规格现算」
//	pointscan        给协议挂上点位扫描能力（只动 PointDiscovery）
//	pointscan2       同 pointscan 但 Title/预算不同，用于验证「热升级后点扫规格现算」
//	pointscan_serial 同时翻转 SerialExclusive，用于验证串口回退规则
//	invalidmeta Meta 非法，Serve 立即失败（模拟新二进制起不来）
const envFixtureVariant = "IOTGW_FIXTURE_VARIANT"

func TestMain(m *testing.M) {
	switch os.Getenv(envFixture) {
	case "1", "badmeta":
		os.Exit(runFixturePlugin())
	case "sleep":
		// 占住进程但不跑协议栈：describe 永远不会有应答。
		select {}
	}
	os.Exit(m.Run())
}

// ==================== 假「独占串口」 ====================
//
// 与 driversdk 单测里的同名设施同构：第二个打开同一端口的人拿到 Access is denied，
// 正是 Windows 下 goburrow/serial 以共享模式 0 调 CreateFile 的行为。
//
// 有了它，「跨进程的 ping 成功了」才真正说明复用发生了 —— 否则也可能只是
// 假注册表根本没在拦第二次打开。测试里用一次 Connect 撞端口即可自证。
var (
	fixturePortsMu sync.Mutex
	fixturePorts   = map[string]string{} // 小写端口名 → 持有者
)

func fixtureOpenPort(port, owner string) error {
	fixturePortsMu.Lock()
	defer fixturePortsMu.Unlock()
	key := strings.ToLower(port)
	if who, ok := fixturePorts[key]; ok {
		return fmt.Errorf("Access is denied: port %s held by %s", port, who)
	}
	fixturePorts[key] = owner
	return nil
}

func fixtureClosePort(port, owner string) {
	fixturePortsMu.Lock()
	defer fixturePortsMu.Unlock()
	key := strings.ToLower(port)
	if fixturePorts[key] == owner {
		delete(fixturePorts, key)
	}
}

// fixtureDriver 夹具插件内的驱动。
type fixtureDriver struct {
	mode      string
	connected bool
	port      string // 本实例当前持有的串口（空 = 没持有）
}

func (d *fixtureDriver) Connect(protocolJSON string) error {
	var cfg struct {
		Mode string `json:"mode"`
		Port string `json:"port"`
	}
	if protocolJSON != "" {
		_ = json.Unmarshal([]byte(protocolJSON), &cfg)
	}
	d.mode = cfg.Mode

	// 只有 protocolJSON 里带 port 的用例才走串口语义。热升级、自愈那些既有用例
	// 的 JSON 里没有这个键，行为与从前完全一致。
	if cfg.Port != "" {
		if err := fixtureOpenPort(cfg.Port, "fixture"); err != nil {
			return err
		}
		d.port = cfg.Port
	}
	d.connected = true
	return nil
}

// MatchConnection 见 driver.ConnectionMatcher。
//
// 只有真的持有该端口的实例才匹配。既有用例的 JSON 里没有 port，因此一律 false，
// 不会改变它们的走向（ping 照旧落到临时实例上）。
func (d *fixtureDriver) MatchConnection(protocolJSON string) bool {
	port := fixturePortOf(protocolJSON)
	return port != "" && d.port != "" && strings.EqualFold(d.port, port)
}

func (d *fixtureDriver) Ping(protocolJSON string) error {
	if protocolJSON == `{"ping":"fail"}` {
		return errors.New("fixture ping failed")
	}
	// 带 port 时模拟"新开一个句柄"：端口被占就是 Access is denied。
	//
	// 复用路径不会走到这一步 —— driversdk 先按 MatchConnection 选中持有者，
	// 再调它自己的 Ping，而那个实例的 d.port 已设，下面这次 open 会被跳过。
	// 走到这里并真的去 open 的只可能是临时实例，它必然撞上端口占用。
	if port := fixturePortOf(protocolJSON); port != "" && !strings.EqualFold(d.port, port) {
		if err := fixtureOpenPort(port, "fixture-ping"); err != nil {
			return err
		}
		fixtureClosePort(port, "fixture-ping")
	}
	return nil
}

func fixturePortOf(protocolJSON string) string {
	if protocolJSON == "" {
		return ""
	}
	var cfg struct {
		Port string `json:"port"`
	}
	if err := json.Unmarshal([]byte(protocolJSON), &cfg); err != nil {
		return ""
	}
	return cfg.Port
}

func (d *fixtureDriver) Read(addrs []driver.DeviceAddress) ([]driver.ReadResult, error) {
	switch d.mode {
	case "crash":
		// 直接退出进程，模拟插件崩溃。
		os.Exit(1)
	case "hang":
		// 永久阻塞当前 goroutine：进程还活着但不再响应任何 RPC。
		select {}
	}
	out := make([]driver.ReadResult, len(addrs))
	tag := os.Getenv(envFixtureTag)
	for i, a := range addrs {
		out[i] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           fmt.Sprintf("fixture-%s%s", tag, a.Name),
			DataType:        a.DataType,
			Kind:            driver.KindInt,
			Quality:         driver.QualityGood,
		}
	}
	return out, nil
}

func (d *fixtureDriver) IsConnected() bool { return d.connected }
func (d *fixtureDriver) Close() error      { d.connected = false; return nil }

// fixtureMeta 夹具插件的 describe 载荷，按 envFixtureVariant 派生变体。
//
// 变体只改 Meta、不改实现：热升级关心的正是「同一个驱动代码、另一份注册面」
// 该怎么处理。
func fixtureMeta() driversdk.Meta {
	meta := driversdk.Meta{
		APIVersion: driversdk.APIVersion,
		Protocols: []driversdk.Protocol{{
			Name:     fixtureProtocol,
			FormJSON: json.RawMessage(`{"rule":[{"type":"input","field":"host","title":"设备IP"}],"options":{}}`),
		}},
		Scope:       fixtureScope,
		Description: "测试夹具协议",
		Version:     "1.0.0",
		Types: []*driver.DataType{
			{Name: "bool", Kind: driver.KindBool, Size: 1},
			{Name: "int16", Kind: driver.KindInt, Size: 2},
		},
		TypeMap: map[string]string{"Boolean": "bool", "Short": "int16", "Bool": "bool"},
	}

	switch os.Getenv(envFixtureVariant) {
	case "addtype":
		// 只动类型表：注册面变了，但不需要重建采集任务。
		meta.Types = append(meta.Types, &driver.DataType{Name: "int32", Kind: driver.KindInt, Size: 4})
		meta.TypeMap["Int32"] = "int32"
	case "renamescope":
		meta.Scope = fixtureScopeAlt
	case "addproto":
		meta.Protocols = append(meta.Protocols, driversdk.Protocol{Name: fixtureProtocolAlt})
	case "serial":
		meta.Protocols[0].SerialExclusive = true
	case "discovery":
		meta.Protocols[0].Discovery = &driver.DiscoverySpec{
			Title:       "夹具扫描",
			DefaultPort: 502,
			TargetMode:  driver.TargetModeUnicast,
			MaxBudgetMs: 2000,
			FormJSON:    json.RawMessage(`{"rule":[{"type":"input","field":"unitIds","title":"站号"}]}`),
		}
	case "discovery2":
		// 与 discovery 只差 Title 与预算。热升级时 registrationSig **不含**
		// Discovery，因此 upgrade 不会重注册 —— 这一对变体正是用来钉住
		// 「Manager 必须每次现算而不是缓存一张表」的。
		meta.Protocols[0].Discovery = &driver.DiscoverySpec{
			Title:       "夹具扫描 v2",
			DefaultPort: 1502,
			TargetMode:  driver.TargetModeUnicast,
			MaxBudgetMs: 3000,
		}
	case "pointscan":
		meta.Protocols[0].PointDiscovery = fixturePointSpec("夹具点扫", 2000)
	case "pointscan_serial":
		// 与 pointscan 只差 SerialExclusive。挂串口标记的协议在插件侧会走
		// 「有会话但不命中时绝不回落临时实例」那条路 —— 只有真串口才验得到。
		meta.Protocols[0].SerialExclusive = true
		meta.Protocols[0].PointDiscovery = fixturePointSpec("夹具点扫(串口)", 2000)
	case "pointscan2":
		// 与 pointscan 只差 Title 与预算，理由同 discovery / discovery2：
		// registrationSig 不含 PointDiscovery，热升级不会重注册。
		meta.Protocols[0].PointDiscovery = fixturePointSpec("夹具点扫 v2", 3000)
	case "invalidmeta":
		// Serve 启动期校验会拒绝空协议表，进程随即退出 —— 等价于一个
		// 编译坏了的新二进制。
		meta.Protocols = nil
	}
	return meta
}

const (
	fixtureProtocol    = "Test.Fixture.Plugin"
	fixtureProtocolAlt = "Test.Fixture.Alt"
	fixtureScope       = "fixture"
	fixtureScopeAlt    = "fixture2"
)

func runFixturePlugin() int {
	meta := fixtureMeta()
	if os.Getenv(envFixture) == "badmeta" {
		// 非法的 Meta：Serve 启动期校验失败并立即返回，进程随即退出。
		meta.Protocols = nil
	}

	factories := driversdk.Factories{
		fixtureProtocol: func() driver.Driver { return &fixtureDriver{} },
	}
	// 工厂表的键必须与 Meta.Protocols 一一对应，缺一个会在插件**启动时**报错。
	if os.Getenv(envFixtureVariant) == "addproto" {
		factories[fixtureProtocolAlt] = func() driver.Driver { return &fixtureDriver{} }
	}

	// 同理：声明了 Discovery / PointDiscovery 的协议必须在对应能力表里有实现。
	// 基线变体两个都不声明，能力表因此保持零值 —— 这同时钉住了「不实现任何扫描
	// 的既有插件传零值 Capabilities 必须能正常启动」。
	var caps driversdk.Capabilities
	if meta.Protocols[0].Discovery != nil {
		caps.Probers = driversdk.Probers{fixtureProtocol: fixtureProbe}
	}
	if meta.Protocols[0].PointDiscovery != nil {
		caps.PointFinders = driversdk.PointFinders{fixtureProtocol: fixturePointFinder}
	}

	if err := driversdk.Serve(factories, meta, caps); err != nil {
		fmt.Fprintf(os.Stderr, "fixture: %v\n", err)
		return 1
	}
	return 0
}

// fixtureProbe 夹具的扫描实现。把收到的输入原样回一条"发现"，使测试能断言
// 「结果确实穿过了进程边界、且 Options/Budget 无损」—— 这两件事在进程内的
// sdk 单测里都验不到。
func fixtureProbe(req driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
	if req.Target.Host == "" {
		// 空目标是"这不是我的设备"，不是错误 —— 顺便钉住 (nil, nil) 这条语义。
		return nil, nil
	}
	return []driver.DiscoveredDevice{{
		Host:         req.Target.Host,
		Port:         req.Target.Port,
		UnitID:       1,
		Name:         "fixture-" + req.Target.Host,
		Vendor:       "iot-gateway",
		Model:        "fixture",
		Version:      "1.0.0",
		ProtocolJSON: fmt.Sprintf(`{"host":%q,"port":%d}`, req.Target.Host, req.Target.Port),
		Extra: []driver.Attribute{
			{Name: "protocol", Value: req.Protocol},
			{Name: "options", Value: string(req.Options)},
			{Name: "budgetMs", Value: strconv.FormatInt(req.Budget.Milliseconds(), 10)},
			{Name: "localIp", Value: req.Target.LocalIP},
		},
	}}, nil
}

// fixturePointSpec 夹具协议的点位扫描声明。
//
// DefaultCommonDataType 必须是 Meta.TypeMap 的键（这里是 "Short"）——
// 写成内部裸名（"int16"）会在启动期被 Meta.Validate 拒掉，这条约束由本夹具
// 顺带钉住。
func fixturePointSpec(title string, budgetMs int) *driver.PointDiscoverySpec {
	return &driver.PointDiscoverySpec{
		Title:                 title,
		Description:           "夹具点位扫描：不读任何真实地址，只把收到的输入回传",
		FormJSON:              json.RawMessage(`{"rule":[{"type":"input","field":"range","title":"范围"}]}`),
		DefaultCommonDataType: "Short",
		MaxBudgetMs:           budgetMs,
		MaxPoints:             100,
	}
}

// fixturePointFinder 夹具的点扫实现。
//
// 与 fixtureProbe 同一条思路：不读任何真实地址，把收到的输入原样回在结果里，
// 使测试能断言「结果确实穿过了进程边界、且 Options/预算/会话无损」——
// 这几件事在进程内的 sdk 单测里都验不到。
//
// 它**刻意真的走一次 Session.Do**：那既钉住"finder 能拿到驱动实例"，
// 也让串口变体能自证"扫描用的正是采集持有的那个实例"（临时实例连同一串口
// 必然 Access is denied，根本 Do 不出来）。
func fixturePointFinder(req driversdk.FindPointsRequest) (*driver.PointScanResult, error) {
	sessionTag := "none"
	if req.Session != nil {
		if err := req.Session.Do(func(drv driver.Driver) error {
			sessionTag = "plain"
			if h, ok := drv.(*fixtureDriver); ok && h.port != "" {
				sessionTag = "holding:" + h.port
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return &driver.PointScanResult{
		AssumedCommonDataType: "Short",
		Scanned:               1,
		Readable:              1,
		Points: []driver.DiscoveredPoint{{
			Name: "fixture-point",
			// Label 承载扫描选项原文：PointScanResult 没有 Extra 字段，
			// 而 Label 是结果里唯一"自由文本且会原样过线"的位置。
			Label: string(req.Options),
			Note: fmt.Sprintf("protocol=%s budgetMs=%d deadlineSet=%t session=%s",
				req.Protocol, req.Budget.Milliseconds(), !req.Deadline.IsZero(), sessionTag),
		}},
	}, nil
}

// ==================== 测试辅助 ====================

// installFixture 把测试二进制复制成插件文件名放进临时目录，返回插件路径与目录。
//
// 文件名以 iot-driver- 开头，与默认 glob 一致。
func installFixture(t *testing.T) (pluginPath string) {
	t.Helper()

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("定位测试二进制: %v", err)
	}
	self, err = filepath.Abs(self)
	if err != nil {
		t.Fatalf("解析测试二进制绝对路径: %v", err)
	}

	dir := t.TempDir()
	name := "iot-driver-fixture"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	pluginPath = filepath.Join(dir, name)

	if err := copyFile(self, pluginPath); err != nil {
		t.Fatalf("复制夹具二进制: %v", err)
	}
	return pluginPath
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// fixtureOptions 返回指向夹具插件的进程参数，Env 中带上夹具标记。
func fixtureOptions(t *testing.T, pluginPath string) processOptions {
	t.Helper()
	env := append(pluginEnv(), envFixture+"=1")
	return processOptions{
		Env:         env,
		Watchdog:    100 * time.Millisecond,
		RestartMin:  10 * time.Millisecond,
		RestartMax:  50 * time.Millisecond,
		MaxRestarts: 20,
		StableAfter: time.Hour, // 测试中不触发"稳定"重置，便于观察退避
		KillGrace:   time.Second,
		Timeouts: Timeouts{
			Handshake: 10 * time.Second,
			Connect:   5 * time.Second,
			Ping:      5 * time.Second,
			Read:      2 * time.Second,
			Close:     time.Second,
		},
		Infof: func(format string, args ...any) { t.Logf(format, args...) },
		Warnf: func(format string, args ...any) { t.Logf(format, args...) },
	}
}

// waitFor 轮询条件直到成立或超时。
func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", desc)
}

// ==================== 测试 ====================

// TestProcessHandshakeAndDriverLifecycle 走真实的 exec.Command 路径：
// 拉起子进程 → describe 握手 → 通过 remoteDriver 完成 connect/read/close。
func TestProcessHandshakeAndDriverLifecycle(t *testing.T) {
	path := installFixture(t)
	p := newPluginProcess(path, fixtureOptions(t, path))
	p.start()
	t.Cleanup(p.stop)

	waitFor(t, 10*time.Second, "夹具进程就绪", p.isUp)

	meta := p.describeMeta()
	if len(meta.Protocols) != 1 || meta.Protocols[0].Name != fixtureProtocol {
		t.Fatalf("describe 返回协议 %+v, want [%s]", meta.Protocols, fixtureProtocol)
	}
	if meta.Scope != "fixture" || len(meta.Types) != 2 {
		t.Fatalf("describe 元数据不符: scope=%q types=%d", meta.Scope, len(meta.Types))
	}
	// 编解码函数不出插件进程。
	for _, dt := range meta.Types {
		if dt.Decode != nil || dt.Encode != nil || dt.Format != nil {
			t.Errorf("类型 %s 不应携带编解码函数", dt.Name)
		}
	}

	rd := newRemoteDriver(p, fixtureProtocol, meta.Protocols[0].SerialExclusive, DefaultTimeouts())
	if err := rd.Connect(`{}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !rd.IsConnected() {
		t.Fatal("Connect 后应已连接")
	}

	addrs := []po.DeviceAddress{
		{ID: "a1", Name: "point1", DataType: "int16"},
		{ID: "a2", Name: "point2", DataType: "bool"},
	}
	got, err := rd.Read(sdkAddrs(addrs))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != len(addrs) {
		t.Fatalf("结果数 = %d, want %d", len(got), len(addrs))
	}
	for i := range addrs {
		if got[i].DeviceAddressID != addrs[i].ID {
			t.Errorf("结果[%d].DeviceAddressID = %q, want %q", i, got[i].DeviceAddressID, addrs[i].ID)
		}
		if got[i].Value != "fixture-"+addrs[i].Name {
			t.Errorf("结果[%d].Value = %q, want %q", i, got[i].Value, "fixture-"+addrs[i].Name)
		}
		if got[i].Kind != driver.KindInt {
			t.Errorf("结果[%d].Kind = %q, want %q", i, got[i].Kind, driver.KindInt)
		}
	}

	if err := rd.Ping(`{}`); err != nil {
		t.Errorf("Ping: %v", err)
	}
	if err := rd.Ping(`{"ping":"fail"}`); err == nil {
		t.Error("Ping 失败应向上返回错误")
	}

	if err := rd.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if rd.IsConnected() {
		t.Error("Close 后应未连接")
	}
}

// TestProcessCrashRestartAndRecovery 是本方案的核心自愈路径：
// 插件进程崩溃后 IsConnected() 必须立刻为 false（触发采集引擎重连），
// 监管重新拉起进程后驱动可以干净地恢复。
func TestProcessCrashRestartAndRecovery(t *testing.T) {
	path := installFixture(t)
	p := newPluginProcess(path, fixtureOptions(t, path))
	p.start()
	t.Cleanup(p.stop)

	waitFor(t, 10*time.Second, "夹具进程就绪", p.isUp)

	rd := newRemoteDriver(p, fixtureProtocol, false, DefaultTimeouts())

	// 先用 crash 模式连接：Read 会让插件进程直接退出。
	if err := rd.Connect(`{"mode":"crash"}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := rd.Read([]driver.DeviceAddress{{ID: "a1", Name: "p1", DataType: "int16"}}); err == nil {
		t.Fatal("插件进程已退出，Read 应报错")
	}

	// 进程崩溃后 IsConnected() 必须为 false —— 这是采集引擎重连的唯一触发条件。
	waitFor(t, 5*time.Second, "IsConnected 变为 false", func() bool { return !rd.IsConnected() })

	// 监管应重新拉起进程。
	waitFor(t, 10*time.Second, "进程被重新拉起", p.isUp)

	// 重连并恢复正常读取（此时 mode 为空，走正常分支）。
	if err := rd.Connect(`{}`); err != nil {
		t.Fatalf("崩溃恢复后 Connect: %v", err)
	}
	if !rd.IsConnected() {
		t.Fatal("恢复后应已连接")
	}
	got, err := rd.Read([]driver.DeviceAddress{{ID: "a1", Name: "p1", DataType: "int16"}})
	if err != nil {
		t.Fatalf("恢复后 Read: %v", err)
	}
	if len(got) != 1 || got[0].Value != "fixture-p1" {
		t.Fatalf("恢复后读值异常: %+v", got)
	}
}

// TestProcessStuckIsKilledAndRestarted 覆盖"进程还活着但卡死"：
// 没有卡死巡检，IsConnected() 会永远停在 true 且 Read 永远超时，形成无恢复的活锁。
func TestProcessStuckIsKilledAndRestarted(t *testing.T) {
	path := installFixture(t)
	opts := fixtureOptions(t, path)
	// 注入极小阈值，让卡死在毫秒级被判定。
	opts.StuckStreak = 1
	opts.StuckAfter = 50 * time.Millisecond
	opts.Timeouts.Read = 100 * time.Millisecond

	p := newPluginProcess(path, opts)
	p.start()
	t.Cleanup(p.stop)

	waitFor(t, 10*time.Second, "夹具进程就绪", p.isUp)
	firstEpoch := p.epoch()

	rd := newRemoteDriver(p, fixtureProtocol, false, opts.Timeouts)
	if err := rd.Connect(`{"mode":"hang"}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// 这次 Read 会永久阻塞在插件里，直到网关侧超时放弃。
	if _, err := rd.Read([]driver.DeviceAddress{{ID: "a1", Name: "p1", DataType: "int16"}}); err == nil {
		t.Fatal("卡死的插件不应返回成功")
	}

	// 卡死巡检应在超时后杀掉并重启进程（世代号递增）。
	waitFor(t, 10*time.Second, "卡死进程被重启", func() bool { return p.epoch() > firstEpoch })

	// 新一代进程可用后，驱动能重新连上并正常读取。
	waitFor(t, 10*time.Second, "新一代进程就绪", p.isUp)
	if err := rd.Connect(`{}`); err != nil {
		t.Fatalf("卡死恢复后 Connect: %v", err)
	}
	got, err := rd.Read([]driver.DeviceAddress{{ID: "a1", Name: "p1", DataType: "int16"}})
	if err != nil {
		t.Fatalf("卡死恢复后 Read: %v", err)
	}
	if len(got) != 1 || got[0].Value != "fixture-p1" {
		t.Fatalf("卡死恢复后读值异常: %+v", got)
	}
}

// TestProcessStopKillsAndReaps stop() 必须在宽限期内返回，且进程被回收
// （cmd.ProcessState 非 nil 说明 Wait 已执行，不留僵尸）。
func TestProcessStopKillsAndReaps(t *testing.T) {
	path := installFixture(t)
	p := newPluginProcess(path, fixtureOptions(t, path))
	p.start()

	waitFor(t, 10*time.Second, "夹具进程就绪", p.isUp)

	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil {
		t.Fatal("进程已就绪但 cmd 为空")
	}

	done := make(chan struct{})
	go func() { p.stop(); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop() 未在宽限期内返回")
	}

	if cmd.ProcessState == nil {
		t.Error("进程未被回收（cmd.ProcessState 为 nil，说明 Wait 未执行）")
	}
	// 重复 stop 必须幂等。
	p.stop()

	if p.isUp() {
		t.Error("stop 后进程不应仍可用")
	}
}

// TestProcessOrphanPreventionOnStdinEOF 模拟父进程被强杀：
// 关闭插件的 stdin 后，SDK 的读循环遇到 EOF 应让插件自行退出。
// 这是防孤儿进程的第一层（也是最重要的一层）。
func TestProcessOrphanPreventionOnStdinEOF(t *testing.T) {
	path := installFixture(t)

	cmd := exec.Command(path)
	cmd.Env = append(pluginEnv(), envFixture+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	if _, err := cmd.StdoutPipe(); err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("StderrPipe: %v", err)
	}
	// 排空 stderr，避免子进程因管道写满而阻塞（插件把日志写 stderr）。
	go func() { _, _ = io.Copy(io.Discard, stderr) }()

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// 关闭 stdin（父子管道断开），插件应自行退出。
	if err := stdin.Close(); err != nil {
		t.Fatalf("关闭 stdin: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-done:
		// 正常退出（父进程消失时不留下孤儿进程）。
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("stdin EOF 后插件未自行退出，会变成孤儿进程")
	}
}

// TestProcessHandshakeFailureIsRetriedThenGivenUp 插件能启动但 Meta 非法
// （describe 或校验失败）：监管必须重试到上限后放弃，过程中不 panic、
// 不留下"假可用"的进程。
func TestProcessHandshakeFailureIsRetriedThenGivenUp(t *testing.T) {
	path := installFixture(t)

	opts := fixtureOptions(t, path)
	opts.Env = append(pluginEnv(), envFixture+"=badmeta")
	opts.Timeouts.Handshake = 2 * time.Second
	opts.MaxRestarts = 2

	p := newPluginProcess(path, opts)
	p.start()
	t.Cleanup(p.stop)

	// 等监管走完重试并放弃（3 次尝试 × 极短退避）。
	waitFor(t, 15*time.Second, "监管放弃重试", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.restarts > opts.MaxRestarts
	})

	if p.isUp() {
		t.Error("Meta 非法的插件不应被判定为可用")
	}
	if p.epoch() != 0 {
		t.Errorf("握手从未成功，世代号应为 0，实际 %d", p.epoch())
	}
	// acquire 必须报错而不是永久阻塞（受 ctx 约束）。
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if _, err := p.acquire(ctx); err == nil {
		t.Error("进程不可用时 acquire 应报错")
	}
}

// TestProcessStopInterruptsHandshake 停机必须能立即中止进行中的握手。
//
// 若不中断，stop() 会一直等到握手超时（默认 10s）才返回 —— 一个启动失败的插件
// 就能把网关的停机流程拖长一个超时周期。这里用超长的握手超时把该行为放大。
func TestProcessStopInterruptsHandshake(t *testing.T) {
	path := installFixture(t)

	opts := fixtureOptions(t, path)
	opts.Env = append(pluginEnv(), envFixture+"=sleep")
	opts.Timeouts.Handshake = 10 * time.Minute // 故意极大，只有中断才能让它提前结束

	p := newPluginProcess(path, opts)
	p.start()
	t.Cleanup(p.stop) // 幂等；失败路径上也要把子进程收掉，否则临时目录删不掉

	// 等监管循环确实进入了 spawn（进程已启动，正卡在握手等待上）。
	waitFor(t, 10*time.Second, "进程被拉起", func() bool { return p.spawned() > 0 })

	done := make(chan struct{})
	go func() { p.stop(); close(done) }()

	select {
	case <-done:
		// 停机能立即返回。
	case <-time.After(5 * time.Second):
		t.Fatal("stop() 未中断进行中的握手，会拖慢网关停机")
	}

	if p.isUp() {
		t.Error("stop 后进程不应仍可用")
	}
	if p.epoch() != 0 {
		t.Errorf("握手从未成功，世代号应为 0，实际 %d", p.epoch())
	}
}

// TestProcessRestartBackoffRamps 崩溃重启的退避必须逐次翻倍，不能每次都退回下限。
//
// 这条挡的是一个很容易被"顺手优化"掉的缺陷：markUp 里多一句 p.delay = 0，
// 退避就再也不会递增 —— 每次崩溃重启都从 RestartMin 重新开始，`delay *= 2`
// 成为不可达代码，MaxRestarts 次重试在 MaxRestarts×RestartMin 内烧完，
// 插件被永久放弃（该插件下所有设备一直断），而文档承诺的是 1s→2s→…→上限。
//
// 判据取"每次重新就绪后记录下来的 delay"：它必须是严格递增的。
func TestProcessRestartBackoffRamps(t *testing.T) {
	path := installFixture(t)

	// StableAfter 由 fixtureOptions 设为 1 小时：测试全程都不会被判定为"稳定"，
	// 因此退避只按失败次数递增，不会被 markDown 的稳定重置抹平。
	opts := fixtureOptions(t, path)
	p := newPluginProcess(path, opts)
	p.start()
	t.Cleanup(p.stop)

	waitFor(t, 10*time.Second, "夹具进程首次就绪", func() bool { return p.epoch() == 1 })

	var delays []time.Duration
	for i := 0; i < 3; i++ {
		prev := p.epoch()
		cmd := p.currentCmd()
		if cmd == nil || cmd.Process == nil {
			t.Fatal("进程未拉起，无法制造崩溃")
		}
		if err := cmd.Process.Kill(); err != nil {
			t.Fatalf("kill: %v", err)
		}
		waitFor(t, 10*time.Second, "进程被监管重新拉起", func() bool { return p.epoch() > prev })

		p.mu.Lock()
		delays = append(delays, p.delay)
		p.mu.Unlock()
	}

	for i := 1; i < len(delays); i++ {
		if delays[i] <= delays[i-1] {
			t.Fatalf("退避未递增: %v —— 每次崩溃重启都退回下限，"+
				"MaxRestarts 次重试会被很快烧完，插件再也不会被拉起", delays)
		}
	}
}

// TestProcessRefusesRestartWhenBinaryReplaced 崩溃重启**不是**一次热升级：
// 磁盘上的内容已经变了就不能照旧拉起。
//
// 照旧拉起的后果有两个：进程用它自己的 Meta（协议名、类型表）服务，而网关的
// 运行时注册表还是上一版 —— 相关协议全部 unknown protocol；以及在
// plugin.disableHotUpgrade=true 时静默换上新版本，直接违背"重启网关后才生效"
// 的承诺（且 lp.sha/lp.meta/协议行供给全部不更新）。
//
// 正确行为是拒绝拉起、把"文件变了"交回目录扫描（热升级会做完整注册）。
// 这里同时钉住"拒绝"与"不把它当成启动失败去烧重试预算"两点。
func TestProcessRefusesRestartWhenBinaryReplaced(t *testing.T) {
	path := installFixture(t)
	sha, err := hashFile(path)
	if err != nil {
		t.Fatalf("hashFile: %v", err)
	}

	opts := fixtureOptions(t, path)
	p := newPluginProcessAt(path, opts, sha)
	p.start()
	t.Cleanup(p.stop)

	waitFor(t, 10*time.Second, "夹具进程首次就绪", func() bool { return p.epoch() == 1 })

	// 替换磁盘上的内容（模拟运维覆盖了二进制），再让它崩溃。
	// 追加而非改坏：万一这条守卫被回退，追加过的二进制在本平台仍能启动，
	// 于是"被错误地拉起"这件事会被下面的 spawned 断言明确抓住。
	appendBytes(t, path, []byte("x"))

	cmd := p.currentCmd()
	if cmd == nil || cmd.Process == nil {
		t.Fatal("进程未拉起，无法制造崩溃")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}

	// 等到"若在重试早该重试过好几轮"的时间，再断言它一次都没再拉起。
	time.Sleep(600 * time.Millisecond)

	if n := p.spawned(); n != 1 {
		t.Errorf("二进制已被替换，不应再拉起进程：spawned = %d, want 1", n)
	}
	if e := p.epoch(); e != 1 {
		t.Errorf("世代号 = %d, want 1（没有任何新进程被拉起）", e)
	}
	if p.isUp() {
		t.Error("被替换的二进制不应被判定为可用")
	}
	if n := p.restarts; n != 1 {
		t.Errorf("restarts = %d, want 1（只应记下这次崩溃本身，不得反复计为启动失败）", n)
	}
}

// TestPluginEnvExcludesSecrets 子进程环境不得包含网关密钥类变量。
func TestPluginEnvExcludesSecrets(t *testing.T) {
	t.Setenv("IOT_GATEWAY_JWT_SECRET", "super-secret")
	t.Setenv("SOME_OTHER_SECRET", "also-secret")

	env := pluginEnv()
	for _, e := range env {
		if strings.HasPrefix(e, "IOT_GATEWAY_JWT_SECRET=") {
			t.Error("插件环境不应包含 JWT 签名密钥")
		}
		if strings.HasPrefix(e, "SOME_OTHER_SECRET=") {
			t.Error("插件环境不应包含任意业务密钥变量")
		}
	}
	// 必须带上标记变量。
	found := false
	for _, e := range env {
		if e == "IOT_GATEWAY_PLUGIN=1" {
			found = true
		}
	}
	if !found {
		t.Error("插件环境应包含 IOT_GATEWAY_PLUGIN=1 标记")
	}
}
