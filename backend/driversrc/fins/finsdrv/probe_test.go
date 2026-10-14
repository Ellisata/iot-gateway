// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/testutil/fake"
)

// probeBudget 用例里的探测预算。远大于 loopback 上的往返耗时，
// 因此它只对"必须等满超时"的那几条路径起作用。
const probeBudget = 3 * time.Second

// targetFor 把一个 "host:port" 地址转成 ScanTarget。
func targetFor(t *testing.T, addr string) driver.ScanTarget {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("拆分地址 %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("端口 %q 不是数字: %v", portStr, err)
	}
	return driver.ScanTarget{Host: host, Port: port}
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
		nodes   string // 逗号分隔的期望候选；"" 表示按 IP 末段推导
		timeout time.Duration
		wantErr bool
	}{
		{"默认值", ``, "", defaultScanTimeoutMs * time.Millisecond, false},
		{"空对象", `{}`, "", defaultScanTimeoutMs * time.Millisecond, false},
		{"节点号候选", `{"nodes":"10, 20 ,30"}`, "10,20,30", defaultScanTimeoutMs * time.Millisecond, false},
		{"超时是字符串", `{"timeoutMs":"800"}`, "", 800 * time.Millisecond, false},
		{"超时是数字", `{"timeoutMs":800}`, "", 800 * time.Millisecond, false},
		{"超时是0回默认", `{"timeoutMs":0}`, "", defaultScanTimeoutMs * time.Millisecond, false},
		// 认不出的超时静默回默认值：这个值错了的表现只是"扫得慢"，
		// 不该让整次扫描失败。
		{"超时认不出", `{"timeoutMs":"很快"}`, "", defaultScanTimeoutMs * time.Millisecond, false},
		// 节点号相反：写错的候选会被悄悄忽略，用户看到的是"某台 PLC 扫不到"，
		// 而原因指向的是网络 —— 必须报错。
		{"节点号非数字", `{"nodes":"abc"}`, "", 0, true},
		{"节点号越界", `{"nodes":"300"}`, "", 0, true},
		{"节点号含0", `{"nodes":"0"}`, "", 0, true},
		{"选项不是JSON", `{`, "", 0, true},
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
			if got := nodesString(opt.nodes); got != c.nodes {
				t.Errorf("nodes = %q，期望 %q", got, c.nodes)
			}
		})
	}
}

// nodesString 把候选渲染成 "10,20,30"，便于逐字断言。
func nodesString(nodes []byte) string {
	if len(nodes) == 0 {
		return ""
	}
	out := ""
	for i, n := range nodes {
		if i > 0 {
			out += ","
		}
		out += strconv.Itoa(int(n))
	}
	return out
}

func TestParseScanNodes(t *testing.T) {
	cases := []struct {
		expr    string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"  ", "", false},
		{"10", "10", false},
		{"10,20", "10,20", false},
		// 重复的候选只试一次，别白花一个超时。
		{"10,10,20", "10,20", false},
		{"1,254", "1,254", false},
		{"abc", "", true},
		{"10,abc", "", true},
		{"0", "", true},
		{"255", "", true},
		{"-1", "", true},
		{"1,2,3,4,5", "", true}, // 超过 maxScanNodes
	}
	for _, c := range cases {
		got, err := parseScanNodes(c.expr)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseScanNodes(%q) 应报错", c.expr)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseScanNodes(%q) 不该报错: %v", c.expr, err)
			continue
		}
		if s := nodesString(got); s != c.want {
			t.Errorf("parseScanNodes(%q) = %q，期望 %q", c.expr, s, c.want)
		}
	}
}

