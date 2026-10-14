// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package discovery

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"iot-gateway/configFile"
	"iot-gateway/model/vo"
)

// fakeIdentity 可注入的假身份源（对应 alarm 里的 fakeStatusSource）。
type fakeIdentity struct {
	mu  sync.Mutex
	gw  *vo.GatewayVO
	err error
}

func (f *fakeIdentity) GetGateway(context.Context) (*vo.GatewayVO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gw, f.err
}

func (f *fakeIdentity) set(gw *vo.GatewayVO, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gw, f.err = gw, err
}

// testApiKey 测试用的默认密钥值，长度与真实密钥一致（32 字节 hex）。
const testApiKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeApiKey 可注入的假默认密钥源。
type fakeApiKey struct {
	mu  sync.Mutex
	key string
	err error
}

func (f *fakeApiKey) DefaultOpenApiKey(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.key, f.err
}

func (f *fakeApiKey) set(key string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key, f.err = key, err
}

// testRig 一套跑在回环上的广播器测试装置。
type testRig struct {
	ann    *Announcer
	rcv    *net.UDPConn // 模拟管理平台的被动接收端，收 notify
	target *net.UDPAddr // 广播目标 == rcv 的地址

	keys *fakeApiKey // 默认密钥源，测试可直接改它的值

	listenConn net.PacketConn // 广播器自己的套接字，Start 之后才有
}

// newTestRig 装配一套指向回环的广播器。
//
// 关键约束：**绝不能真往局域网广播** —— 否则在开发机上跑一次测试就会往所在
// 网段扔包。所以两个测试缝都指向回环：targets 指向测试自己开的接收端，
// listen 绑回环临时端口。绑临时端口还顺带避免了与正在运行的网关抢 9083，
// 以及测试之间互相干扰。
func newTestRig(t *testing.T, src IdentitySource) *testRig {
	t.Helper()

	rcv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("监听回环接收端: %v", err)
	}
	t.Cleanup(func() { _ = rcv.Close() })
	rcvAddr := rcv.LocalAddr().(*net.UDPAddr)

	cfg := &configFile.Config{}
	cfg.Discovery.Enabled = true
	cfg.Discovery.IntervalMs = 20
	cfg.Server.Port = 9082

	rig := &testRig{target: rcvAddr, rcv: rcv, keys: &fakeApiKey{key: testApiKey}}
	rig.ann = NewAnnouncer(cfg, src, rig.keys)
	rig.ann.listen = func(_, _ string) (net.PacketConn, error) {
		c, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		rig.listenConn = c
		return c, nil
	}
	rig.ann.targets = func(int) []target {
		return []target{{iface: "lo", ip: net.IPv4(127, 0, 0, 1), bcast: rcvAddr}}
	}
	return rig
}

func (r *testRig) start(t *testing.T) {
	t.Helper()
	if err := r.ann.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(r.ann.Stop)
}

// probeAddr 广播器实际监听的地址（模拟管理平台该往哪儿发探询）。
func (r *testRig) probeAddr(t *testing.T) *net.UDPAddr {
	t.Helper()
	if r.listenConn == nil {
		t.Fatal("Start 尚未调用，还没有监听地址")
	}
	return r.listenConn.LocalAddr().(*net.UDPAddr)
}

// recvNotify 收一条 notify，超时返回 false。
func (r *testRig) recvNotify(t *testing.T, timeout time.Duration) (Message, bool) {
	t.Helper()
	_ = r.rcv.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, maxDatagram)
	n, _, err := r.rcv.ReadFromUDP(buf)
	if err != nil {
		return Message{}, false
	}
	return decode(buf[:n])
}

