// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package s7

import (
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robinson/gos7"

	"iot-gateway/driver"
	"iot-gateway/testutil/fake"
)

// probeBudget 用例使用的探测预算，远大于本地假设备的往返耗时。
const probeBudget = 5 * time.Second

// hostPortOf 把 "127.0.0.1:port" 拆成 host 与端口号。
func hostPortOf(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("解析地址 %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("解析端口 %q: %v", portStr, err)
	}
	return host, port
}

// targetFor 把一个 "host:port" 地址串变成探测目标。
func targetFor(t *testing.T, addr string) driver.ScanTarget {
	t.Helper()
	host, port := hostPortOf(t, addr)
	return driver.ScanTarget{Host: host, Port: port}
}

// ==================== 选项解析 ====================

// TestParseScanOptions 选项解析：缺项静默回默认值，非法值报错。
//
// 与点位扫描的取舍正相反（那边缺项一律报错）——"没填机架/槽位"就是"按最常见的
// 两组试"，而"扫多少"没有合理的默认值。
func TestParseScanOptions(t *testing.T) {
	t.Run("空选项走默认", func(t *testing.T) {
		opt, err := parseScanOptions(nil)
		if err != nil {
			t.Fatalf("空选项不应报错: %v", err)
		}
		if len(opt.rackSlots) != len(defaultRackSlots) {
			t.Errorf("候选 = %v，期望默认 %v", opt.rackSlots, defaultRackSlots)
		}
		if opt.connectWait != defaultScanConnectTimeoutMs*time.Millisecond {
			t.Errorf("连接超时 = %v，期望 %dms", opt.connectWait, defaultScanConnectTimeoutMs)
		}
	})

	t.Run("字符串形态的超时也接受", func(t *testing.T) {
		// form-create 的部分控件产出字符串（"2000"）。声明成 int 会让
		// json.Unmarshal 整体报错 —— 不是那一个字段失效，而是整次扫描全失败。
		opt, err := parseScanOptions(json.RawMessage(`{"rackSlots":"0/1","connectTimeoutMs":"2000"}`))
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if opt.connectWait != 2*time.Second {
			t.Errorf("连接超时 = %v，期望 2s", opt.connectWait)
		}
	})

	t.Run("非法 JSON 报错", func(t *testing.T) {
		if _, err := parseScanOptions(json.RawMessage(`{`)); err == nil {
			t.Fatal("非法 JSON 应报错")
		}
	})

	t.Run("非法的候选表达式报错", func(t *testing.T) {
		// 写错的候选若被静默忽略，用户看到的是"某台 PLC 扫不到"，
		// 而排查方向会被引向网络 —— 必须报出来。
		if _, err := parseScanOptions(json.RawMessage(`{"rackSlots":"0-1"}`)); err == nil {
			t.Fatal("少一个斜杠的候选应报错，而不是被静默丢弃")
		}
	})
}

