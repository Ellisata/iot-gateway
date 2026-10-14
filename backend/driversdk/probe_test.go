// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driversdk_test

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
	"iot-gateway/pluginwire"
)

// ==================== 局域网扫描发现（probe） ====================

// probeConst 探测用例共用的协议名。与 testProtocol 分开，便于构造
// "声明了扫描能力的协议"与"没声明的协议"并存的 Meta。
const probeConst = "Test.Plugin.Probe"

// probeMeta 一个声明了扫描能力的 Meta。
func probeMeta(maxBudgetMs int) driversdk.Meta {
	m := testMeta()
	m.Protocols = append(m.Protocols, driversdk.Protocol{
		Name: probeConst,
		Discovery: &driver.DiscoverySpec{
			Title:       "测试扫描",
			DefaultPort: 502,
			TargetMode:  driver.TargetModeUnicast,
			MaxBudgetMs: maxBudgetMs,
		},
	})
	return m
}

// probeFactories 两个协议都要有工厂（否则启动校验先失败）。
func probeFactories() driversdk.Factories {
	return driversdk.Factories{
		testProtocol: func() driver.Driver { return &stubDriver{} },
		probeConst:   func() driver.Driver { return &stubDriver{} },
	}
}

// probeCall 发一次 probe 请求并返回应答帧。
func probeCall(t *testing.T, c *rpcClient, params driversdk.ProbeParams) pluginwire.Frame {
	t.Helper()
	return c.call(pluginwire.MethodProbe, params)
}

func TestServeRoutesProbeToProber(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)

	var got driversdk.ProbeRequest
	probers := driversdk.Probers{probeConst: func(req driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
		got = req
		return []driver.DiscoveredDevice{{
			Host:         req.Target.Host,
			Port:         req.Target.Port,
			Name:         "假设备",
			ProtocolJSON: `{"host":"10.0.0.9"}`,
		}}, nil
	}}

	go func() {
		done <- driversdk.ServeConn(server, probeFactories(), probeMeta(2500), driversdk.Capabilities{Probers: probers})
	}()
	t.Cleanup(func() {
		_ = client.Close()
		<-done
	})

	c := newRPCClient(t, client)
	resp := probeCall(t, c, driversdk.ProbeParams{
		Protocol: probeConst,
		Options:  `{"unitIds":"1,2"}`,
		Target:   driver.ScanTarget{Host: "10.0.0.9", Port: 1502, LocalIP: "10.0.0.1"},
		BudgetMs: 2000,
	})
	if resp.Error != nil {
		t.Fatalf("probe 返回错误: %v", resp.Error)
	}

	var reply driversdk.ProbeReply
	if err := pluginwire.DecodeBody(resp.Codec, resp.Result, &reply); err != nil {
		t.Fatalf("解析应答: %v", err)
	}
	if len(reply.Devices) != 1 || reply.Devices[0].Host != "10.0.0.9" {
		t.Fatalf("应答内容不对: %+v", reply.Devices)
	}

	// 参数必须无损过线：Options / Target / Budget 任一丢失都会让用户在页面上
	// 填的选项静默失效。
	if string(got.Options) != `{"unitIds":"1,2"}` {
		t.Errorf("Options = %q，期望原样透传", got.Options)
	}
	if got.Target.Port != 1502 {
		t.Errorf("Target.Port = %d，期望 1502", got.Target.Port)
	}
	if got.Target.LocalIP != "10.0.0.1" {
		t.Errorf("Target.LocalIP = %q，期望 10.0.0.1（广播探测要用它绑源地址）", got.Target.LocalIP)
	}
	if got.Protocol != probeConst {
		t.Errorf("Protocol = %q", got.Protocol)
	}
	// 预算取 min(本次请求 2000ms, 插件声明 2500ms) = 2000ms。
	if got.Budget != 2*time.Second {
		t.Errorf("Budget = %s，期望 2s（请求值小于声明值时取请求值）", got.Budget)
	}
}