// TestScanCandidates 未填节点号时只有一个候选，且**就是采集时用的那个推导值**。
//
// 这条一致性是整个扫描功能"扫得到 = 采得到"的来源：DA1 的推导规则在探测与
// 采集之间只能有一份。
func TestScanCandidates(t *testing.T) {
	opt, err := parseScanOptions(nil)
	if err != nil {
		t.Fatal(err)
	}

	nodes, explicit := opt.candidates("192.168.1.50")
	if explicit {
		t.Error("未填候选时 explicit 必须为 false —— 它决定要不要把推导值写进设备配置")
	}
	if len(nodes) != 1 || nodes[0] != 50 {
		t.Errorf("候选 = %v，期望 [50]（IP 末段）", nodes)
	}

	// 推导不出来时（域名、末段为 0/255）回落到 0，与 resolveDstNode 一致。
	nodes, _ = opt.candidates("plc.example.com")
	if len(nodes) != 1 || nodes[0] != 0 {
		t.Errorf("非 IPv4 主机的候选 = %v，期望 [0]", nodes)
	}

	withNodes, err := parseScanOptions(json.RawMessage(`{"nodes":"7,8"}`))
	if err != nil {
		t.Fatal(err)
	}
	nodes, explicit = withNodes.candidates("192.168.1.50")
	if !explicit {
		t.Error("填了候选时 explicit 必须为 true")
	}
	if len(nodes) != 2 || nodes[0] != 7 || nodes[1] != 8 {
		t.Errorf("候选 = %v，期望 [7 8] —— 用户填的候选优先于推导值", nodes)
	}
}

// ==================== 设备组装 ====================

// TestBuildFINSDeviceProtocolJSON 生成的连接配置必须能被**插件自己的配置解析器**
// 吃下，且租出的地址与端口与探测到的一致。
//
// 这条是本文件里最要紧的一条：一份键名写错、或把 dstNode 写成数字的 JSON
// 会让「扫描 → 一键建设备」建出来的设备连不上，而**两边都不报错** ——
// dstNode 声明的是 string，写成数字会被 json.Unmarshal 静默丢弃、
// 回落到"按 IP 末段推导"，表单上看着完全正常。
func TestBuildFINSDeviceProtocolJSON(t *testing.T) {
	const host = "192.168.1.50"

	// 未填候选：不写 dstNode，设备沿用与采集一致的推导。
	dev := buildFINSDevice(host, 9600, 50, false, TransportUDP, "CJ2M-CPU31")
	cfg, err := ParseFINSConfig(dev.ProtocolJSON)
	if err != nil {
		t.Fatalf("连接配置解析失败: %v（原文 %s）", err, dev.ProtocolJSON)
	}
	if cfg.Host != host || cfg.Port != 9600 {
		t.Errorf("配置 = %s:%d，期望 %s:9600", cfg.Host, cfg.Port, host)
	}
	if cfg.DstNode != 50 {
		t.Errorf("DstNode = %d，期望 50（按 IP 末段推导）", cfg.DstNode)
	}
	if dev.UnitID != 50 {
		t.Errorf("UnitID = %d，期望 50（FINS 节点号就是这个协议的站地址）", dev.UnitID)
	}
	if dev.Model != "CJ2M-CPU31" || dev.Name != "CJ2M-CPU31" {
		t.Errorf("型号/展示名 = %q/%q，期望都是 CJ2M-CPU31", dev.Model, dev.Name)
	}
	if dev.Vendor != "" {
		t.Errorf("Vendor = %q，必须留空 —— FINS 应答里没有厂商字段，填 Omron "+
			"是拿协议名冒充设备自述", dev.Vendor)
	}

	// 填了候选：命中的那个必须**写回**配置，否则用户填的候选白填。
	dev = buildFINSDevice(host, 9600, 7, true, TransportTCP, "")
	cfg, err = ParseFINSConfig(dev.ProtocolJSON)
	if err != nil {
		t.Fatalf("连接配置解析失败: %v（原文 %s）", err, dev.ProtocolJSON)
	}
	if cfg.DstNode != 7 {
		t.Errorf("DstNode = %d，期望 7 —— 用户填的候选命中了就必须写回，"+
			"否则一键建出来的设备会退回推导值、连不上", cfg.DstNode)
	}
	if dev.Name != "FINS 设备" {
		t.Errorf("读不出型号时展示名 = %q，期望「FINS 设备」", dev.Name)
	}
	if !hasAttr(dev, "传输", "FINS/TCP") {
		t.Errorf("结果里应标出传输层，实际 %+v", dev.Extra)
	}
	if !hasAttr(dev, "识别方式", "读到 DM 区数据(0x0101)；设备未回控制器数据（多为该型号不支持 0x0501）") {
		t.Errorf("识别方式应如实标注，实际 %+v", dev.Extra)
	}
}

