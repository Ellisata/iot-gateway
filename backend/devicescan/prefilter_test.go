// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package devicescan

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"sort"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/configFile"
)

// ==================== 假拨号器 ====================

// fakeDialer 代替真实的 net.Dialer。
//
// alive 里的地址返回"连得上"；errFor 里的返回指定错误；其余按不可达处理。
// blockAll / blockHost 用来把预筛停在半途，断言"阶段 1 未结束前阶段 2 不启动"。
type fakeDialer struct {
	mu        sync.Mutex
	calls     []driver.ScanTarget
	alive     map[string]bool
	errFor    map[string]error
	gate      chan struct{}
	blockAll  bool
	blockHost string

	active    int
	maxActive int
}

func (d *fakeDialer) dial(ctx context.Context, t driver.ScanTarget) error {
	d.mu.Lock()
	d.calls = append(d.calls, t)
	d.active++
	if d.active > d.maxActive {
		d.maxActive = d.active
	}
	// gate 已被 release 置空后不再阻塞：否则 release 之后才派发的拨号
	// 会对着一个 nil channel 永久等待。
	gate := d.gate
	block := gate != nil && (d.blockAll || d.blockHost == t.Host)
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.active--
		d.mu.Unlock()
	}()

	if block {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	d.mu.Lock()
	err, alive := d.errFor[t.Host], d.alive[t.Host]
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if !alive {
		return fmt.Errorf("dial tcp %s:%d: 不可达", t.Host, t.Port)
	}
	return nil
}

func (d *fakeDialer) hosts() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.calls))
	for _, c := range d.calls {
		out = append(out, c.Host)
	}
	return out
}

func (d *fakeDialer) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.calls)
}

func (d *fakeDialer) activeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active
}

func (d *fakeDialer) peakActive() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.maxActive
}

// release 放行所有被 gate 阻塞的拨号（关闭后新到的拨号也不再阻塞）。
func (d *fakeDialer) release() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.gate != nil {
		close(d.gate)
		d.gate = nil
	}
}

func (d *fakeDialer) setAlive(hosts ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.alive = make(map[string]bool, len(hosts))
	for _, h := range hosts {
		d.alive[h] = true
	}
}

// ==================== 夹具 ====================

func testSpecWithTransport(mode driver.TargetMode, transport driver.ProbeTransport,
	maxBudgetMs int) driver.ProtocolDiscovery {

	spec := testSpecWith(mode, maxBudgetMs)
	spec.Spec.Transport = transport
	return spec
}

func prefilterCfg(concurrency int) *configFile.Config {
	return &configFile.Config{DeviceScan: configFile.DeviceScanConfig{
		Enabled:              true,
		MaxConcurrentProbes:  8,
		PrefilterEnabled:     true,
		PrefilterConcurrency: concurrency,
		PrefilterTimeoutMs:   500,
	}}
}

// newPrefilterFixture 构造一个声明了指定 Transport、并注入了假拨号器的引擎。
// logf 置空，避免测试触发真实日志文件的初始化。
func newPrefilterFixture(t *testing.T, cfg *configFile.Config, mode driver.TargetMode,
	transport driver.ProbeTransport, d *fakeDialer) *fixture {

	t.Helper()
	f := newFixtureWith(t, cfg, testSpecWithTransport(mode, transport, 1000))
	f.engine.prefilterDial = d.dial
	f.engine.logf = func(string, ...interface{}) {}
	return f
}

func sortedHosts(hosts []string) []string {
	out := append([]string(nil), hosts...)
	sort.Strings(out)
	return out
}

func proberHosts(p *fakeProber) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.calls))
	for _, c := range p.calls {
		out = append(out, c.Host)
	}
	return out
}

// ==================== 预筛行为 ====================