// TestServeProbeBudgetClampedToMeta 请求值大于插件声明值时**以声明值为准**。
//
// 插件是必须不超时的那一方，不能把这条不变量交给对端给的值 ——
// 网关侧 probe RPC 的超时正是按插件的声明值推导的。
func TestServeProbeBudgetClampedToMeta(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)

	var got driversdk.ProbeRequest
	probers := driversdk.Probers{probeConst: func(req driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
		got = req
		return nil, nil
	}}

	go func() {
		done <- driversdk.ServeConn(server, probeFactories(), probeMeta(1500), driversdk.Capabilities{Probers: probers})
	}()
	t.Cleanup(func() { _ = client.Close(); <-done })

	c := newRPCClient(t, client)
	resp := probeCall(t, c, driversdk.ProbeParams{
		Protocol: probeConst,
		Target:   driver.ScanTarget{Host: "10.0.0.9", Port: 502},
		BudgetMs: 60000, // 远大于声明值
	})
	if resp.Error != nil {
		t.Fatalf("probe 返回错误: %v", resp.Error)
	}
	if got.Budget != 1500*time.Millisecond {
		t.Fatalf("Budget = %s，期望被插件声明值 1.5s 钳住", got.Budget)
	}
}

// TestServeProbeNilResultIsNotAnError "没扫到"必须是一条正常的空应答。
func TestServeProbeNilResultIsNotAnError(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)

	probers := driversdk.Probers{probeConst: func(driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
		return nil, nil
	}}

	go func() {
		done <- driversdk.ServeConn(server, probeFactories(), probeMeta(1000), driversdk.Capabilities{Probers: probers})
	}()
	t.Cleanup(func() { _ = client.Close(); <-done })

	c := newRPCClient(t, client)
	resp := probeCall(t, c, driversdk.ProbeParams{
		Protocol: probeConst,
		Target:   driver.ScanTarget{Host: "10.0.0.9", Port: 502},
	})
	if resp.Error != nil {
		t.Fatalf("(nil, nil) 不该变成错误: %v", resp.Error)
	}
}

// TestServeProbeUnknownProtocol 网关向没声明扫描能力的协议派发 probe 时，
// 必须如实报错而不是静默返回空列表 —— 后者会让用户以为"扫不到设备"。
func TestServeProbeUnknownProtocol(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)

	probers := driversdk.Probers{probeConst: func(driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
		t.Fatal("不该被调用")
		return nil, nil
	}}

	go func() {
		done <- driversdk.ServeConn(server, probeFactories(), probeMeta(1000), driversdk.Capabilities{Probers: probers})
	}()
	t.Cleanup(func() { _ = client.Close(); <-done })

	c := newRPCClient(t, client)
	resp := probeCall(t, c, driversdk.ProbeParams{
		Protocol: testProtocol, // 这个协议没声明 Discovery
		Target:   driver.ScanTarget{Host: "10.0.0.9", Port: 502},
	})
	if resp.Error == nil {
		t.Fatal("未声明扫描能力的协议应报错")
	}
	if !strings.Contains(resp.Error.Message, "does not support discovery") {
		t.Errorf("错误文案应指明原因，实际 %q", resp.Error.Message)
	}
}

// TestServeProbePanicBecomesRemoteError 单个 probe 的 panic 只该是一次 RPC 错误：
// 若它带走整个插件进程，该插件下所有协议、所有设备都会一起断。
func TestServeProbePanicBecomesRemoteError(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)

	probers := driversdk.Probers{probeConst: func(driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
		panic("prober 故意 panic")
	}}

	go func() {
		done <- driversdk.ServeConn(server, probeFactories(), probeMeta(1000), driversdk.Capabilities{Probers: probers})
	}()
	t.Cleanup(func() { _ = client.Close(); <-done })

	c := newRPCClient(t, client)
	resp := probeCall(t, c, driversdk.ProbeParams{
		Protocol: probeConst,
		Target:   driver.ScanTarget{Host: "10.0.0.9", Port: 502},
	})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "panic") {
		t.Fatalf("panic 应降级为一条 RPC 错误，实际 %+v", resp.Error)
	}

	// 而且进程必须还活着：紧接着的一次正常调用要能成功。
	resp2 := probeCall(t, c, driversdk.ProbeParams{
		Protocol: probeConst,
		Target:   driver.ScanTarget{Host: "10.0.0.9", Port: 502},
	})
	if resp2.Error == nil || !strings.Contains(resp2.Error.Message, "panic") {
		t.Fatalf("第二次调用仍应得到 panic 降级后的错误，实际 %+v", resp2.Error)
	}
}

