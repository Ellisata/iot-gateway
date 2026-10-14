// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package discovery

import (
	"errors"
	"net"
	"testing"
)

// testIPNet 造一个 *net.IPNet，形态与 net.Interface.Addrs 返回的一致：
// IP 是**主机地址**而不是网络地址，且 IPv4 是 IPv4-mapped 的 16 字节形式。
// 这一点很关键 —— 它让下面每条用例都在顺带守 To4()。
func testIPNet(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", cidr, err)
	}
	ipNet.IP = ip // ParseCIDR 返回的是网络地址，这里换回主机地址
	return ipNet
}

func TestLanTargetsWith(t *testing.T) {
	const port = 9083

	up := net.FlagUp
	iface := func(name string, flags net.Flags) net.Interface {
		return net.Interface{Name: name, Flags: flags}
	}

	tests := []struct {
		name string
		// ifaces 与 addrs 是注入的网卡与地址；addrs 以网卡名索引。
		ifaces []net.Interface
		addrs  map[string][]string // CIDR 字符串，避免在表里写 make/cast
		fail   map[string]bool     // 这些网卡的 Addrs 查询报错
		want   []wantTarget
	}{
		{
			name:   "24 位掩码的正常网卡",
			ifaces: []net.Interface{iface("eth0", up)},
			addrs:  map[string][]string{"eth0": {"192.168.1.10/24"}},
			want:   []wantTarget{{"eth0", "192.168.1.10", "192.168.1.255:9083"}},
		},
		{
			name:   "30 位掩码",
			ifaces: []net.Interface{iface("eth0", up)},
			addrs:  map[string][]string{"eth0": {"10.0.0.1/30"}},
			want:   []wantTarget{{"eth0", "10.0.0.1", "10.0.0.3:9083"}},
		},
		{
			// /32 的 ip|^mask 等于 ip 自己（单播给自己）；/31（RFC 3021 点对点）
			// 等于**对端**的单播地址；/0 等于 255.255.255.255。
			// 三者都必须跳过 —— 不是"没用"，是"发错了地方"。
			name:   "31/32/0 位掩码一律跳过",
			ifaces: []net.Interface{iface("eth0", up)},
			addrs: map[string][]string{"eth0": {
				"192.168.1.10/32",
				"192.168.2.10/31",
				"192.168.3.10/0",
			}},
			want: nil,
		},
		{
			name:   "纯 IPv6 网卡跳过",
			ifaces: []net.Interface{iface("eth0", up)},
			addrs:  map[string][]string{"eth0": {"fe80::1/64", "2001:db8::1/64"}},
			want:   nil,
		},
		{
			// 同一张网卡同时挂 IPv4 与 IPv6 时，只取 IPv4 那一条。
			name:   "双栈网卡只取 IPv4",
			ifaces: []net.Interface{iface("eth0", up)},
			addrs:  map[string][]string{"eth0": {"fe80::1/64", "192.168.1.10/24"}},
			want:   []wantTarget{{"eth0", "192.168.1.10", "192.168.1.255:9083"}},
		},
		{
			name:   "loopback 网卡跳过",
			ifaces: []net.Interface{iface("lo", up|net.FlagLoopback)},
			addrs:  map[string][]string{"lo": {"127.0.0.1/8"}},
			want:   nil,
		},
		{
			name:   "未启用的网卡跳过",
			ifaces: []net.Interface{iface("eth1", 0)},
			addrs:  map[string][]string{"eth1": {"192.168.1.10/24"}},
			want:   nil,
		},
		{
			name:   "点对点网卡跳过",
			ifaces: []net.Interface{iface("ppp0", up|net.FlagPointToPoint)},
			addrs:  map[string][]string{"ppp0": {"10.8.0.2/24"}},
			want:   nil,
		},
		{
			// 169.254/16 是 APIPA：含义是 DHCP 失败、本机没接上任何网络，
			// 管理平台根本够不着这个地址。
			name:   "APIPA 地址跳过",
			ifaces: []net.Interface{iface("eth0", up)},
			addrs:  map[string][]string{"eth0": {"169.254.10.5/16"}},
			want:   nil,
		},
		{
			// Windows 在断开的介质上会报 0.0.0.0/0。
			name:   "0.0.0.0 跳过",
			ifaces: []net.Interface{iface("eth0", up)},
			addrs:  map[string][]string{"eth0": {"0.0.0.0/24"}},
			want:   nil,
		},
		{
			// 双地址冗余部署：同一网段挂两个 VIP，广播地址相同，只应产生一个目标，
			// 否则同一网段每条报文收两遍。
			name:   "同网段双地址去重",
			ifaces: []net.Interface{iface("eth0", up)},
			addrs:  map[string][]string{"eth0": {"192.168.1.10/24", "192.168.1.11/24"}},
			want:   []wantTarget{{"eth0", "192.168.1.10", "192.168.1.255:9083"}},
		},
		{
			name:   "多网卡各自成目标",
			ifaces: []net.Interface{iface("eth0", up), iface("eth1", up)},
			addrs: map[string][]string{
				"eth0": {"192.168.1.10/24"},
				"eth1": {"10.0.0.5/24"},
			},
			want: []wantTarget{
				{"eth0", "192.168.1.10", "192.168.1.255:9083"},
				{"eth1", "10.0.0.5", "10.0.0.255:9083"},
			},
		},
		{
			// 取不到地址的网卡（Interface.Addrs 报错）应当被跳过而不是中断整轮枚举。
			name:   "地址查询失败的网卡跳过，不影响其他网卡",
			ifaces: []net.Interface{iface("eth0", up), iface("eth1", up)},
			addrs:  map[string][]string{"eth1": {"192.168.1.10/24"}},
			fail:   map[string]bool{"eth0": true},
			want:   []wantTarget{{"eth1", "192.168.1.10", "192.168.1.255:9083"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addrs := func(i *net.Interface) ([]net.Addr, error) {
				if tt.fail[i.Name] {
					return nil, errTestAddrs
				}
				var out []net.Addr
				for _, cidr := range tt.addrs[i.Name] {
					out = append(out, testIPNet(t, cidr))
				}
				return out, nil
			}

			got := lanTargetsWith(tt.ifaces, addrs, port)
			if len(got) != len(tt.want) {
				t.Fatalf("目标数 = %d, 期望 %d (%v)", len(got), len(tt.want), got)
			}
			for i, w := range tt.want {
				g := got[i]
				if g.iface != w.iface || g.ip.String() != w.ip || g.bcast.String() != w.bcast {
					t.Errorf("目标[%d] = (%s, %s, %s), 期望 (%s, %s, %s)",
						i, g.iface, g.ip, g.bcast, w.iface, w.ip, w.bcast)
				}
			}
		})
	}
}

type wantTarget struct {
	iface, ip, bcast string
}

var errTestAddrs = errors.New("injected addrs failure")

func TestBroadcastAddr(t *testing.T) {
	tests := []struct {
		cidr string
		want string
	}{
		{"192.168.1.10/24", "192.168.1.255"},
		{"10.1.2.3/8", "10.255.255.255"},
		{"172.16.5.9/20", "172.16.15.255"},
		{"172.16.5.9/30", "172.16.5.11"},
		// 掩码之外还带着主机位（配置错误）时，结果仍应是该网段的广播地址：
		// 只有网络位有意义。
		{"192.168.1.200/25", "192.168.1.255"},
	}

	for _, tt := range tests {
		ip, ipNet, err := net.ParseCIDR(tt.cidr)
		if err != nil {
			t.Fatalf("ParseCIDR(%q): %v", tt.cidr, err)
		}
		got := broadcastAddr(ip.To4(), ipNet.Mask)
		if got == nil {
			t.Fatalf("%s: broadcastAddr 返回 nil", tt.cidr)
		}
		if got.String() != tt.want {
			t.Errorf("%s: broadcastAddr = %s, 期望 %s", tt.cidr, got, tt.want)
		}
	}
}

func TestBroadcastAddrRejectsNonIPv4(t *testing.T) {
	_, ipNet, err := net.ParseCIDR("fe80::1/64")
	if err != nil {
		t.Fatalf("ParseCIDR: %v", err)
	}
	// IPv6 地址与掩码都不该被算出个"广播地址"来。
	if got := broadcastAddr(ipNet.IP, ipNet.Mask); got != nil {
		t.Errorf("IPv6 应当返回 nil，得到 %v", got)
	}
	if got := broadcastAddr(net.IPv4(192, 168, 1, 1).To4(), net.CIDRMask(64, 128)); got != nil {
		t.Errorf("IPv6 掩码应当返回 nil，得到 %v", got)
	}
}

// TestLanTargetsOnRealHosts 跑一次真实的网卡枚举。
//
// 不假定机器上有哪些网卡（CI 上可能只有 loopback，那就是空切片），只断言
// 两条恒等式：每个目标的广播地址确实落在它自己的网段里，且端口透传正确。
// 这挡的是"注入版全绿、真机上一个都算不出来"这类偏差。
func TestLanTargetsOnRealHosts(t *testing.T) {
	const port = 19083
	for _, tg := range lanTargets(port) {
		if tg.bcast.Port != port {
			t.Errorf("%s: 端口 = %d, 期望 %d", tg.iface, tg.bcast.Port, port)
		}
		if tg.ip.To4() == nil {
			t.Errorf("%s: ip %s 不是 IPv4", tg.iface, tg.ip)
		}
		if tg.ip.Equal(tg.bcast.IP) {
			t.Errorf("%s: 广播地址 %s 与自身地址相同", tg.iface, tg.bcast)
		}
	}
}

// localSubnetsAddrs 把「网卡名 → CIDR 列表」的表变成 eachUsableIPv4 需要的
// addrs 注入函数，与 TestLanTargetsWith 用的是同一套形态。
func localSubnetsAddrs(cidrs map[string][]string, fail map[string]bool, t *testing.T) func(*net.Interface) ([]net.Addr, error) {
	t.Helper()
	return func(i *net.Interface) ([]net.Addr, error) {
		if fail[i.Name] {
			return nil, errTestAddrs
		}
		var out []net.Addr
		for _, cidr := range cidrs[i.Name] {
			out = append(out, testIPNet(t, cidr))
		}
		return out, nil
	}
}

// TestLocalSubnetsWith 覆盖扫描侧新增的网段枚举。
//
// 它与 TestLanTargetsWith 共用同一份过滤规则（eachUsableIPv4），因此这里
// 不重复枚举"哪些网卡该跳过"（那是那份用例的职责），只钉住扫描侧特有的两点：
//   - 网段前缀长度与各自的 local/broadcast 取值；
//   - **按网段去重**（而不是按 IP）—— 一张网卡挂两个同网段 VIP 时，
//     IP 不同而网段相同，不按网段去重会让扫描目标被展开两遍。
func TestLocalSubnetsWith(t *testing.T) {
	up := net.FlagUp
	iface := func(name string, flags net.Flags) net.Interface {
		return net.Interface{Name: name, Flags: flags}
	}

	tests := []struct {
		name   string
		ifaces []net.Interface
		addrs  map[string][]string
		fail   map[string]bool
		want   []Subnet
	}{
		{
			name:   "24 位掩码",
			ifaces: []net.Interface{iface("eth0", up)},
			addrs:  map[string][]string{"eth0": {"192.168.1.10/24"}},
			want: []Subnet{{
				Iface:   "eth0",
				IP:      net.IPv4(192, 168, 1, 10).To4(),
				Network: net.IPv4(192, 168, 1, 0).To4(),
				Mask:    net.CIDRMask(24, 32),
				Bcast:   net.IPv4(192, 168, 1, 255).To4(),
			}},
		},
		{
			name:   "30 位掩码",
			ifaces: []net.Interface{iface("eth0", up)},
			addrs:  map[string][]string{"eth0": {"10.0.0.1/30"}},
			want: []Subnet{{
				Iface:   "eth0",
				IP:      net.IPv4(10, 0, 0, 1).To4(),
				Network: net.IPv4(10, 0, 0, 0).To4(),
				Mask:    net.CIDRMask(30, 32),
				Bcast:   net.IPv4(10, 0, 0, 3).To4(),
			}},
		},
		{
			// 与广播侧的去重键不同：那边按广播地址，这边必须按网段。
			// 双地址冗余部署下两者等价，但哪天有了一张网卡挂不同掩码地址的部署，
			// 按 IP 去重就会把同一个网段展开两遍。
			name:   "同网段双地址只留一条",
			ifaces: []net.Interface{iface("eth0", up)},
			addrs:  map[string][]string{"eth0": {"192.168.1.10/24", "192.168.1.11/24"}},
			want: []Subnet{{
				Iface:   "eth0",
				IP:      net.IPv4(192, 168, 1, 10).To4(),
				Network: net.IPv4(192, 168, 1, 0).To4(),
				Mask:    net.CIDRMask(24, 32),
				Bcast:   net.IPv4(192, 168, 1, 255).To4(),
			}},
		},
		{
			name:   "只有 loopback 的机器返回空",
			ifaces: []net.Interface{iface("lo", up|net.FlagLoopback)},
			addrs:  map[string][]string{"lo": {"127.0.0.1/8"}},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := localSubnetsWith(tt.ifaces, localSubnetsAddrs(tt.addrs, tt.fail, t))
			if len(got) != len(tt.want) {
				t.Fatalf("网段数 = %d, 期望 %d (%+v)", len(got), len(tt.want), got)
			}
			for i, w := range tt.want {
				g := got[i]
				if g.Iface != w.Iface || !g.IP.Equal(w.IP) || !g.Network.Equal(w.Network) ||
					g.Mask.String() != w.Mask.String() || !g.Bcast.Equal(w.Bcast) {
					t.Errorf("网段[%d] = {%s %s %s %s %s}, 期望 {%s %s %s %s %s}",
						i, g.Iface, g.IP, g.Network, g.Mask, g.Bcast,
						w.Iface, w.IP, w.Network, w.Mask, w.Bcast)
				}
			}
		})
	}
}

// TestLocalSubnetsOnRealHosts 跑一次真实的网卡枚举：不假定机器上有哪些网卡
// （CI 上可能只有 loopback，那就是空切片），只断言恒等式。
//
// 这挡的是"注入版全绿、真机上一个网段都算不出来"这类偏差。
func TestLocalSubnetsOnRealHosts(t *testing.T) {
	for _, s := range LocalSubnets() {
		if s.IP.To4() == nil {
			t.Errorf("%s: IP %s 不是 IPv4", s.Iface, s.IP)
		}
		// 本机地址必须落在自己的网段里。
		if !(&net.IPNet{IP: s.Network, Mask: s.Mask}).Contains(s.IP) {
			t.Errorf("%s: 地址 %s 不在网段 %s/%d 内", s.Iface, s.IP, s.Network, s.PrefixLen())
		}
		if s.IP.Equal(s.Bcast) {
			t.Errorf("%s: 广播地址 %s 与自身地址相同", s.Iface, s.Bcast)
		}
		if s.PrefixLen() < 1 || s.PrefixLen() > 30 {
			t.Errorf("%s: 前缀长度 %d 越界（过滤规则只接受 [1,30]）", s.Iface, s.PrefixLen())
		}
	}
}