func newProbeClient(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("监听回环探询端: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func probeBytes(t *testing.T) []byte {
	t.Helper()
	b, err := encode(Message{Protocol: Protocol, Version: ProtocolVersion, Type: TypeProbe})
	if err != nil {
		t.Fatalf("encode probe: %v", err)
	}
	return b
}

func sendProbe(t *testing.T, c *net.UDPConn, to *net.UDPAddr) {
	t.Helper()
	if _, err := c.WriteToUDP(probeBytes(t), to); err != nil {
		t.Fatalf("发送探询: %v", err)
	}
}

func recvReply(t *testing.T, c *net.UDPConn, timeout time.Duration) (Message, bool) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, maxDatagram)
	n, _, err := c.ReadFromUDP(buf)
	if err != nil {
		return Message{}, false
	}
	return decode(buf[:n])
}

func TestAnnouncerSendsNotify(t *testing.T) {
	src := &fakeIdentity{gw: &vo.GatewayVO{
		ID: "gw-1", Name: "车间网关-01",
		// 库里那一列是首启写死的陈旧值；要发布的必须是当前运行值。
		Port:            1234,
		FirmwareVersion: "1.0.0",
		Description:     "1 号车间",
	}}
	rig := newTestRig(t, src)
	rig.start(t)

	m, ok := rig.recvNotify(t, 2*time.Second)
	if !ok {
		t.Fatal("未收到 notify")
	}
	if m.Type != TypeNotify {
		t.Errorf("type = %q, 期望 %q", m.Type, TypeNotify)
	}
	if m.Protocol != Protocol || m.Version != ProtocolVersion {
		t.Errorf("协议头 = %q/%d", m.Protocol, m.Version)
	}
	if m.GatewayID != "gw-1" || m.Name != "车间网关-01" || m.Description != "1 号车间" {
		t.Errorf("身份字段不符: %+v", m)
	}
	if m.FirmwareVersion != "1.0.0" {
		t.Errorf("firmwareVersion = %q", m.FirmwareVersion)
	}
	// 端口必须是 cfg.Server.Port（当前运行值 9082），而不是 gw.Port（陈旧的 1234）。
	if m.Port != 9082 {
		t.Errorf("port = %d, 期望 9082（当前运行值优先于库里那列）", m.Port)
	}
	// ip 填的是本次广播所用网卡的地址。
	if m.IP != "127.0.0.1" {
		t.Errorf("ip = %q, 期望 127.0.0.1", m.IP)
	}
	if m.Timestamp == 0 {
		t.Error("timestamp 未填写")
	}
}

// TestAnnouncerBroadcastsDefaultApiKey 守住"默认密钥随广播一起发布"。
//
// notify 与 reply 是两条各自构造 Message 的路径（announce / reply），
// 漏掉任何一条都表现为"管理平台偶尔拿不到密钥"，所以两条都要断言。
func TestAnnouncerBroadcastsDefaultApiKey(t *testing.T) {
	src := &fakeIdentity{gw: &vo.GatewayVO{ID: "gw-1", Name: "车间网关-01"}}
	rig := newTestRig(t, src)
	rig.start(t)

	if m, ok := rig.recvNotify(t, 2*time.Second); !ok || m.ApiKey != testApiKey {
		t.Fatalf("notify 的 apiKey = %q (ok=%v), 期望 %q", m.ApiKey, ok, testApiKey)
	}

	client := newProbeClient(t)
	sendProbe(t, client, rig.probeAddr(t))
	if m, ok := recvReply(t, client, 2*time.Second); !ok || m.ApiKey != testApiKey {
		t.Fatalf("reply 的 apiKey = %q (ok=%v), 期望 %q", m.ApiKey, ok, testApiKey)
	}
}

