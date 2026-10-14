// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driversdk_test

import (
	"encoding/json"
	"fmt"
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

// ==================== 假「独占串口」 ====================
//
// 真串口 e2e 在本仓库做不了：Linux CI 上造虚拟串口对需要 socat，Windows 上需要
// com0com，都是外部依赖。这里把要防的那个 OS 行为本身抽象出来 —— 一个进程内的
// 端口表，第二个打开者拿到 Access is denied。它就是 Windows 下 goburrow/serial
// 以共享模式 0 调 CreateFile 的忠实模型，且在任意平台都跑得起来。
type serialPortRegistry struct {
	mu    sync.Mutex
	owner map[string]string // 小写端口名 → 持有者描述
	opens atomic.Int64      // 成功打开次数（"复用"的核心断言量）
}

func newSerialPortRegistry() *serialPortRegistry {
	return &serialPortRegistry{owner: make(map[string]string)}
}

func (r *serialPortRegistry) open(port, owner string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if who, ok := r.owner[strings.ToLower(port)]; ok {
		// 措辞对齐 Windows 的真实报错，好让"未复用"的失败一眼可辨。
		return fmt.Errorf("Access is denied: port %s held by %s", port, who)
	}
	r.owner[strings.ToLower(port)] = owner
	r.opens.Add(1)
	return nil
}

// close 只释放仍属于 owner 的那次打开：否则一个并不持有端口的实例误调 close
// 会把真正持有者的登记抹掉，测试就再也测不出"占用"了。
func (r *serialPortRegistry) close(port, owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.ToLower(port)
	if r.owner[key] == owner {
		delete(r.owner, key)
	}
}

// serialCfg 测试驱动认的 protocolJSON 形状。
type serialCfg struct {
	Port string `json:"port"`
	Baud int    `json:"baud"`
}

func parseSerialCfg(protocolJSON string) (serialCfg, error) {
	var c serialCfg
	if err := json.Unmarshal([]byte(protocolJSON), &c); err != nil {
		return c, err
	}
	if c.Port == "" {
		return c, fmt.Errorf("port is required")
	}
	return c, nil
}

// serialDriver 模拟串口驱动：Connect 打开端口，Ping 尽量复用已打开的句柄。
//
// 它实现 driver.ConnectionMatcher，因此 driversdk 的 ping 路径会把它选为候选。
type serialDriver struct {
	reg  *serialPortRegistry
	port string
	baud int

	mu     sync.Mutex
	opened bool

	pings   atomic.Int64
	closes  atomic.Int64
	pingErr error
	// readGate 非 nil 时 Read 阻塞到它关闭，用于构造"会话正忙"的场景。
	readGate chan struct{}
}

func (d *serialDriver) Connect(protocolJSON string) error {
	cfg, err := parseSerialCfg(protocolJSON)
	if err != nil {
		return err
	}
	if err := d.reg.open(cfg.Port, "serialDriver"); err != nil {
		return err
	}
	d.mu.Lock()
	d.port, d.baud, d.opened = cfg.Port, cfg.Baud, true
	d.mu.Unlock()
	return nil
}

// MatchConnection 复刻真实驱动的判据：端口 + 波特率逐项一致且句柄在手里。
func (d *serialDriver) MatchConnection(protocolJSON string) bool {
	cfg, err := parseSerialCfg(protocolJSON)
	if err != nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.opened && strings.EqualFold(d.port, cfg.Port) && d.baud == cfg.Baud
}

func (d *serialDriver) Ping(protocolJSON string) error {
	cfg, err := parseSerialCfg(protocolJSON)
	if err != nil {
		return err
	}
	d.pings.Add(1)
	d.mu.Lock()
	reuse := d.opened && strings.EqualFold(d.port, cfg.Port) && d.baud == cfg.Baud
	d.mu.Unlock()
	if !reuse {
		// 没得复用就新开一个句柄 —— 端口被占时这里就是 Access is denied。
		if err := d.reg.open(cfg.Port, "serialDriver-ping"); err != nil {
			return err
		}
		d.reg.close(cfg.Port, "serialDriver-ping")
	}
	return d.pingErr
}

func (d *serialDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	if d.readGate != nil {
		<-d.readGate
	}
	out := make([]driver.ReadResult, len(addrs))
	for i, a := range addrs {
		out[i] = driver.ReadResult{DeviceAddressID: a.ID, Value: "v", DataType: a.DataType}
	}
	return out, nil
}

func (d *serialDriver) IsConnected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.opened
}

