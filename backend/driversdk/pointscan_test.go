// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driversdk_test

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
	"iot-gateway/model/po"
	"iot-gateway/pluginwire"
)

// ==================== 点位范围扫描（discoverPoints） ====================
//
// 本文件盯的是 handleDiscoverPoints 的两条路径与那条**串口回退表**。
// 扫出来的数据对不对是协议实现的事（见 modbusdrv 的 pointscan_test.go），
// 这里只管会话：复用哪一条、什么时候不能回退、锁有没有在块间让出去。

const pointConst = "Test.Plugin.Point"

// ==================== 假串口 ====================
//
// 与 pluginhost/fixture_test.go 的同名设施同构：第二个打开同一端口的人拿到
// Access is denied，正是 Windows 下 goburrow/serial 以共享模式 0 调 CreateFile
// 的行为。有了它，"扫描复用了采集那个实例"才真正可证 —— 否则也可能只是
// SDK 根本没在拦第二次打开。

var (
	pointPortsMu sync.Mutex
	pointPorts   = map[string]string{} // 小写端口名 → 持有者
)

func pointOpenPort(port, owner string) error {
	pointPortsMu.Lock()
	defer pointPortsMu.Unlock()
	key := strings.ToLower(port)
	if who, ok := pointPorts[key]; ok {
		return fmt.Errorf("Access is denied: port %s held by %s", port, who)
	}
	pointPorts[key] = owner
	return nil
}

func pointClosePort(port, owner string) {
	pointPortsMu.Lock()
	defer pointPortsMu.Unlock()
	if pointPorts[strings.ToLower(port)] == owner {
		delete(pointPorts, strings.ToLower(port))
	}
}

// pointDriver 点位扫描用例的驱动。
//
// 三条行为要点：
//   - protocolJSON 里带 port 时**独占该串口**（Connect 打开、Close 释放），
//     第二次打开必然失败；
//   - 实现 driver.ConnectionMatcher，于是"这条连接正是本次要连的那条"可判；
//   - Read 可以被 readGate 卡住，用来把会话锁按住不放（构造"设备正忙"与
//     "块间锁已让出"两种场景）。
type pointDriver struct {
	mu        sync.Mutex
	connected bool
	port      string // 本实例当前持有的串口（空 = 没持有）
	tag       string // 身份标记，finder 把它回传出来供断言
	readGate  chan struct{}
	readEnt   chan struct{}

	onConnect func()
	onRead    func()
}

func pointConfigOf(protocolJSON string) (port, tag string) {
	if protocolJSON == "" {
		return "", ""
	}
	var cfg struct {
		Port string `json:"port"`
		Tag  string `json:"tag"`
	}
	_ = json.Unmarshal([]byte(protocolJSON), &cfg)
	return cfg.Port, cfg.Tag
}

func (d *pointDriver) Connect(protocolJSON string) error {
	port, tag := pointConfigOf(protocolJSON)
	if port != "" {
		if err := pointOpenPort(port, "point"); err != nil {
			return err
		}
		d.mu.Lock()
		d.port = port
		d.mu.Unlock()
	}
	d.mu.Lock()
	d.tag = tag
	d.connected = true
	d.mu.Unlock()
	if d.onConnect != nil {
		d.onConnect()
	}
	return nil
}

// MatchConnection 见 driver.ConnectionMatcher：只有真的持有该端口的实例才匹配。
func (d *pointDriver) MatchConnection(protocolJSON string) bool {
	port, _ := pointConfigOf(protocolJSON)
	d.mu.Lock()
	defer d.mu.Unlock()
	return port != "" && d.port != "" && strings.EqualFold(d.port, port)
}

func (d *pointDriver) Ping(string) error { return nil }

func (d *pointDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	if d.readEnt != nil {
		close(d.readEnt)
	}
	if d.readGate != nil {
		<-d.readGate
	}
	if d.onRead != nil {
		d.onRead()
	}
	out := make([]driver.ReadResult, len(addrs))
	for i, a := range addrs {
		out[i] = driver.ReadResult{DeviceAddressID: a.ID, Value: "v", Quality: 192}
	}
	return out, nil
}

func (d *pointDriver) IsConnected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.connected
}

func (d *pointDriver) Close() error {
	d.mu.Lock()
	port := d.port
	d.port = ""
	d.connected = false
	d.mu.Unlock()
	if port != "" {
		pointClosePort(port, "point")
	}
	return nil
}

// tagOf 读出 finder 从 Do 拿到的那个实例的身份。
func tagOf(drv driver.Driver) string {
	if p, ok := drv.(*pointDriver); ok {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.tag
	}
	return ""
}

// ==================== 脚手架 ====================

