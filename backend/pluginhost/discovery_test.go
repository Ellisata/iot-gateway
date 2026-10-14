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

	"iot-gateway/driver"
)

// ==================== 扫描能力发现（describe → Manager → probe） ====================

// startDiscoveryFixture 起一个声明了扫描能力的夹具插件。
func startDiscoveryFixture(t *testing.T, variant string) (*Manager, string) {
	t.Helper()
	m, _, dir := newTestManager(t, &fakeReloader{})
	setFixtureEnv(t, m, envFixtureVariant+"="+variant)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)
	return m, dir
}

// TestDiscoverySpecsFollowPluginMeta 能力声明必须能穿过进程边界被网关读到。
func TestDiscoverySpecsFollowPluginMeta(t *testing.T) {
	m, _ := startDiscoveryFixture(t, "discovery")

	specs := m.DiscoverySpecs()
	if len(specs) != 1 {
		t.Fatalf("应有 1 个可扫描协议，实际 %d 个: %+v", len(specs), specs)
	}
	s := specs[0]
	if s.Protocol != fixtureProtocol {
		t.Errorf("协议 = %q，期望 %q", s.Protocol, fixtureProtocol)
	}
	if s.Spec.Title != "夹具扫描" {
		t.Errorf("Title = %q", s.Spec.Title)
	}
	if s.Spec.DefaultPort != 502 || s.Spec.MaxBudgetMs != 2000 {
		t.Errorf("端口/预算 = %d/%d，期望 502/2000", s.Spec.DefaultPort, s.Spec.MaxBudgetMs)
	}
	if len(s.Spec.FormJSON) == 0 || !json.Valid(s.Spec.FormJSON) {
		t.Errorf("FormJSON 应随 Meta 一起过线，实际 %q", s.Spec.FormJSON)
	}
	// 未声明扫描能力的协议不能出现在列表里 —— 这是"协议下拉只列可扫协议"的依据。
	for _, others := range specs {
		if others.Protocol == fixtureProtocolAlt {
			t.Error("未声明 Discovery 的协议不该出现")
		}
	}
}

// TestDiscoverySpecsEmptyWhenNotDeclared 没声明扫描能力的插件产出一个空列表，
// 而不是报错或返回 nil 指针。
func TestDiscoverySpecsEmptyWhenNotDeclared(t *testing.T) {
	m, _, _ := newTestManager(t, &fakeReloader{})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	if specs := m.DiscoverySpecs(); len(specs) != 0 {
		t.Fatalf("基线夹具没声明扫描能力，列表应为空，实际 %+v", specs)
	}
}

// TestDiscoverySpecsClearedAfterUnload 插件文件被移除后能力必须跟着消失。
func TestDiscoverySpecsClearedAfterUnload(t *testing.T) {
	m, dir := startDiscoveryFixture(t, "discovery")
	if len(m.DiscoverySpecs()) != 1 {
		t.Fatal("前置条件不成立")
	}

	// 删掉插件文件：协议与能力声明都应随之消失。
	//
	// Windows 不允许删除正在运行的 exe（Linux 无此限制），所以先停掉插件进程，
	// 模拟"运维停掉/替换插件后文件消失"这一真实可达的状态 ——
	// 与 TestManagerUnloadRemovesRuntime 用的是同一套做法。
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
		return len(m.DiscoverySpecs()) == 0
	})
}