// ==================== 错误分类 ====================

// TestIsAddressEndCode 只有"与地址有关"的结束码才允许用来推断"这个区到此为止"。
//
// 判错的代价是不对称的：把"节点忙"当成地址不存在，会把一份正常的点位清单
// 从中间截断，而用户完全看不出为什么。
func TestIsAddressEndCode(t *testing.T) {
	cases := []struct {
		code uint16
		want bool
		why  string
	}{
		{0x0401, true, "不可执行：地址范围越界"},
		{0x2102, true, "数据不可更改类"},
		{0x0105, false, "本机节点错误：与地址无关，且可恢复"},
		{0x0204, false, "目标节点忙：可恢复"},
		{0x0205, false, "目标节点响应超时：可恢复"},
		{0x0301, false, "PLC 自身报错"},
	}
	for _, c := range cases {
		got := isAddressEndCode(newEndCodeError(c.code))
		if got != c.want {
			t.Errorf("isAddressEndCode(0x%04X) = %v，期望 %v（%s）", c.code, got, c.want, c.why)
		}
	}
	// 非结束码错误（网络/超时）一律不算地址问题。
	if isAddressEndCode(errors.New("fins udp: read timeout")) {
		t.Error("普通错误不该被判成地址问题")
	}
}

// ==================== 探测：真实套接字 ====================

// TestProbeUDPFindsDevice 打一台真实的 FINS/UDP 假 PLC。
func TestProbeUDPFindsDevice(t *testing.T) {
	srv, err := fake.NewFINSUDP()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	target := targetFor(t, srv.Addr())
	devices, err := ProbeUDP(target, nil, probeBudget)
	if err != nil {
		t.Fatalf("ProbeUDP 不应报错: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("应发现 1 台设备，实际 %d 台: %+v", len(devices), devices)
	}
	d := devices[0]
	if d.Host != target.Host || d.Port != target.Port {
		t.Errorf("设备地址 = %s:%d，期望 %s:%d", d.Host, d.Port, target.Host, target.Port)
	}
	// 假 PLC 会回控制器数据（0x0501），因此型号应被识别出来。
	if d.Model != "CJ2M-CPU31" {
		t.Errorf("型号 = %q，期望 CJ2M-CPU31（0x0501 响应的前 20 字节）", d.Model)
	}
	if !hasAttr(d, "识别方式", "读控制器数据(0x0501)") {
		t.Errorf("识别方式应标注为控制器数据读取，实际 %+v", d.Extra)
	}
}

// TestProbeTCPFindsDevice 打一台真实的 FINS/TCP 假 PLC（含连接握手）。
func TestProbeTCPFindsDevice(t *testing.T) {
	srv, err := fake.NewFINSTCP()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	target := targetFor(t, srv.Addr())
	devices, err := ProbeTCP(target, nil, probeBudget)
	if err != nil {
		t.Fatalf("ProbeTCP 不应报错: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("应发现 1 台设备，实际 %d 台: %+v", len(devices), devices)
	}
	if devices[0].Model != "CJ2M-CPU31" {
		t.Errorf("型号 = %q，期望 CJ2M-CPU31", devices[0].Model)
	}
}

// TestProbeReturnsNilWhenNothingListens 没人监听时返回 (nil, nil)。
//
// **"没扫到"不是错误**：目标不可达、跑着别的服务、或者压根没设备，都是扫描的
// 正常结果。把这类情况报成 error 会让一次 /24 扫描产生两百多条错误噪声。
func TestProbeReturnsNilWhenNothingListens(t *testing.T) {
	// 占一个端口再放掉：loopback 上会被立刻拒绝，用例不必等超时。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占端口: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	target := targetFor(t, addr)

	for name, probe := range map[string]func(driver.ScanTarget, json.RawMessage, time.Duration) ([]driver.DiscoveredDevice, error){
		"TCP": ProbeTCP,
		"UDP": ProbeUDP,
	} {
		devices, err := probe(target, nil, probeBudget)
		if err != nil {
			t.Errorf("%s：目标不可达不是错误，实际 %v", name, err)
		}
		if len(devices) != 0 {
			t.Errorf("%s：不该发现任何设备，实际 %+v", name, devices)
		}
	}
}

// TestProbeReturnsNilForSilentUDPTarget 端口上有个"收下数据报就不说话"的服务。
//
// 这是防火墙 DROP、以及各类非 FINS 服务在 9600 上的等价形态。判据是"能不能读到
// 一次自洽的 FINS 应答"，读不到就是**不是我的设备**，返回 (nil, nil) 而不是
// 错误 —— 刻意不上报"9600 端口有服务"，否则串口服务器、SCADA 网关都会被报成 PLC。
func TestProbeReturnsNilForSilentUDPTarget(t *testing.T) {
	srv, err := fake.NewUDPServer(func([]byte) []byte { return nil }) // 只收不回
	if err != nil {
		t.Fatalf("起假服务: %v", err)
	}
	defer srv.Close()

	target := targetFor(t, srv.Addr())
	start := time.Now()
	devices, err := ProbeUDP(target, json.RawMessage(`{"timeoutMs":300}`), probeBudget)
	if err != nil {
		t.Fatalf("不应答不是错误，实际 %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("不该发现任何设备，实际 %+v", devices)
	}
	// 必须靠超时止损，而不是干等：一次探测只有一次"发帧-等应答"。
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("静默目标耗时 %v，超时没有生效", elapsed)
	}
}

// TestProbeTCPReturnsNilWhenNotFINS 端口上有个"接了线就不说话"的服务（TCP 侧）。
func TestProbeTCPReturnsNilWhenNotFINS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("起监听: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return // 监听器已关闭
			}
			go func() {
				defer c.Close()
				// 只读不写：客户端永远等不到连接响应，握手停在读那一步。
				_, _ = io.Copy(io.Discard, c)
			}()
		}
	}()

	target := targetFor(t, ln.Addr().String())
	devices, err := ProbeTCP(target, json.RawMessage(`{"timeoutMs":300}`), probeBudget)
	if err != nil {
		t.Fatalf("握手协商不出来不是错误，实际 %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("不该发现任何设备，实际 %+v", devices)
	}
}