// ==================== 启动期校验 ====================

// TestServeRejectsDiscoveryWithoutProber 声明了扫描能力却没给实现，
// 必须在**进程启动时**报错，而不是等运维点开扫描页才发现。
func TestServeRejectsDiscoveryWithoutProber(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	err := driversdk.ServeConn(server, probeFactories(), probeMeta(1000), driversdk.Capabilities{})
	if err == nil {
		t.Fatal("声明了 Discovery 却没有 prober，应启动失败")
	}
	if !strings.Contains(err.Error(), probeConst) {
		t.Errorf("错误应指明是哪个协议，实际 %v", err)
	}
}

// TestServeAcceptsNilProbers 不声明扫描能力的插件传 nil probers 必须能正常启动。
//
// 这条钉住的是"8 个既有插件一行不改地继续工作"：任何把校验写成
// "probers 不能为空"的改动都会在这里被拦下，而不是等到现场 8 个插件同时挂掉。
func TestServeAcceptsNilProbers(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)

	go func() {
		done <- driversdk.ServeConn(server, testFactories(func() driver.Driver {
			return &stubDriver{}
		}), testMeta(), driversdk.Capabilities{})
	}()
	t.Cleanup(func() { _ = client.Close(); <-done })

	c := newRPCClient(t, client)
	// describe 能应答即说明进程正常起来了。
	resp := c.call(pluginwire.MethodDescribe, struct{}{})
	if resp.Error != nil {
		t.Fatalf("describe 应成功: %v", resp.Error)
	}
}

// TestServeWarnsOnUnusedProber 多余的 prober 只警告不拒绝（与工厂表的行为对称）。
func TestServeWarnsOnUnusedProber(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)

	// 只声明了 testProtocol（无 Discovery），却给了两个 prober。
	probers := driversdk.Probers{
		testProtocol: func(driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) { return nil, nil },
		"Not.Declared": func(driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
			return nil, nil
		},
	}

	go func() {
		done <- driversdk.ServeConn(server, testFactories(func() driver.Driver {
			return &stubDriver{}
		}), testMeta(), driversdk.Capabilities{Probers: probers})
	}()
	t.Cleanup(func() { _ = client.Close(); <-done })

	c := newRPCClient(t, client)
	resp := c.call(pluginwire.MethodDescribe, struct{}{})
	if resp.Error != nil {
		t.Fatalf("多余的 prober 不该导致启动失败: %v", resp.Error)
	}
}

// TestProbeParamsStayJSON 钉住 probe 的载荷编码：它必须走 JSON。
//
// pluginwire 按类型是否实现 BinaryEncoder 自动选编码，而"意外实现了"
// 的症状是**两侧都按另一套编解码**却一个错都不报（见 binary.go 的注释）。
// 这里显式断言，改了就会立刻红。
func TestProbeParamsStayJSON(t *testing.T) {
	for name, v := range map[string]any{
		"ProbeParams":      driversdk.ProbeParams{},
		"ProbeReply":       driversdk.ProbeReply{},
		"ProbeTargetError": driversdk.ProbeTargetError{},
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

// TestProbeParamsEmptyOptionsMarshal 空选项必须能正常编码。
//
// Options 若声明成 json.RawMessage，空值会让 json.Marshal 校验失败，
// 报出来是一句指向"编码失败"的错，与真正的根因（用户没填扫描选项）相隔十万八千里。
func TestProbeParamsEmptyOptionsMarshal(t *testing.T) {
	body, _, err := pluginwire.EncodeBody(driversdk.ProbeParams{
		Protocol: probeConst,
		Options:  "",
		Target:   driver.ScanTarget{Host: "10.0.0.9", Port: 502},
	})
	if err != nil {
		t.Fatalf("空选项不该导致编码失败: %v", err)
	}
	var back driversdk.ProbeParams
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("回解失败: %v", err)
	}
	if back.Options != "" {
		t.Errorf("Options = %q，期望空串", back.Options)
	}
}