// TestAnnouncerOmitsApiKeyWhenAbsent 默认密钥缺失（老库未升级、被直接改库）时
// 广播照常进行，只是不带 apiKey —— 不能让整个发现流程跟着一起失效。
func TestAnnouncerOmitsApiKeyWhenAbsent(t *testing.T) {
	src := &fakeIdentity{gw: &vo.GatewayVO{ID: "gw-1", Name: "车间网关-01"}}
	rig := newTestRig(t, src)
	rig.keys.set("", nil)
	rig.start(t)

	m, ok := rig.recvNotify(t, 2*time.Second)
	if !ok {
		t.Fatal("未收到 notify")
	}
	if m.ApiKey != "" {
		t.Errorf("无默认密钥时 apiKey = %q, 期望空", m.ApiKey)
	}
	if m.GatewayID != "gw-1" {
		t.Errorf("密钥缺失不应影响身份字段: %+v", m)
	}
}

// TestAnnouncerPicksUpRenamedIdentity 守住"每轮重读身份"这条设计：
// 在系统设置页改完名称，下一个广播周期就该带上新值，不需要重启。
func TestAnnouncerPicksUpRenamedIdentity(t *testing.T) {
	src := &fakeIdentity{gw: &vo.GatewayVO{ID: "gw-1", Name: "旧名称"}}
	rig := newTestRig(t, src)
	rig.start(t)

	if m, ok := rig.recvNotify(t, 2*time.Second); !ok || m.Name != "旧名称" {
		t.Fatalf("首个 notify 的 name = %q (ok=%v)", m.Name, ok)
	}

	src.set(&vo.GatewayVO{ID: "gw-1", Name: "新名称"}, nil)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m, ok := rig.recvNotify(t, time.Second); ok && m.Name == "新名称" {
			return // 改名已经在广播里生效
		}
	}
	t.Fatal("改名后两个周期内仍未广播新名称")
}

func TestAnnouncerRepliesToProbe(t *testing.T) {
	src := &fakeIdentity{gw: &vo.GatewayVO{ID: "gw-1", Name: "车间网关-01", Port: 9082}}
	rig := newTestRig(t, src)
	rig.start(t)

	client := newProbeClient(t)
	sendProbe(t, client, rig.probeAddr(t))

	m, ok := recvReply(t, client, 2*time.Second)
	if !ok {
		t.Fatal("未收到 reply")
	}
	if m.Type != TypeReply {
		t.Errorf("type = %q, 期望 %q", m.Type, TypeReply)
	}
	if m.GatewayID != "gw-1" || m.Name != "车间网关-01" {
		t.Errorf("身份字段不符: %+v", m)
	}
	// 回包里的 ip 应当是路由选出来的本机地址（回环场景下就是 127.0.0.1）。
	if m.IP != "127.0.0.1" {
		t.Errorf("ip = %q, 期望 127.0.0.1", m.IP)
	}
}

// TestAnnouncerIgnoresForeignDatagrams 守住读循环里"不认识的报文一律丢弃、
// 不能让连接垮掉"这条：畸形 JSON、别的协议、未来版本、以及别的网关的 notify
// 都不能影响后续探询的应答。
func TestAnnouncerIgnoresForeignDatagrams(t *testing.T) {
	rig := newTestRig(t, &fakeIdentity{gw: &vo.GatewayVO{ID: "gw-1", Name: "车间网关-01"}})
	rig.start(t)

	client := newProbeClient(t)
	addr := rig.probeAddr(t)

	garbage := [][]byte{
		[]byte("这不是 JSON"),
		[]byte(`{"protocol":"别的协议","version":1,"type":"probe"}`),
		[]byte(`{"protocol":"iot-gateway-discovery","version":99,"type":"probe"}`),
		[]byte(`{"protocol":"iot-gateway-discovery","version":1,"type":"notify"}`), // 别的网关的广播
		[]byte(`{"protocol":"iot-gateway-discovery","version":1}`),                 // 无 type
		[]byte(``),
	}
	for _, b := range garbage {
		if _, err := client.WriteToUDP(b, addr); err != nil {
			t.Fatalf("发送垃圾报文: %v", err)
		}
	}

	// 这些都不该换来回复。
	if m, ok := recvReply(t, client, 300*time.Millisecond); ok {
		t.Fatalf("对垃圾报文作出了应答: %+v", m)
	}

	// 而随后的合法探询必须照常被应答 —— 这就是"读循环还活着"的证据。
	sendProbe(t, client, addr)
	if m, ok := recvReply(t, client, 2*time.Second); !ok || m.Type != TypeReply {
		t.Fatalf("垃圾报文之后探询未被应答: %+v (ok=%v)", m, ok)
	}
}