// TestParseRackSlots 候选表达式的解析与边界。
func TestParseRackSlots(t *testing.T) {
	t.Run("正常解析", func(t *testing.T) {
		got, err := parseRackSlots(" 0/1 , 0/2 ,1/3 ")
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		want := []rackSlot{{0, 1}, {0, 2}, {1, 3}}
		if len(got) != len(want) {
			t.Fatalf("候选 = %v，期望 %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("第 %d 个候选 = %v，期望 %v", i, got[i], want[i])
			}
		}
	})

	t.Run("空串回落到默认", func(t *testing.T) {
		got, err := parseRackSlots("   ")
		if err != nil {
			t.Fatalf("空串不该报错: %v", err)
		}
		if got != nil {
			t.Errorf("空串应返回 nil（由调用方回落到默认候选），实际 %v", got)
		}
	})

	t.Run("重复候选只保留一个", func(t *testing.T) {
		got, err := parseRackSlots("0/1,0/1,0/1")
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("候选 = %v，重复项应去重（每个候选要花掉一个超时）", got)
		}
	})

	t.Run("越界报错而不是钳制", func(t *testing.T) {
		// 钳过之后发出去的是一条**别的** TSAP：用户拿到的结果是"这一组试过了
		// 但没通"，与事实不符。gos7 的公式是 connectType<<8 + rack*0x20 + slot，
		// 机架占 3 位、槽位占 5 位，越界会串到连接类型字节上。
		for _, expr := range []string{"8/1", "0/32", "-1/0", "0/-1"} {
			if _, err := parseRackSlots(expr); err == nil {
				t.Errorf("%q 越界，应报错", expr)
			}
		}
	})

	t.Run("超过上限报错", func(t *testing.T) {
		expr := "0/1,0/2,0/3,0/4,0/5"
		if _, err := parseRackSlots(expr); err == nil {
			t.Errorf("%q 有 5 组候选，超过单次最多 %d 组，应报错", expr, maxScanRackSlots)
		}
	})

	t.Run("非数字报错", func(t *testing.T) {
		for _, expr := range []string{"a/1", "0/b", "0/", "/1"} {
			if _, err := parseRackSlots(expr); err == nil {
				t.Errorf("%q 不是合法的 rack/slot，应报错", expr)
			}
		}
	})
}

// ==================== 设备组装 ====================

// TestBuildDeviceUnidentified 认不出型号时也要如实报出设备，并说清为什么是空的。
//
// 与 Modbus 的 TestProbeTCPFindsDeviceWithoutFC43 同一条取舍：读不出标识不影响
// "发现了设备"，但用户得知道标识为什么空着。
func TestBuildDeviceUnidentified(t *testing.T) {
	dev := buildDevice("192.168.1.10", 102, rackSlot{0, 2}, gos7.S7CpuInfo{}, false)

	if dev.Host != "192.168.1.10" || dev.Port != 102 {
		t.Errorf("地址 = %s:%d", dev.Host, dev.Port)
	}
	if dev.Name != "S7 设备" {
		t.Errorf("展示名 = %q，期望「S7 设备」", dev.Name)
	}
	if dev.Vendor != "" || dev.Model != "" || dev.Version != "" {
		t.Errorf("认不出型号时三项都应为空，实际 vendor=%q model=%q version=%q",
			dev.Vendor, dev.Model, dev.Version)
	}
	if !hasAttr(dev, "机架/槽位", "0/2") {
		t.Errorf("应带上实际连上的机架/槽位，实际 %+v", dev.Extra)
	}
	if !hasAttrContains(dev, "识别方式", "S7 会话协商") {
		t.Errorf("应标注识别方式，实际 %+v", dev.Extra)
	}
}

// TestBuildDeviceIdentified 读出 CPU 标识时的组装。
func TestBuildDeviceIdentified(t *testing.T) {
	info := gos7.S7CpuInfo{
		ModuleTypeName: "CPU 1214C DC/DC/DC",
		ModuleName:     "IO-Controller",
		SerialNumber:   "S C-A1B2C3",
	}
	dev := buildDevice("10.0.0.5", 102, rackSlot{0, 1}, info, true)

	if dev.Model != "CPU 1214C DC/DC/DC" {
		t.Errorf("型号 = %q", dev.Model)
	}
	if dev.Name != "CPU 1214C DC/DC/DC" {
		t.Errorf("展示名 = %q，期望取型号", dev.Name)
	}
	// Vendor 一律留空：S7 协议与 SZL 都不携带厂商名，填 "Siemens" 是拿协议名
	// 冒充设备自述 —— 说 S7 的设备也不都是西门子（VIPA 等同样实现 S7 通信）。
	if dev.Vendor != "" {
		t.Errorf("厂商 = %q，S7 协议不回厂商名，不该猜一个", dev.Vendor)
	}
	if !hasAttr(dev, "序列号", "S C-A1B2C3") {
		t.Errorf("应带上序列号，实际 %+v", dev.Extra)
	}
	if !hasAttr(dev, "模块名称", "IO-Controller") {
		t.Errorf("应带上模块名称，实际 %+v", dev.Extra)
	}
}