// pointMeta 一份只声明点位扫描能力的 Meta（可选串口独占）。
func pointMeta(serialExclusive bool, maxBudgetMs int) driversdk.Meta {
	return driversdk.Meta{
		APIVersion: driversdk.APIVersion,
		Protocols: []driversdk.Protocol{{
			Name:            pointConst,
			SerialExclusive: serialExclusive,
			PointDiscovery: &driver.PointDiscoverySpec{
				Title:                 "测试点位扫描",
				DefaultCommonDataType: "Short",
				MaxBudgetMs:           maxBudgetMs,
				MaxPoints:             100,
			},
		}},
		Scope: "testpluginpoint",
		Types: []*driver.DataType{
			{Name: "bool", Kind: driver.KindBool, Size: 1},
			{Name: "int16", Kind: driver.KindInt, Size: 2},
		},
		TypeMap: map[string]string{"Boolean": "bool", "Short": "int16"},
	}
}

// pointFactories 单协议工厂。
func pointFactories(mk func() driver.Driver) driversdk.Factories {
	return driversdk.Factories{pointConst: mk}
}

// pointFixture 一个跑在 net.Pipe 上的点位扫描服务端。
type pointFixture struct {
	c     *rpcClient
	conns *atomic.Int64 // 工厂被调用（= 新建实例）的次数
}

// startPoint 起一个声明了点位扫描能力的服务端。
func startPoint(t *testing.T, meta driversdk.Meta, finder driversdk.FindPointsFunc,
	mk func() driver.Driver) *pointFixture {

	t.Helper()
	client, server := net.Pipe()
	done := make(chan error, 1)
	conns := &atomic.Int64{}
	factory := func() driver.Driver {
		conns.Add(1)
		return mk()
	}
	go func() {
		done <- driversdk.ServeConn(server, pointFactories(factory), meta,
			driversdk.Capabilities{PointFinders: driversdk.PointFinders{pointConst: finder}})
	}()

	// 启动期校验失败时 ServeConn 会立刻返回错误。不在这里拦下的话，后面每一次
	// 写帧都会**永久阻塞在 net.Pipe 上**（它无缓冲），表现为"测试挂死"，
	// 完全指不到"其实是 Meta 写错了"这个根因。
	select {
	case err := <-done:
		_ = client.Close()
		t.Fatalf("ServeConn 启动失败: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	t.Cleanup(func() {
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("ServeConn 未在 2s 内返回")
		}
	})
	return &pointFixture{c: newRPCClient(t, client), conns: conns}
}

// discover 发一次 discoverPoints 并返回原始应答帧。
func (f *pointFixture) discover(params driversdk.DiscoverPointsParams) pluginwire.Frame {
	return f.c.call(pluginwire.MethodDiscoverPoints, params)
}

// pointSend 只写帧、不等应答（写也持 c.mu，避免与其它调用交错成坏帧）。
//
// 与 call 的区别是"写"与"读"分开了。rpcClient.call 会在**等应答的整段时间里**
// 持着 c.mu，于是"起一个 goroutine 发请求、自己再发一个"会立刻自死锁
// —— 需要让某个请求在途时，用 pointSend + 稍后 pointAwait 配一对。
func (f *pointFixture) pointSend(id uint64, method string, params any) {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	f.c.send(id, method, params)
}

// pointAwait 等指定 id 的应答（途中遇到的其它应答先存进 buffered）。
func (f *pointFixture) pointAwait(id uint64) pluginwire.Frame {
	f.c.mu.Lock()
	defer f.c.mu.Unlock()
	return f.c.readFor(id)
}

// discoverOK 发一次 discoverPoints 并要求成功，返回解码后的结果。
func (f *pointFixture) discoverOK(t *testing.T, params driversdk.DiscoverPointsParams) driver.PointScanResult {
	t.Helper()
	resp := f.discover(params)
	if resp.Error != nil {
		t.Fatalf("discoverPoints 返回错误: %s", resp.Error.Message)
	}
	var out driver.PointScanResult
	if err := pluginwire.DecodeBody(resp.Codec, resp.Result, &out); err != nil {
		t.Fatalf("解码结果: %v", err)
	}
	return out
}

// connect 建立一个会话，返回会话 id。
func (f *pointFixture) connect(t *testing.T, protocolJSON string) uint64 {
	t.Helper()
	var out driversdk.ConnectResult
	f.c.callOK(pluginwire.MethodConnect, driversdk.ConnectParams{
		Protocol: pointConst, ProtocolJSON: protocolJSON,
	}, &out)
	if out.Session == 0 {
		t.Fatal("Connect 未返回会话 id")
	}
	return out.Session
}

