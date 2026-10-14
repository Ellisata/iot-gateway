// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package discovery

import "net"

// target 一个广播目标：往哪儿发，以及报文里的 ip 字段填什么。
//
// iface 只用于日志 —— 启动时逐条打出来，运维据此判断"这台网关把身份发到了
// 哪几个网段"，这是排查"平台怎么没发现我"时的第一手信息。
type target struct {
	iface string
	ip    net.IP
	bcast *net.UDPAddr
}

// lanTargets 枚举本机可用的 IPv4 网卡，算出各自的子网广播地址。
//
// 返回空切片是**合法状态**而不是错误：只有 loopback、或全部网卡都不可用时，
// 确实无处可发。此时接收侧仍然正常工作（探询应答走的是路由选出的地址），
// 所以调用方不应该把"空"当成启动失败去处理。
func lanTargets(port int) []target {
	ifaces, err := net.Interfaces()
	if err != nil {
		// 拿不到网卡列表就当作"没有可用网卡"：局域网发现本来也发不出去，
		// 没必要为它中断启动。真正需要报错的失败在 listen 阶段
		//（见 Announcer.Start），那里才会向调用方返回 error。
		return nil
	}
	return lanTargetsWith(ifaces, (*net.Interface).Addrs, port)
}

// lanTargetsWith 是 lanTargets 的可测形态：网卡列表与地址查询都以参数注入。
//
// 之所以要注入 `addrs`：Interface.Addrs 是方法而不是字段，无法用假数据替换，
// 而"哪些网卡参与广播"恰恰是这段逻辑里唯一值得测的部分。这是本包仅有的测试缝。
func lanTargetsWith(ifaces []net.Interface, addrs func(*net.Interface) ([]net.Addr, error), port int) []target {
	var out []target
	seen := make(map[string]bool)

	eachUsableIPv4(ifaces, addrs, func(iface string, ip4 net.IP, mask net.IPMask) {
		bcast := broadcastAddr(ip4, mask)
		if bcast == nil {
			return
		}

		// 按广播地址去重：一张网卡挂两个同网段 VIP（常见于双地址冗余部署）
		// 会算出同一个广播地址，不去重则同一网段每条报文收两遍。
		key := bcast.String()
		if seen[key] {
			return
		}
		seen[key] = true

		out = append(out, target{
			iface: iface,
			ip:    ip4,
			bcast: &net.UDPAddr{IP: bcast, Port: port},
		})
	})

	return out
}

// eachUsableIPv4 逐个回调「本机可用的 IPv4 网段」：网卡名、该网卡上的地址、掩码。
//
// 过滤规则收在这唯一一处，供两处消费：
//   - lanTargetsWith —— 算子网广播地址，广播网关身份（供管理平台发现自己）；
//   - localSubnetsWith —— 算本机网段，交给 devicescan 展开成扫描目标。
//
// 抽出来是必需的而非风格选择：两处的过滤条件只要多差一条（比如一处放过 /31、
// 另一处不放过），"广播得到达的网段"与"扫描得到达的网段"就会悄悄分叉，
// 而这种分叉只会表现为"某个网段扫不到设备"，不报任何错。
//
// 返回空（一次回调都没有）是**合法状态**而不是错误：只有 loopback、或全部网卡
// 都不可用时确实无处可去，调用方不该把这当成失败。
func eachUsableIPv4(ifaces []net.Interface, addrs func(*net.Interface) ([]net.Addr, error),
	fn func(iface string, ip4 net.IP, mask net.IPMask)) {

	for i := range ifaces {
		iface := &ifaces[i]

		// 网卡没起来：它上面的地址此刻不可达。
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		// loopback：没有"局域网"可言。
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		// 点对点（PPP / tun / 多数 VPN）：掩码通常是 /32，对端是路由器而不是
		// 一个网段；即便不是，往那里广播也不属于"让本网段的管理平台发现我"。
		if iface.Flags&net.FlagPointToPoint != 0 {
			continue
		}

		addrsOfIface, err := addrs(iface)
		if err != nil {
			continue
		}

		for _, a := range addrsOfIface {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue // 极少数平台会给出非 IPNet 的地址
			}

			// 用 To4() 而不是 len(ip) == 4：InterfaceAddrs 返回的 IPv4 地址是
			// IPv4-mapped 的 16 字节形式，按长度判会把所有 IPv4 都当成 IPv6 丢掉，
			// 结果是"一台网关也发现不了"。
			ip4 := ipNet.IP.To4()
			if ip4 == nil {
				continue // IPv6（本功能只做 IPv4 广播）
			}

			mask := ipNet.Mask
			if len(mask) != 4 {
				continue // IPv6 掩码，与 ip4 对不上
			}

			ones, bits := mask.Size()
			// /31 与 /32 不是"无用"而是**错误**：
			//   /32 的 ip|^mask 等于 ip 自己 —— 那是单播给自己；
			//   /31（RFC 3021 点对点）算出的是**对端**的单播地址，发过去
			//   等于给一台路由器定向投递。
			// /0 算出 255.255.255.255，即受限广播，理由见 Announcer 的说明。
			// 所以只接受 [1, 30]。
			if bits != 32 || ones < 1 || ones > 30 {
				continue
			}

			if ip4.IsLoopback() {
				continue // 兜底：loopback 偶尔会挂在非 loopback 标志的网卡上
			}
			// 169.254.0.0/16：APIPA 自动地址，含义是 DHCP 失败、本机没接上
			// 任何网络。管理平台根本够不着这个地址，广播出去只会污染日志。
			if ip4[0] == 169 && ip4[1] == 254 {
				continue
			}
			// Windows 在断开的介质上会报 0.0.0.0/0，这里一并挡掉
			//（ones == 0 其实已经覆盖，多一条便宜的兜底）。
			if ip4.Equal(net.IPv4zero) {
				continue
			}

			fn(iface.Name, ip4, mask)
		}
	}
}