// TestBuildDeviceProtocolJSON ProtocolJSON 必须能被本包自己的配置解析器吃下，
// 且落在目标上。
//
// 这条最容易错的地方是 rack/slot 的**类型**：s7ConfigRaw 把它们声明成 string，
// 写成数字会被 json.Unmarshal 静默丢弃，ParseS7Config 于是回落到默认的 0/1 ——
// 设备建出来即连不上，而表单上显示的机架/槽位看着完全正确。
func TestBuildDeviceProtocolJSON(t *testing.T) {
	dev := buildDevice("192.168.1.77", 102, rackSlot{0, 2}, gos7.S7CpuInfo{}, false)

	if !strings.Contains(dev.ProtocolJSON, `"rack":"0"`) ||
		!strings.Contains(dev.ProtocolJSON, `"slot":"2"`) {
		t.Fatalf("rack/slot 必须是**字符串**（s7ConfigRaw 里就是 string），实际 %s",
			dev.ProtocolJSON)
	}

	cfg, err := ParseS7Config(dev.ProtocolJSON)
	if err != nil {
		t.Fatalf("探测器给出的连接配置解析失败: %v（原文 %s）", err, dev.ProtocolJSON)
	}
	if cfg.Host != "192.168.1.77" || cfg.Port != 102 {
		t.Errorf("连接配置指向 %s:%d", cfg.Host, cfg.Port)
	}
	if cfg.Rack != 0 || cfg.Slot != 2 {
		t.Errorf("连接配置的机架/槽位 = %d/%d，期望 0/2 —— "+
			"写成数字会让这两个字段被静默丢弃", cfg.Rack, cfg.Slot)
	}
	if cfg.ConnectionType != ConnectionTypePG {
		t.Errorf("连接类型 = %d，期望 %d（PG）", cfg.ConnectionType, ConnectionTypePG)
	}
}

// ==================== 探测 ====================

// TestProbeTCPFindsDevice 打一台真实的 S7 假 PLC。
//
// fake.NewS7 实现了 TPKT/COTP 连接与 PDU 协商，但不实现 SZL —— 正好覆盖
// "S7 会话建立了、标识读不出来"这条路径（多数老设备与部分新固件就是如此）。
func TestProbeTCPFindsDevice(t *testing.T) {
	srv, err := fake.NewS7()
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

	d := devices[0]
	if d.Host != target.Host || d.Port != target.Port {
		t.Errorf("设备地址 = %s:%d，期望 %s:%d", d.Host, d.Port, target.Host, target.Port)
	}
	// 假 PLC 不实现 SZL，因此标识为空 —— 但它确实完成了 S7 会话协商，必须被报出来。
	if d.Name != "S7 设备" {
		t.Errorf("展示名 = %q，期望「S7 设备」", d.Name)
	}
	if !hasAttr(d, "机架/槽位", "0/1") {
		t.Errorf("第一组候选 0/1 应命中，实际 %+v", d.Extra)
	}

	// 生成的连接配置必须直接可用：拿它建出来的设备要能真的连上。
	cfg, err := ParseS7Config(d.ProtocolJSON)
	if err != nil {
		t.Fatalf("连接配置解析失败: %v（原文 %s）", err, d.ProtocolJSON)
	}
	if cfg.Host != target.Host || cfg.Port != target.Port {
		t.Errorf("连接配置 = %s:%d，期望 %s:%d", cfg.Host, cfg.Port, target.Host, target.Port)
	}
}

