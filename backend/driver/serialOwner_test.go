// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"iot-gateway/model/po"
)

// fakeSerialOwner 假「独占串口」驱动：实现 Driver + SerialOwner。
//
// port 非空表示句柄在手里，与 live 刻意分开——串口读失败只标记断开、不释放句柄，
// 「占着端口但链路已断」是真实存在且最需要提示的状态。
//
// live 与 pings 都必须是并发安全的：并发用例里同一个实例会被多个 goroutine
// 经 PingDevice 读到（IsConnected），同时被它自己的归属 goroutine Close 掉。
// 真实驱动同样用锁/原子量保护这两处状态，替身不这么做就是在测试里制造假竞争。
type fakeSerialOwner struct {
	mu      sync.Mutex
	port    string
	live    bool
	match   bool
	pingErr error
	pings   atomic.Int64
}

func (f *fakeSerialOwner) Connect(string) error                          { return nil }
func (f *fakeSerialOwner) Ping(string) error                             { f.pings.Add(1); return f.pingErr }
func (f *fakeSerialOwner) Read([]po.DeviceAddress) ([]ReadResult, error) { return nil, nil }
func (f *fakeSerialOwner) MatchConnection(string) bool                   { return f.match }
func (f *fakeSerialOwner) SerialResource() string                        { return f.port }

func (f *fakeSerialOwner) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live
}

func (f *fakeSerialOwner) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live = false
	return nil
}

// fakePlainDriver 只有 Driver、没有 SerialOwner —— 模拟 TCP 类驱动。
type fakePlainDriver struct{ pings atomic.Int64 }

func (f *fakePlainDriver) Connect(string) error                          { return nil }
func (f *fakePlainDriver) Ping(string) error                             { f.pings.Add(1); return nil }
func (f *fakePlainDriver) Read([]po.DeviceAddress) ([]ReadResult, error) { return nil, nil }
func (f *fakePlainDriver) IsConnected() bool                             { return true }
func (f *fakePlainDriver) Close() error                                  { return nil }

// withTestProtocol 往全局注册表里挂一个临时协议，测试结束自动摘掉。
//
// registry 是包级的，且 Register 对重名会 panic，所以这里先查重、
// 并在 Cleanup 里删除，避免污染同包的其它用例。
func withTestProtocol(t *testing.T, name string, fn NewFunc) {
	t.Helper()
	if _, ok := registry[name]; ok {
		t.Fatalf("测试协议名 %q 已被占用（全局注册表是包级的，换个更独特的名字）", name)
	}
	registry[name] = fn
	t.Cleanup(func() { delete(registry, name) })
}

// trackOwner 登记后自动注销。断言一律用增量，绝不依赖绝对值——
// 同包其它用例可能也往登记簿里放东西。
func trackOwner(t *testing.T, protocol, label string, d Driver) {
	t.Helper()
	before := SerialOwnerCount()
	RegisterSerialOwner(protocol, label, d)
	t.Cleanup(func() { UnregisterSerialOwner(d) })

	// 不实现 SerialOwner 的驱动（TCP 类）本就不会进登记簿
	want := before
	if _, ok := d.(SerialOwner); ok {
		want = before + 1
	}
	if got := SerialOwnerCount(); got != want {
		t.Fatalf("登记后实例数 = %d, want %d", got, want)
	}
}

func TestRegisterAndUnregisterSerialOwner(t *testing.T) {
	base := SerialOwnerCount()

	owner := &fakeSerialOwner{port: "COM1", live: true}
	RegisterSerialOwner("Test.Registry", "设备A", owner)
	if got := SerialOwnerCount(); got != base+1 {
		t.Fatalf("登记后实例数 = %d, want %d", got, base+1)
	}

	// 重复登记同一实例不产生额外条目
	RegisterSerialOwner("Test.Registry", "设备A", owner)
	if got := SerialOwnerCount(); got != base+1 {
		t.Errorf("重复登记后实例数 = %d, want %d", got, base+1)
	}

	// 不实现 SerialOwner 的驱动（TCP 类）不进登记簿
	RegisterSerialOwner("Test.Registry", "设备B", &fakePlainDriver{})
	if got := SerialOwnerCount(); got != base+1 {
		t.Errorf("非串口驱动被登记了：实例数 = %d, want %d", got, base+1)
	}

	// 注销未登记的实例是无操作，允许重复调用
	UnregisterSerialOwner(&fakeSerialOwner{})
	UnregisterSerialOwner(owner)
	if got := SerialOwnerCount(); got != base {
		t.Errorf("注销后实例数 = %d, want %d", got, base)
	}
	UnregisterSerialOwner(owner)
	if got := SerialOwnerCount(); got != base {
		t.Errorf("重复注销后实例数 = %d, want %d", got, base)
	}
}

// 命中活实例时必须调用它的 Ping，而不是再造一个临时实例——
// 这正是「测试连接报 Access is denied」的修法：临时实例会去抢已被独占的串口。
func TestPingDevicePrefersLiveOwner(t *testing.T) {
	const proto = "Test.PrefersLive"
	factoryCalls := 0
	withTestProtocol(t, proto, func() Driver { factoryCalls++; return &fakePlainDriver{} })

	owner := &fakeSerialOwner{port: "COM1", live: true, match: true}
	trackOwner(t, proto, "设备A", owner)

	if err := PingDevice(proto, `{}`); err != nil {
		t.Fatalf("PingDevice 失败: %v", err)
	}
	if got := owner.pings.Load(); got != 1 {
		t.Errorf("活实例的 Ping 被调用 %d 次, want 1", got)
	}
	if factoryCalls != 0 {
		t.Errorf("命中了活实例却仍构造了 %d 个临时实例", factoryCalls)
	}
}

