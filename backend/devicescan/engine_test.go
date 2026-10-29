// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package devicescan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"iot-gateway/configFile"
)

const testProtocol = "Test.Scan.TCP"

// ==================== 假实现 ====================

type fakeSpecs struct {
	specs []driver.ProtocolDiscovery
}

func (f *fakeSpecs) DiscoverySpecs() []driver.ProtocolDiscovery { return f.specs }

// fakeProber 可编程的探测替身。
//
// gate 非 nil 时每次探测都先等它放行 —— 这让"扫描正在进行中"成为可断言的
// 稳定状态，而不是靠 sleep 去撞一个窗口。它是**令牌**通道：
//   - 往里发一个值 → 只放行一个探测（用它精确制造"部分完成"的状态）；
//   - 关闭它 → 全部放行（收尾用）。
//
// 测试末尾务必关闭它，否则阻塞中的探测只能靠取消退出。
type fakeProber struct {
	mu      sync.Mutex
	calls   []driver.ScanTarget
	devices int           // 每个目标产出多少台设备
	err     error         // 非 nil 时每次探测都失败
	gate    chan struct{} // 非 nil 时探测等一个令牌（或等它被关闭）

	// fixedHost/fixedPort 非空时，产出的设备一律用它们而不是目标地址 ——
	// 用来模拟"广播与逐主机两个目标各自发现了同一台设备"（去重用例）。
	fixedHost string
	fixedPort int

	// active/maxActive 是**在飞**探测数与它的峰值。
	// 必须是"进入 +1、返回 -1"的当前值，而不是累计进入次数 ——
	// 用累计值去断言并发上限，会随着探测一个个结束而虚增，永远判失败。
	active    int
	maxActive int
}

func (p *fakeProber) ProbeTargets(ctx context.Context, protocol string, options json.RawMessage,
	target driver.ScanTarget) (*driversdk.ProbeReply, error) {

	p.mu.Lock()
	p.calls = append(p.calls, target)
	p.active++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	gate := p.gate
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.active--
		p.mu.Unlock()
	}()

	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			// 取消语义与生产路径一致：client.call 在 ctx.Done 上立即返回。
			return nil, ctx.Err()
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	reply := &driversdk.ProbeReply{}
	host, port := target.Host, target.Port
	if p.fixedHost != "" {
		host = p.fixedHost
	}
	if p.fixedPort != 0 {
		port = p.fixedPort
	}
	for i := 0; i < p.devices; i++ {
		reply.Devices = append(reply.Devices, driver.DiscoveredDevice{
			Host:         host,
			Port:         port,
			UnitID:       i + 1,
			Name:         fmt.Sprintf("%s-%d", host, i+1),
			ProtocolJSON: fmt.Sprintf(`{"host":%q,"port":%d,"unitId":%d}`, host, port, i+1),
		})
	}
	return reply, nil
}

func (p *fakeProber) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// activeCount 当前在飞的探测数。
func (p *fakeProber) activeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active
}

// peakActive 观测到的在飞探测数峰值。
func (p *fakeProber) peakActive() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxActive
}

// ==================== 夹具 ====================

// testSubnet 一个 10.0.0.0/29 的网段：网络地址 .0、广播 .7，主机 .1-.6。
// 本机地址 .1 **也在目标里**（扫描不跳过本机地址，理由见 targets.go），
// 因此展开后是 6 个目标。
const (
	testSubnetCIDR = "10.0.0.0/29"
	testLocalIP    = "10.0.0.1"
	testHosts      = 6
)

func testSubnets() []SubnetInfo {
	return []SubnetInfo{{
		Iface:     "eth0",
		CIDR:      testSubnetCIDR,
		LocalIP:   testLocalIP,
		Broadcast: "10.0.0.7",
		HostCount: testHosts,
	}}
}

func testSpec(maxBudgetMs int) driver.ProtocolDiscovery {
	return testSpecWith(driver.TargetModeUnicast, maxBudgetMs)
}

// testSpecWith 指定 TargetMode 的规格 —— 广播与双模的用例靠它构造。
func testSpecWith(mode driver.TargetMode, maxBudgetMs int) driver.ProtocolDiscovery {
	return driver.ProtocolDiscovery{
		Protocol: testProtocol,
		Spec: driver.DiscoverySpec{
			Title:       "测试扫描",
			DefaultPort: 502,
			TargetMode:  mode,
			MaxBudgetMs: maxBudgetMs,
		},
	}
}

type fixture struct {
	engine *Engine
	prober *fakeProber
	now    time.Time
}