// TestProbeTreatsEndCodeAsDevice 设备回了结束码错误，照样要报成设备。
//
// 结束码非 0 意味着**设备应答了**，只是拒绝了这次读取（区域不存在、地址越界、
// 单元号不对……）。把它当成"没扫到"，会让一台真实存在的 PLC 因为一句不认识的
// 结束码而消失，而用户在扫描页上完全看不出发生过什么 —— 与 Modbus 那边
// "不支持读设备标识的设备照样报出来"是同一条取舍。
func TestProbeTreatsEndCodeAsDevice(t *testing.T) {
	srv, err := fake.NewUDPServer(endCodeResponder(0x2108))
	if err != nil {
		t.Fatalf("起假设备: %v", err)
	}
	defer srv.Close()

	devices, err := ProbeUDP(targetFor(t, srv.Addr()), nil, probeBudget)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("回了结束码的设备也必须被报出来，实际 %+v", devices)
	}
	// 结束码那条分支不读型号：设备已经明确拒绝了一次读取，
	// 紧接着再发一条更冷门的命令只会多花一个超时。
	if devices[0].Model != "" {
		t.Errorf("型号 = %q，期望留空", devices[0].Model)
	}
}

// endCodeResponder 一个对任何 FINS 命令都回指定结束码的假设备。
func endCodeResponder(code uint16) func([]byte) []byte {
	return func(req []byte) []byte {
		if len(req) < 12 {
			return nil
		}
		resp := append([]byte(nil), req[:10]...) // 回显 FINS 头（含 SID）
		resp = append(resp, req[10], req[11])    // 命令回显
		return append(resp, byte(code>>8), byte(code))
	}
}