// TestAnnouncerSurvivesMissingIdentity 身份行缺失时不应答，但读循环必须活着：
// 行一旦出现（或播种补上），下一次探询就得能收到回复。
func TestAnnouncerSurvivesMissingIdentity(t *testing.T) {
	// 用普通 error 即可：代码路径对任何 error 的处置都一样（跳过本轮）。
	src := &fakeIdentity{err: errors.New("网关身份不存在")}
	rig := newTestRig(t, src)
	rig.start(t)

	client := newProbeClient(t)
	addr := rig.probeAddr(t)

	sendProbe(t, client, addr)
	if m, ok := recvReply(t, client, 300*time.Millisecond); ok {
		t.Fatalf("身份行缺失时不应有应答: %+v", m)
	}

	// 注意这里刻意不等限流窗口过期：未应答的探询本就不该占用限流额度，
	// 否则读循环即便活着，用户看到的也是"探询时灵时不灵"。
	src.set(&vo.GatewayVO{ID: "gw-1", Name: "车间网关-01"}, nil)

	sendProbe(t, client, addr)
	if m, ok := recvReply(t, client, 2*time.Second); !ok || m.Type != TypeReply {
		t.Fatalf("身份行恢复后探询仍未被应答: %+v (ok=%v)", m, ok)
	}
}

func TestAnnouncerStopBeforeStartIsSafe(t *testing.T) {
	rig := newTestRig(t, &fakeIdentity{gw: &vo.GatewayVO{ID: "gw-1"}})
	// 未 Start 就 Stop：必须立即返回，不能阻塞在 <-done 上。
	done := make(chan struct{})
	go func() { rig.ann.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("未启动时 Stop 阻塞了")
	}
}

func TestAnnouncerStartIsIdempotent(t *testing.T) {
	rig := newTestRig(t, &fakeIdentity{gw: &vo.GatewayVO{ID: "gw-1"}})
	rig.start(t)

	first := rig.listenConn
	if err := rig.ann.Start(); err != nil {
		t.Fatalf("重复 Start 返回错误: %v", err)
	}
	if rig.listenConn != first {
		t.Error("重复 Start 重新绑定了套接字")
	}
}

// TestAnnouncerStopIsIdempotent 守住 channel.go 那条 Stop 语义：可重复调用。
func TestAnnouncerStopIsIdempotent(t *testing.T) {
	rig := newTestRig(t, &fakeIdentity{gw: &vo.GatewayVO{ID: "gw-1"}})
	rig.start(t)

	done := make(chan struct{})
	go func() {
		rig.ann.Stop()
		rig.ann.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("重复 Stop 阻塞了")
	}
}

// TestAnnouncerStartBindFailure 覆盖 main.go 上真实存在的那条路径：
// Start 失败只记日志、随后仍会执行 defer Stop()。这要求 Start 失败时
// stopCh 保持 nil，从而 Stop 是无操作而不是死等一个永远不存在的协程。
func TestAnnouncerStartBindFailure(t *testing.T) {
	rig := newTestRig(t, &fakeIdentity{gw: &vo.GatewayVO{ID: "gw-1"}})
	rig.ann.listen = func(_, _ string) (net.PacketConn, error) {
		return nil, errors.New("bind: address already in use")
	}

	if err := rig.ann.Start(); err == nil {
		t.Fatal("绑定失败时 Start 应当返回错误")
	}

	done := make(chan struct{})
	go func() { rig.ann.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start 失败后 Stop 阻塞了")
	}
}