// newFixture 构造一个注入了假网段与可控时钟的引擎。
func newFixture(t *testing.T, cfg *configFile.Config, maxBudgetMs int) *fixture {
	t.Helper()
	return newFixtureWith(t, cfg, testSpec(maxBudgetMs))
}

// newFixtureWith 同上，但用调用方给的协议规格（广播/双模的用例靠它）。
func newFixtureWith(t *testing.T, cfg *configFile.Config, spec driver.ProtocolDiscovery) *fixture {
	t.Helper()
	prober := &fakeProber{}
	engine := NewEngine(cfg, &fakeSpecs{specs: []driver.ProtocolDiscovery{spec}}, prober)
	f := &fixture{engine: engine, prober: prober, now: time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)}
	engine.subnets = testSubnets
	engine.now = func() time.Time { return f.now }
	t.Cleanup(engine.Stop)
	return f
}

// advance 推进夹具时钟。
func (f *fixture) advance(d time.Duration) { f.now = f.now.Add(d) }

// waitState 等任务进入指定状态。
func (f *fixture) waitState(t *testing.T, jobID string, want JobState) *Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := f.engine.Status(jobID)
		if err == nil && snap.State == want {
			return snap
		}
		time.Sleep(5 * time.Millisecond)
	}
	snap, err := f.engine.Status(jobID)
	t.Fatalf("等任务进入 %s 超时，当前 %+v (err=%v)", want, snap, err)
	return nil
}

func submit(t *testing.T, e *Engine, req SubmitRequest) *Snapshot {
	t.Helper()
	snap, err := e.Submit(req)
	if err != nil {
		t.Fatalf("Submit 失败: %v", err)
	}
	return snap
}

// ==================== 规划期校验 ====================

func TestSubmitRejectsDisabled(t *testing.T) {
	f := newFixture(t, &configFile.Config{DeviceScan: configFile.DeviceScanConfig{Enabled: false}}, 1000)
	if _, err := f.engine.Submit(SubmitRequest{Protocol: testProtocol}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("关闭开关后应返回 ErrDisabled，实际 %v", err)
	}
}

func TestSubmitRejectsUnsupportedProtocol(t *testing.T) {
	f := newFixture(t, nil, 1000)
	_, err := f.engine.Submit(SubmitRequest{Protocol: "No.Such.Protocol"})
	if !errors.Is(err, ErrUnsupportedProtocol) {
		t.Fatalf("应返回 ErrUnsupportedProtocol，实际 %v", err)
	}
}

// TestSubmitRejectsForeignSubnet 只允许扫本机网段。
//
// 放开任意 CIDR 等于把网关变成一台可以扫任何地址的通用端口扫描器。
func TestSubmitRejectsForeignSubnet(t *testing.T) {
	f := newFixture(t, nil, 1000)
	_, err := f.engine.Submit(SubmitRequest{Protocol: testProtocol, Subnets: []string{"192.168.1.0/24"}})
	if !errors.Is(err, ErrUnknownSubnet) {
		t.Fatalf("应返回 ErrUnknownSubnet，实际 %v", err)
	}
}

func TestSubmitRejectsInvalidPort(t *testing.T) {
	f := newFixture(t, nil, 1000)
	_, err := f.engine.Submit(SubmitRequest{Protocol: testProtocol, Port: 70000})
	if !errors.Is(err, ErrInvalidPort) {
		t.Fatalf("应返回 ErrInvalidPort，实际 %v", err)
	}
}

func TestSubmitRejectsNoTargets(t *testing.T) {
	f := newFixture(t, nil, 1000)
	f.engine.subnets = func() []SubnetInfo { return nil }
	if _, err := f.engine.Submit(SubmitRequest{Protocol: testProtocol}); !errors.Is(err, ErrNoTargets) {
		t.Fatalf("没有网段时应返回 ErrNoTargets，实际 %v", err)
	}
}

// TestSubmitHonoursDefaultPort 未指定端口时用协议声明的默认端口。
func TestSubmitHonoursDefaultPort(t *testing.T) {
	f := newFixture(t, nil, 1000)
	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	if snap.Planned != testHosts {
		t.Fatalf("计划目标数 = %d，期望 %d", snap.Planned, testHosts)
	}
	// 等它跑完，确认探测目标用的是 502。
	f.waitState(t, snap.JobID, JobDone)
	for _, c := range f.prober.calls {
		if c.Port != 502 {
			t.Fatalf("目标端口 = %d，期望协议默认端口 502", c.Port)
		}
	}
}