// TestPrefilterOnlyProbesReachableHosts 只有端口开着的主机进入阶段 2；
// 判死的目标计入 Scanned，但不产生错误。
func TestPrefilterOnlyProbesReachableHosts(t *testing.T) {
	d := &fakeDialer{alive: map[string]bool{"10.0.0.2": true, "10.0.0.4": true}}
	f := newPrefilterFixture(t, prefilterCfg(4), driver.TargetModeUnicast, driver.ProbeTransportTCP, d)
	f.prober.devices = 1

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	done := f.waitState(t, snap.JobID, JobDone)

	if got, want := d.callCount(), testHosts; got != want {
		t.Errorf("预筛拨号数 = %d，期望 %d（每个单播目标一次）", got, want)
	}
	if got, want := sortedHosts(proberHosts(f.prober)), []string{"10.0.0.2", "10.0.0.4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("进入插件探测的主机 = %v，期望 %v", got, want)
	}
	if done.Scanned != done.Total || done.Total != testHosts {
		t.Errorf("进度 = %d/%d，期望 %d/%d —— 预筛判死的目标也要计入",
			done.Scanned, done.Total, testHosts, testHosts)
	}
	if done.Found != 2 {
		t.Errorf("Found = %d，期望 2", done.Found)
	}
	if len(done.Errors) != 0 {
		t.Errorf("预筛判死不应产生错误，实际 %+v", done.Errors)
	}
}

// TestPrefilterBarrier 阶段 1 全部结束前不得启动任何插件探测。
func TestPrefilterBarrier(t *testing.T) {
	d := &fakeDialer{
		gate:     make(chan struct{}),
		blockAll: true,
		alive:    map[string]bool{"10.0.0.2": true},
	}
	f := newPrefilterFixture(t, prefilterCfg(4), driver.TargetModeUnicast, driver.ProbeTransportTCP, d)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	if !waitFor(3*time.Second, func() bool { return d.activeCount() > 0 }) {
		t.Fatal("预筛没有开始")
	}
	if n := f.prober.callCount(); n != 0 {
		t.Fatalf("阶段 1 未结束时插件探测已被调用 %d 次 —— 两阶段之间应当有屏障", n)
	}

	d.release()
	f.waitState(t, snap.JobID, JobDone)
	if n := f.prober.callCount(); n != 1 {
		t.Errorf("放行后插件探测调用数 = %d，期望 1（只有 .2 可达）", n)
	}
}

// TestPrefilterBroadcastBypasses 广播目标永远不进 TCP 预筛。
func TestPrefilterBroadcastBypasses(t *testing.T) {
	d := &fakeDialer{} // 所有单播都不可达
	f := newPrefilterFixture(t, prefilterCfg(4), driver.TargetModeBoth, driver.ProbeTransportTCP, d)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	done := f.waitState(t, snap.JobID, JobDone)

	if got, want := d.callCount(), testHosts; got != want {
		t.Errorf("预筛拨号数 = %d，期望 %d（只拨单播目标）", got, want)
	}
	for _, h := range d.hosts() {
		if h == "10.0.0.7" {
			t.Error("广播地址 10.0.0.7 被做了 TCP 预筛 —— 广播目标必须绕过")
		}
	}
	if n := f.prober.callCount(); n != 1 {
		t.Fatalf("插件探测调用数 = %d，期望 1（广播目标）", n)
	}
	f.prober.mu.Lock()
	isBroadcast := f.prober.calls[0].Broadcast
	f.prober.mu.Unlock()
	if !isBroadcast {
		t.Error("进入插件探测的应当只有广播目标")
	}
	if done.Scanned != done.Total || done.Total != testHosts+1 {
		t.Errorf("进度 = %d/%d，期望 %d/%d", done.Scanned, done.Total, testHosts+1, testHosts+1)
	}
}

// TestPrefilterSkipsUDPAndUnknown UDP 与未声明传输层的协议整批不做 TCP 预筛。
func TestPrefilterSkipsUDPAndUnknown(t *testing.T) {
	for _, tr := range []driver.ProbeTransport{driver.ProbeTransportUDP, driver.ProbeTransportUnknown} {
		tr := tr
		t.Run(fmt.Sprintf("transport=%q", tr), func(t *testing.T) {
			d := &fakeDialer{}
			f := newPrefilterFixture(t, prefilterCfg(4), driver.TargetModeUnicast, tr, d)

			snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
			f.waitState(t, snap.JobID, JobDone)

			if n := d.callCount(); n != 0 {
				t.Errorf("Transport=%q 时预筛拨号数 = %d，期望 0", tr, n)
			}
			if n := f.prober.callCount(); n != testHosts {
				t.Errorf("插件探测数 = %d，期望 %d", n, testHosts)
			}
		})
	}
}