// Subnet 本机一张网卡上的一个可用 IPv4 网段。
type Subnet struct {
	// Iface 网卡名，仅用于展示（"这个网段扫的是哪张网卡"）。
	Iface string
	// IP 本机在该网段上的地址。
	IP net.IP
	// Network 网络地址（IP & Mask）。
	Network net.IP
	// Mask 子网掩码。
	Mask net.IPMask
	// Bcast 子网广播地址（IP | ^Mask）。
	Bcast net.IP
}

// PrefixLen 子网前缀长度（如 192.168.1.0/24 的 24）。
func (s Subnet) PrefixLen() int {
	ones, _ := s.Mask.Size()
	return ones
}

// LocalSubnets 返回本机全部可用的 IPv4 网段，与广播目标共用同一套过滤规则
// （见 eachUsableIPv4）。
//
// 返回空切片是合法状态（只有 loopback 的机器），调用方不该当成错误 ——
// 与 lanTargets 的约定一致。
func LocalSubnets() []Subnet {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	return localSubnetsWith(ifaces, (*net.Interface).Addrs)
}

// localSubnetsWith 是 LocalSubnets 的可测形态，理由同 lanTargetsWith。
func localSubnetsWith(ifaces []net.Interface, addrs func(*net.Interface) ([]net.Addr, error)) []Subnet {
	var out []Subnet
	// 按「网络地址/掩码」去重（而不是按 IP）：一张网卡挂两个同网段 VIP 时会给出
	// 两个不同 IP、同一个网段，不去重则扫描目标会被展开两遍。
	seen := make(map[string]bool)

	eachUsableIPv4(ifaces, addrs, func(iface string, ip4 net.IP, mask net.IPMask) {
		bcast := broadcastAddr(ip4, mask)
		if bcast == nil {
			return
		}
		network := ip4.Mask(mask)
		key := network.String() + "/" + mask.String()
		if seen[key] {
			return
		}
		seen[key] = true

		out = append(out, Subnet{
			Iface:   iface,
			IP:      ip4,
			Network: network,
			Mask:    mask,
			Bcast:   bcast,
		})
	})

	return out
}

// broadcastAddr 计算子网广播地址，即 ip | ^mask。
//
// 掩码之外的位一律置 1 —— 即便网卡被配上了落在掩码之外的主机位
// （一种配置错误），结果仍然是那个网段的广播地址：只有网络位有意义。
//
// mask 与 ip 长度不是 4 字节时返回 nil（调用方跳过该地址）。
func broadcastAddr(ip net.IP, mask net.IPMask) net.IP {
	if len(ip) != 4 || len(mask) != 4 {
		return nil
	}
	bcast := make(net.IP, 4)
	for i := range bcast {
		bcast[i] = ip[i] | ^mask[i]
	}
	return bcast
}