// echoFinder 一个把观察到的输入回传出来的 finder。
//
// Label 承载观察结果：PointScanResult 没有 Extra 字段，而 Label 是结果里唯一
// 会原样过线的自由文本（pluginhost 的夹具用同一条路数）。
func echoFinder(observe func(req driversdk.FindPointsRequest) string) driversdk.FindPointsFunc {
	return func(req driversdk.FindPointsRequest) (*driver.PointScanResult, error) {
		note := observe(req)
		return &driver.PointScanResult{
			AssumedCommonDataType: "Short",
			Scanned:               1,
			Readable:              1,
			Points: []driver.DiscoveredPoint{{
				Name:  "fixture-point",
				Label: note,
				Value: string(req.Options),
			}},
		}, nil
	}
}

// ==================== 基本路由与预算 ====================

// TestDiscoverPointsRoutesToFinder 参数必须无损过线，且预算取
// min(网关请求值, 插件声明值)。
func TestDiscoverPointsRoutesToFinder(t *testing.T) {
	var got driversdk.FindPointsRequest
	f := startPoint(t, pointMeta(false, 2000), echoFinder(func(req driversdk.FindPointsRequest) string {
		got = req
		return "ok"
	}), func() driver.Driver { return &pointDriver{} })

	res := f.discoverOK(t, driversdk.DiscoverPointsParams{
		Protocol:     pointConst,
		ProtocolJSON: `{"tag":"t1"}`,
		Options:      `{"range":"1-10"}`,
		BudgetMs:     1500,
	})
	if len(res.Points) != 1 || res.Points[0].Value != `{"range":"1-10"}` {
		t.Fatalf("选项没有原样过线: %+v", res.Points)
	}
	if got.Protocol != pointConst {
		t.Errorf("Protocol = %q", got.Protocol)
	}
	if string(got.Options) != `{"range":"1-10"}` {
		t.Errorf("Options = %q", got.Options)
	}
	// 请求值 1500 < 声明值 2000 ⇒ 取请求值。
	if got.Budget != 1500*time.Millisecond {
		t.Errorf("Budget = %s，期望 1.5s", got.Budget)
	}
	if got.Deadline.IsZero() {
		t.Error("Deadline 必须填上：finder 的主循环靠它判「还有没有时间」")
	}
	if got.Session == nil {
		t.Error("Session 必须非 nil —— 它是 finder 拿驱动实例的唯一入口")
	}
}

// TestDiscoverPointsBudgetClampedToMeta 请求值大于声明值时以**声明值**为准。
//
// 插件是必须不超时的那一方，不能把这条不变量交给对端给的值。
func TestDiscoverPointsBudgetClampedToMeta(t *testing.T) {
	var got driversdk.FindPointsRequest
	f := startPoint(t, pointMeta(false, 1500), echoFinder(func(req driversdk.FindPointsRequest) string {
		got = req
		return "ok"
	}), func() driver.Driver { return &pointDriver{} })

	f.discoverOK(t, driversdk.DiscoverPointsParams{Protocol: pointConst, BudgetMs: 60000})
	if got.Budget != 1500*time.Millisecond {
		t.Fatalf("Budget = %s，期望被插件声明值 1.5s 钳住", got.Budget)
	}
}

// TestDiscoverPointsEmptyOptions 未填选项时 finder 收到的应是长度 0 的 Options
// （而不是空 JSON 串）—— 否则 json.Unmarshal 会报 "unexpected end of JSON input"。
func TestDiscoverPointsEmptyOptions(t *testing.T) {
	var got driversdk.FindPointsRequest
	f := startPoint(t, pointMeta(false, 1000), echoFinder(func(req driversdk.FindPointsRequest) string {
		got = req
		return "ok"
	}), func() driver.Driver { return &pointDriver{} })

	f.discoverOK(t, driversdk.DiscoverPointsParams{Protocol: pointConst})
	if len(got.Options) != 0 {
		t.Fatalf("Options 长度 = %d，期望 0", len(got.Options))
	}
}

// TestDiscoverPointsUnknownProtocol 网关向没声明点扫能力的协议派发时，
// 必须如实报错而不是静默返回空列表（后者会让用户以为"这里没有点位"）。
func TestDiscoverPointsUnknownProtocol(t *testing.T) {
	f := startPoint(t, pointMeta(false, 1000), echoFinder(func(driversdk.FindPointsRequest) string {
		t.Fatal("不该被调用")
		return ""
	}), func() driver.Driver { return &pointDriver{} })

	resp := f.discover(driversdk.DiscoverPointsParams{Protocol: "No.Such.Protocol"})
	if resp.Error == nil {
		t.Fatal("未声明点扫能力的协议应报错")
	}
	if !strings.Contains(resp.Error.Message, "does not support point discovery") {
		t.Errorf("错误文案应指明原因，实际 %q", resp.Error.Message)
	}
}

// ==================== 会话复用 ====================