// TestPlanDoesNotReportFalseTruncation 未达上限时绝不能提示"被截断"。
//
// 这条挡的是一个具体的回归：requested 与实际展开逻辑一旦对不上（例如一边
// 跳过本机地址、另一边不跳），它就会恒比 len(targets) 差一个固定数，
// 于是每一次正常扫描都挂着"只扫了前 N 个地址"，而用户无从判断真假。
func TestPlanDoesNotReportFalseTruncation(t *testing.T) {
	f := newFixture(t, nil, 1000)
	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	if snap.Truncated {
		t.Fatalf("未达上限却被判为截断：requested=%d planned=%d", snap.Requested, snap.Planned)
	}
	if snap.Requested != testHosts || snap.Planned != testHosts {
		t.Fatalf("requested=%d planned=%d，都应为 %d", snap.Requested, snap.Planned, testHosts)
	}
}

// TestPlanTruncatesAtMaxTargets 超过上限时截断，且**明确报出来**。
func TestPlanTruncatesAtMaxTargets(t *testing.T) {
	f := newFixture(t, &configFile.Config{DeviceScan: configFile.DeviceScanConfig{
		Enabled: true, MaxTargets: 3,
	}}, 1000)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	if !snap.Truncated {
		t.Fatal("超过上限应标记 truncated")
	}
	if snap.Planned != 3 {
		t.Errorf("planned = %d，期望 3", snap.Planned)
	}
	if snap.Requested != testHosts {
		t.Errorf("requested = %d，期望 %d —— 它必须反映**未截断前**的真实数量，"+
			"否则前端提示的\"只扫了前 N 个\"就没有分母", snap.Requested, testHosts)
	}
	f.waitState(t, snap.JobID, JobDone)
	if got := f.prober.callCount(); got != 3 {
		t.Errorf("实际探测 %d 个目标，期望 3", got)
	}
}

// ==================== 并发与进度 ====================

// TestSingleJobAtATime 同一时刻只允许一个任务。
func TestSingleJobAtATime(t *testing.T) {
	f := newFixture(t, nil, 1000)
	f.prober.gate = make(chan struct{})
	defer close(f.prober.gate)

	first := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	if _, err := f.engine.Submit(SubmitRequest{Protocol: testProtocol}); !errors.Is(err, ErrBusy) {
		t.Fatalf("应返回 ErrBusy，实际 %v", err)
	}

	// 终态之后可以再发起：用户主动点了"开始扫描"，就是要换一轮结果。
	if err := f.engine.Cancel(first.JobID); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	f.waitState(t, first.JobID, JobCanceled)

	// 取消后在飞的探测可能还没退干净，等 worker 组退出再提交。
	// （生产路径里 Submit 只看 job 终态，不等待 worker —— 这里显式等一下，
	// 避免两个任务的探测计数混在一起。）
	f.engine.mu.Lock()
	done := f.engine.job.doneCh
	f.engine.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker 组未退出")
	}

	second := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	if second.JobID == first.JobID {
		t.Fatal("新任务应有新 id")
	}
}

// TestProgressAndFound 进度以"已完成目标数"为准，设备数是副产品。
func TestProgressAndFound(t *testing.T) {
	f := newFixture(t, nil, 1000)
	// 每个目标产出 2 台设备（模拟同 IP 的两个站号）。
	f.prober.devices = 2

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	done := f.waitState(t, snap.JobID, JobDone)

	if done.Scanned != testHosts || done.Total != testHosts {
		t.Errorf("scanned/total = %d/%d，期望 %d/%d", done.Scanned, done.Total, testHosts, testHosts)
	}
	if done.Found != testHosts*2 {
		t.Errorf("found = %d，期望 %d（每个目标 2 台）", done.Found, testHosts*2)
	}
	if len(done.Devices) != testHosts*2 {
		t.Errorf("devices 长度 = %d，期望 %d", len(done.Devices), testHosts*2)
	}
	// 快照里的设备必须带上可用的连接配置（前端据此建设备）。
	for _, d := range done.Devices {
		if !json.Valid([]byte(d.ProtocolJSON)) {
			t.Errorf("连接配置不是合法 JSON: %q", d.ProtocolJSON)
		}
	}
}