// TestPrefilterDisabledByConfig 关闭开关后行为与旧版本完全一致。
func TestPrefilterDisabledByConfig(t *testing.T) {
	cfg := prefilterCfg(4)
	cfg.DeviceScan.PrefilterEnabled = false
	d := &fakeDialer{}
	f := newPrefilterFixture(t, cfg, driver.TargetModeUnicast, driver.ProbeTransportTCP, d)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	f.waitState(t, snap.JobID, JobDone)

	if n := d.callCount(); n != 0 {
		t.Errorf("关闭预筛后拨号数 = %d，期望 0", n)
	}
	if n := f.prober.callCount(); n != testHosts {
		t.Errorf("插件探测数 = %d，期望 %d", n, testHosts)
	}
}

// TestPrefilterAllDead 全不可达是正常结果：正常结束、进度走满、无错误。
func TestPrefilterAllDead(t *testing.T) {
	d := &fakeDialer{}
	f := newPrefilterFixture(t, prefilterCfg(4), driver.TargetModeUnicast, driver.ProbeTransportTCP, d)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	done := f.waitState(t, snap.JobID, JobDone)

	if n := f.prober.callCount(); n != 0 {
		t.Errorf("全不可达时插件探测数 = %d，期望 0", n)
	}
	if done.Scanned != testHosts || done.Total != testHosts {
		t.Errorf("进度 = %d/%d，期望 %d/%d", done.Scanned, done.Total, testHosts, testHosts)
	}
	if done.Found != 0 || len(done.Errors) != 0 {
		t.Errorf("Found/Errors = %d/%d，期望 0/0", done.Found, len(done.Errors))
	}
}

// TestPrefilterLocalResourceErrorFailsOpen 网关自身 fd 耗尽时不能把目标判死，
// 必须放行给插件（独立进程，有自己的 fd 配额）。
func TestPrefilterLocalResourceErrorFailsOpen(t *testing.T) {
	d := &fakeDialer{errFor: map[string]error{
		"10.0.0.2": fmt.Errorf("dial: %w", syscall.EMFILE),
	}}
	f := newPrefilterFixture(t, prefilterCfg(4), driver.TargetModeUnicast, driver.ProbeTransportTCP, d)
	f.prober.devices = 1

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	done := f.waitState(t, snap.JobID, JobDone)

	if got, want := sortedHosts(proberHosts(f.prober)), []string{"10.0.0.2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("EMFILE 的目标应放行给插件，实际探测主机 = %v", got)
	}
	if done.Scanned != testHosts {
		t.Errorf("Scanned = %d，期望 %d（放行的目标随后被插件探测）", done.Scanned, testHosts)
	}
	if len(done.Errors) != 0 {
		t.Errorf("不应产生错误，实际 %+v", done.Errors)
	}
}

// TestPrefilterConcurrencyCap 预筛并发受独立配置限制。
func TestPrefilterConcurrencyCap(t *testing.T) {
	d := &fakeDialer{
		gate:     make(chan struct{}),
		blockAll: true,
	}
	f := newPrefilterFixture(t, prefilterCfg(2), driver.TargetModeUnicast, driver.ProbeTransportTCP, d)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	if !waitFor(3*time.Second, func() bool { return d.peakActive() >= 2 }) {
		t.Fatal("预筛并发没有达到配置值")
	}
	if peak := d.peakActive(); peak > 2 {
		t.Errorf("预筛并发峰值 = %d，超过配置的 2", peak)
	}

	d.release()
	f.waitState(t, snap.JobID, JobDone)
	if active := d.activeCount(); active != 0 {
		t.Errorf("结束后在飞预筛 = %d，期望 0", active)
	}
}