// TestDiscoverPointsReusesLiveSession 有会话且命中时必须**复用**它，
// 而不是另建一个实例。
//
// 判据有两条，缺一不可：finder 拿到的实例带着会话实例的 tag（是同一个对象），
// 且工厂**没有被再次调用**（没新建实例）。
func TestDiscoverPointsReusesLiveSession(t *testing.T) {
	f := startPoint(t, pointMeta(true, 1000), echoFinder(func(req driversdk.FindPointsRequest) string {
		var tag string
		if err := req.Session.Do(func(drv driver.Driver) error {
			tag = tagOf(drv)
			return nil
		}); err != nil {
			return "do-err:" + err.Error()
		}
		return "tag=" + tag
	}), func() driver.Driver { return &pointDriver{} })

	sess := f.connect(t, `{"port":"COM9","tag":"collector"}`)
	_ = sess
	before := f.conns.Load()

	res := f.discoverOK(t, driversdk.DiscoverPointsParams{
		Protocol: pointConst, ProtocolJSON: `{"port":"COM9","tag":"collector"}`,
	})
	if got := res.Points[0].Label; got != "tag=collector" {
		t.Fatalf("finder 看到的实例标记 = %q，期望复用采集那个（collector）", got)
	}
	if f.conns.Load() != before {
		t.Errorf("复用路径不该新建驱动实例（工厂调用次数 %d → %d）", before, f.conns.Load())
	}
}

