// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package devicescan

import (
	"testing"

	"iot-gateway/driver"
)

// 目标展开是**纯函数**，所以广播/双模这几条可以直接断言，不必经 Engine。
// 经 Engine 的那几条（目标真的被派发给插件、重复设备被丢掉）见 engine_test.go。

func TestNormalizeTargetMode(t *testing.T) {
	cases := []struct {
		in   driver.TargetMode
		want driver.TargetMode
	}{
		{"", driver.TargetModeUnicast},
		{driver.TargetModeUnicast, driver.TargetModeUnicast},
		{driver.TargetModeBroadcast, driver.TargetModeBroadcast},
		{driver.TargetModeBoth, driver.TargetModeBoth},
		// 无法识别的值按 unicast 处理，而不是"两种都不产出"（那会让一次扫描
		// 一个目标都没有、直接报 ErrNoTargets，用户完全看不出是哪个环节的问题）。
		{"multicast", driver.TargetModeUnicast},
	}
	for _, c := range cases {
		if got := normalizeTargetMode(c.in); got != c.want {
			t.Errorf("normalizeTargetMode(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

// TestExpandTargetsUnicastIsUnchanged 单播展开的行为必须与引入 TargetMode 之前逐字一致。
//
// 这是本改动最容易出的回归：路径没变但多了一层分支，而全部既有协议
// （Modbus / S7 / FINS 的单播部分）都靠这段展开 —— 它们扫不到设备时，
// 没人会想到是"目标少了一个"。
func TestExpandTargetsUnicastIsUnchanged(t *testing.T) {
	targets, requested, truncated := expandTargets(testSubnets(), 502, 1024, driver.TargetModeUnicast)
	if truncated {
		t.Fatal("远未触顶却报告截断")
	}
	if requested != testHosts || len(targets) != testHosts {
		t.Fatalf("requested=%d len(targets)=%d，都应为 %d", requested, len(targets), testHosts)
	}
	// 网络地址 .0 与广播地址 .7 都不是主机；本机地址 .1 照扫。
	want := map[string]bool{"10.0.0.1": true, "10.0.0.2": true, "10.0.0.3": true,
		"10.0.0.4": true, "10.0.0.5": true, "10.0.0.6": true}
	for _, tg := range targets {
		if !want[tg.Host] {
			t.Errorf("出现了不该有的目标 %q", tg.Host)
		}
		if tg.Broadcast {
			t.Errorf("单播目标 %q 被标成了广播", tg.Host)
		}
		if tg.LocalIP != testLocalIP {
			t.Errorf("目标 %q 的 LocalIP = %q，期望 %q", tg.Host, tg.LocalIP, testLocalIP)
		}
		if tg.Port != 502 {
			t.Errorf("目标 %q 的端口 = %d，期望 502", tg.Host, tg.Port)
		}
	}
}

// TestExpandTargetsBroadcast 广播模式只产出子网广播地址，一个网段一个。
//
// 这就是 TargetMode 存在的全部理由：EtherNet/IP 的 ListIdentity 一次广播能
// 拿回整个网段的答案，逐主机单播只是把同一个问题问 254 遍。
func TestExpandTargetsBroadcast(t *testing.T) {
	targets, requested, truncated := expandTargets(testSubnets(), 44818, 1024, driver.TargetModeBroadcast)
	if truncated {
		t.Fatal("远未触顶却报告截断")
	}
	if requested != 1 || len(targets) != 1 {
		t.Fatalf("requested=%d len(targets)=%d，都应为 1（一个网段一个广播目标）", requested, len(targets))
	}
	tg := targets[0]
	if tg.Host != "10.0.0.7" {
		t.Errorf("广播目标 = %q，期望子网广播地址 10.0.0.7（不是 255.255.255.255）", tg.Host)
	}
	if !tg.Broadcast {
		t.Error("广播目标必须带 Broadcast 标记，插件靠它决定要不要绑源地址")
	}
	// LocalIP 是广播的源地址绑定：多网卡机器上不绑就只会从默认路由那张网卡
	// 出去，一台设备也发现不了。
	if tg.LocalIP != testLocalIP {
		t.Errorf("LocalIP = %q，期望 %q", tg.LocalIP, testLocalIP)
	}
	if tg.Subnet != testSubnetCIDR {
		t.Errorf("Subnet = %q，期望 %q", tg.Subnet, testSubnetCIDR)
	}
}

// TestExpandTargetsBothPutsBroadcastFirst 双模下广播目标排在最前。
//
// 顺序是设计的一部分，不是巧合：一个大网段触顶时，广播目标若排在两百多个
// 逐主机目标之后就会**永远轮不到它**——而它恰恰是最廉价、最先出结果的那一个。
func TestExpandTargetsBothPutsBroadcastFirst(t *testing.T) {
	targets, requested, truncated := expandTargets(testSubnets(), 44818, 1024, driver.TargetModeBoth)
	if truncated {
		t.Fatal("远未触顶却报告截断")
	}
	if requested != testHosts+1 || len(targets) != testHosts+1 {
		t.Fatalf("requested=%d len(targets)=%d，都应为 %d（6 个主机 + 1 个广播）",
			requested, len(targets), testHosts+1)
	}
	if !targets[0].Broadcast || targets[0].Host != "10.0.0.7" {
		t.Fatalf("第一个目标应是广播目标，实际 %+v", targets[0])
	}
	seen := 0
	for _, tg := range targets[1:] {
		if tg.Broadcast {
			t.Errorf("第 %d 个目标不该是广播：%+v", seen+2, tg)
		}
		seen++
	}
	if seen != testHosts {
		t.Errorf("广播之后有 %d 个单播目标，期望 %d", seen, testHosts)
	}
}

// TestExpandTargetsBroadcastSurvivesTinyCap 上限只有 1 时，留下的必须是广播目标。
//
// 这正是"广播先产出"要保证的事：maxTargets 默认 1024，但配小一点是允许的，
// 而一个 /16 的网段配上 maxTargets=1 时，单播优先就等于这台协议永远扫不到东西。
func TestExpandTargetsBroadcastSurvivesTinyCap(t *testing.T) {
	targets, requested, truncated := expandTargets(testSubnets(), 44818, 1, driver.TargetModeBoth)
	if !truncated {
		t.Fatal("上限为 1 而请求 7 个目标，必须报告截断")
	}
	if len(targets) != 1 || !targets[0].Broadcast {
		t.Fatalf("唯一的目标应是广播目标，实际 %+v", targets)
	}
	// requested 反映**未截断前**的真实数量：它是前端"只扫了前 N 个"的分母，
	// 少算就等于把"其实有 7 个目标"说成"只有 1 个"。
	if requested != testHosts+1 {
		t.Errorf("requested = %d，期望 %d", requested, testHosts+1)
	}
}

// TestExpandTargetsRequestedMatchesTargets 未触顶时 requested 与 len(targets) 恒等。
//
// 三种模式都要成立。两者一旦对不上，症状是每一次正常扫描都挂着"只扫了前 N 个
// 地址"，而用户无从判断真假（见 TestPlanDoesNotReportFalseTruncation）。
func TestExpandTargetsRequestedMatchesTargets(t *testing.T) {
	modes := []driver.TargetMode{"", driver.TargetModeUnicast, driver.TargetModeBroadcast, driver.TargetModeBoth}
	for _, mode := range modes {
		targets, requested, truncated := expandTargets(testSubnets(), 502, 65536, mode)
		if truncated {
			t.Fatalf("模式 %q：远未触顶却报告截断", mode)
		}
		if requested != len(targets) {
			t.Errorf("模式 %q：requested=%d 而 len(targets)=%d", mode, requested, len(targets))
		}
	}
}

// TestExpandTargetsSkipsUnusableMasks /31 与 /32 没有子网广播地址。
//
// 对着它们算出来的 ip|^mask 就是地址本身或全 1，发出去要么无人应答、
// 要么打到别的网段上 —— 而用户会从"扫不到设备"得出"这里没有设备"。
func TestExpandTargetsSkipsUnusableMasks(t *testing.T) {
	subnets := []SubnetInfo{
		{Iface: "a", CIDR: "10.0.0.0/31", LocalIP: "10.0.0.0", Broadcast: "10.0.0.1"},
		{Iface: "b", CIDR: "10.0.1.1/32", LocalIP: "10.0.1.1", Broadcast: "10.0.1.1"},
	}
	targets, requested, _ := expandTargets(subnets, 44818, 1024, driver.TargetModeBroadcast)
	if len(targets) != 0 || requested != 0 {
		t.Fatalf("不可用的掩码不该产出广播目标，实际 targets=%+v requested=%d", targets, requested)
	}
}

// TestExpandTargetsSkipsBadCIDR 解析不了的网段被跳过，且不影响其它网段。
func TestExpandTargetsSkipsBadCIDR(t *testing.T) {
	subnets := []SubnetInfo{
		{Iface: "a", CIDR: "not-a-cidr", LocalIP: "10.0.0.1"},
		{Iface: "b", CIDR: "10.1.0.0/29", LocalIP: "10.1.0.1", Broadcast: "10.1.0.7"},
	}
	targets, requested, truncated := expandTargets(subnets, 44818, 1024, driver.TargetModeBoth)
	if truncated {
		t.Fatal("不该报告截断")
	}
	// 6 个主机 + 1 个广播，全部来自第二个网段。
	if requested != testHosts+1 || len(targets) != testHosts+1 {
		t.Fatalf("requested=%d len(targets)=%d，都应为 %d", requested, len(targets), testHosts+1)
	}
	for _, tg := range targets {
		if tg.Subnet != "10.1.0.0/29" {
			t.Errorf("目标 %+v 来自不该被展开的网段", tg)
		}
	}
}

func TestSubnetOnes(t *testing.T) {
	cases := []struct {
		cidr string
		ones int
		ok   bool
	}{
		{"10.0.0.0/29", 29, true},
		{"10.0.0.0/8", 8, true},
		{"not-a-cidr", 0, false},
		{"10.0.0.0", 0, false},
	}
	for _, c := range cases {
		ones, ok := subnetOnes(c.cidr)
		if ok != c.ok || (ok && ones != c.ones) {
			t.Errorf("subnetOnes(%q) = (%d,%v)，期望 (%d,%v)", c.cidr, ones, ok, c.ones, c.ok)
		}
	}
}

func TestBroadcastable(t *testing.T) {
	// 区间与 discovery.eachUsableIPv4 的掩码过滤一致：[1,30]。
	want := map[int]bool{0: false, 1: true, 30: true, 31: false, 32: false}
	for ones, w := range want {
		if got := broadcastable(ones); got != w {
			t.Errorf("broadcastable(%d) = %v，期望 %v", ones, got, w)
		}
	}
}