// TestConcurrencyIsCapped 并发度不得超过配置值。
func TestConcurrencyIsCapped(t *testing.T) {
	f := newFixture(t, &configFile.Config{DeviceScan: configFile.DeviceScanConfig{
		Enabled: true, MaxConcurrentProbes: 2,
	}}, 1000)
	f.prober.gate = make(chan struct{})

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	// 等到并发度被顶满（或超时）：一旦达到 2，就说明限流在起作用。
	deadline := time.Now().Add(3 * time.Second)
	peak := 0
	for time.Now().Before(deadline) {
		if p := f.prober.peakActive(); p > peak {
			peak = p
		}
		if peak >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(f.prober.gate)
	f.waitState(t, snap.JobID, JobDone)

	if peak < 2 {
		t.Fatalf("在飞探测数峰值只有 %d，夹具没生效（限流没被观察到）", peak)
	}
	if peak > 2 {
		t.Fatalf("在飞探测数峰值达到 %d，超过上限 2", peak)
	}
	// 收尾之后必须归零，否则说明有探测的 defer 没跑到（会泄漏 goroutine）。
	if n := f.prober.activeCount(); n != 0 {
		t.Errorf("任务结束后仍有 %d 个探测在飞", n)
	}
}

// ==================== 取消与停机 ====================

// TestCancelKeepsPartialResults 取消保留已扫到的结果 —— 用户点了取消不代表
// 那些结果没用，而且**不能**把在飞的探测记成错误。
//
// 用令牌精确制造"扫到一台之后就取消"这个状态，而不是靠 sleep 去撞窗口。
func TestCancelKeepsPartialResults(t *testing.T) {
	f := newFixture(t, &configFile.Config{DeviceScan: configFile.DeviceScanConfig{
		Enabled: true, MaxConcurrentProbes: 1, // 一次只放一个探测进去，时序才确定
	}}, 1000)
	f.prober.devices = 1
	f.prober.gate = make(chan struct{}, testHosts)
	defer close(f.prober.gate)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})

	// 放第一个探测进去，再给它一张令牌让它产出结果。
	if !waitFor(3*time.Second, func() bool { return f.prober.activeCount() >= 1 }) {
		t.Fatal("第一个探测没有进入")
	}
	f.prober.gate <- struct{}{}
	if !waitFor(3*time.Second, func() bool {
		s, err := f.engine.Status(snap.JobID)
		return err == nil && s.Found >= 1
	}) {
		t.Fatal("第一个探测的結果没有落进任务")
	}

	// 此刻第二个探测已进入并卡在 gate 上。取消。
	if err := f.engine.Cancel(snap.JobID); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	canceled := f.waitState(t, snap.JobID, JobCanceled)

	if canceled.Reason == "" {
		t.Error("取消应有原因文案")
	}
	// 已扫到的结果必须保留下来。
	if canceled.Found != 1 || len(canceled.Devices) != 1 {
		t.Errorf("取消后 found=%d devices=%d，期望 1/1 —— 取消不代表结果没用",
			canceled.Found, len(canceled.Devices))
	}
	// 被取消的那个目标不算扫过、也不算失败。
	if canceled.Scanned != 1 {
		t.Errorf("scanned = %d，期望 1（只有第一个目标真的被扫过）", canceled.Scanned)
	}
	if len(canceled.Errors) != 0 {
		t.Errorf("取消不该产生错误明细，实际 %+v", canceled.Errors)
	}
}

// waitFor 轮询等待条件成立。
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// TestCancelDoesNotCountCanceledTargetsAsScanned 取消后未扫到的目标不计入进度：
// 进度条假装扫完了会让用户以为"这个网段没有别的设备"。
func TestCancelDoesNotCountCanceledTargetsAsScanned(t *testing.T) {
	f := newFixture(t, &configFile.Config{DeviceScan: configFile.DeviceScanConfig{
		Enabled: true, MaxConcurrentProbes: 1,
	}}, 1000)
	f.prober.gate = make(chan struct{}) // 全部探测都卡住

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})

	// 等第一个探测真的进去了，再取消 —— 这样"在飞的那一个"是确定的。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f.prober.activeCount() >= 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := f.engine.Cancel(snap.JobID); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	close(f.prober.gate)

	canceled := f.waitState(t, snap.JobID, JobCanceled)
	if canceled.Scanned != 0 {
		t.Errorf("scanned = %d，取消后应为 0（在飞的那个目标并没有被扫过）", canceled.Scanned)
	}
	if canceled.Found != 0 {
		t.Errorf("found = %d，期望 0", canceled.Found)
	}
}