// TestProbeTriesNextNode 第一个节点号没应答时继续试下一个候选。
//
// 这正是"节点号候选"存在的理由：PLC 的节点号被改成了非 IP 末段时，
// 推导值必然是错的，而错的表现就是"没有应答"，与"这里没有设备"无法区分。
func TestProbeTriesNextNode(t *testing.T) {
	// 只对 DA1 == 7 的命令应答。DA1 是 FINS 头的第 5 字节（index 4）。
	srv, err := fake.NewUDPServer(func(req []byte) []byte {
		if len(req) < 12 || req[4] != 7 {
			return nil
		}
		return buildFakeReadResponse(req)
	})
	if err != nil {
		t.Fatalf("起假设备: %v", err)
	}
	defer srv.Close()

	target := targetFor(t, srv.Addr())
	devices, err := ProbeUDP(target, json.RawMessage(`{"nodes":"5,6,7","timeoutMs":300}`), probeBudget)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("第三个候选应命中，实际 %+v", devices)
	}
	if devices[0].UnitID != 7 {
		t.Errorf("UnitID = %d，期望 7（命中的那个候选）", devices[0].UnitID)
	}
	// 用户填的候选命中了，必须写回配置 —— 这是探测才回答得出来的事实。
	cfg, err := ParseFINSConfig(devices[0].ProtocolJSON)
	if err != nil {
		t.Fatalf("连接配置解析失败: %v（原文 %s）", err, devices[0].ProtocolJSON)
	}
	if cfg.DstNode != 7 {
		t.Errorf("DstNode = %d，期望 7", cfg.DstNode)
	}
}

// buildFakeReadResponse 构造一条成功的内存区读取应答（零值数据）。
// 与 testutil/fake 里那份同形，这里只需最小可用版本。
func buildFakeReadResponse(req []byte) []byte {
	resp := append([]byte(nil), req[:10]...)
	resp = append(resp, 0x01, 0x01)
	resp = append(resp, 0x00, 0x00)
	return append(resp, 0x00, 0x00) // 一个字
}

// TestProbeRespectsBudget 探测必须在 Budget 内返回，哪怕用户把超时填得很大。
//
// **这是本文件里最要紧的一条。** 一次探测每个节点号候选要完整地走一遍
// "发帧-等应答"，不按剩余预算反推每个候选的超时，最坏耗时就是
// 超时 × 候选数 —— 而超预算会撞网关侧的 RPC 超时，并在 pluginhost 的卡死计数上
// 累加，**连续几次会把整个插件进程连同它下面所有协议、所有设备一起重启**。
func TestProbeRespectsBudget(t *testing.T) {
	srv, err := fake.NewUDPServer(func([]byte) []byte { return nil }) // 永远不应答
	if err != nil {
		t.Fatalf("起假服务: %v", err)
	}
	defer srv.Close()

	const budget = 1200 * time.Millisecond
	target := targetFor(t, srv.Addr())
	// 故意填一个远大于预算的超时，并给满 4 个候选：正确的实现会把它按剩余预算压回去。
	start := time.Now()
	devices, err := ProbeUDP(target, json.RawMessage(`{"timeoutMs":60000,"nodes":"1,2,3,4"}`), budget)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("不该发现任何设备，实际 %+v", devices)
	}
	// 留一点调度余量：断言的是"没有跑满 4 × 60s"，不是毫秒级精度。
	if elapsed > budget+time.Second {
		t.Errorf("耗时 %v 超出预算 %v（+1s 余量）", elapsed, budget)
	}
}

// TestProbeSkipsBroadcastTarget 收到广播目标时什么都不做。
//
// 本协议的 Discovery 声明的是 unicast，引擎不会派发广播目标。真收到就意味着
// 有人在插件或引擎那边改错了 —— 朝一个子网广播地址发一帧 DA1=0 的读取命令，
// 等于让整个网段的设备同时回一条应答，而我们对裸数据报的应答方毫无校验手段。
func TestProbeSkipsBroadcastTarget(t *testing.T) {
	srv, err := fake.NewFINSUDP()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	target := targetFor(t, srv.Addr())
	target.Broadcast = true
	// 就算对面真的有一台能应答的 PLC，也不该产出任何结果。
	devices, err := ProbeUDP(target, nil, probeBudget)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("广播目标应被跳过，实际 %+v", devices)
	}
}