func (d *serialDriver) Close() error {
	d.closes.Add(1)
	d.mu.Lock()
	port, opened := d.port, d.opened
	d.opened = false
	d.mu.Unlock()
	if opened {
		d.reg.close(port, "serialDriver")
	}
	return nil
}

// noMatchSerialDriver 是**反例**：同样模拟串口独占，但不实现
// driver.ConnectionMatcher。于是 ping 无从复用，必然新开句柄 → Access is denied。
//
// 它的存在是为了证明下面的用例不是永真 —— 没有它，"ping 成功了"也可能只是
// 因为假注册表根本没在拦第二次打开。
type noMatchSerialDriver struct {
	reg *serialPortRegistry
}

func (d *noMatchSerialDriver) Connect(protocolJSON string) error {
	cfg, err := parseSerialCfg(protocolJSON)
	if err != nil {
		return err
	}
	return d.reg.open(cfg.Port, "noMatch")
}

func (d *noMatchSerialDriver) Ping(protocolJSON string) error {
	cfg, err := parseSerialCfg(protocolJSON)
	if err != nil {
		return err
	}
	if err := d.reg.open(cfg.Port, "noMatch-ping"); err != nil {
		return err
	}
	d.reg.close(cfg.Port, "noMatch-ping")
	return nil
}

func (d *noMatchSerialDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	return make([]driver.ReadResult, len(addrs)), nil
}

func (d *noMatchSerialDriver) IsConnected() bool { return true }
func (d *noMatchSerialDriver) Close() error      { return nil }

// panicPingDriver 在被复用时 panic，模拟插件作者的确定性缺陷。
type panicPingDriver struct {
	serialDriver
}

func (d *panicPingDriver) Ping(string) error {
	panic("panicPingDriver 故意 panic")
}

// ==================== 夹具 ====================

// serialFactory 复刻真实插件工厂的形态：**每次调用返回一个新实例**。
// 这一点很关键 —— 临时实例与采集会话必须是不同对象，否则"复用没有发生"
// 这类断言会被同一个实例的计数器糊弄过去。
type serialFactory struct {
	mu        sync.Mutex
	reg       *serialPortRegistry
	instances []*serialDriver
	newDriver func(*serialPortRegistry) *serialDriver
}

func newSerialFactory(reg *serialPortRegistry) *serialFactory {
	return &serialFactory{
		reg:       reg,
		newDriver: func(r *serialPortRegistry) *serialDriver { return &serialDriver{reg: r} },
	}
}

func (f *serialFactory) mk() driver.Driver {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.newDriver(f.reg)
	f.instances = append(f.instances, d)
	return d
}

// connected 返回第一个实例 —— 也就是 Connect 落到的那一个。
func (f *serialFactory) connected(t *testing.T) *serialDriver {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.instances) == 0 {
		t.Fatal("工厂从未被调用")
	}
	return f.instances[0]
}

// connectSerial 建立一次串口会话，返回会话 id。
func connectSerial(t *testing.T, c *rpcClient, protocolJSON string) uint64 {
	t.Helper()
	var cr driversdk.ConnectResult
	c.callOK(pluginwire.MethodConnect,
		driversdk.ConnectParams{Protocol: testProtocol, ProtocolJSON: protocolJSON}, &cr)
	if cr.Session == 0 {
		t.Fatal("connect 应返回非 0 会话 id")
	}
	return cr.Session
}

// ping 发起一次「测试连接」。
func ping(c *rpcClient, protocolJSON string) pluginwire.Frame {
	return c.call(pluginwire.MethodPing,
		driversdk.PingParams{Protocol: testProtocol, ProtocolJSON: protocolJSON})
}

// sessionExists 探测某个会话 id 是否存在（read 未知会话会报错）。
func sessionExists(c *rpcClient, id uint64) bool {
	return c.call(pluginwire.MethodRead, driversdk.ReadParams{Session: id}).Error == nil
}

// ==================== 用例 ====================