// TestDiscoverPointsSerialNeverOpensSecondHandle 串口协议复用不到时**绝不回落**
// 到临时实例。
//
// 这里用的是正面的自证法：先证明"临时实例连同一串口必然失败"（直接建一个撞端口），
// 再说扫描成功 —— 于是扫描唯一可能的路径就只剩复用。
func TestDiscoverPointsSerialNeverOpensSecondHandle(t *testing.T) {
	// 自证：端口已被占时，新建实例的 Connect 一定失败。
	probe := &pointDriver{}
	if err := probe.Connect(`{"port":"COM9"}`); err != nil {
		t.Fatalf("第一把打开 COM9 应成功: %v", err)
	}
	second := &pointDriver{}
	if err := second.Connect(`{"port":"COM9"}`); err == nil {
		t.Fatal("第二个句柄不该打开成功 —— 这条自证不成立的话下面的断言就没有意义")
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f := startPoint(t, pointMeta(true, 1000), echoFinder(func(req driversdk.FindPointsRequest) string {
		if err := req.Session.Do(func(driver.Driver) error { return nil }); err != nil {
			return "do-err:" + err.Error()
		}
		return "reused"
	}), func() driver.Driver { return &pointDriver{} })

	f.connect(t, `{"port":"COM9","tag":"collector"}`)

	res := f.discoverOK(t, driversdk.DiscoverPointsParams{
		Protocol: pointConst, ProtocolJSON: `{"port":"COM9","tag":"collector"}`,
	})
	if got := res.Points[0].Label; got != "reused" {
		t.Fatalf("扫描没有复用采集会话: %q —— 串口上那就只能是新开了句柄，"+
			"而新开的那个会报 Access is denied", got)
	}
}

// TestDiscoverPointsSerialForeignSessionIsRejected 串口上"有会话但不是本次要连的
// 那一条"必须**报错**，而不是回落到临时实例。
//
// 回落会打开另一个串口的第二个句柄，一次长扫描把它持几十秒：采集侧下一次重连
// 报 Access is denied 并退避 —— 用户看到的是"点一次扫描把设备打成离线"。
func TestDiscoverPointsSerialForeignSessionIsRejected(t *testing.T) {
	f := startPoint(t, pointMeta(true, 1000), echoFinder(func(driversdk.FindPointsRequest) string {
		t.Fatal("不该走到 finder —— 选会话阶段就该拒绝")
		return ""
	}), func() driver.Driver { return &pointDriver{} })

	// 会话占着 COM9（别的设备），本次要扫的是 COM7。
	f.connect(t, `{"port":"COM9","tag":"other"}`)

	resp := f.discover(driversdk.DiscoverPointsParams{
		Protocol: pointConst, ProtocolJSON: `{"port":"COM7","tag":"mine"}`,
	})
	if resp.Error == nil {
		t.Fatal("串口上复用不到会话时应报错")
	}
	if !strings.Contains(resp.Error.Message, "串口") {
		t.Errorf("错误文案应点明串口，实际 %q", resp.Error.Message)
	}
	// 而且**绝不能**因为回落而打开了 COM7。
	pointPortsMu.Lock()
	_, held := pointPorts["com7"]
	pointPortsMu.Unlock()
	if held {
		t.Error("被拒绝的扫描不该打开任何串口句柄")
	}
}

// TestDiscoverPointsSerialWithoutAnySessionUsesTempInstance 该协议一个会话都
// 没有时走临时实例 —— 那时端口按定义是空的，回落是安全的。
func TestDiscoverPointsSerialWithoutAnySessionUsesTempInstance(t *testing.T) {
	f := startPoint(t, pointMeta(true, 1000), echoFinder(func(req driversdk.FindPointsRequest) string {
		var tag string
		if err := req.Session.Do(func(drv driver.Driver) error {
			tag = tagOf(drv)
			return nil
		}); err != nil {
			return "do-err:" + err.Error()
		}
		return "tag=" + tag
	}), func() driver.Driver { return &pointDriver{} })

	res := f.discoverOK(t, driversdk.DiscoverPointsParams{
		Protocol: pointConst, ProtocolJSON: `{"port":"COM7","tag":"temp"}`,
	})
	if got := res.Points[0].Label; got != "tag=temp" {
		t.Fatalf("应走临时实例（tag=temp），实际 %q", got)
	}
	// 临时实例用完必须关掉：端口要还回去。
	pointPortsMu.Lock()
	_, held := pointPorts["com7"]
	pointPortsMu.Unlock()
	if held {
		t.Error("临时实例用完必须 Close（端口要还回去），否则采集侧再也连不上")
	}
}

// TestDiscoverPointsNonSerialFallsBackToTempInstance 非串口协议有会话但不命中时
// 走临时实例（第二个 socket 无害）。
func TestDiscoverPointsNonSerialFallsBackToTempInstance(t *testing.T) {
	f := startPoint(t, pointMeta(false, 1000), echoFinder(func(req driversdk.FindPointsRequest) string {
		var tag string
		_ = req.Session.Do(func(drv driver.Driver) error {
			tag = tagOf(drv)
			return nil
		})
		return "tag=" + tag
	}), func() driver.Driver { return &pointDriver{} })

	// 会话存在，但 pointDriver 的 MatchConnection 只在两边都带 port 时才命中；
	// 这里给会话一个不带 port 的配置，于是它永远不匹配。
	f.connect(t, `{"tag":"collector"}`)

	res := f.discoverOK(t, driversdk.DiscoverPointsParams{
		Protocol: pointConst, ProtocolJSON: `{"tag":"temp"}`,
	})
	if got := res.Points[0].Label; got != "tag=temp" {
		t.Fatalf("非串口协议应回落到临时实例（tag=temp），实际 %q", got)
	}
}

// TestDiscoverPointsBusySessionIsRejected 命中了会话但拿不到它的锁时，
// 串口协议必须报"设备正忙"而不是无限等下去。
//
// 无限等的后果是撞网关侧的 RPC 超时并累积插件卡死计数 —— 连续几次会把整个
// 插件进程连同它下面所有协议、所有设备一起重启。
//
// **本用例耗时约 5 秒**（pointScanSessionBudget）：这是那条等待上限本身，
// 它必须是个有限的实在值，快不了。
func TestDiscoverPointsBusySessionIsRejected(t *testing.T) {
	readEnt := make(chan struct{})
	readGate := make(chan struct{})

	f := startPoint(t, pointMeta(true, 1000), echoFinder(func(driversdk.FindPointsRequest) string {
		t.Fatal("拿不到会话锁时不该走到 finder")
		return ""
	}), func() driver.Driver {
		return &pointDriver{readEnt: readEnt, readGate: readGate}
	})

	sess := f.connect(t, `{"port":"COM9","tag":"collector"}`)

	// 起一次读并让它卡在驱动里 —— 会话锁因此被按住。
	// 用 pointSend 而不是 call：后者会在等应答的整段时间里抱着 c.mu，
	// 下面那次 discover 就永远发不出去（自死锁）。
	readID := uint64(101)
	f.pointSend(readID, pluginwire.MethodRead, driversdk.ReadParams{
		Session: sess,
		Addrs:   []driversdk.WireAddr{{ID: "a1", Name: "p", DataType: "int16"}},
	})
	select {
	case <-readEnt:
	case <-time.After(3 * time.Second):
		t.Fatal("读没有进入驱动")
	}

	start := time.Now()
	resp := f.discover(driversdk.DiscoverPointsParams{
		Protocol: pointConst, ProtocolJSON: `{"port":"COM9","tag":"collector"}`,
	})
	elapsed := time.Since(start)

	close(readGate) // 先放行那次读，免得下面的 t.Fatal 留下卡住的协程
	if resp.Error == nil {
		t.Fatal("拿不到会话锁时应报错")
	}
	if !strings.Contains(resp.Error.Message, "设备正忙") {
		t.Errorf("错误文案应说明设备正忙，实际 %q", resp.Error.Message)
	}
	if elapsed < time.Second {
		t.Errorf("等待了 %s 就放弃 —— 太少会让正常采集节奏下的扫描频繁假失败", elapsed)
	}
	if got := f.pointAwait(readID); got.Error != nil {
		t.Errorf("被卡住的那次读应正常返回: %s", got.Error.Message)
	}
}

// ==================== 块间让锁 ====================

// TestDiscoverPointsReleasesLockBetweenChunks 这是**整个会话复用设计成立的前提**：
// 一次 Do 覆盖整段扫描会把采集卡死几十秒，因此 finder 必须在块与块之间重新
// 调 Do，而每次 Do 进出都会放开锁。
//
// 构造方式：finder 在两次 Do 之间停下来等测试放行；测试趁这个空档发一次 read。
// 若锁被整段持有，那次 read 会一直等下去（这里以超时断言）。
func TestDiscoverPointsReleasesLockBetweenChunks(t *testing.T) {
	firstDone := make(chan struct{})
	allowSecond := make(chan struct{})

	finder := func(req driversdk.FindPointsRequest) (*driver.PointScanResult, error) {
		if err := req.Session.Do(func(driver.Driver) error { return nil }); err != nil {
			return nil, err
		}
		close(firstDone)
		<-allowSecond // 等测试在锁空闲时完成一次 read
		if err := req.Session.Do(func(driver.Driver) error { return nil }); err != nil {
			return nil, err
		}
		return &driver.PointScanResult{Scanned: 2, Readable: 2}, nil
	}

	f := startPoint(t, pointMeta(true, 1000), finder,
		func() driver.Driver { return &pointDriver{} })
	sess := f.connect(t, `{"port":"COM9","tag":"collector"}`)

	// 扫描在后台跑（它会停在 allowSecond 上）。
	discoverID := uint64(102)
	f.pointSend(discoverID, pluginwire.MethodDiscoverPoints, driversdk.DiscoverPointsParams{
		Protocol: pointConst, ProtocolJSON: `{"port":"COM9","tag":"collector"}`,
	})

	select {
	case <-firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("finder 没有执行第一次 Do")
	}

	// 锁此刻应当是空闲的：这次读必须能拿到它。
	// 若实现退化成"整段扫描持锁"，这次读会一直等到会话锁 —— 下面的超时即判据。
	readID := uint64(103)
	f.pointSend(readID, pluginwire.MethodRead, driversdk.ReadParams{
		Session: sess,
		Addrs:   []driversdk.WireAddr{{ID: "a1", Name: "p", DataType: "int16"}},
	})
	readDone := make(chan pluginwire.Frame, 1)
	go func() { readDone <- f.pointAwait(readID) }()
	select {
	case resp := <-readDone:
		if resp.Error != nil {
			close(allowSecond)
			t.Fatalf("块间的那次读失败: %s", resp.Error.Message)
		}
	case <-time.After(2 * time.Second):
		close(allowSecond)
		t.Fatal("块与块之间锁没有放开 —— 一次扫描会把采集卡死几十秒，" +
			"这正是不能用「一次 Do 覆盖整段」的原因")
	}

	close(allowSecond)

	// 取扫描的结果（它此时才会回，途中可能已被缓冲）。
	if resp := f.pointAwait(discoverID); resp.Error != nil {
		t.Fatalf("discoverPoints 失败: %s", resp.Error.Message)
	}
}

// ==================== 会话中途关闭 ====================

// TestDiscoverPointsClosedSessionReturnsPartial 会话在扫描途中被关闭时，
// 已经拿到的部分结果要带回去，并把"到此为止"如实报出来。
//
// 这与"设备掉线"是同一种处置：后半段没有结论，但前半段是真实的。
func TestDiscoverPointsClosedSessionReturnsPartial(t *testing.T) {
	firstDone := make(chan struct{})
	allowClose := make(chan struct{})

	finder := func(req driversdk.FindPointsRequest) (*driver.PointScanResult, error) {
		res := &driver.PointScanResult{
			Scanned: 1, Readable: 1,
			Points: []driver.DiscoveredPoint{{Name: "first"}},
		}
		if err := req.Session.Do(func(driver.Driver) error { return nil }); err != nil {
			return res, err
		}
		close(firstDone)
		<-allowClose // 等测试把会话关掉
		if err := req.Session.Do(func(driver.Driver) error { return nil }); err != nil {
			res.Incomplete = true
			return res, err
		}
		return res, nil
	}

	f := startPoint(t, pointMeta(true, 1000), finder,
		func() driver.Driver { return &pointDriver{} })
	sess := f.connect(t, `{"port":"COM9","tag":"collector"}`)

	discoverID := uint64(104)
	f.pointSend(discoverID, pluginwire.MethodDiscoverPoints, driversdk.DiscoverPointsParams{
		Protocol: pointConst, ProtocolJSON: `{"port":"COM9","tag":"collector"}`,
	})
	select {
	case <-firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("finder 没有执行第一次 Do")
	}

	// 关掉会话（它此时不持锁，能立刻拿到）。
	var closeOut driversdk.CloseReply
	f.c.callOK(pluginwire.MethodClose, driversdk.SessionParams{Session: sess}, &closeOut)
	close(allowClose)

	resp := f.pointAwait(discoverID)

	// 部分结果必须在应答里 —— 丢掉它们对用户没有任何好处。
	var res driver.PointScanResult
	if err := pluginwire.DecodeBody(resp.Codec, resp.Result, &res); err != nil {
		t.Fatalf("解码结果: %v", err)
	}
	if len(res.Points) != 1 || res.Points[0].Name != "first" {
		t.Fatalf("会话关闭后应回传已完成的部分结果，实际 %+v", res.Points)
	}
	if resp.Error == nil {
		t.Fatal("会话在途中被关闭，应当有一条错误说明")
	}
	if !strings.Contains(resp.Error.Message, "closed") {
		t.Errorf("错误文案应指明会话已关闭，实际 %q", resp.Error.Message)
	}
}

// ==================== panic 与锁 ====================

// TestDiscoverPointsFinderPanicReleasesLock finder panic 要降级成一次 RPC 错误，
// 且**会话锁必须被放开**。
//
// 锁泄漏的后果比 panic 本身严重得多：shutdown() 会永久阻塞在 sess.mu.Lock() 上，
// 会话表已清空、剩余会话与底层流都没关、进程停不下来。
// 这里用"panic 之后同一会话还能正常读"来证明锁确实放开了。
func TestDiscoverPointsFinderPanicReleasesLock(t *testing.T) {
	f := startPoint(t, pointMeta(true, 1000),
		func(req driversdk.FindPointsRequest) (*driver.PointScanResult, error) {
			return nil, req.Session.Do(func(driver.Driver) error {
				panic("finder 故意 panic")
			})
		}, func() driver.Driver { return &pointDriver{} })

	sess := f.connect(t, `{"port":"COM9","tag":"collector"}`)

	resp := f.discover(driversdk.DiscoverPointsParams{
		Protocol: pointConst, ProtocolJSON: `{"port":"COM9","tag":"collector"}`,
	})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "panic") {
		t.Fatalf("panic 应降级为一条 RPC 错误，实际 %+v", resp.Error)
	}

	// 锁若没放开，这一次读会永远等下去。
	readID := uint64(105)
	f.pointSend(readID, pluginwire.MethodRead, driversdk.ReadParams{
		Session: sess,
		Addrs:   []driversdk.WireAddr{{ID: "a1", Name: "p", DataType: "int16"}},
	})
	readDone := make(chan pluginwire.Frame, 1)
	go func() { readDone <- f.pointAwait(readID) }()
	select {
	case r := <-readDone:
		if r.Error != nil {
			t.Fatalf("panic 之后会话应仍可用: %s", r.Error.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("finder panic 后会话锁被泄漏了 —— shutdown 会永久阻塞在这把锁上")
	}
}

// ==================== 启动期校验 ====================

// TestServeRejectsPointDiscoveryWithoutFinder 声明了点位扫描能力却没给实现，
// 必须在**进程启动时**报错。
func TestServeRejectsPointDiscoveryWithoutFinder(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	err := driversdk.ServeConn(server, pointFactories(func() driver.Driver { return &pointDriver{} }),
		pointMeta(false, 1000), driversdk.Capabilities{})
	if err == nil {
		t.Fatal("声明了 PointDiscovery 却没有 finder，应启动失败")
	}
	if !strings.Contains(err.Error(), pointConst) {
		t.Errorf("错误应指明是哪个协议，实际 %v", err)
	}
}

// TestServeAcceptsNilPointFinders 不声明点扫能力的插件（零值 Capabilities）
// 必须能正常启动。
func TestServeAcceptsNilPointFinders(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- driversdk.ServeConn(server, testFactories(func() driver.Driver {
			return &stubDriver{}
		}), testMeta(), driversdk.Capabilities{})
	}()
	t.Cleanup(func() { _ = client.Close(); <-done })

	c := newRPCClient(t, client)
	if resp := c.call(pluginwire.MethodDescribe, struct{}{}); resp.Error != nil {
		t.Fatalf("describe 应成功: %v", resp.Error)
	}
}

// TestServeWarnsOnUnusedPointFinder 多余的 finder 只警告不拒绝（与工厂表对称）。
func TestServeWarnsOnUnusedPointFinder(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)
	finders := driversdk.PointFinders{
		"Not.Declared": func(driversdk.FindPointsRequest) (*driver.PointScanResult, error) {
			return nil, nil
		},
	}
	go func() {
		done <- driversdk.ServeConn(server, testFactories(func() driver.Driver {
			return &stubDriver{}
		}), testMeta(), driversdk.Capabilities{PointFinders: finders})
	}()
	t.Cleanup(func() { _ = client.Close(); <-done })

	c := newRPCClient(t, client)
	if resp := c.call(pluginwire.MethodDescribe, struct{}{}); resp.Error != nil {
		t.Fatalf("多余的 finder 不该导致启动失败: %v", resp.Error)
	}
}