// TestCancelIsIdempotentOnFinishedJob 已结束的任务再取消是幂等的成功。
func TestCancelIsIdempotentOnFinishedJob(t *testing.T) {
	f := newFixture(t, nil, 1000)
	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	f.waitState(t, snap.JobID, JobDone)

	if err := f.engine.Cancel(snap.JobID); err != nil {
		t.Fatalf("对已结束任务取消应成功，实际 %v", err)
	}
	after := f.waitState(t, snap.JobID, JobDone)
	if after.State != JobDone {
		t.Fatalf("状态被改成了 %s —— 终态只能被设置一次，否则用户点取消后再看到"+
			"\"完成\"会以为取消没生效", after.State)
	}
}

func TestCancelUnknownJob(t *testing.T) {
	f := newFixture(t, nil, 1000)
	if err := f.engine.Cancel("scan-999"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("应返回 ErrJobNotFound，实际 %v", err)
	}
}

// TestStopCancelsRunningJob 停机必须取消在跑的任务 —— 否则在飞探测会继续往
// 一个正在退役的插件进程的管道里写帧。
func TestStopCancelsRunningJob(t *testing.T) {
	f := newFixture(t, nil, 1000)
	f.prober.gate = make(chan struct{})
	defer close(f.prober.gate)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})

	done := make(chan struct{})
	go func() { f.engine.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop 未在预期时间内返回")
	}

	// 停机后任务被置为已取消；且不得再接受新任务。
	f.engine.mu.Lock()
	st := f.engine.job.state
	f.engine.mu.Unlock()
	if st != JobCanceled {
		t.Errorf("停机后任务状态 = %s，期望 canceled", st)
	}
	if _, err := f.engine.Submit(SubmitRequest{Protocol: testProtocol}); !errors.Is(err, ErrDisabled) {
		t.Errorf("停机后 Submit 应被拒，实际 %v", err)
	}
	_ = snap
}

// TestStopIsIdempotent 重复停机必须安全（main.go 里 defer 与显式调用可能撞上）。
func TestStopIsIdempotent(t *testing.T) {
	f := newFixture(t, nil, 1000)
	f.engine.Stop()
	f.engine.Stop()
}

// ==================== 过期与替换 ====================

// TestExpiredJobIsGone 终态任务过了保留期就查不到 —— 前端据此停止轮询。
func TestExpiredJobIsGone(t *testing.T) {
	f := newFixture(t, &configFile.Config{DeviceScan: configFile.DeviceScanConfig{
		Enabled: true, JobTTLMinutes: 10,
	}}, 1000)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	f.waitState(t, snap.JobID, JobDone)

	// 未过期：查得到。
	f.advance(9 * time.Minute)
	if _, err := f.engine.Status(snap.JobID); err != nil {
		t.Fatalf("9 分钟后任务应还在: %v", err)
	}

	// 过期：查不到，且是一次性清理（不依赖任何后台协程）。
	f.advance(2 * time.Minute)
	if _, err := f.engine.Status(snap.JobID); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("过期后应返回 ErrJobNotFound，实际 %v", err)
	}
}

// TestRunningJobNeverExpires 运行中的任务不受保留期影响。
func TestRunningJobNeverExpires(t *testing.T) {
	f := newFixture(t, &configFile.Config{DeviceScan: configFile.DeviceScanConfig{
		Enabled: true, JobTTLMinutes: 1,
	}}, 1000)
	f.prober.gate = make(chan struct{})
	defer close(f.prober.gate)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	f.advance(time.Hour)

	got, err := f.engine.Status(snap.JobID)
	if err != nil {
		t.Fatalf("运行中的任务不该过期: %v", err)
	}
	if got.State != JobRunning {
		t.Fatalf("状态 = %s，期望 running", got.State)
	}
}

func TestStatusUnknownJob(t *testing.T) {
	f := newFixture(t, nil, 1000)
	if _, err := f.engine.Status("scan-does-not-exist"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("应返回 ErrJobNotFound，实际 %v", err)
	}
}

