// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
)

// ==================== 点位扫描能力发现（describe → Manager → discoverPoints） ====================
//
// 与 discovery_test.go 同构，盯的是同一类事情：能力声明能不能穿过进程边界、
// 热升级后是不是现算、端到端参数是否无损。差别在于点扫**要复用会话**，
// 因此多了一组串口用例。

// startPointFixture 起一个声明了点位扫描能力的夹具插件。
func startPointFixture(t *testing.T, variant string) (*Manager, string) {
	t.Helper()
	m, _, dir := newTestManager(t, &fakeReloader{})
	setFixtureEnv(t, m, envFixtureVariant+"="+variant)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)
	return m, dir
}

// TestPointDiscoverySpecsFollowPluginMeta 能力声明必须能穿过进程边界被网关读到。
func TestPointDiscoverySpecsFollowPluginMeta(t *testing.T) {
	m, _ := startPointFixture(t, "pointscan")

	specs := m.PointDiscoverySpecs()
	if len(specs) != 1 {
		t.Fatalf("应有 1 个可点扫的协议，实际 %d 个: %+v", len(specs), specs)
	}
	s := specs[0]
	if s.Protocol != fixtureProtocol {
		t.Errorf("协议 = %q，期望 %q", s.Protocol, fixtureProtocol)
	}
	if s.Spec.Title != "夹具点扫" {
		t.Errorf("Title = %q", s.Spec.Title)
	}
	// DefaultCommonDataType 必须是**通用类型名**（TypeMap 的 key），不是内部裸名 ——
	// 写错的症状是前端下拉默认选中项是空的，不报任何错。
	if s.Spec.DefaultCommonDataType != "Short" {
		t.Errorf("DefaultCommonDataType = %q，期望 Short", s.Spec.DefaultCommonDataType)
	}
	if s.Spec.MaxBudgetMs != 2000 || s.Spec.MaxPoints != 100 {
		t.Errorf("预算/上限 = %d/%d，期望 2000/100", s.Spec.MaxBudgetMs, s.Spec.MaxPoints)
	}
	if len(s.Spec.FormJSON) == 0 || !json.Valid(s.Spec.FormJSON) {
		t.Errorf("FormJSON 应随 Meta 一起过线，实际 %q", s.Spec.FormJSON)
	}

	// 能力表是**并列**的两套：声明了 Discovery 不等于声明了 PointDiscovery。
	// 这个变体只声明了后者，所以扫描页的下拉里不该出现它。
	if got := m.DiscoverySpecs(); len(got) != 0 {
		t.Errorf("该变体没声明 Discovery，列表应为空，实际 %+v", got)
	}
}

// TestPointDiscoverySpecsEmptyWhenNotDeclared 没声明的插件产出空列表，
// 而不是报错或返回 nil 指针。
func TestPointDiscoverySpecsEmptyWhenNotDeclared(t *testing.T) {
	m, _, _ := newTestManager(t, &fakeReloader{})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	if specs := m.PointDiscoverySpecs(); len(specs) != 0 {
		t.Fatalf("基线夹具没声明点扫能力，列表应为空，实际 %+v", specs)
	}
}

// TestPointDiscoverySpecsClearedAfterUnload 插件文件被移除后能力必须跟着消失。
func TestPointDiscoverySpecsClearedAfterUnload(t *testing.T) {
	m, dir := startPointFixture(t, "pointscan")
	if len(m.PointDiscoverySpecs()) != 1 {
		t.Fatal("前置条件不成立")
	}

	// Windows 不允许删除正在运行的 exe（Linux 无此限制），所以先停掉插件进程，
	// 模拟"运维停掉/替换插件后文件消失"这一真实可达的状态。
	matches, _ := filepath.Glob(filepath.Join(dir, "iot-driver-*"))
	if len(matches) != 1 {
		t.Fatalf("期望 1 个插件文件，实际 %d", len(matches))
	}
	m.mu.Lock()
	for _, lp := range m.plugins {
		lp.link.retireCurrent()
	}
	m.mu.Unlock()
	if err := os.Remove(matches[0]); err != nil {
		t.Fatalf("删除插件文件: %v", err)
	}
	m.Reload()
	waitFor(t, 5*time.Second, "能力声明被清理", func() bool {
		return len(m.PointDiscoverySpecs()) == 0
	})
}