// ==================== 线协议 ====================

// TestDiscoverPointsParamsStayJSON 钉住 discoverPoints 的载荷编码：必须走 JSON。
//
// pluginwire 按类型是否实现 BinaryEncoder 自动选编码，而"意外实现了"的症状是
// 两侧都按另一套编解码却一个错都不报。这里显式断言，改了就会立刻红。
func TestDiscoverPointsParamsStayJSON(t *testing.T) {
	for name, v := range map[string]any{
		"DiscoverPointsParams": driversdk.DiscoverPointsParams{},
		"PointScanResult":      driver.PointScanResult{},
		"DiscoveredPoint":      driver.DiscoveredPoint{},
	} {
		_, codec, err := pluginwire.EncodeBody(v)
		if err != nil {
			t.Fatalf("%s 编码失败: %v", name, err)
		}
		if codec != pluginwire.CodecJSON {
			t.Errorf("%s 的编码是 %d，期望 JSON(%d)", name, codec, pluginwire.CodecJSON)
		}
	}
}

// TestDiscoverPointsEmptyOptionsMarshal 空选项必须能正常编码。
//
// Options 若声明成 json.RawMessage，空值会让 json.Marshal 校验失败，
// 报出来是一句指向"编码失败"的错，与真正的根因（用户没填扫描选项）相隔十万八千里。
func TestDiscoverPointsEmptyOptionsMarshal(t *testing.T) {
	body, _, err := pluginwire.EncodeBody(driversdk.DiscoverPointsParams{
		Protocol: pointConst, ProtocolJSON: `{"host":"10.0.0.9"}`,
	})
	if err != nil {
		t.Fatalf("空选项不该导致编码失败: %v", err)
	}
	var back driversdk.DiscoverPointsParams
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("回解失败: %v", err)
	}
	if back.Options != "" || back.ProtocolJSON != `{"host":"10.0.0.9"}` {
		t.Errorf("回解结果 = %+v", back)
	}
}