// TestPingReusesLiveSession 是本次改动的验收点：
// 设备正在采集（端口已被本插件的会话打开）时，「测试连接」必须复用那个句柄，
// 而不是新开一个必然失败的。
func TestPingReusesLiveSession(t *testing.T) {
	t.Run("实现 ConnectionMatcher：复用，端口只被打开一次", func(t *testing.T) {
		reg := newSerialPortRegistry()
		f := newSerialFactory(reg)
		c := newRPCClient(t, startServer(t, f.mk, testMeta()))

		connectSerial(t, c, `{"port":"COM1","baud":9600}`)

		if fr := ping(c, `{"port":"COM1","baud":9600}`); fr.Error != nil {
			t.Fatalf("采集中的端口上 ping 应复用成功，实得错误: %s", fr.Error.Message)
		}
		if got := reg.opens.Load(); got != 1 {
			t.Errorf("端口被打开 %d 次，want 1（复用不应再开句柄）", got)
		}
		if got := f.connected(t).pings.Load(); got != 1 {
			t.Errorf("采集会话收到 %d 次 Ping，want 1", got)
		}
	})

	t.Run("反例：不实现 ConnectionMatcher 时同一场景必须失败", func(t *testing.T) {
		reg := newSerialPortRegistry()
		c := newRPCClient(t, startServer(t, func() driver.Driver {
			return &noMatchSerialDriver{reg: reg}
		}, testMeta()))

		connectSerial(t, c, `{"port":"COM1","baud":9600}`)

		fr := ping(c, `{"port":"COM1","baud":9600}`)
		if fr.Error == nil {
			t.Fatal("不实现 ConnectionMatcher 的驱动无从复用，ping 必须失败（否则本用例永真）")
		}
		if !strings.Contains(fr.Error.Message, "Access is denied") {
			t.Errorf("错误应来自端口独占，实得: %s", fr.Error.Message)
		}
	})
}

// TestPingReuseDoesNotCloseSession 复用路径**绝不能** Close 掉被复用的会话 ——
// 那会把正在采集的设备连接掐掉。临时实例路径要 Close，复用路径一次都不行。
func TestPingReuseDoesNotCloseSession(t *testing.T) {
	reg := newSerialPortRegistry()
	f := newSerialFactory(reg)
	c := newRPCClient(t, startServer(t, f.mk, testMeta()))

	sess := connectSerial(t, c, `{"port":"COM1","baud":9600}`)
	stub := f.connected(t)

	if fr := ping(c, `{"port":"COM1","baud":9600}`); fr.Error != nil {
		t.Fatalf("ping 应成功: %s", fr.Error.Message)
	}
	if got := stub.closes.Load(); got != 0 {
		t.Fatalf("复用路径调用了 %d 次 Close —— 会话会被掐断", got)
	}

	// 会话必须仍然可用：紧跟一次 Read 应拿到结果。
	var rr driversdk.ReadReply
	c.callOK(pluginwire.MethodRead, driversdk.ReadParams{
		Session: sess,
		Addrs:   []driversdk.WireAddr{{ID: "a1", Name: "p1", DataType: "int16"}},
	}, &rr)
	if len(rr.Results) != 1 {
		t.Fatalf("复用后的会话读不到数据（长度 %d）", len(rr.Results))
	}
}

// TestPingRefusesReuseOnDifferentConfig 参数不一致时**不得**复用。
//
// 这是"假成功"的防线：复用一条 9600 的连接去测 115200 的配置，
// 会给出一个与被测配置无关的绿色结论。拒绝复用后落到临时实例，
// 端口被占时如实报错 —— 用户至少知道"这条配置没验成"。
func TestPingRefusesReuseOnDifferentConfig(t *testing.T) {
	reg := newSerialPortRegistry()
	f := newSerialFactory(reg)
	c := newRPCClient(t, startServer(t, f.mk, testMeta()))

	connectSerial(t, c, `{"port":"COM1","baud":9600}`)

	if fr := ping(c, `{"port":"COM1","baud":115200}`); fr.Error == nil {
		t.Fatal("波特率不同却复用了连接 —— 会报出与被测配置无关的成功")
	}
	if got := f.connected(t).pings.Load(); got != 0 {
		t.Errorf("采集会话收到了 %d 次 Ping，want 0（本就不该选中它）", got)
	}
}

