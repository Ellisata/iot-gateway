// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"strings"
	"testing"

	"iot-gateway/driver"
)

// TestPingReusesSessionAcrossRPC 串口「测试连接」在**真实的进程边界**上复用采集会话。
//
// driversdk 的单测已经覆盖了会话选择的全部逻辑分支，但只有这条能证明
// 「网关口发出的一次 ping RPC 真的命中了插件进程里另一个会话持有的句柄」——
// 中间隔着帧编解码、进程监管与 RPC 代理三层，任何一层把 PingParams 传丢都会
// 在这里暴露，而单测看不出来。
//
// 自证机制：同一个用例里先用 Connect 去撞一次同一个端口，必须失败 ——
// 失败才说明夹具里的假独占注册表真的在拦人，于是后面 PingDevice 的成功
// 只可能来自复用，而不是"注册表形同虚设"。
func TestPingReusesSessionAcrossRPC(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	m, _, _ := newTestManager(t, &fakeReloader{})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	const portJSON = `{"port":"COM1"}`

	// 采集侧：一台设备已经连上 COM1 并持有句柄。
	collecting, err := driver.Create(fixtureProtocol)
	if err != nil {
		t.Fatalf("driver.Create: %v", err)
	}
	t.Cleanup(func() { _ = collecting.Close() })
	if err := collecting.Connect(portJSON); err != nil {
		t.Fatalf("采集侧连接 COM1 失败: %v", err)
	}

	// 自证：另一个实例去连同一个端口必须被假注册表拦下。
	// 这一步若成功，说明"独占"没生效，下面的断言就毫无意义。
	squatter, err := driver.Create(fixtureProtocol)
	if err != nil {
		t.Fatalf("driver.Create: %v", err)
	}
	t.Cleanup(func() { _ = squatter.Close() })
	err = squatter.Connect(portJSON)
	if err == nil {
		t.Fatal("第二个实例也打开了 COM1 —— 假独占注册表没生效，本用例的前提不成立")
	}
	if !strings.Contains(err.Error(), "Access is denied") {
		t.Fatalf("第二个实例的失败原因不是端口独占: %v", err)
	}

	// 生产路径：网关口「测试连接」走的就是 driver.PingDevice。
	if err := driver.PingDevice(fixtureProtocol, portJSON); err != nil {
		t.Fatalf("设备正在采集时「测试连接」应复用会话成功，实得: %v", err)
	}
}

// TestPingDoesNotReuseAcrossProtocols 会话按协议名过滤：另一个协议选不到
// 本协议的会话，只能新开句柄 → 撞端口占用，如实失败。
//
// 这条钉住的是复用的边界。不过滤协议的后果不是"少复用一次"，而是把一份
// 串口 JSON 交给另一个协议的驱动，报出完全指错方向的错误。
func TestPingDoesNotReuseAcrossProtocols(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	m, _, _ := newTestManager(t, &fakeReloader{})
	// 让夹具多暴露一个协议（Test.Fixture.Alt），才能构造"同进程、跨协议"。
	m.opts.Env = append(m.opts.Env, envFixtureVariant+"=addproto")
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)
	waitForDriverRegistered(t, fixtureProtocolAlt)

	const portJSON = `{"port":"COM1"}`

	holder, err := driver.Create(fixtureProtocol)
	if err != nil {
		t.Fatalf("driver.Create: %v", err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	if err := holder.Connect(portJSON); err != nil {
		t.Fatalf("占用 COM1 失败: %v", err)
	}

	if err := driver.PingDevice(fixtureProtocolAlt, portJSON); err == nil {
		t.Fatal("跨协议不该复用会话 —— 会把一份串口 JSON 交给另一个协议的驱动")
	}
}