// TestProbeRejectsBadOptions 选项本身有问题时必须报错。
//
// 与"没扫到"严格区分：那是用户的输入错误，值得原样报出来让前端显示；
// 报成"没扫到"会让用户以为网段里没有设备。
func TestProbeRejectsBadOptions(t *testing.T) {
	target := driver.ScanTarget{Host: "127.0.0.1", Port: 9600}
	if _, err := ProbeUDP(target, json.RawMessage(`{"nodes":"abc"}`), probeBudget); err == nil {
		t.Error("非法节点号候选应报错")
	}
	if _, err := ProbeTCP(target, json.RawMessage(`{"nodes":"0"}`), probeBudget); err == nil {
		t.Error("越界节点号应报错")
	}
}

// TestProbeEmptyHost 空主机地址直接返回 (nil, nil)，不去构造请求。
func TestProbeEmptyHost(t *testing.T) {
	devices, err := ProbeUDP(driver.ScanTarget{Host: "  ", Port: 9600}, nil, probeBudget)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("不该发现任何设备，实际 %+v", devices)
	}
}

// ==================== 控制器型号读取 ====================

// TestReadControllerModel 型号来自 0x0501 响应载荷的前 20 字节，且必须是可打印 ASCII。
//
// 后半条同时兜住了"命令码或布局与设备实际不符"的情形：那时拿到的是一串二进制，
// 宁可留空也不要把乱码当型号展示给用户。
func TestReadControllerModel(t *testing.T) {
	model := func(payload []byte) string {
		t := &stubCommander{payload: payload}
		got, ok := readControllerModel(t)
		if !ok {
			return ""
		}
		return got
	}

	// 正常的 20 字节型号，NUL 补齐。
	good := make([]byte, 20)
	copy(good, "CJ2M-CPU31")
	if got := model(good); got != "CJ2M-CPU31" {
		t.Errorf("型号 = %q，期望 CJ2M-CPU31", got)
	}

	// 尾部用空格补齐的写法同样要能识别。
	spaced := make([]byte, 20)
	for i := range spaced {
		spaced[i] = ' '
	}
	copy(spaced, "NJ501-1300")
	if got := model(spaced); got != "NJ501-1300" {
		t.Errorf("型号 = %q，期望 NJ501-1300", got)
	}

	// 全 NUL：读通了但没有型号，与"读失败"对用户是同一件事。
	if got := model(make([]byte, 20)); got != "" {
		t.Errorf("全 NUL 载荷得到 %q，期望空", got)
	}

	// 二进制乱码：布局对不上时的形态，宁可留空。
	garbage := make([]byte, 20)
	for i := range garbage {
		garbage[i] = byte(i + 1)
	}
	if got := model(garbage); got != "" {
		t.Errorf("二进制载荷得到 %q，期望空（不可打印 ASCII 的型号一律不采纳）", got)
	}

	// 载荷太短。
	if got := model([]byte("CJ2M")); got != "" {
		t.Errorf("短载荷得到 %q，期望空", got)
	}
}

// stubCommander 一个只会回固定载荷的传输层，用来单测型号解析。
//
// 它同时实现了 finsTransport 与 finsCommander，因此 readControllerModel
// 的类型断言会命中 —— 这也是给"传输层不支持该命令"留的口子（串口与
// HostLinkTCP 就没有实现它）。
type stubCommander struct {
	payload []byte
	err     error
}

func (s *stubCommander) Read(finsArea, uint16, uint16) ([]byte, error) { return nil, nil }
func (s *stubCommander) Command(uint16, []byte) ([]byte, error)        { return s.payload, s.err }
func (s *stubCommander) IsConnected() bool                             { return true }
func (s *stubCommander) Close() error                                  { return nil }

// TestReadControllerModelNeedsCommander 没有实现 finsCommander 的传输层返回"未识别"，
// 而不是报错。
//
// 串口与 Host Link over TCP 就是这种形态（Host Link 是另一套封帧，没有实现
// 就没有猜测的余地）。它必须表现为"型号留空"，而不是让整次探测失败 ——
// 那会把一台真实存在的 PLC 报成扫不到。
func TestReadControllerModelNeedsCommander(t *testing.T) {
	plain := &mockTransport{fn: func(finsArea, uint16, uint16) ([]byte, error) { return nil, nil }}
	if got, ok := readControllerModel(plain); ok || got != "" {
		t.Errorf("未实现 Command 的传输层应返回未识别，实际 (%q, %v)", got, ok)
	}
}