// TestPingPrefersNewestMatchingSession 多个候选都匹配时取**后登记**的那个。
//
// 降序不是正确性保证（Refresh 时旧会话先关），只是确定性择优：同一端口出现
// 两个活会话只可能来自"两台设备配到了同一个串口"这类错配，此时取新的更贴近
// 当前配置。改用各自独立的假注册表，避免第二个 Connect 撞上端口占用。
func TestPingPrefersNewestMatchingSession(t *testing.T) {
	old := &serialDriver{reg: newSerialPortRegistry()}
	fresh := &serialDriver{reg: newSerialPortRegistry()}

	var mu sync.Mutex
	queue := []*serialDriver{old, fresh}
	mk := func() driver.Driver {
		mu.Lock()
		defer mu.Unlock()
		if len(queue) == 0 {
			return &serialDriver{reg: newSerialPortRegistry()}
		}
		d := queue[0]
		queue = queue[1:]
		return d
	}
	c := newRPCClient(t, startServer(t, mk, testMeta()))

	connectSerial(t, c, `{"port":"COM1","baud":9600}`) // 会话 1 = old
	connectSerial(t, c, `{"port":"COM1","baud":9600}`) // 会话 2 = fresh

	if fr := ping(c, `{"port":"COM1","baud":9600}`); fr.Error != nil {
		t.Fatalf("ping 应命中可复用会话: %s", fr.Error.Message)
	}
	if fresh.pings.Load() != 1 || old.pings.Load() != 0 {
		t.Errorf("ping 落在旧会话上 (fresh=%d old=%d)，应取后登记的会话",
			fresh.pings.Load(), old.pings.Load())
	}
}

// TestPingBusySessionDoesNotHang 会话正忙（有在途读取持着锁）时，ping 必须在
// 预算内返回，而不是挂到网关侧的 30s 超时。
//
// 挂到超时会累积 pluginhost 的连续超时计数，连续几次可能触发卡死检测把整个
// 插件进程重启（该插件下所有协议、所有设备一起断）。锁预算是那条链的止损点。
//
// 超时后**回落临时实例**而不是报一句"忙"：此刻无法判断那个忙会话是不是目标
// 端口（MatchConnection 本身就要拿锁），一律报忙会让「测 COM2 时因为 COM1 在
// 读取而失败」这种与被测对象无关的假失败出现。端口真被占的话，临时实例会
// 如实报 Access is denied，网关再补上「COM1 当前由设备「xxx」占用」。
func TestPingBusySessionDoesNotHang(t *testing.T) {
	t.Run("端口被那个忙会话占着：如实失败且不挂死", func(t *testing.T) {
		reg := newSerialPortRegistry()
		gate := make(chan struct{})
		// 工厂必须每次给新实例：临时实例与采集会话若是同一个对象，
		// "没复用到"与"复用到了"就分不出来（它会命中自己已持有的端口）。
		f := newSerialFactory(reg)
		f.newDriver = func(r *serialPortRegistry) *serialDriver {
			return &serialDriver{reg: r, readGate: gate}
		}
		c := newRPCClient(t, startServer(t, f.mk, testMeta()))
		defer close(gate)

		sess := connectSerial(t, c, `{"port":"COM1","baud":9600}`)

		// 挂一个长时间持会话锁的 Read（只发不等），让它一直在服务端跑。
		c.send(999, pluginwire.MethodRead, driversdk.ReadParams{
			Session: sess,
			Addrs:   []driversdk.WireAddr{{ID: "a1", Name: "p1", DataType: "int16"}},
		})
		time.Sleep(200 * time.Millisecond) // 让它真正进入会话锁

		start := time.Now()
		fr := ping(c, `{"port":"COM1","baud":9600}`)
		elapsed := time.Since(start)

		if fr.Error == nil {
			t.Fatal("端口被那个忙会话占着，回落后的临时实例必然会撞上 Access is denied")
		}
		if !strings.Contains(fr.Error.Message, "Access is denied") {
			t.Errorf("错误应来自端口独占，实得: %s", fr.Error.Message)
		}
		// 预算 3s；只挡住"挂到网关侧 30s 超时"这一类回归，不做精确断言。
		if elapsed > 15*time.Second {
			t.Errorf("ping 耗时 %s —— 应在会话锁预算内返回，而不是挂到网关侧超时", elapsed)
		}
	})

	t.Run("忙的是另一个端口：照常成功，不产生假失败", func(t *testing.T) {
		reg := newSerialPortRegistry()
		gate := make(chan struct{})
		f := newSerialFactory(reg)
		f.newDriver = func(r *serialPortRegistry) *serialDriver {
			return &serialDriver{reg: r, readGate: gate}
		}
		c := newRPCClient(t, startServer(t, f.mk, testMeta()))
		defer close(gate)

		// 唯一那个会话占着 COM1，且正忙。
		sess := connectSerial(t, c, `{"port":"COM1","baud":9600}`)
		c.send(999, pluginwire.MethodRead, driversdk.ReadParams{
			Session: sess,
			Addrs:   []driversdk.WireAddr{{ID: "a1", Name: "p1", DataType: "int16"}},
		})
		time.Sleep(200 * time.Millisecond)

		// 但这次测的是 COM2 —— 与被占用的 COM1 无关，应当照常成功。
		// （若把"有会话忙"直接当成失败，这里就会报一句与被测对象无关的假失败。）
		start := time.Now()
		if fr := ping(c, `{"port":"COM2","baud":9600}`); fr.Error != nil {
			t.Errorf("忙的是 COM1，测 COM2 不该失败（假失败），实得: %s", fr.Error.Message)
		}
		if elapsed := time.Since(start); elapsed > 15*time.Second {
			t.Errorf("ping 耗时 %s —— 不应挂到网关侧超时", elapsed)
		}
	})
}