// TestProbeTCPReturnsNilWhenNothingListens 没人监听时返回 (nil, nil)。
//
// **"没扫到"不是错误**：目标不可达、跑着别的服务、或者压根没设备，都是扫描的
// 正常结果。把这类情况报成 error 会让一次 /24 扫描产生两百多条错误噪声。
func TestProbeTCPReturnsNilWhenNothingListens(t *testing.T) {
	// 占一个端口再放掉：loopback 上的连接会被立刻拒绝，用例不必等超时。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占端口: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	devices, err := ProbeTCP(targetFor(t, addr), nil, probeBudget)
	if err != nil {
		t.Fatalf("目标不可达不是错误，实际 %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("不该发现任何设备，实际 %+v", devices)
	}
}

// TestProbeTCPReturnsNilWhenNotS7 端口上有个"接了线就不说话"的服务。
//
// 这是防火墙 DROP、以及各类非 S7 服务在 102 上的等价形态。判据是"能不能完成
// S7 会话协商"，协商不出来就是**不是我的设备**，返回 (nil, nil) 而不是错误 ——
// 刻意不上报"102 端口有服务"，否则同样监听 102 的 HMI 面板会被报成 PLC。
func TestProbeTCPReturnsNilWhenNotS7(t *testing.T) {
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
				// 只读不写：客户端永远等不到应答，握手停在读那一步。
				_, _ = io.Copy(io.Discard, c)
			}()
		}
	}()

	devices, err := ProbeTCP(targetFor(t, ln.Addr().String()),
		json.RawMessage(`{"connectTimeoutMs":500}`), probeBudget)
	if err != nil {
		t.Fatalf("协商不出来不是错误，实际 %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("不该发现任何设备，实际 %+v", devices)
	}
}

// silentListener 起一个"收下连接就再也不说话"的监听器，返回它的地址与
// 已接受的连接数。
//
// 这是防火墙 DROP 与"端口开着但对面完不成握手"的等价形态，也是唯一能稳定驱动
// "协议层失败"这条分支的办法。连接数用于断言候选循环真的跑了几轮。
func silentListener(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("起监听: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var accepted atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go func() {
				defer c.Close()
				_, _ = io.Copy(io.Discard, c) // 只读不写：客户端永远等不到应答
			}()
		}
	}()
	return ln.Addr().String(), &accepted
}

// TestProbeTCPRespectsBudget 探测必须在 Budget 内返回，哪怕用户把连接超时填得很大。
//
// **这是本文件里最要紧的一条。** 一次 S7 握手最多要顺序做 5 次"等一个 Timeout"
// 的 socket 操作（拨号 / ISO 连接 / PDU 协商 / SZL 首帧 / SZL 续帧），
// 不按剩余预算反推每个候选的超时，最坏耗时就是 5 × connectTimeout × 候选数 ——
// 而超预算会撞网关侧的 RPC 超时，并在 pluginhost 的卡死计数上累加，
// **连续几次会把整个插件进程连同它下面所有协议、所有设备一起重启**。
func TestProbeTCPRespectsBudget(t *testing.T) {
	addr, _ := silentListener(t)

	const budget = 1200 * time.Millisecond
	start := time.Now()
	devices, err := ProbeTCP(targetFor(t, addr),
		// 故意填一个远大于预算的超时：正确的实现会把它按剩余预算压回去。
		json.RawMessage(`{"connectTimeoutMs":30000}`), budget)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("协商超时不是错误: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("不该发现任何设备，实际 %+v", devices)
	}
	// 不按剩余预算反推的话这里会是 2 个候选 × 30s。留 800ms 给调度抖动，
	// 仍然远小于任何"没被压回去"的量级。
	if limit := budget + 800*time.Millisecond; elapsed > limit {
		t.Errorf("耗时 %v 超出预算 %v（上限 %v）—— "+
			"每个候选的超时必须从剩余预算里反推，否则用户填一个大超时就能打破这条承诺",
			elapsed, budget, limit)
	}
}

