// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/driversdk"
)

// ==================== 测试辅助 ====================

// setFixtureEnv 改夹具子进程的环境变量。
//
// 只在测试自己驱动的扫描里安全：newTestManager 把扫描间隔设成了 1 小时，
// 所有 scanOnce 都由 m.Reload() 在测试协程上串行触发，不存在与 ticker 的竞争。
// 另外 m.opts 只在构造 pluginProcess 时被复制一次，改它只影响之后拉起的新进程。
func setFixtureEnv(t *testing.T, m *Manager, kv ...string) {
	t.Helper()
	m.opts.Env = append(append(pluginEnv(), envFixture+"=1"), kv...)
}

// fixturePluginPath 返回目录里唯一的插件文件，解析方式与 Manager 内部一致。
func fixturePluginPath(t *testing.T, dir string) string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, "iot-driver-*"))
	if len(matches) != 1 {
		t.Fatalf("期望 1 个插件文件，实际 %d", len(matches))
	}
	resolved, err := filepath.EvalSymlinks(matches[0])
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	return abs
}

// onlyPlugin 返回管理器里唯一的已加载插件。
func onlyPlugin(t *testing.T, m *Manager) *loadedPlugin {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, lp := range m.plugins {
		return lp
	}
	t.Fatal("没有已加载的插件")
	return nil
}

// replacePluginBinary 就地改写插件文件，模拟运维替换了插件二进制。
//
// 夹具没有第二个可执行文件，所以「新版本长什么样」由子进程环境变量承载
// （见 envFixtureTag / envFixtureVariant），这里只负责让**文件**的指纹与内容
// 都发生变化 —— 正是 upgrade 里「先比 (size, mtime)、再比 sha」要区分的两件事。
func replacePluginBinary(t *testing.T, path string) {
	t.Helper()
	appendBytes(t, path, []byte("x"))
}