// TestPingAfterCloseUsesFreshInstance 会话关闭后端口已释放，ping 应走临时实例
// 并成功，且**不得**触碰那个已拆解的实例。
func TestPingAfterCloseUsesFreshInstance(t *testing.T) {
	reg := newSerialPortRegistry()
	f := newSerialFactory(reg)
	c := newRPCClient(t, startServer(t, f.mk, testMeta()))

	sess := connectSerial(t, c, `{"port":"COM1","baud":9600}`)
	stub := f.connected(t)

	c.callOK(pluginwire.MethodClose, driversdk.SessionParams{Session: sess}, nil)

	if fr := ping(c, `{"port":"COM1","baud":9600}`); fr.Error != nil {
		t.Fatalf("端口已释放，ping 应走临时实例并成功: %s", fr.Error.Message)
	}
	if got := stub.pings.Load(); got != 0 {
		t.Errorf("ping 触碰了已拆解的会话 %d 次", got)
	}
	if got := stub.closes.Load(); got != 1 {
		t.Errorf("Close 被调用 %d 次，want 1（ping 不得再关一次）", got)
	}
}

// TestPingReusePanicStillReleasesSessionLock 复用路径上驱动 panic 时，
// 会话锁必须被释放：否则该会话此后再也拿不到锁，shutdown() 还会永久阻塞
// （sessionCall 的 defer 是为这两件事而存在的）。
func TestPingReusePanicStillReleasesSessionLock(t *testing.T) {
	reg := newSerialPortRegistry()
	stub := &panicPingDriver{serialDriver: serialDriver{reg: reg}}
	c := newRPCClient(t, startServer(t, func() driver.Driver { return stub }, testMeta()))

	sess := connectSerial(t, c, `{"port":"COM1","baud":9600}`)

	if fr := ping(c, `{"port":"COM1","baud":9600}`); fr.Error == nil {
		t.Fatal("驱动 panic 应降级为一次 RPC 错误")
	}

	// 锁若泄漏，这次 read 会永远拿不到会话锁（测试超时即失败）。
	c.callOK(pluginwire.MethodRead, driversdk.ReadParams{
		Session: sess,
		Addrs:   []driversdk.WireAddr{{ID: "a1", Name: "p1", DataType: "int16"}},
	}, nil)
}

// TestPingAllocatesNoSessionOnReusePath 复用路径不新增会话 ——
// 与 TestPingAllocatesNoSession 互补：那条走的是临时实例路径。
func TestPingAllocatesNoSessionOnReusePath(t *testing.T) {
	reg := newSerialPortRegistry()
	f := newSerialFactory(reg)
	c := newRPCClient(t, startServer(t, f.mk, testMeta()))

	connectSerial(t, c, `{"port":"COM1","baud":9600}`) // 会话 1
	if sessionExists(c, 2) {
		t.Fatal("前置条件不成立：会话 2 不该存在")
	}

	if fr := ping(c, `{"port":"COM1","baud":9600}`); fr.Error != nil {
		t.Fatalf("ping 应成功: %s", fr.Error.Message)
	}
	if sessionExists(c, 2) {
		t.Error("ping 分配了会话 2 —— driver.PingDevice 从不 Close，这会每次泄漏一个")
	}
}