// TestProgressOmitsResults 轻量快照只给计数、不给清单，而计数与 Status 完全同源。
//
// 两条都不能破：清单为 nil 是"这一份没带"的标记（前端据此保留自己持有的那份，
// 归一成空数组就会把已扫到的设备抹掉）；计数不一致则会让前端漏掉"该补一次完整
// 请求"的时机，症状是"扫到的东西一直不出现"。
func TestProgressOmitsResults(t *testing.T) {
	f := newFixture(t, nil, 1000)
	f.prober.devices = 1

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	done := f.waitState(t, snap.JobID, JobDone)
	if len(done.Devices) == 0 {
		t.Fatal("这个夹具应当扫到设备，否则下面的断言没有意义")
	}

	light, err := f.engine.Progress(snap.JobID)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if light.Devices != nil || light.Errors != nil {
		t.Errorf("轻量快照不该带结果清单：devices=%d errors=%d",
			len(light.Devices), len(light.Errors))
	}
	if light.Found != done.Found || light.Scanned != done.Scanned {
		t.Errorf("计数应与 Status 一致：light found=%d scanned=%d，full found=%d scanned=%d",
			light.Found, light.Scanned, done.Found, done.Scanned)
	}
	// 空结果也必须是数组而不是 nil：nil 专指"没带清单"，拿它表示"扫了但没结果"
	// 会让调用方分不清该保留还是该清空。
	empty := newFixture(t, nil, 1000)
	emptySnap := submit(t, empty.engine, SubmitRequest{Protocol: testProtocol})
	if full := empty.waitState(t, emptySnap.JobID, JobDone); full.Devices == nil {
		t.Error("带清单的快照里，空结果也必须是数组，不能是 nil")
	}
}

// ==================== 错误明细 ====================

// TestProbeErrorsAreReported 探测本身失败要进 errors 明细（带原因），
// 而不是静默变成"没扫到" —— 两者的处置完全不同。
func TestProbeErrorsAreReported(t *testing.T) {
	f := newFixture(t, nil, 1000)
	f.prober.err = errors.New("选项解析失败：站号非法")

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	done := f.waitState(t, snap.JobID, JobDone)

	if len(done.Errors) != testHosts {
		t.Fatalf("错误明细 = %d 条，期望 %d 条", len(done.Errors), testHosts)
	}
	for _, e := range done.Errors {
		if e.Host == "" || e.Reason == "" {
			t.Errorf("错误明细缺少定位信息: %+v", e)
		}
	}
	// 一个都没扫到，但任务仍是"正常结束"——"没扫到"不是失败。
	if done.Found != 0 {
		t.Errorf("found = %d，期望 0", done.Found)
	}
}

// ==================== 快照隔离 ====================

// TestSnapshotIsIsolated 快照必须是副本：调用方在锁外遍历时，worker 仍在
// 往任务里 append。
func TestSnapshotIsIsolated(t *testing.T) {
	f := newFixture(t, nil, 1000)
	f.prober.devices = 1

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	if len(snap.Devices) != 0 {
		t.Fatalf("刚提交时不该有结果，实际 %d 条", len(snap.Devices))
	}

	done := f.waitState(t, snap.JobID, JobDone)
	if len(snap.Devices) != 0 {
		t.Fatalf("先前的快照被后续结果改写了：现在有 %d 条", len(snap.Devices))
	}
	if len(done.Devices) != testHosts {
		t.Fatalf("终态快照应有 %d 条，实际 %d 条", testHosts, len(done.Devices))
	}
}

// ==================== TargetMode 与去重 ====================

// TestBroadcastSpecPlansBroadcastTarget 声明 TargetMode=broadcast 的协议，
// 派发给插件的必须**是广播目标本身**（带 Broadcast 标记与源地址绑定）。
//
// 这条挡的是一个真实存在过的缺口：TargetMode 早就写在契约里、也由
// driversdk.Meta.Validate 校验，但引擎侧的展开逻辑从来没有读它 ——
// 于是"声明了广播的插件"拿到的仍是两百多个逐主机目标，而两边都不报错。
// 症状是 EtherNet/IP 那类协议扫得又慢又（在某些设备上）扫不到。
func TestBroadcastSpecPlansBroadcastTarget(t *testing.T) {
	f := newFixtureWith(t, nil, testSpecWith(driver.TargetModeBroadcast, 8000))

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	if snap.Planned != 1 {
		t.Fatalf("planned = %d，广播协议一个网段只该有 1 个目标", snap.Planned)
	}
	f.waitState(t, snap.JobID, JobDone)

	if f.prober.callCount() != 1 {
		t.Fatalf("实际探测 %d 次，期望 1 次", f.prober.callCount())
	}
	c := f.prober.calls[0]
	if c.Host != "10.0.0.7" {
		t.Errorf("目标地址 = %q，期望子网广播地址 10.0.0.7", c.Host)
	}
	if !c.Broadcast {
		t.Error("目标没有带 Broadcast 标记，插件无法区分该不该绑源地址")
	}
	if c.LocalIP != testLocalIP {
		t.Errorf("LocalIP = %q，期望 %q", c.LocalIP, testLocalIP)
	}
	// 端口仍走协议声明的默认端口。
	if c.Port != 502 {
		t.Errorf("端口 = %d，期望 502", c.Port)
	}
}