// TestPointDiscoverySpecsRefreshAfterHotUpgrade **不建缓存表**这条取舍的回归。
//
// 热升级只替换 lp.meta，而 registrationSig 只含 scope/protocols/types/typeMap ——
// 一份**只改了 PointDiscovery** 的新二进制不会触发重注册。若把能力另存一张表，
// 它就会停在旧值上，症状是"插件明明改了扫描参数、前端却一直显示旧的"，
// 而两边的日志都写着成功。
func TestPointDiscoverySpecsRefreshAfterHotUpgrade(t *testing.T) {
	m, dir := startPointFixture(t, "pointscan")

	before := m.PointDiscoverySpecs()
	if len(before) != 1 || before[0].Spec.Title != "夹具点扫" {
		t.Fatalf("前置条件不成立: %+v", before)
	}

	// 换一份只改了 PointDiscovery 的 Meta，并**真正改写文件内容**以触发热升级。
	setFixtureEnv(t, m, envFixtureVariant+"=pointscan2")
	replacePluginBinary(t, fixturePluginPath(t, dir))
	m.Reload()

	waitFor(t, 10*time.Second, "点扫规格随热升级刷新", func() bool {
		specs := m.PointDiscoverySpecs()
		return len(specs) == 1 && specs[0].Spec.Title == "夹具点扫 v2"
	})

	after := m.PointDiscoverySpecs()
	if after[0].Spec.MaxBudgetMs != 3000 {
		t.Fatalf("升级后的规格 = %+v，期望预算 3000", after[0].Spec)
	}
}