// 活实例的 Ping 报错要原样上抛，不得改走临时实例重试——
// 那只会退回去再抢一次已经被占用的串口。
func TestPingDevicePropagatesLiveError(t *testing.T) {
	const proto = "Test.PropagatesErr"
	factoryCalls := 0
	withTestProtocol(t, proto, func() Driver { factoryCalls++; return &fakePlainDriver{} })

	want := errors.New("等待应答超时")
	owner := &fakeSerialOwner{port: "COM1", live: true, match: true, pingErr: want}
	trackOwner(t, proto, "设备A", owner)

	err := PingDevice(proto, `{}`)
	if !errors.Is(err, want) {
		t.Fatalf("错误 = %v, want %v", err, want)
	}
	if factoryCalls != 0 {
		t.Errorf("复用失败后又造了 %d 个临时实例（会再抢一次串口）", factoryCalls)
	}
}

// 不能复用的一律退回临时实例：未连接、参数不匹配、以及压根不实现 SerialOwner 的 TCP 驱动。
func TestPingDeviceFallsBackToFreshInstance(t *testing.T) {
	cases := map[string]struct {
		owner Driver
	}{
		"未连接":             {&fakeSerialOwner{port: "COM1", live: false, match: true}},
		"参数不匹配":           {&fakeSerialOwner{port: "COM1", live: true, match: false}},
		"不实现 SerialOwner": {&fakePlainDriver{}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			proto := "Test.Fallback." + name
			factoryCalls := 0
			withTestProtocol(t, proto, func() Driver { factoryCalls++; return &fakePlainDriver{} })
			trackOwner(t, proto, "设备A", c.owner)

			if err := PingDevice(proto, `{}`); err != nil {
				t.Fatalf("PingDevice 失败: %v", err)
			}
			if factoryCalls != 1 {
				t.Errorf("临时实例构造 %d 次, want 1", factoryCalls)
			}
		})
	}
}

// 热刷新窗口期新旧两个实例同时在册，必须命中后登记（更贴近当前配置）的那个。
func TestPingDeviceNewestOwnerWins(t *testing.T) {
	const proto = "Test.NewestWins"
	withTestProtocol(t, proto, func() Driver { return &fakePlainDriver{} })

	old := &fakeSerialOwner{port: "COM1", live: true, match: true}
	fresh := &fakeSerialOwner{port: "COM1", live: true, match: true}
	trackOwner(t, proto, "旧", old)
	trackOwner(t, proto, "新", fresh)

	if err := PingDevice(proto, `{}`); err != nil {
		t.Fatalf("PingDevice 失败: %v", err)
	}
	if fresh.pings.Load() != 1 || old.pings.Load() != 0 {
		t.Errorf("命中次数 新=%d 旧=%d, want 新=1 旧=0", fresh.pings.Load(), old.pings.Load())
	}
}

// 未注册的协议名错误文案保持不变（实现「测试连接」的 service 层依赖它做 404 判定）。
func TestPingDeviceUnknownProtocol(t *testing.T) {
	err := PingDevice("Test.NotRegistered", `{}`)
	if err == nil || !strings.Contains(err.Error(), "unsupported protocol") {
		t.Fatalf("错误 = %v, want 含 unsupported protocol", err)
	}
}

// 占用提示按串口名查、大小写不敏感；查不到时返回空串（调用方直接拼进错误里，不做判空）。
func TestSerialBusyHint(t *testing.T) {
	holder := &fakeSerialOwner{port: "COM1", live: true}
	trackOwner(t, "Test.BusyHint", "dlt1997-串口", holder)

	if got := SerialBusyHint("COM1"); !strings.Contains(got, "dlt1997-串口") {
		t.Errorf("COM1 的占用提示 = %q, want 含设备名", got)
	}
	// Windows 的串口名大小写不敏感，配置里写 com1 也要能查出来
	if got := SerialBusyHint("com1"); !strings.Contains(got, "dlt1997-串口") {
		t.Errorf("com1 的占用提示 = %q, want 命中 COM1", got)
	}
	if got := SerialBusyHint("COM2"); got != "" {
		t.Errorf("无人占用的 COM2 提示 = %q, want 空", got)
	}

	// 句柄还握着但链路已断：串口读失败只标记断开、不释放句柄，
	// 这时端口仍被占着，正是最该提示的时刻。
	broken := &fakeSerialOwner{port: "COM5", live: false}
	trackOwner(t, "Test.BusyHint", "断链设备", broken)
	if got := SerialBusyHint("COM5"); !strings.Contains(got, "断链设备") {
		t.Errorf("COM5 的占用提示 = %q, want 含设备名（断开不等于释放句柄）", got)
	}
}

// 登记簿的读（PingDevice）与写（登记/注销）必须在 -race 下干净。
//
// 这条用例是「锁序铁律」的回归网：一旦有人把 findLiveSerialOwner 改成
// 持着登记表锁去调驱动方法，就会与 Stop 里「持驱动锁 Close、随后注销」构成 AB-BA。
func TestSerialOwnerConcurrentAccess(t *testing.T) {
	const proto = "Test.Concurrent"
	withTestProtocol(t, proto, func() Driver { return &fakePlainDriver{} })

	const workers = 8
	const rounds = 50
	base := SerialOwnerCount()

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				d := &fakeSerialOwner{port: "COM1", live: true, match: true}
				RegisterSerialOwner(proto, "并发设备", d)
				_ = PingDevice(proto, `{}`)
				SerialBusyHint("COM1")
				SerialOwnerCount()
				UnregisterSerialOwner(d)
				d.Close()
			}
		}()
	}
	wg.Wait()

	if got := SerialOwnerCount(); got != base {
		t.Errorf("并发用例结束后实例数 = %d, want %d（有登记泄漏）", got, base)
	}
}