// TestPrefilterCancelDoesNotCount 取消时未完成的预筛目标不计数；
// 续扫沿用预筛设置，从批首重扫整批。
func TestPrefilterCancelDoesNotCount(t *testing.T) {
	d := &fakeDialer{
		gate:      make(chan struct{}),
		blockHost: "10.0.0.2",
		errFor:    map[string]error{"10.0.0.1": fmt.Errorf("dial tcp: 不可达")},
	}
	f := newPrefilterFixture(t, prefilterCfg(1), driver.TargetModeUnicast, driver.ProbeTransportTCP, d)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	if !waitFor(3*time.Second, func() bool {
		s, err := f.engine.Status(snap.JobID)
		return err == nil && s.Scanned == 1 && d.activeCount() == 1
	}) {
		t.Fatal("等 .1 判死、.2 进入在飞状态超时")
	}

	if err := f.engine.Cancel(snap.JobID); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	canceled := f.waitState(t, snap.JobID, JobCanceled)

	if canceled.Scanned != 1 {
		t.Errorf("Scanned = %d，期望 1（只有取消前完成判死的 .1）", canceled.Scanned)
	}
	if want := testHosts - 1; canceled.Remaining != want {
		t.Errorf("Remaining = %d，期望 %d", canceled.Remaining, want)
	}
	if !canceled.Truncated {
		t.Error("取消后仍有未扫目标，Truncated 应为 true")
	}

	// 续扫：放行预筛，让 .4 可达。
	d.setAlive("10.0.0.4")
	d.release()
	if _, err := f.engine.Continue(snap.JobID); err != nil {
		t.Fatalf("Continue 失败: %v", err)
	}
	done := f.waitState(t, snap.JobID, JobDone)

	if done.ScannedTotal != testHosts {
		t.Errorf("ScannedTotal = %d，期望 %d（取消后续扫整批）", done.ScannedTotal, testHosts)
	}
	if done.Remaining != 0 || done.Truncated {
		t.Errorf("Remaining/Truncated = %d/%v，期望 0/false", done.Remaining, done.Truncated)
	}
	if got, want := sortedHosts(proberHosts(f.prober)), []string{"10.0.0.4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("续扫进入插件的主机 = %v，期望 %v", got, want)
	}
}

// TestPrefilterContinueAcrossPages 分批续扫沿用同一个预筛决定，两批都不重不漏。
func TestPrefilterContinueAcrossPages(t *testing.T) {
	cfg := prefilterCfg(4)
	cfg.DeviceScan.MaxTargets = 3
	d := &fakeDialer{alive: map[string]bool{"10.0.0.2": true, "10.0.0.4": true}}
	f := newPrefilterFixture(t, cfg, driver.TargetModeUnicast, driver.ProbeTransportTCP, d)
	f.prober.devices = 1

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol})
	first := f.waitState(t, snap.JobID, JobDone)
	if !first.Truncated || first.Scanned != 3 {
		t.Fatalf("第一批应扫 3 个且截断，实际 scanned=%d truncated=%v", first.Scanned, first.Truncated)
	}
	if _, err := f.engine.Continue(snap.JobID); err != nil {
		t.Fatalf("Continue 失败: %v", err)
	}
	done := f.waitState(t, snap.JobID, JobDone)

	if done.ScannedTotal != testHosts || done.Remaining != 0 || done.Truncated {
		t.Errorf("续扫后 scannedTotal/remaining/truncated = %d/%d/%v，期望 %d/0/false",
			done.ScannedTotal, done.Remaining, done.Truncated, testHosts)
	}
	if got, want := d.callCount(), testHosts; got != want {
		t.Errorf("两批预筛拨号数 = %d，期望 %d（每批各自预筛，不跨批复用）", got, want)
	}
	if got, want := sortedHosts(proberHosts(f.prober)), []string{"10.0.0.2", "10.0.0.4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("两批进入插件的主机 = %v，期望 %v", got, want)
	}
}