// TestDedupKeepsDistinctUnits 去重键含 unitId：同一 IP:端口 上的不同站号
// 是**协议定义的真实区分**（Modbus 多站号探测、一台 PLC 的多个 FINS 节点），
// 合并它们会把本该建成多台设备的发现压成一台。
func TestDedupKeepsDistinctUnits(t *testing.T) {
	f := newFixture(t, nil, 1000)
	// fakeProber 对每个目标产出 devices 台设备，UnitID 从 1 开始递增。
	f.prober.devices = 3

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	done := f.waitState(t, snap.JobID, JobDone)

	if done.Found != testHosts*3 {
		t.Fatalf("found = %d，期望 %d（%d 个目标 × 3 个站号）", done.Found, testHosts*3, testHosts)
	}
}

// TestDedupDropsSameDeviceFromTwoTargets 同一台设备被两个目标报出来时只留一条。
//
// 这正是 TargetMode=both 的形态：广播目标产出一批设备，逐主机目标又命中
// 其中一台。不去重的话用户看到的会是两份一模一样的设备行，而他从界面上
// 完全无法判断这是"两台设备"还是"重复"——只能靠人工比对。
func TestDedupDropsSameDeviceFromTwoTargets(t *testing.T) {
	f := newFixtureWith(t, nil, testSpecWith(driver.TargetModeBoth, 1000))
	// 让**每个**目标都报出同一台设备（不看 target.Host）：模拟广播与单播
	// 各自发现了同一台 PLC。fakeProber 的单元号固定为 1，因此三条都同键。
	f.prober.fixedHost = "10.0.0.9"
	f.prober.fixedPort = 502
	f.prober.devices = 1

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	// 双模：6 个主机 + 1 个广播 = 7 个目标，每个都报同一台设备。
	if snap.Planned != testHosts+1 {
		t.Fatalf("planned = %d，期望 %d", snap.Planned, testHosts+1)
	}
	done := f.waitState(t, snap.JobID, JobDone)

	if done.Found != 1 {
		t.Fatalf("found = %d，期望 1 —— 7 个目标报的是同一台设备，只该留一条", done.Found)
	}
	// 进度仍按目标数算：去重不该让进度条假装少扫了几个目标。
	if done.Scanned != testHosts+1 {
		t.Errorf("scanned = %d，期望 %d", done.Scanned, testHosts+1)
	}
}

// ==================== 分批续扫 ====================

// TestContinueScansRemainingTargetsInSameJob 单批上限只扫前 N 个时，续扫把剩下的
// 目标扫完，且**结果落在同一个任务里**（jobId 不变、设备清单累计）。
//
// 这是分批续扫的核心承诺：建库接口按 snap.Devices[index] 定位，任务一换，
// 前面几批扫到的结果就从服务端消失了。
func TestContinueScansRemainingTargetsInSameJob(t *testing.T) {
	f := newFixture(t, &configFile.Config{DeviceScan: configFile.DeviceScanConfig{
		Enabled: true, MaxTargets: 3,
	}}, 1000)
	f.prober.devices = 1 // 每个目标产出一台设备，用来断言结果跨批累计

	first := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	if first.Planned != 3 || !first.Truncated || first.Remaining != testHosts-3 {
		t.Fatalf("第一批 planned=%d truncated=%v remaining=%d，期望 3/true/%d",
			first.Planned, first.Truncated, first.Remaining, testHosts-3)
	}
	f.waitState(t, first.JobID, JobDone)

	second, err := f.engine.Continue(first.JobID)
	if err != nil {
		t.Fatalf("Continue 失败: %v", err)
	}
	if second.JobID != first.JobID {
		t.Fatalf("续扫换了任务 id：%s → %s", first.JobID, second.JobID)
	}
	if second.State != JobRunning {
		t.Fatalf("续扫后状态 = %s，期望 running", second.State)
	}
	if second.Planned != testHosts-3 || second.Remaining != 0 || second.Truncated {
		t.Fatalf("第二批 planned=%d remaining=%d truncated=%v，期望 %d/0/false",
			second.Planned, second.Remaining, second.Truncated, testHosts-3)
	}

	done := f.waitState(t, first.JobID, JobDone)
	if done.Scanned != testHosts-3 || done.ScannedTotal != testHosts || done.Total != testHosts-3 {
		t.Errorf("scanned=%d scannedTotal=%d total=%d，期望 %d/%d/%d",
			done.Scanned, done.ScannedTotal, done.Total,
			testHosts-3, testHosts, testHosts-3)
	}
	if done.Found != testHosts {
		t.Errorf("found = %d，期望 %d —— 结果必须跨批次累计", done.Found, testHosts)
	}

	// 两批合起来恰好覆盖 6 个主机地址，不重不漏。
	seen := map[string]bool{}
	for _, c := range f.prober.calls {
		if seen[c.Host] {
			t.Errorf("目标 %s 被探测了两次", c.Host)
		}
		seen[c.Host] = true
	}
	if len(seen) != testHosts {
		t.Errorf("两批共探测 %d 个不同地址，期望 %d", len(seen), testHosts)
	}
}