// touchPlugin 只改 mtime、不改内容（模拟 touch / 备份工具触碰了文件）。
func touchPlugin(t *testing.T, path string) {
	t.Helper()
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

// startWithRunningReloader 启动管理器，等初始加载落地后**才**把 Reloader
// 标记为运行中，并把期间的刷新清零。
//
// 顺序不能反：首次发现插件本身就会 scheduleRefresh，若提前把 running 置真，
// 那次刷新会被算进来，「升级到底刷没刷」就再也断言不出来了。
// 这与 collector 的真实时序一致 —— Manager.Start() 早于 CollectorEngine.Start()。
func startWithRunningReloader(t *testing.T, m *Manager, reload *fakeReloader) {
	t.Helper()
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	// 首轮加载的防抖窗口走完再启用，并断言基线确实为 0。
	time.Sleep(200 * time.Millisecond)
	reload.calls.Store(0)
	reload.running.Store(true)
}

// connectFixture 创建一个该协议的驱动实例并连上。
func connectFixture(t *testing.T, protocol string) driver.Driver {
	t.Helper()
	d, err := driver.Create(protocol)
	if err != nil {
		t.Fatalf("driver.Create(%s): %v", protocol, err)
	}
	if err := d.Connect(`{}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// readFixture 读一个点位，返回格式化后的值。
func readFixture(t *testing.T, d driver.Driver, name string) string {
	t.Helper()
	got, err := d.Read([]driver.DeviceAddress{{ID: "a1", Name: name, DataType: "int16"}})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("结果数 = %d, want 1", len(got))
	}
	return got[0].Value
}

// ==================== 热升级：注册面比对（纯函数） ====================

// TestRegistrationSigIgnoresCosmeticFields 固化「哪些字段参与注册面比对」。
//
// 判错的代价是不对称的：把该比的漏掉 → 注册表与插件实际能力不符（静默错配）；
// 把不该比的比上 → 每次改版本号都白走一次重注册 + 可能的全局 Refresh。
func TestRegistrationSigIgnoresCosmeticFields(t *testing.T) {
	base := func() driversdk.Meta {
		return driversdk.Meta{
			APIVersion:  driversdk.APIVersion,
			Scope:       "acme",
			Description: "说明",
			Version:     "1.0.0",
			Protocols: []driversdk.Protocol{
				{Name: "Acme.TCP", FormJSON: []byte(`{"a":1}`)},
				{Name: "Acme.RTU", SerialExclusive: true},
			},
			Types: []*driver.DataType{
				{Name: "int16", Kind: driver.KindInt, Size: 2},
				{Name: "bool", Kind: driver.KindBool, Size: 1},
			},
			TypeMap: map[string]string{"Short": "int16", "Boolean": "bool"},
		}
	}

	// 与注册无关的字段随便改，指纹必须不变。
	cosmetic := base()
	cosmetic.Description = "换了说明"
	cosmetic.Version = "2.0.0"
	cosmetic.Protocols[0].FormJSON = []byte(`{"b":2}`)
	cosmetic.Protocols[1].Description = "串口协议"
	cosmetic.Protocols[0], cosmetic.Protocols[1] = cosmetic.Protocols[1], cosmetic.Protocols[0] // 顺序无关

	if !registrationSigOf(base()).equal(registrationSigOf(cosmetic)) {
		t.Error("Description/Version/FormJSON/协议顺序变化不应被判定为注册面变化")
	}

	// Types 顺序同样无关（TypeRegistry.Register 按名字索引）。
	reordered := base()
	reordered.Types[0], reordered.Types[1] = reordered.Types[1], reordered.Types[0]
	if !registrationSigOf(base()).equal(registrationSigOf(reordered)) {
		t.Error("Types 顺序变化不应被判定为注册面变化")
	}

	cases := []struct {
		name   string
		mutate func(*driversdk.Meta)
	}{
		{"换 scope", func(m *driversdk.Meta) { m.Scope = "acme2" }},
		{"加协议", func(m *driversdk.Meta) { m.Protocols = append(m.Protocols, driversdk.Protocol{Name: "Acme.UDP"}) }},
		{"改协议名", func(m *driversdk.Meta) { m.Protocols[0].Name = "Acme.TCP2" }},
		{"翻转 SerialExclusive", func(m *driversdk.Meta) { m.Protocols[0].SerialExclusive = true }},
		{"加类型", func(m *driversdk.Meta) {
			m.Types = append(m.Types, &driver.DataType{Name: "int32", Kind: driver.KindInt, Size: 4})
		}},
		{"改类型 Size", func(m *driversdk.Meta) { m.Types[0].Size = 8 }},
		{"改类型 Kind", func(m *driversdk.Meta) { m.Types[0].Kind = driver.KindUInt }},
		{"加 TypeMap 条目", func(m *driversdk.Meta) { m.TypeMap["Int32"] = "int32" }},
		{"删 TypeMap 条目", func(m *driversdk.Meta) { delete(m.TypeMap, "Boolean") }},
		// TypeMap 的 key 大小写敏感：AvailableTypes 用 CommonDataTypes 里的
		// 规范名直接取值，key 换大小写会让该条目静默从类型下拉里消失。
		{"改 TypeMap key 大小写", func(m *driversdk.Meta) { m.TypeMap["boolean"] = m.TypeMap["Boolean"]; delete(m.TypeMap, "Boolean") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := base()
			tc.mutate(&mutated)
			if registrationSigOf(base()).equal(registrationSigOf(mutated)) {
				t.Errorf("%s 必须被判定为注册面变化", tc.name)
			}
		})
	}
}

// TestNeedsTaskRebuild 只有「协议名集合」与「SerialExclusive」才需要重建采集任务。
//
// 采集任务在建任务时就固化了这两件事（collector.serialExclusive 与
// 「哪些设备能建出驱动」）；类型表 / TypeMap / scope 都是查询时现算的。
// 判宽的代价是每次改类型都白白中断全部采集。
func TestNeedsTaskRebuild(t *testing.T) {
	meta := func(scope string, protocols ...driversdk.Protocol) driversdk.Meta {
		return driversdk.Meta{Scope: scope, Protocols: protocols}
	}

	tcp := driversdk.Protocol{Name: "Acme.TCP"}
	rtu := driversdk.Protocol{Name: "Acme.RTU", SerialExclusive: true}

	cases := []struct {
		name     string
		old, new driversdk.Meta
		want     bool
	}{
		{"完全相同", meta("a", tcp, rtu), meta("a", tcp, rtu), false},
		{"仅 scope 变化", meta("a", tcp), meta("b", tcp), false},
		{"加协议", meta("a", tcp), meta("a", tcp, rtu), true},
		{"删协议", meta("a", tcp, rtu), meta("a", tcp), true},
		{"换协议名", meta("a", tcp), meta("a", rtu), true},
		{"翻转 SerialExclusive", meta("a", tcp), meta("a", driversdk.Protocol{Name: "Acme.TCP", SerialExclusive: true}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsTaskRebuild(tc.old, tc.new); got != tc.want {
				t.Errorf("needsTaskRebuild = %v, want %v", got, tc.want)
			}
		})
	}
}

// ==================== 热升级：端到端 ====================

// TestManagerUpgradesReplacedBinary 热升级的核心路径。
//
// 四件事必须同时成立，缺一条这个能力就不成立：
//  1. 换代后**既有**驱动实例看得见（IsConnected 转 false），否则采集任务永远
//     挂在旧进程上，等于没升级；
//  2. 重连后读到的是新版本的数据；
//  3. 旧进程确实退役了（否则串口/句柄仍被占着）；
//  4. **没有**触发全局 Refresh —— 这正是热升级相对「重启网关」的全部价值。
func TestManagerUpgradesReplacedBinary(t *testing.T) {
	reload := &fakeReloader{}
	m, _, dir := newTestManager(t, reload)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	path := fixturePluginPath(t, dir)
	before := m.Status()[0]
	oldProc := onlyPlugin(t, m).link.current()

	d := connectFixture(t, fixtureProtocol)
	if got := readFixture(t, d, "p1"); got != "fixture-p1" {
		t.Fatalf("升级前读到 %q, want fixture-p1", got)
	}

	// 替换插件："新版本"的行为由环境变量承载，文件本身也要变。
	setFixtureEnv(t, m, envFixtureTag+"=v2-")
	replacePluginBinary(t, path)

	m.Reload()

	after := m.Status()[0]
	if after.SHA256 == before.SHA256 {
		t.Error("sha256 应已更新为新版本")
	}
	if after.PreviousSHA256 != before.SHA256 {
		t.Errorf("PreviousSHA256 = %q, want %q（应留档被替换掉的版本）", after.PreviousSHA256, before.SHA256)
	}
	if after.Upgrades != 1 {
		t.Errorf("Upgrades = %d, want 1", after.Upgrades)
	}
	if after.UpgradedAt == "" {
		t.Error("UpgradedAt 不应为空")
	}
	if after.LastUpgradeError != "" {
		t.Errorf("LastUpgradeError = %q, want 空", after.LastUpgradeError)
	}
	if after.Epoch != before.Epoch+1 {
		t.Errorf("Epoch = %d, want %d（每次成功升级恰好 +1）", after.Epoch, before.Epoch+1)
	}
	if oldProc.isUp() {
		t.Error("旧进程实例应已退役")
	}
	if onlyPlugin(t, m).link.current() == oldProc {
		t.Error("link 应已换入新进程实例")
	}

	// 1. 既有实例必须感知换代，否则采集引擎不会重连（它只在 IsConnected
	//    返回 false 时才重连，Read 报错本身不触发）。
	if d.IsConnected() {
		t.Fatal("换代后既有驱动实例应报告未连接")
	}
	// 2. 重连后走的是新进程。
	if err := d.Connect(`{}`); err != nil {
		t.Fatalf("重连: %v", err)
	}
	if got := readFixture(t, d, "p1"); got != "fixture-v2-p1" {
		t.Errorf("升级后读到 %q, want fixture-v2-p1（说明还在用旧进程）", got)
	}

	// 4. 全程不得触发全局刷新。
	time.Sleep(200 * time.Millisecond)
	if n := reload.calls.Load(); n != 0 {
		t.Errorf("热升级不应触发全局采集刷新，实际 %d 次", n)
	}
}

// TestManagerUpgradeWithIdenticalContentDoesNotRestart 指纹变了但内容没变
// （touch、备份工具、重复拷贝同一构建）时不得重启驱动。
//
// 只看 (size, mtime) 的话，一次 touch 就会让该插件下所有设备白重连一轮。
func TestManagerUpgradeWithIdenticalContentDoesNotRestart(t *testing.T) {
	m, _, dir := newTestManager(t, &fakeReloader{})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	d := connectFixture(t, fixtureProtocol)
	before := m.Status()[0]

	touchPlugin(t, fixturePluginPath(t, dir))
	m.Reload()

	after := m.Status()[0]
	if after.SHA256 != before.SHA256 || after.Epoch != before.Epoch || after.Upgrades != 0 {
		t.Errorf("内容未变不应重启驱动: sha %v→%v epoch %d→%d upgrades=%d",
			before.SHA256[:8], after.SHA256[:8], before.Epoch, after.Epoch, after.Upgrades)
	}
	if !d.IsConnected() {
		t.Error("内容未变时既有驱动应保持连接")
	}
}

// TestManagerUpgradeFailureKeepsOldVersion 新二进制起不来时，旧版本必须毫发无损。
//
// 这是整个设计里最要紧的失败模式：一次失手的替换不该让现场停摆。
func TestManagerUpgradeFailureKeepsOldVersion(t *testing.T) {
	m, _, dir := newTestManager(t, &fakeReloader{})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	path := fixturePluginPath(t, dir)
	d := connectFixture(t, fixtureProtocol)
	before := m.Status()[0]

	// 握手超时决定这条测试的耗时（spawnAndDescribe 额外留 2s 余量）。
	m.opts.Timeouts.Handshake = 100 * time.Millisecond
	setFixtureEnv(t, m, envFixtureVariant+"=invalidmeta")
	replacePluginBinary(t, path)

	m.Reload()

	after := m.Status()[0]
	if after.SHA256 != before.SHA256 {
		t.Error("升级失败时不应更新 sha（旧版本仍在运行）")
	}
	if after.Epoch != before.Epoch || after.Upgrades != 0 {
		t.Error("升级失败时不应换代")
	}
	if after.LastUpgradeError == "" {
		t.Error("LastUpgradeError 应记录失败原因 —— 失败时 Up 仍为 true，只看 Up 会误以为已生效")
	}
	// 旧版本继续可用。
	if !d.IsConnected() {
		t.Error("升级失败后旧驱动应保持连接")
	}
	if got := readFixture(t, d, "p1"); got != "fixture-p1" {
		t.Errorf("升级失败后应继续由旧版本出数，实际 %q", got)
	}

	// 同一个坏指纹不得被反复重试（每次代价是拉起进程 + 等满握手超时）。
	m.mu.Lock()
	_, suppressed := m.failed[path]
	m.mu.Unlock()
	if !suppressed {
		t.Error("失败的新指纹应记入 failed 以抑制重试")
	}
	for i := 0; i < 3; i++ {
		m.Reload()
	}
	if got := m.Status()[0]; got.Epoch != before.Epoch || got.Upgrades != 0 {
		t.Error("同一坏版本不应被反复重试")
	}

	// 换成能起来的版本：升级应成功，且抑制标记被清除。
	setFixtureEnv(t, m, envFixtureTag+"=v2-")
	replacePluginBinary(t, path)
	m.Reload()

	after = m.Status()[0]
	if after.Upgrades != 1 || after.LastUpgradeError != "" {
		t.Errorf("改为合法版本后应升级成功: upgrades=%d err=%q", after.Upgrades, after.LastUpgradeError)
	}
	m.mu.Lock()
	_, stillSuppressed := m.failed[path]
	m.mu.Unlock()
	if stillSuppressed {
		t.Error("升级成功后应清除 failed 抑制标记")
	}
}

// TestManagerUpgradeEpochIsStrictlyMonotonicAcrossGenerations 世代号必须跨代
// 严格单调 —— 这条直接挡住一类静默串数据的缺陷。
//
// 插件进程内的会话 id 每次都从 1 重新分配。若换代时世代号归零重排，某个
// epochAt 恰好等于新一代世代号的在途驱动会被 stale() 判为「不陈旧」，把旧会话
// id 发给新进程 —— 该 id 在新进程里可能属于另一台设备的会话，既不报错也察觉
// 不到。崩溃重启会自然推高世代号，正是它让「归零重排」变得可发生。
func TestManagerUpgradeEpochIsStrictlyMonotonicAcrossGenerations(t *testing.T) {
	m, _, dir := newTestManager(t, &fakeReloader{})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	link := onlyPlugin(t, m).link

	// 强制 3 次崩溃，把世代号从 1 推到 4 —— 有崩溃历史才暴露重排问题。
	for i := 0; i < 3; i++ {
		prev := link.epoch()
		cmd := link.current().currentCmd()
		if cmd == nil || cmd.Process == nil {
			t.Fatal("进程未拉起，无法制造崩溃")
		}
		if err := cmd.Process.Kill(); err != nil {
			t.Fatalf("kill: %v", err)
		}
		waitFor(t, 10*time.Second, "进程被监管重新拉起", func() bool { return link.epoch() > prev })
	}
	if got := link.epoch(); got != 4 {
		t.Fatalf("3 次崩溃后世代号 = %d, want 4", got)
	}

	// 连一个驱动，让它把当前世代号记进 epochAt。
	d := connectFixture(t, fixtureProtocol)
	rd, ok := d.(*remoteDriver)
	if !ok {
		t.Fatalf("driver.Create 返回 %T, want *remoteDriver", d)
	}
	if got := rd.epochAt.Load(); got != 4 {
		t.Fatalf("epochAt = %d, want 4", got)
	}

	setFixtureEnv(t, m, envFixtureTag+"=v2-")
	replacePluginBinary(t, fixturePluginPath(t, dir))
	m.Reload()

	if got := link.epoch(); got != 5 {
		t.Errorf("升级后世代号 = %d, want 5（新实例必须携带旧实例的 epoch 作基数）", got)
	}
	if rd.epochAt.Load() >= link.epoch() {
		t.Errorf("epochAt %d 未小于新世代号 %d —— 陈旧会话会被判为新鲜",
			rd.epochAt.Load(), link.epoch())
	}
	if d.IsConnected() {
		t.Error("换代后既有驱动应报告未连接")
	}
}

// TestRetireAndSwapEpochExceedsOldFinalEpoch 换代时新一代的世代号必须严格大于
// 旧实例**曾经到达过的最大值**，而不只是"创建新实例那一刻读到的那一个值"。
//
// 为什么必须区分：从拉起新实例到换入它有整个握手窗口（可达数十秒的一个区间），
// 而旧实例的监管协程在这期间照常运行 —— 它崩溃重启一次，就把自己的 epoch 推高 1。
// 若基数在构造期就定成当时读到的旧 epoch，新实例 markUp 后恰好等于那个值：
// 于是所有在窗口内重连到旧实例的驱动（epochAt = 重启后的值）在换代后 stale()
// 为假，IsConnected() 仍报已连接，继续把旧会话 id 发进新进程 —— 该 id 在新进程里
// 可能属于另一台设备的会话，既不报错也察觉不到，表现为静默读到别的设备的数据。
//
// 上一条测试只在升级**之前**制造崩溃，窗口内不崩，因此挡不住这件事；
// 这里用进程级原语把"窗口内崩一次"这一步显式做出来。
func TestRetireAndSwapEpochExceedsOldFinalEpoch(t *testing.T) {
	path := installFixture(t)
	opts := fixtureOptions(t, path)

	// 旧实例：换代的"前身"。
	old := newPluginProcess(path, opts)
	old.start()
	t.Cleanup(old.stop)
	waitFor(t, 10*time.Second, "旧实例就绪", func() bool { return old.epoch() == 1 })

	link := newPluginLink(path, old)

	// 新实例：模拟 upgrade 里"已拉起、正等握手"的那个 next。
	next := newPluginProcess(path, opts)
	next.start()
	t.Cleanup(next.stop)
	waitFor(t, 10*time.Second, "新实例就绪", func() bool { return next.epoch() == 1 })

	// 窗口内旧实例崩溃重启一次。重连到它的驱动会把 epochAt 记成重启后的世代号 ——
	// 这正是下面必须被严格超过的那个值。
	prev := old.epoch()
	cmd := old.currentCmd()
	if cmd == nil || cmd.Process == nil {
		t.Fatal("旧实例未拉起，无法制造窗口内崩溃")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	waitFor(t, 10*time.Second, "旧实例被监管重新拉起", func() bool { return old.epoch() > prev })
	driverEpochAt := old.epoch()

	link.retireAndSwap(next)

	if got := link.epoch(); got <= driverEpochAt {
		t.Errorf("换代后世代号 %d 未大于旧实例的终值 %d —— "+
			"窗口内重连到旧实例的驱动会被 stale() 判为不陈旧，"+
			"把旧会话 id 发进新进程（可能命中另一台设备的会话）", got, driverEpochAt)
	}
}

// TestRegisterScopeCollisionLeavesExistingTypesIntact 插件 scope 与**已注册的
// scope** 撞名（含仅大小写不同）时，必须在**动到类型注册表之前**被拒绝。
//
// 反过来的顺序（先注册类型、再校验 scope）会留下一个极难归因的破坏：
// TypeRegistry.Register 是覆盖写，后到的那批没有 Decode/Encode/Format 的类型会把
// 先到的顶掉；而驱动包用 sync.Once 按前缀扫注册表建「裸名 → *DataType」缓存
// （见 driver/s7/datatypes.go 的 s7Lookup），缓存到没有 Decode 的条目，
// 解析时调用 nil 的 Decode 直接 panic，被 worker pool 吞掉 ——
// 症状是「某台设备的点位永远采不到值」，而插件的加载日志上明明写着「失败」。
//
// 先前的版本拿内置 modbus 当"已存在的 scope"。内置驱动全部插件化之后
// 基线已空，这条路径只剩「插件撞插件」一种形态 —— 也正是这里构造的形态，
// 所以哨兵 scope 由测试自己用 RegisterScope 建，不再依赖任何具体协议。
func TestRegisterScopeCollisionLeavesExistingTypesIntact(t *testing.T) {
	const (
		scope    = "testscope_fixturecollision"
		protocol = "Test.Fixture.CollisionOwner"
	)

	// 先前那个"插件"：认领这个 scope，并注册一个带 Decode 的类型。
	// Decode 非空是关键 —— 被覆盖成 nil 才是这条测试要拦住的破坏。
	driver.GetTypeRegistry().ForProtocol(scope).Register(&driver.DataType{
		Name: "bool", Kind: driver.KindBool, Size: 1,
		Decode: func([]byte, binary.ByteOrder) (any, error) { return true, nil },
	})
	if err := driver.RegisterScope(scope, []string{protocol}, map[string]string{"Boolean": "bool"}); err != nil {
		t.Fatalf("前置注册失败: %v", err)
	}
	t.Cleanup(func() { driver.UnregisterScope(scope) })

	before, ok := driver.GetTypeRegistry().Get(scope + ".bool")
	if !ok || before.Decode == nil {
		t.Fatal("前置条件不成立：哨兵 scope 的 bool 应已注册且带 Decode")
	}

	m, _, _ := newTestManager(t, &fakeReloader{})

	path := installFixture(t)
	p := newPluginProcess(path, fixtureOptions(t, path))
	p.start()
	t.Cleanup(p.stop)
	link := newPluginLink(path, p)

	meta := fixtureMeta()
	meta.Scope = strings.ToUpper(scope) // 与已注册 scope 仅大小写不同

	if err := m.register(meta, link); err == nil {
		t.Fatal("与已注册 scope 只差大小写的插件必须被拒绝加载")
	}

	after, ok := driver.GetTypeRegistry().Get(scope + ".bool")
	if !ok {
		t.Fatal("先到插件的类型条目被后到的注册弄丢了")
	}
	if after.Decode == nil {
		t.Error("先到类型的 Decode 被后到的那份覆盖成了 nil —— 该协议的点位解析时会 panic")
	}
	if after != before {
		t.Error("先到类型的条目被覆盖了（指针已变）")
	}
}

// TestManagerUpgradeWithTypeMapChangeReRegisters 只改类型表：要重注册，
// 但**不需要**重建采集任务。
//
// 类型表与类型映射都是查询时现算的（被采集路径的 NormalizeAddressType 与
// 前端的类型下拉直接读全局注册表），换完立刻生效。
func TestManagerUpgradeWithTypeMapChangeReRegisters(t *testing.T) {
	reload := &fakeReloader{}
	m, _, dir := newTestManager(t, reload)
	startWithRunningReloader(t, m, reload)

	if _, ok := driver.LookupDataType(fixtureProtocol, "Int32"); ok {
		t.Fatal("升级前不应存在 Int32 映射")
	}

	setFixtureEnv(t, m, envFixtureTag+"=v2-", envFixtureVariant+"=addtype")
	replacePluginBinary(t, fixturePluginPath(t, dir))
	m.Reload()

	if got := m.Status()[0].Upgrades; got != 1 {
		t.Fatalf("Upgrades = %d, want 1", got)
	}
	if internal, ok := driver.LookupDataType(fixtureProtocol, "Int32"); !ok || internal != "int32" {
		t.Errorf("新类型映射未生效: %q,%v; want int32,true", internal, ok)
	}
	time.Sleep(200 * time.Millisecond)
	if n := reload.calls.Load(); n != 0 {
		t.Errorf("只改类型表不应重建采集任务，实际刷新 %d 次", n)
	}
}

// TestManagerUpgradeWithScopeRenameKeepsProtocol 换 scope：协议名不变，
// 驱动仍可创建，采集任务也不用重建。
func TestManagerUpgradeWithScopeRenameKeepsProtocol(t *testing.T) {
	reload := &fakeReloader{}
	m, _, dir := newTestManager(t, reload)
	startWithRunningReloader(t, m, reload)

	if got := driver.ScopeOf(fixtureProtocol); got != fixtureScope {
		t.Fatalf("升级前 ScopeOf = %q, want %q", got, fixtureScope)
	}

	setFixtureEnv(t, m, envFixtureVariant+"=renamescope")
	replacePluginBinary(t, fixturePluginPath(t, dir))
	m.Reload()

	if got := m.Status()[0].Scope; got != fixtureScopeAlt {
		t.Errorf("Scope = %q, want %q", got, fixtureScopeAlt)
	}
	if got := driver.ScopeOf(fixtureProtocol); got != fixtureScopeAlt {
		t.Errorf("ScopeOf = %q, want %q", got, fixtureScopeAlt)
	}
	if _, err := driver.Create(fixtureProtocol); err != nil {
		t.Errorf("换 scope 后协议应仍可创建: %v", err)
	}
	// 新 scope 下类型映射可用。
	if internal, ok := driver.LookupDataType(fixtureProtocol, "Short"); !ok || internal != "int16" {
		t.Errorf("新 scope 类型映射失效: %q,%v", internal, ok)
	}
	time.Sleep(200 * time.Millisecond)
	if n := reload.calls.Load(); n != 0 {
		t.Errorf("换 scope 不应重建采集任务，实际刷新 %d 次", n)
	}
}

// TestManagerUpgradeSerialExclusiveChangeRebuildsTask 翻转 SerialExclusive
// **必须**重建采集任务。
//
// 锁粒度是建任务时固化的：不重建的话，本该整轮独占串行总线的协议会以
// 「只锁单次 Read」运行，RS-485 上的帧会交错。
func TestManagerUpgradeSerialExclusiveChangeRebuildsTask(t *testing.T) {
	reload := &fakeReloader{}
	m, _, dir := newTestManager(t, reload)
	startWithRunningReloader(t, m, reload)

	setFixtureEnv(t, m, envFixtureVariant+"=serial")
	replacePluginBinary(t, fixturePluginPath(t, dir))
	m.Reload()

	if got := m.Status()[0].Upgrades; got != 1 {
		t.Fatalf("Upgrades = %d, want 1", got)
	}
	waitFor(t, 3*time.Second, "SerialExclusive 变化触发采集任务重建", func() bool {
		return reload.calls.Load() == 1
	})
	time.Sleep(200 * time.Millisecond)
	if n := reload.calls.Load(); n != 1 {
		t.Errorf("应恰好刷新 1 次（防抖后），实际 %d 次", n)
	}

	// 新实例必须带出新的 SerialExclusive，否则重建出来的任务锁粒度还是错的。
	d, err := driver.Create(fixtureProtocol)
	if err != nil {
		t.Fatalf("driver.Create: %v", err)
	}
	se, ok := d.(driver.SerialExclusive)
	if !ok || !se.SerialExclusive() {
		t.Error("新实例应报告独占串行总线")
	}
}

// TestManagerUpgradeProtocolNameChangeRebuildsTask 协议名集合变化必须重建
// 采集任务：它决定「哪些设备能建出驱动」。
func TestManagerUpgradeProtocolNameChangeRebuildsTask(t *testing.T) {
	reload := &fakeReloader{}
	m, _, dir := newTestManager(t, reload)
	startWithRunningReloader(t, m, reload)
	if _, err := driver.Create(fixtureProtocolAlt); err == nil {
		t.Fatal("升级前不应存在第二个协议")
	}

	setFixtureEnv(t, m, envFixtureVariant+"=addproto")
	replacePluginBinary(t, fixturePluginPath(t, dir))
	m.Reload()

	if got := m.Status()[0].Upgrades; got != 1 {
		t.Fatalf("Upgrades = %d, want 1", got)
	}
	if _, err := driver.Create(fixtureProtocolAlt); err != nil {
		t.Errorf("新协议应已注册: %v", err)
	}
	if _, err := driver.Create(fixtureProtocol); err != nil {
		t.Errorf("原协议应仍然存在: %v", err)
	}
	waitFor(t, 3*time.Second, "协议名集合变化触发采集任务重建", func() bool {
		return reload.calls.Load() == 1
	})
}

// TestManagerHotUpgradeDisabledFallsBackToWarnOnly disableHotUpgrade=true 时
// 回退到旧行为：只警告，继续跑旧版本，重启网关才生效。
func TestManagerHotUpgradeDisabledFallsBackToWarnOnly(t *testing.T) {
	m, _, dir := newTestManager(t, &fakeReloader{})
	m.cfg.DisableHotUpgrade = true
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	d := connectFixture(t, fixtureProtocol)
	before := m.Status()[0]

	setFixtureEnv(t, m, envFixtureTag+"=v2-")
	replacePluginBinary(t, fixturePluginPath(t, dir))
	m.Reload()

	after := m.Status()[0]
	if after.SHA256 != before.SHA256 || after.Epoch != before.Epoch || after.Upgrades != 0 {
		t.Error("关闭热升级后不应替换运行中的插件")
	}
	if !d.IsConnected() {
		t.Error("关闭热升级后既有驱动应保持连接")
	}
	if got := readFixture(t, d, "p1"); got != "fixture-p1" {
		t.Errorf("关闭热升级后应继续由旧版本出数，实际 %q", got)
	}
	// 协议仍然可用（继续跑旧版本）。
	if _, err := driver.Create(fixtureProtocol); err != nil {
		t.Errorf("替换文件后旧版本驱动应继续可用: %v", err)
	}

	// 反复扫描不得刷屏：只在首次发现替换时警告一次。
	m.Reload()
	m.Reload()
}