// TestPrefilterPortOverride 预筛必须用目标端口（用户覆盖后的端口）。
func TestPrefilterPortOverride(t *testing.T) {
	d := &fakeDialer{alive: map[string]bool{"10.0.0.2": true}}
	f := newPrefilterFixture(t, prefilterCfg(4), driver.TargetModeUnicast, driver.ProbeTransportTCP, d)

	snap := submit(t, f.engine, SubmitRequest{Protocol: testProtocol, Port: 1502})
	f.waitState(t, snap.JobID, JobDone)

	d.mu.Lock()
	calls := append([]driver.ScanTarget(nil), d.calls...)
	d.mu.Unlock()
	for _, c := range calls {
		if c.Port != 1502 {
			t.Errorf("预筛端口 = %d，期望 1502", c.Port)
		}
	}
	f.prober.mu.Lock()
	for _, c := range f.prober.calls {
		if c.Port != 1502 {
			t.Errorf("插件探测端口 = %d，期望 1502", c.Port)
		}
	}
	f.prober.mu.Unlock()
}

// ==================== 估算与错误分类 ====================

func TestEstimateBatchMaxMs(t *testing.T) {
	unicast := func(n int) []driver.ScanTarget {
		out := make([]driver.ScanTarget, n)
		for i := range out {
			out[i] = driver.ScanTarget{Host: fmt.Sprintf("10.0.0.%d", i+1), Port: 502}
		}
		return out
	}

	// 无预筛：旧公式 ceil(6/8)*1000。
	if got, want := estimateBatchMaxMs(unicast(6), false, 256, 500, 8, 1000), 1000; got != want {
		t.Errorf("无预筛估算 = %d，期望 %d", got, want)
	}
	// 有预筛：ceil(6/2)*500 + ceil(6/8)*1000 = 1500 + 1000。
	if got, want := estimateBatchMaxMs(unicast(6), true, 2, 500, 8, 1000), 2500; got != want {
		t.Errorf("有预筛估算 = %d，期望 %d", got, want)
	}
	// 广播目标不计入阶段 1。
	targets := append([]driver.ScanTarget{{Host: "10.0.0.7", Port: 502, Broadcast: true}}, unicast(6)...)
	if got, want := estimateBatchMaxMs(targets, true, 2, 500, 8, 1000), 2500; got != want {
		t.Errorf("含广播目标的估算 = %d，期望 %d", got, want)
	}
	// 32 位平台溢出：饱和到 2^31-1。
	if got := estimateMaxMsOf(65536, 1, 60000); got != int(maxEstimateMs) {
		t.Errorf("极端估算 = %d，期望饱和到 %d", got, maxEstimateMs)
	}
	if got := estimateBatchMaxMs(unicast(65536), true, 1, 1000, 1, 60000); got != int(maxEstimateMs) {
		t.Errorf("极端估算（含预筛）= %d，期望饱和到 %d", got, maxEstimateMs)
	}
}

// TestDialForPrefilterRealSocket 生产拨号器对一个真的监听端口判定可达，
// 端口关闭后判定不可达 —— 这是 fakeDialer 覆盖不到的最后一层。
func TestDialForPrefilterRealSocket(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	e := NewEngine(nil, nil, nil)
	target := driver.ScanTarget{Host: "127.0.0.1", Port: port}
	if err := e.dialForPrefilter(context.Background(), target); err != nil {
		t.Fatalf("监听中的端口应判为可达: %v", err)
	}

	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if err := e.dialForPrefilter(context.Background(), target); err == nil {
		t.Fatal("端口关闭后应判为不可达")
	}
}

func TestIsLocalDialError(t *testing.T) {
	if !isLocalDialError(fmt.Errorf("dial: %w", syscall.EMFILE)) {
		t.Error("EMFILE 应判为本地资源错误")
	}
	if isLocalDialError(fmt.Errorf("dial tcp: 不可达")) {
		t.Error("普通网络错误不应判为本地资源错误")
	}
	if isLocalDialError(nil) {
		t.Error("nil 不应判为本地资源错误")
	}
}