// TestDiscoveredPointCommonDataTypeCrossesWire 逐点数据类型必须无损过线。
//
// 这是原生枚举协议（Rockwell.CIP）唯一能把"每个标签自己的类型"交给前端的方式：
// 结果级的 AssumedCommonDataType 只有一个值，表达不了 DINT 与 REAL 混在一起。
// 字段丢了不会报任何错 —— 症状是前端把每一行都初始化成同一个类型，
// 用户以为扫出来的类型是对的，而建出的点位采出来的值是错的。
//
// 同时钉住它的**可选语义**：空值必须被省略（omitempty），否则老协议的应答里会
// 多出一串空字符串，而前端拿到的"空"要与"没有这个字段"是同一种含义。
func TestDiscoveredPointCommonDataTypeCrossesWire(t *testing.T) {
	in := driver.PointScanResult{
		Points: []driver.DiscoveredPoint{
			{Name: "MotorSpeed", CommonDataType: driver.TypeLong},
			{Name: "%MW100"}, // 读一段地址的协议：留空
		},
	}

	body, codec, err := pluginwire.EncodeBody(in)
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	if codec != pluginwire.CodecJSON {
		t.Fatalf("编码是 %d，期望 JSON —— 点扫的载荷必须走 JSON", codec)
	}

	var out driver.PointScanResult
	if err := pluginwire.DecodeBody(codec, body, &out); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if len(out.Points) != 2 {
		t.Fatalf("点位 %d 条，期望 2", len(out.Points))
	}
	if out.Points[0].CommonDataType != driver.TypeLong {
		t.Errorf("逐点类型 = %q，期望 %q —— 它在过线时丢了",
			out.Points[0].CommonDataType, driver.TypeLong)
	}
	if out.Points[1].CommonDataType != "" {
		t.Errorf("空类型回解成了 %q，期望空串", out.Points[1].CommonDataType)
	}

	// 空值必须**不出现在 JSON 里**（omitempty），这样"没这个字段"与"空"才是一回事。
	if raw := string(body); strings.Contains(raw, `"commonDataType":""`) {
		t.Errorf("空类型被编码成了空串字段 —— 它与「没有这个字段」在别处不是一回事：%s", raw)
	}
	if !strings.Contains(string(body), `"commonDataType":"Long"`) {
		t.Errorf("非空类型没有编码进去：%s", body)
	}
}
