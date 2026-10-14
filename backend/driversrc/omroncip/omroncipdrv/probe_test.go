// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"iot-gateway/driver"
	cipcore "iot-gateway/driversrc/omroncip/cipcore"
	"iot-gateway/testutil/fake"
)

// probeBudget 用例里的探测预算。远大于 loopback 上的往返耗时，
// 因此它只对"必须等满超时"的那几条路径起作用。
const probeBudget = 5 * time.Second

// targetFor 把一个 "host:port" 地址转成 ScanTarget。
func targetFor(t *testing.T, addr string, broadcast bool) driver.ScanTarget {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("拆分地址 %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("端口 %q 不是数字: %v", portStr, err)
	}
	return driver.ScanTarget{Host: host, Port: port, Broadcast: broadcast}
}

func hasAttr(dev driver.DiscoveredDevice, name, value string) bool {
	for _, a := range dev.Extra {
		if a.Name == name && a.Value == value {
			return true
		}
	}
	return false
}

// ==================== 选项解析 ====================

func TestParseScanOptions(t *testing.T) {
	cases := []struct {
		name    string
		options string
		timeout time.Duration
		window  time.Duration
		wantErr bool
	}{
		{"默认值", ``, 1500 * time.Millisecond, 500 * time.Millisecond, false},
		{"空对象", `{}`, 1500 * time.Millisecond, 500 * time.Millisecond, false},
		{"超时是数字", `{"timeoutMs":900}`, 900 * time.Millisecond, 300 * time.Millisecond, false},
		{"超时是字符串", `{"timeoutMs":"900"}`, 900 * time.Millisecond, 300 * time.Millisecond, false},
		// 安静窗口跟着超时缩小（想扫快一点时它必须一起小），但**不跟着放大**：
		// 应答是同时到达的，把超时调大没有理由让每次扫描多等 1 秒。
		{"超时调大窗口封顶", `{"timeoutMs":6000}`, 6000 * time.Millisecond, 500 * time.Millisecond, false},
		{"超时是0回默认", `{"timeoutMs":0}`, 1500 * time.Millisecond, 500 * time.Millisecond, false},
		// 认不出的超时静默回默认值：这个值错了的表现只是"扫得慢"。
		{"超时认不出", `{"timeoutMs":"很快"}`, 1500 * time.Millisecond, 500 * time.Millisecond, false},
		{"选项不是JSON", `{`, 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var raw json.RawMessage
			if c.options != "" {
				raw = json.RawMessage(c.options)
			}
			opt, err := parseScanOptions(raw)
			if c.wantErr {
				if err == nil {
					t.Fatal("应报错")
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if opt.timeout != c.timeout {
				t.Errorf("timeout = %v，期望 %v", opt.timeout, c.timeout)
			}
			if opt.replyWindow != c.window {
				t.Errorf("replyWindow = %v，期望 %v", opt.replyWindow, c.window)
			}
		})
	}
}

// ==================== 设备组装 ====================

// TestBuildDeviceProtocolJSON 生成的连接配置必须能被**插件自己的配置解析器**吃下。
//
// 一份键名写错、或把 port 写成字符串的 JSON 会让「扫描 → 一键建设备」建出来的
// 设备连不上，而**两边都不报错**。
func TestBuildDeviceProtocolJSON(t *testing.T) {
	id := fake.OmronNJ501("192.168.1.50", 44818)
	dev, ok := buildDevice(cipIdentityOf(t, id), nil)
	if !ok {
		t.Fatal("这条 Identity 有地址，应能建成设备")
	}

	cfg, err := ParseCIPConfig(dev.ProtocolJSON)
	if err != nil {
		t.Fatalf("连接配置解析失败: %v（原文 %s）", err, dev.ProtocolJSON)
	}
	if cfg.Host != "192.168.1.50" || cfg.Port != 44818 {
		t.Errorf("配置 = %s:%d，期望 192.168.1.50:44818", cfg.Host, cfg.Port)
	}

	// 与 FINS/S7 不同，这里的厂商、型号、版本都是**设备自报**的，可以填。
	if dev.Vendor != "Omron" {
		t.Errorf("Vendor = %q，期望 Omron（ListIdentity 的厂商字段是设备自述的）", dev.Vendor)
	}
	if dev.Model != "NJ501-1300" || dev.Name != "NJ501-1300" {
		t.Errorf("型号/展示名 = %q/%q，期望都是 NJ501-1300", dev.Model, dev.Name)
	}
	if dev.Version != "1.14" {
		t.Errorf("版本 = %q，期望 1.14", dev.Version)
	}
	if !hasAttr(dev, "识别方式", "EtherNet/IP ListIdentity(0x0063)") {
		t.Errorf("识别方式应标注为 ListIdentity，实际 %+v", dev.Extra)
	}
	if !hasAttr(dev, "厂商号", "47") {
		t.Errorf("应标出厂商号，实际 %+v", dev.Extra)
	}
	if !hasAttr(dev, "序列号", "0x12345678") {
		t.Errorf("应标出序列号，实际 %+v", dev.Extra)
	}
}

// TestBuildDeviceFallsBackToSource 设备自述 0.0.0.0 时回落到数据报的源地址。
//
// 直接采信 0.0.0.0 会建出一台 host=0.0.0.0 的设备 —— 它连不上，
// 而在扫描结果里看着完全正常。
func TestBuildDeviceFallsBackToSource(t *testing.T) {
	id := fake.CIPIdentity{VendorID: 47, ProductName: "NJ501-1300", Port: 44818}
	from := &net.UDPAddr{IP: net.IPv4(10, 0, 0, 7), Port: 44818}

	dev, ok := buildDevice(cipIdentityOf(t, id), from)
	if !ok {
		t.Fatal("源地址可用，应能建成设备")
	}
	if dev.Host != "10.0.0.7" {
		t.Errorf("Host = %q，期望回落到源地址 10.0.0.7", dev.Host)
	}
}

// TestBuildDeviceNoAddress 自述地址为空且拿不到源地址时，这条 Identity 建不出设备。
func TestBuildDeviceNoAddress(t *testing.T) {
	id := fake.CIPIdentity{VendorID: 47, ProductName: "X", Port: 44818}
	if _, ok := buildDevice(cipIdentityOf(t, id), nil); ok {
		t.Fatal("没有地址就不该建出设备")
	}
	// 源地址是 0.0.0.0 时同样建不出来。
	zero := &net.UDPAddr{IP: net.IPv4zero, Port: 44818}
	if _, ok := buildDevice(cipIdentityOf(t, id), zero); ok {
		t.Fatal("源地址为 0.0.0.0 时不该建出设备")
	}
}

// TestBuildDeviceProductNameFallback 设备没自报产品名时给一个中性展示名，
// **而不是编造型号**，更不能因此不报这台设备。
func TestBuildDeviceProductNameFallback(t *testing.T) {
	id := fake.CIPIdentity{VendorID: 47, IP: net.IPv4(10, 0, 0, 9), Port: 44818}
	dev, ok := buildDevice(cipIdentityOf(t, id), nil)
	if !ok {
		t.Fatal("有地址就该建出设备，产品名缺失不是理由")
	}
	if dev.Name != "EtherNet/IP 设备" {
		t.Errorf("展示名 = %q，期望「EtherNet/IP 设备」", dev.Name)
	}
	if dev.Model != "" {
		t.Errorf("型号 = %q，设备没自报就该留空，不能编造", dev.Model)
	}
}

// cipIdentityOf 解析一条假身份，得到插件侧解析器的输出 —— 测试因此走的是
// 真实路径（fake 构造字节 → cipcore 解析 → buildDevice），而不是手搓结构体。
func cipIdentityOf(t *testing.T, id fake.CIPIdentity) cipcore.Identity {
	t.Helper()
	ids, err := cipcore.ParseListIdentity(fake.BuildListIdentityResponse(id))
	if err != nil {
		t.Fatalf("解析假身份的应答失败: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("应解析出 1 项，实际 %d 项", len(ids))
	}
	return ids[0]
}

// ==================== 探测：真实套接字 ====================

// TestProbeFindsOmronDevice 打一台真实的 ListIdentity 假设备。
func TestProbeFindsOmronDevice(t *testing.T) {
	srv, err := fake.NewOmronCIPDiscovery(fake.OmronNJ501("127.0.0.1", 44818))
	if err != nil {
		t.Fatalf("起假设备: %v", err)
	}
	defer srv.Close()

	devices, err := Probe(targetFor(t, srv.Addr(), false), nil, probeBudget)
	if err != nil {
		t.Fatalf("Probe 不应报错: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("应发现 1 台设备，实际 %d 台: %+v", len(devices), devices)
	}
	if devices[0].Host != "127.0.0.1" {
		t.Errorf("Host = %q，期望 127.0.0.1（设备自述的通信地址）", devices[0].Host)
	}
	if devices[0].Model != "NJ501-1300" {
		t.Errorf("型号 = %q，期望 NJ501-1300", devices[0].Model)
	}
}

// TestProbeFiltersOtherVendors 非欧姆龙的 EtherNet/IP 设备**刻意不上报**。
//
// 这不是过滤洁癖而是正确性要求：一键建出来的设备会用 Omron.CIP 去采，
// 而 Omron.CIP 说的是欧姆龙的 0x4C 数据表读取与变量寻址。把一台
// Allen-Bradley 报成"欧姆龙 PLC"，用户会建出一台永远采不到值的设备。
func TestProbeFiltersOtherVendors(t *testing.T) {
	// 厂商号 1 = Allen-Bradley（Rockwell），设备型号是 Logix。
	srv, err := fake.NewOmronCIPDiscovery(fake.CIPIdentity{
		VendorID:    1,
		DeviceType:  0x000E,
		ProductName: "1756-L83E",
		IP:          net.IPv4(127, 0, 0, 1),
		Port:        44818,
	})
	if err != nil {
		t.Fatalf("起假设备: %v", err)
	}
	defer srv.Close()

	devices, err := Probe(targetFor(t, srv.Addr(), false), nil, probeBudget)
	if err != nil {
		t.Fatalf("别的厂商不是错误: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("不该上报非欧姆龙的设备，实际 %+v", devices)
	}
}

// TestProbeMultipleDevicesFromOneBroadcast 一条广播应答可以带多台设备。
//
// 这是本协议与 FINS/S7/Modbus 形态上的根本差别：那三个一个目标一台设备，
// 这里一个目标可能产出整个网段的答案。
func TestProbeMultipleDevicesFromOneBroadcast(t *testing.T) {
	srv, err := fake.NewOmronCIPDiscovery(
		fake.OmronNJ501("192.168.1.50", 44818),
		fake.OmronNJ501("192.168.1.51", 44818),
		fake.OmronNJ501("192.168.1.52", 44818),
	)
	if err != nil {
		t.Fatalf("起假设备: %v", err)
	}
	defer srv.Close()

	devices, err := Probe(targetFor(t, srv.Addr(), true), nil, probeBudget)
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(devices) != 3 {
		t.Fatalf("应发现 3 台设备，实际 %d 台: %+v", len(devices), devices)
	}
	seen := map[string]bool{}
	for _, d := range devices {
		if seen[d.Host] {
			t.Errorf("设备 %s 被重复上报", d.Host)
		}
		seen[d.Host] = true
	}
}

// TestProbeReturnsNilWhenNothingListens 没人监听时返回 (nil, nil)。
//
// **"没扫到"不是错误**：目标不可达、跑着别的服务、或者压根没设备，都是扫描的
// 正常结果。把这类情况报成 error 会让一次 /24 扫描产生两百多条错误噪声。
func TestProbeReturnsNilWhenNothingListens(t *testing.T) {
	// 占一个 UDP 端口再放掉：loopback 上写过去会触发 ICMP 端口不可达。
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("占端口: %v", err)
	}
	addr := conn.LocalAddr().String()
	_ = conn.Close()

	devices, err := Probe(targetFor(t, addr, false), json.RawMessage(`{"timeoutMs":300}`), probeBudget)
	if err != nil {
		t.Fatalf("目标不可达不是错误，实际 %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("不该发现任何设备，实际 %+v", devices)
	}
}

// TestProbeReturnsNilForNonEtherNetIPService 端口上跑着别的 UDP 服务。
//
// 判据是"回的是不是自洽的 ListIdentity 应答"，不是"有没有应答" ——
// 否则任何一个会回包的 UDP 服务都会被报成 PLC。
func TestProbeReturnsNilForNonEtherNetIPService(t *testing.T) {
	srv, err := fake.NewUDPServer(func([]byte) []byte {
		return []byte("HTTP/1.1 400 Bad Request\r\n\r\n") // 一个"回包但不是 ListIdentity"的服务
	})
	if err != nil {
		t.Fatalf("起假服务: %v", err)
	}
	defer srv.Close()

	devices, err := Probe(targetFor(t, srv.Addr(), false), nil, probeBudget)
	if err != nil {
		t.Fatalf("不是 ListIdentity 不是错误: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("不该发现任何设备，实际 %+v", devices)
	}
}

// TestProbeRespectsBudget 探测必须在 Budget 内返回，哪怕用户把超时填得很大。
//
// 超预算会撞网关侧的 RPC 超时，并在 pluginhost 的卡死计数上累加，
// **连续几次会把整个插件进程连同它下面所有协议、所有设备一起重启**。
func TestProbeRespectsBudget(t *testing.T) {
	srv, err := fake.NewUDPServer(func([]byte) []byte { return nil }) // 只收不回
	if err != nil {
		t.Fatalf("起假服务: %v", err)
	}
	defer srv.Close()

	const budget = 1000 * time.Millisecond
	start := time.Now()
	devices, err := Probe(targetFor(t, srv.Addr(), false),
		json.RawMessage(`{"timeoutMs":60000}`), budget)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("不该发现任何设备，实际 %+v", devices)
	}
	if elapsed > budget+time.Second {
		t.Errorf("耗时 %v 超出预算 %v（+1s 余量）", elapsed, budget)
	}
}

// TestProbeBroadcastRespectsBudget 广播模式下"等第一个应答 + 安静窗口"也要在预算内。
func TestProbeBroadcastRespectsBudget(t *testing.T) {
	srv, err := fake.NewUDPServer(func([]byte) []byte { return nil })
	if err != nil {
		t.Fatalf("起假服务: %v", err)
	}
	defer srv.Close()

	const budget = 1200 * time.Millisecond
	start := time.Now()
	if _, err := Probe(targetFor(t, srv.Addr(), true),
		json.RawMessage(`{"timeoutMs":60000}`), budget); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if elapsed := time.Since(start); elapsed > budget+time.Second {
		t.Errorf("耗时 %v 超出预算 %v（+1s 余量）", elapsed, budget)
	}
}

// TestProbeRejectsNonIPv4Target 目标不是字面 IPv4 时直接跳过。
//
// 目标由网关从网段展开，永远是字面 IP。解析域名会引入一次可能阻塞的 DNS 查询，
// 那会突破预算。
func TestProbeRejectsNonIPv4Target(t *testing.T) {
	for _, host := range []string{"  ", "plc.example.com"} {
		devices, err := Probe(driver.ScanTarget{Host: host, Port: 44818}, nil, probeBudget)
		if err != nil {
			t.Errorf("目标 %q 不该报错: %v", host, err)
		}
		if len(devices) != 0 {
			t.Errorf("目标 %q 不该产出设备，实际 %+v", host, devices)
		}
	}
}

// TestProbeCapsDevices 单次探测上报的设备数有上限。
//
// 上限存在的意义不是性能而是**事故半径**：一条广播在大型网络上可能招来几百条
// 应答，而 pluginwire 写帧持插件进程的写锁 —— 一份巨大的应答会把该进程上
// 所有协议的应答一起头阻塞。
func TestProbeCapsDevices(t *testing.T) {
	addr := blastListener(t, maxScanDevices+10)

	devices, err := Probe(targetFor(t, addr, true), json.RawMessage(`{"timeoutMs":800}`), probeBudget)
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(devices) != maxScanDevices {
		t.Fatalf("设备数 = %d，期望被截到上限 %d", len(devices), maxScanDevices)
	}
}

// blastListener 起一个"收到 ListIdentity 就回 n 个数据报"的 UDP 监听器，
// 每个数据报是一台不同 IP 的设备。
//
// 用**多个数据报**而不是一个含 n 项的大帧：真机上每台设备各回自己那一包，
// 而一个含几十项的帧会超出 UDP 的读取缓冲 —— 那样测的就不是截断逻辑了。
func blastListener(t *testing.T, n int) string {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("起监听: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	go func() {
		buf := make([]byte, 1024)
		for {
			_, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return // 套接字已关闭
			}
			for i := 0; i < n; i++ {
				id := fake.OmronNJ501(fmt.Sprintf("10.0.%d.%d", i/250, i%250+1), 44818)
				resp := fake.BuildListIdentityResponse(id)
				if _, err := conn.WriteToUDP(resp, from); err != nil {
					return
				}
			}
		}
	}()
	return conn.LocalAddr().String()
}