// TestDiscoverPointsEndToEnd 端到端：网关 → 帧 RPC → 插件进程内的 finder。
//
// 这条覆盖的是进程内单测覆盖不到的三层：帧编解码、进程监管、参数过线无损。
// 夹具的 finder 把收到的输入回在结果里，因此每一项都能被断言。
func TestDiscoverPointsEndToEnd(t *testing.T) {
	m, _ := startPointFixture(t, "pointscan")

	options := json.RawMessage(`{"functionCode":3,"startAddress":100,"count":8}`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := m.DiscoverPoints(ctx, fixtureProtocol, `{"host":"10.0.0.9"}`, options)
	if err != nil {
		t.Fatalf("DiscoverPoints: %v", err)
	}
	if len(res.Points) != 1 {
		t.Fatalf("应返回 1 条点位，实际 %d 条", len(res.Points))
	}
	p := res.Points[0]
	if p.Name != "fixture-point" {
		t.Errorf("点位地址 = %q", p.Name)
	}
	// Options 无损过线。夹具把它回在 Label 里（PointScanResult 没有 Extra 字段，
	// 而 Label 是结果里唯一会原样过线的自由文本）。
	if p.Label != string(options) {
		t.Errorf("扫描选项过线后变成 %q，期望 %q", p.Label, options)
	}
	// 预算由插件声明的 2000ms 决定；Deadline 必须已经填上（finder 靠它判时间）。
	if !strings.Contains(p.Note, "budgetMs=2000") {
		t.Errorf("预算过线后不对：%q", p.Note)
	}
	if !strings.Contains(p.Note, "deadlineSet=true") {
		t.Errorf("Deadline 必须填上：%q", p.Note)
	}
	if !strings.Contains(p.Note, "session=plain") {
		// 本进程内没有会话、夹具也没实现 ConnectionMatcher ⇒ 走**临时实例**；
		// 关键是 finder 仍然拿到一个可用的 Session（= "plain"：有驱动实例，
		// 但它不持有任何端口）。Session 为 nil 的话 finder 根本没法读。
		t.Errorf("临时实例路径下 Session 应可用：%q", p.Note)
	}
}

// TestDiscoverPointsEmptyOptions 空选项要能让 finder 认出"没填"（长度 0）。
func TestDiscoverPointsEmptyOptions(t *testing.T) {
	m, _ := startPointFixture(t, "pointscan")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := m.DiscoverPoints(ctx, fixtureProtocol, `{"host":"10.0.0.9"}`, nil)
	if err != nil {
		t.Fatalf("DiscoverPoints: %v", err)
	}
	if len(res.Points) != 1 {
		t.Fatalf("应返回 1 条点位，实际 %d 条", len(res.Points))
	}
	if res.Points[0].Label != "" {
		t.Errorf("未填选项时 finder 收到的应是空串，实际 %q", res.Points[0].Label)
	}
}

// TestDiscoverPointsUnsupportedProtocol 没声明点扫能力的协议必须明确报错
// （而不是让调用方以为"这里没有点位"）。
func TestDiscoverPointsUnsupportedProtocol(t *testing.T) {
	m, _ := startPointFixture(t, "pointscan")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := m.DiscoverPoints(ctx, fixtureProtocolAlt, `{"host":"10.0.0.9"}`, nil)
	if !errors.Is(err, ErrPointDiscoveryUnsupported) {
		t.Fatalf("应返回 ErrPointDiscoveryUnsupported，实际 %v", err)
	}
}

// TestDiscoverPointsHonoursContextCancel 取消后调用要立即返回。
func TestDiscoverPointsHonoursContextCancel(t *testing.T) {
	m, _ := startPointFixture(t, "pointscan")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.DiscoverPoints(ctx, fixtureProtocol, `{"host":"10.0.0.9"}`, nil)
	if err == nil {
		t.Fatal("已取消的 ctx 应导致调用失败")
	}
	if !strings.Contains(err.Error(), "cancel") {
		t.Errorf("错误应指明是被取消的，实际 %v", err)
	}
}

// TestDiscoverPointsReusesSessionAcrossProcess 端到端验证**串口会话复用**。
//
// 夹具插件的 MatchConnection 只在"本实例真的持有该端口"时为真，而 finder 会把
// 它看到的实例端口回在 Note 里。于是"扫描用的是采集那个实例"这件事在**跨进程**
// 的这一层上被正面证明 —— 端口被独占的话，回落临时实例必然 Access is denied。
func TestDiscoverPointsReusesSessionAcrossProcess(t *testing.T) {
	m, _ := startPointFixture(t, "pointscan_serial")

	d, err := driver.Create(fixtureProtocol)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if err := d.Connect(`{"port":"COM9","tag":"collector"}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := m.DiscoverPoints(ctx, fixtureProtocol, `{"port":"COM9","tag":"collector"}`, nil)
	if err != nil {
		t.Fatalf("DiscoverPoints: %v", err)
	}
	if len(res.Points) != 1 {
		t.Fatalf("应返回 1 条点位，实际 %d 条", len(res.Points))
	}
	if got := res.Points[0].Note; !strings.Contains(got, "session=holding:COM9") {
		t.Fatalf("扫描没有复用采集会话：%q\n"+
			"（串口上那就只能意味着新开了句柄，而那个必然 Access is denied）", got)
	}
}

// TestDiscoverPointsSerialForeignSessionAcrossProcess 串口上"有会话但不是本次要连的
// 那一条"必须报错，**不回退**。
//
// 回退会打开另一个串口的句柄并长期持有：采集侧下次重连报 Access is denied 并进入
// 退避 —— 用户看到的是"点一次扫描把设备打成离线"。
func TestDiscoverPointsSerialForeignSessionAcrossProcess(t *testing.T) {
	m, _ := startPointFixture(t, "pointscan_serial")

	d, err := driver.Create(fixtureProtocol)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if err := d.Connect(`{"port":"COM9","tag":"other"}`); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = m.DiscoverPoints(ctx, fixtureProtocol, `{"port":"COM7","tag":"mine"}`, nil)
	if err == nil {
		t.Fatal("串口上复用不到会话时应报错，而不是回落到临时实例")
	}
	if !strings.Contains(err.Error(), "串口") {
		t.Errorf("错误文案应点明串口，实际 %v", err)
	}
}

// TestDiscoverPointsAfterHotUpgradeUsesNewProcess 热升级后调用要落在新进程上。
func TestDiscoverPointsAfterHotUpgradeUsesNewProcess(t *testing.T) {
	m, dir := startPointFixture(t, "pointscan")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := m.DiscoverPoints(ctx, fixtureProtocol, `{"host":"10.0.0.9"}`, nil); err != nil {
		t.Fatalf("升级前调用失败: %v", err)
	}

	setFixtureEnv(t, m, envFixtureVariant+"=pointscan2")
	replacePluginBinary(t, fixturePluginPath(t, dir))
	m.Reload()

	waitFor(t, 10*time.Second, "调用落在新进程上（预算变为新值）", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, err := m.DiscoverPoints(ctx, fixtureProtocol, `{"host":"10.0.0.9"}`, nil)
		if err != nil || len(res.Points) == 0 {
			return false
		}
		return strings.Contains(res.Points[0].Note, "budgetMs=3000")
	})
}

// ==================== 预算钳制 ====================

// TestBudgetWithinGatewayCap 网关侧的预算钳制是"网关停不掉"那个故障的唯一防线。
//
// 它的另一半（main.go 的 10s 关机宽限期）在测试里断言不了，所以这条纯函数
// 测试是这条耦合唯一的可执行证据：**改 pointScanGatewayBudgetCapMs 必须同时
// 看一眼 main.go 的 srv.Shutdown 超时**，反之亦然。
func TestBudgetWithinGatewayCap(t *testing.T) {
	if pointScanGatewayBudgetCapMs+int(pointScanRPCGrace/time.Millisecond) >= 10000 {
		t.Fatalf("最坏 RPC 超时 = %dms + %dms ≥ 10s —— "+
			"http.Server.Shutdown 会卡满整个宽限期，网关停不下来",
			pointScanGatewayBudgetCapMs, pointScanRPCGrace/time.Millisecond)
	}

	cases := []struct {
		declaredMs int
		want       time.Duration
	}{
		{500, 500 * time.Millisecond},
		{2000, 2 * time.Second},
		{pointScanGatewayBudgetCapMs, pointScanGatewayBudgetCapMs * time.Millisecond},
		// 超过上限的一律钳到上限：插件声明的是 30s 以内的任意值，
		// 而这条链路是同步 HTTP，不能信它。
		{30000, pointScanGatewayBudgetCapMs * time.Millisecond},
		// 防御：绕过 Meta.Validate 的零值不该变成"零预算"。
		{0, pointScanGatewayBudgetCapMs * time.Millisecond},
	}
	for _, c := range cases {
		if got := budgetWithinGatewayCap(c.declaredMs); got != c.want {
			t.Errorf("budgetWithinGatewayCap(%d) = %s，期望 %s", c.declaredMs, got, c.want)
		}
	}
}