// TestProbeTCPTriesNextRackSlot 协议层失败时要接着试下一组候选，TCP 层失败时要收手。
//
// 这两条方向相反，各自都有明确的坏结果：
//   - 不试下一组 → S7-300/400（0/2）永远扫不到，而用户看不出是参数问题还是网络问题；
//   - TCP 层失败还硬试 → 一个 /24 里绝大多数地址都不可达，耗时白白乘以候选数。
//
// 判据是"监听器接了几条连接"：协议层失败应当接满 len(defaultRackSlots) 条。
func TestProbeTCPTriesNextRackSlot(t *testing.T) {
	t.Run("协议层失败：逐组试完", func(t *testing.T) {
		addr, accepted := silentListener(t)

		devices, err := ProbeTCP(targetFor(t, addr),
			// 超时给足，让每一组都能走到"等应答超时"那一步。
			json.RawMessage(`{"connectTimeoutMs":300}`), probeBudget)
		if err != nil {
			t.Fatalf("协商超时不是错误: %v", err)
		}
		if len(devices) != 0 {
			t.Fatalf("不该发现任何设备，实际 %+v", devices)
		}
		if got := int(accepted.Load()); got != len(defaultRackSlots) {
			t.Errorf("监听器接了 %d 条连接，期望 %d 条 —— "+
				"协议层失败必须换下一组候选，否则机架/槽位不是第一组的 PLC 永远扫不到",
				got, len(defaultRackSlots))
		}
	})

	t.Run("TCP 层失败：不再白试", func(t *testing.T) {
		// 占一个端口再放掉：loopback 上的连接会被立刻拒绝，即 opErr.Op == "dial"。
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("占端口: %v", err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()

		// 给足候选，并让超时大到"如果每组都试一遍"会明显变慢。
		opts := json.RawMessage(`{"rackSlots":"0/1,0/2,0/3,0/4","connectTimeoutMs":2000}`)
		start := time.Now()
		devices, err := ProbeTCP(targetFor(t, addr), opts, probeBudget)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("目标不可达不是错误: %v", err)
		}
		if len(devices) != 0 {
			t.Fatalf("不该发现任何设备，实际 %+v", devices)
		}
		// 拨号被立刻拒绝，所以四组都试也只是几百微秒；这里真正的判据是行为而非耗时，
		// 断言一个宽松上界，挡住"每组都等满 2s 超时"那种回归。
		if elapsed > 1500*time.Millisecond {
			t.Errorf("耗时 %v —— 拨号失败后应立刻放弃该目标，"+
				"可达性与机架/槽位无关，逐组重试只会让整次扫描的耗时乘以候选数", elapsed)
		}
	})
}

// TestProbeTCPRejectsBadOptions 选项本身有问题时必须报错，而不是静默返回空。
//
// 那是用户的输入错误，值得原样报出来让前端显示 —— 一句"没扫到设备"
// 会让用户去查网络，而真正的问题是他写错了候选表达式。
func TestProbeTCPRejectsBadOptions(t *testing.T) {
	devices, err := ProbeTCP(driver.ScanTarget{Host: "127.0.0.1", Port: 102},
		json.RawMessage(`{"rackSlots":"0-1"}`), probeBudget)
	if err == nil {
		t.Fatal("非法的候选表达式应报错")
	}
	if devices != nil {
		t.Errorf("报错时不该带结果，实际 %+v", devices)
	}
}

// ==================== 测试辅助 ====================

// hasAttr 结果里是否存在指定名字与取值的附加属性。
func hasAttr(dev driver.DiscoveredDevice, name, value string) bool {
	for _, a := range dev.Extra {
		if a.Name == name && a.Value == value {
			return true
		}
	}
	return false
}

// hasAttrContains 结果里是否存在指定名字、取值包含子串的附加属性。
func hasAttrContains(dev driver.DiscoveredDevice, name, sub string) bool {
	for _, a := range dev.Extra {
		if a.Name == name && strings.Contains(a.Value, sub) {
			return true
		}
	}
	return false
}