// TestContinueAtEndIsIdempotent 已扫完的任务再续扫是幂等的空操作。
func TestContinueAtEndIsIdempotent(t *testing.T) {
	f := newFixture(t, nil, 1000)
	first := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	f.waitState(t, first.JobID, JobDone)
	before := f.prober.callCount()

	snap, err := f.engine.Continue(first.JobID)
	if err != nil {
		t.Fatalf("已扫完时 Continue 应幂等成功: %v", err)
	}
	if snap.JobID != first.JobID || snap.State != JobDone || snap.Remaining != 0 {
		t.Fatalf("幂等返回的快照不对: %+v", snap)
	}
	if got := f.prober.callCount(); got != before {
		t.Errorf("幂等续扫又发了 %d 次探测", got-before)
	}
}

// TestContinueRejectedWhileRunning 上一批还在跑时不能续扫。
func TestContinueRejectedWhileRunning(t *testing.T) {
	f := newFixture(t, nil, 1000)
	f.prober.gate = make(chan struct{})
	defer close(f.prober.gate)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	if _, err := f.engine.Continue(snap.JobID); !errors.Is(err, ErrBusy) {
		t.Fatalf("运行中续扫应返回 ErrBusy，实际 %v", err)
	}
	if _, err := f.engine.Continue("scan-nope"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("未知任务应返回 ErrJobNotFound，实际 %v", err)
	}
}

// TestContinueAfterCancelRescansCurrentBatch 取消后续扫必须从**当前批的起点**重来。
//
// 取消时并发探测完成到哪一步无法确定（完成顺序与目标顺序无关），所以不能从
// "下一批"接着扫 —— 那会静默跳过本批未完成的一半。重扫整批的重复结果由
// acceptDevice 去重，代价只是多几次探测。
func TestContinueAfterCancelRescansCurrentBatch(t *testing.T) {
	f := newFixture(t, &configFile.Config{DeviceScan: configFile.DeviceScanConfig{
		Enabled: true, MaxTargets: 3,
	}}, 1000)
	f.prober.gate = make(chan struct{})

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})

	// 精确放行一个探测：制造"本批只扫了一部分"的稳定状态。
	f.prober.gate <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := f.engine.Progress(snap.JobID)
		if err == nil && got.Scanned >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("等第一个目标扫完超时")
		}
		time.Sleep(2 * time.Millisecond)
	}

	if err := f.engine.Cancel(snap.JobID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	canceled := f.waitState(t, snap.JobID, JobCanceled)
	// 未扫的 5 个 = 本批剩下 2 个 + 下一批 3 个。
	if canceled.Remaining != testHosts-1 {
		t.Fatalf("取消后 remaining = %d，期望 %d", canceled.Remaining, testHosts-1)
	}
	if !canceled.Truncated {
		t.Fatal("取消留下未扫的目标时必须报告 truncated，否则前端不会给出继续入口")
	}

	close(f.prober.gate) // 之后所有探测立即放行（含旧批仍在收尾的那些）
	second, err := f.engine.Continue(snap.JobID)
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if second.Planned != 3 {
		t.Fatalf("续扫 planned = %d，期望从批首重扫 3 个目标", second.Planned)
	}
	f.waitState(t, snap.JobID, JobDone)

	// 取消发生在第一批；重扫把第一批补完整后，再续一次才能覆盖第二批。
	if _, err := f.engine.Continue(snap.JobID); err != nil {
		t.Fatalf("第二次 Continue: %v", err)
	}
	f.waitState(t, snap.JobID, JobDone)

	seen := map[string]bool{}
	for _, c := range f.prober.calls {
		seen[c.Host] = true
	}
	if len(seen) != testHosts {
		t.Errorf("重扫后共覆盖 %d 个地址，期望 %d（不能因为取消而漏掉本批后半段）",
			len(seen), testHosts)
	}
}