// TestAnnouncerDisabledIsSilent 关掉开关应当彻底静默：不绑端口、不起协程。
func TestAnnouncerDisabledIsSilent(t *testing.T) {
	rig := newTestRig(t, &fakeIdentity{gw: &vo.GatewayVO{ID: "gw-1"}})
	rig.ann.cfg.Discovery.Enabled = false

	if err := rig.ann.Start(); err != nil {
		t.Fatalf("关闭状态下 Start 返回错误: %v", err)
	}
	if rig.listenConn != nil {
		t.Error("关闭状态下不应绑定任何套接字")
	}
	if rig.ann.stopCh != nil {
		t.Error("关闭状态下不应创建 stopCh")
	}
	rig.ann.Stop() // 必须是安全的无操作
}

// TestProbeAllowedRateLimit 直接测限流器 —— 比靠真发三个包去猜时序稳定得多。
func TestProbeAllowedRateLimit(t *testing.T) {
	a := NewAnnouncer(&configFile.Config{}, &fakeIdentity{}, &fakeApiKey{})

	if !a.probeAllowed("192.168.1.9") {
		t.Fatal("首个探询应被放行")
	}
	a.recordProbe("192.168.1.9")

	if a.probeAllowed("192.168.1.9") {
		t.Fatal("同源一秒内的第二个探询应被拒")
	}
	if !a.probeAllowed("192.168.1.10") {
		t.Fatal("不同源应被放行")
	}

	// 窗口过后恢复放行。
	a.lastProbe["192.168.1.9"] = time.Now().Add(-probeReplyWindow - time.Millisecond)
	if !a.probeAllowed("192.168.1.9") {
		t.Fatal("窗口过后应放行")
	}
}

// TestProbeAllowedIsBounded 守住内存上界：源地址无认证即可伪造，
// 这张表必须既会清理过期条目、又不会无限增长。
func TestProbeAllowedIsBounded(t *testing.T) {
	a := NewAnnouncer(&configFile.Config{}, &fakeIdentity{}, &fakeApiKey{})

	// 全部过期：下一次判断应当顺手清掉，而不是让表一直攥着。
	for i := 0; i < probeWindowMax; i++ {
		a.lastProbe[strconv.Itoa(i)] = time.Now().Add(-time.Hour)
	}
	if !a.probeAllowed("新的源") {
		t.Fatal("过期条目应被清理后放行")
	}
	if len(a.lastProbe) >= probeWindowMax {
		t.Fatalf("限流表未被清理，仍有 %d 条", len(a.lastProbe))
	}

	// 全部未过期且已满：应当拒绝新条目，而不是无界增长。
	a.lastProbe = make(map[string]time.Time, probeWindowMax+1)
	for i := 0; i < probeWindowMax; i++ {
		a.lastProbe[strconv.Itoa(i)] = time.Now()
	}
	if a.probeAllowed("新的源") {
		t.Fatal("表满且无过期条目时应拒绝新条目")
	}
	if len(a.lastProbe) > probeWindowMax {
		t.Fatalf("限流表越界: %d 条", len(a.lastProbe))
	}
}

func TestAnnouncePortFallsBackToIdentity(t *testing.T) {
	gw := &vo.GatewayVO{Port: 1234}

	// 没有 server 段（负载测试 harness 那种手工构造的 Config）时回退到身份行。
	a := NewAnnouncer(&configFile.Config{}, &fakeIdentity{}, &fakeApiKey{})
	if got := a.announcePort(gw); got != 1234 {
		t.Errorf("无 server 段时 announcePort = %d, 期望 1234", got)
	}

	// 配了就以当前运行值为准。
	cfg := &configFile.Config{}
	cfg.Server.Port = 9082
	a = NewAnnouncer(cfg, &fakeIdentity{}, &fakeApiKey{})
	if got := a.announcePort(gw); got != 9082 {
		t.Errorf("有 server 段时 announcePort = %d, 期望 9082", got)
	}
}