// TestProbeTargetsEndToEnd 端到端：网关 → 帧 RPC → 插件进程内的 prober。
//
// 这条覆盖的是进程内单测覆盖不到的三层：帧编解码、进程监管、参数过线无损。
// 夹具的 prober 把收到的输入原样回在 Extra 里，因此每一项都能被断言。
func TestProbeTargetsEndToEnd(t *testing.T) {
	m, _ := startDiscoveryFixture(t, "discovery")

	options := json.RawMessage(`{"unitIds":"1,2"}`)
	target := driver.ScanTarget{
		Host:    "10.0.0.9",
		Port:    1502,
		LocalIP: "10.0.0.1",
		Subnet:  "10.0.0.0/24",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reply, err := m.ProbeTargets(ctx, fixtureProtocol, options, target)
	if err != nil {
		t.Fatalf("ProbeTargets: %v", err)
	}
	if len(reply.Devices) != 1 {
		t.Fatalf("应发现 1 台设备，实际 %d 台", len(reply.Devices))
	}
	d := reply.Devices[0]
	if d.Host != "10.0.0.9" || d.Port != 1502 {
		t.Errorf("设备地址 = %s:%d", d.Host, d.Port)
	}
	if !json.Valid([]byte(d.ProtocolJSON)) {
		t.Errorf("连接配置不是合法 JSON: %q", d.ProtocolJSON)
	}

	// Extra 是插件的自述：逐项核对参数确实无损过线。
	extra := map[string]string{}
	for _, a := range d.Extra {
		extra[a.Name] = a.Value
	}
	if extra["protocol"] != fixtureProtocol {
		t.Errorf("协议名过线后变成 %q", extra["protocol"])
	}
	if extra["options"] != string(options) {
		t.Errorf("扫描选项过线后变成 %q，期望 %q", extra["options"], options)
	}
	if extra["localIp"] != "10.0.0.1" {
		t.Errorf("LocalIP 过线后变成 %q —— 广播型协议靠它绑源地址", extra["localIp"])
	}
	// 预算由插件声明的 2000ms 决定（网关侧 RPC 超时也是按它推导的）。
	if extra["budgetMs"] != "2000" {
		t.Errorf("预算过线后变成 %q，期望 2000", extra["budgetMs"])
	}
}

// TestProbeTargetsEmptyOptions 空选项要能让 prober 认出"没填"（长度 0），
// 而不是收到一个空的 JSON 字符串导致 Unmarshal 报错。
func TestProbeTargetsEmptyOptions(t *testing.T) {
	m, _ := startDiscoveryFixture(t, "discovery")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reply, err := m.ProbeTargets(ctx, fixtureProtocol, nil, driver.ScanTarget{Host: "10.0.0.9", Port: 502})
	if err != nil {
		t.Fatalf("ProbeTargets: %v", err)
	}
	if len(reply.Devices) != 1 {
		t.Fatalf("应发现 1 台设备，实际 %d 台", len(reply.Devices))
	}
	for _, a := range reply.Devices[0].Extra {
		if a.Name == "options" && a.Value != "" {
			t.Errorf("未填选项时 prober 收到的应是空串，实际 %q", a.Value)
		}
	}
}

// TestProbeTargetsUnsupportedProtocol 没声明扫描能力的协议必须明确报错
// （而不是让调用方以为"扫到了 0 台"）。
func TestProbeTargetsUnsupportedProtocol(t *testing.T) {
	m, _ := startDiscoveryFixture(t, "discovery")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := m.ProbeTargets(ctx, fixtureProtocolAlt, nil, driver.ScanTarget{Host: "10.0.0.9", Port: 502})
	if !errors.Is(err, ErrDiscoveryUnsupported) {
		t.Fatalf("应返回 ErrDiscoveryUnsupported，实际 %v", err)
	}
}

// TestProbeTargetsHonoursContextCancel 取消后调用要立即返回。
//
// 这一条同时是"扫描取消"链路的下半截：上半截（不计入卡死计数）在
// client_test.go 的 TestCancelDoesNotCountAsTimeout 里。
func TestProbeTargetsHonoursContextCancel(t *testing.T) {
	m, _ := startDiscoveryFixture(t, "discovery")

	// 夹具 prober 对空 Host 立即返回，因此这里靠一个极短的超时来构造"来不及"，
	// 用已取消的 ctx 更直接。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.ProbeTargets(ctx, fixtureProtocol, nil, driver.ScanTarget{Host: "10.0.0.9", Port: 502})
	if err == nil {
		t.Fatal("已取消的 ctx 应导致调用失败")
	}
	if !strings.Contains(err.Error(), "cancel") {
		t.Errorf("错误应指明是被取消的，实际 %v", err)
	}
}

// TestDiscoverySpecsRefreshAfterHotUpgrade **不建缓存表**这条取舍的回归。
//
// 热升级只替换 lp.meta，而 registrationSig 只含 scope/protocols/types/typeMap ——
// 一份**只改了 Discovery** 的新二进制不会触发重注册。若把能力另存一张表，
// 它就会停在旧值上，症状是"插件明明改了扫描配置、前端却一直显示旧的"，
// 而两边的日志都写着成功。
func TestDiscoverySpecsRefreshAfterHotUpgrade(t *testing.T) {
	m, dir := startDiscoveryFixture(t, "discovery")

	before := m.DiscoverySpecs()
	if len(before) != 1 || before[0].Spec.Title != "夹具扫描" {
		t.Fatalf("前置条件不成立: %+v", before)
	}

	// 换一份只改了 Discovery 的 Meta，并**真正改写文件内容**以触发热升级
	//（只 touch mtime 会被 upgrade 的"内容未变"短路掉，见其注释）。
	setFixtureEnv(t, m, envFixtureVariant+"=discovery2")
	replacePluginBinary(t, fixturePluginPath(t, dir))
	m.Reload()

	waitFor(t, 10*time.Second, "扫描规格随热升级刷新", func() bool {
		specs := m.DiscoverySpecs()
		return len(specs) == 1 && specs[0].Spec.Title == "夹具扫描 v2"
	})

	after := m.DiscoverySpecs()
	if after[0].Spec.DefaultPort != 1502 || after[0].Spec.MaxBudgetMs != 3000 {
		t.Fatalf("升级后的规格 = %+v，期望端口 1502 / 预算 3000", after[0].Spec)
	}
}

// TestProbeAfterHotUpgradeUsesNewProcess 热升级后探测要落在新进程上
// （旧进程已被退役，会话/连接都不该再被复用）。
func TestProbeAfterHotUpgradeUsesNewProcess(t *testing.T) {
	m, dir := startDiscoveryFixture(t, "discovery")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := m.ProbeTargets(ctx, fixtureProtocol, nil, driver.ScanTarget{Host: "10.0.0.9", Port: 502}); err != nil {
		t.Fatalf("升级前探测失败: %v", err)
	}

	setFixtureEnv(t, m, envFixtureVariant+"=discovery2")
	replacePluginBinary(t, fixturePluginPath(t, dir))
	m.Reload()

	// 探测本身是无会话的，换代后 acquire 拿到的就是新进程的 client ——
	// 不需要 remote.go 里那种会话 id 的世代号复核。
	waitFor(t, 10*time.Second, "探测落在新进程上（预算变为新值）", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		reply, err := m.ProbeTargets(ctx, fixtureProtocol, nil, driver.ScanTarget{Host: "10.0.0.9", Port: 502})
		if err != nil || len(reply.Devices) == 0 {
			return false
		}
		for _, a := range reply.Devices[0].Extra {
			if a.Name == "budgetMs" && a.Value == "3000" {
				return true
			}
		}
		return false
	})
}
