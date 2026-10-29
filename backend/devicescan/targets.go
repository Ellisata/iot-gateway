// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package devicescan 是局域网设备扫描发现的**任务引擎**：把本机网段展开成
// 目标列表，并发交给插件探测，累积结果与进度，并支持取消。
//
// 与 iot-gateway/discovery 的区别（命名上刻意分开，别混）：
//
//	discovery/    网关把自己广播出去，供管理平台发现 —— "我告诉别人我在哪"
//	devicescan/   网关扫本机网段找 PLC 并建成设备 —— "我去问别人谁在"
//
// 职责边界：**目标由本包规划，探测由插件实现**。并发、限速、进度、取消这些
// 与协议无关的事只写一遍；插件只回答"给我的这个 IP:端口是不是我的设备"。
// 广播型协议（EtherNet/IP ListIdentity、FINS/UDP）由本包额外把各网段的子网
// 广播地址加进目标列表即可覆盖，不需要插件自己扫网段。
package devicescan

import (
	"fmt"
	"net"
	"sort"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/discovery"
)

// SubnetInfo 本机一个可扫描的 IPv4 网段。前端据此给用户列网段多选。
type SubnetInfo struct {
	// Iface 网卡名。多网卡网关（办公网 + OT 网）上用户靠它区分"扫的是哪张网"。
	Iface string `json:"iface"`
	// CIDR 网段，形如 192.168.1.0/24，同时作为选择该网段的键。
	CIDR string `json:"cidr"`
	// LocalIP 本机在该网段上的地址。它**也**会被扫描（见 expandTargets），
	// 这个值主要给广播型协议当源地址绑定用。
	LocalIP string `json:"localIp"`
	// Broadcast 子网广播地址。展示给用户看，广播型协议会把它作为目标。
	Broadcast string `json:"broadcast"`
	// HostCount 展开后的目标数（已排除网络地址、广播地址与本机地址）。
	HostCount int `json:"hostCount"`
}

// localSubnets 返回本机可扫描的网段。
//
// 直接复用 discovery 包的网卡枚举：它已经把「哪张网卡算一个可用网段」的过滤
// 规则（排除 loopback / 点对点 / APIPA / 掩码越界）写全了，两处各写一份必然分叉，
// 而分叉只会表现为"某个网段扫不到设备"，不报任何错。
func localSubnets() []SubnetInfo {
	return subnetsFrom(discovery.LocalSubnets())
}

// subnetsFrom 把 discovery 的网段投影成 SubnetInfo。
func subnetsFrom(subnets []discovery.Subnet) []SubnetInfo {
	out := make([]SubnetInfo, 0, len(subnets))
	for _, s := range subnets {
		cidr := fmt.Sprintf("%s/%d", s.Network, s.PrefixLen())
		out = append(out, SubnetInfo{
			Iface:     s.Iface,
			CIDR:      cidr,
			LocalIP:   s.IP.String(),
			Broadcast: s.Bcast.String(),
			HostCount: hostCount(s.PrefixLen()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CIDR < out[j].CIDR })
	return out
}

// hostCount 一个 /ones 网段展开后的目标数：地址总数减去网络地址与广播地址。
//
// 与 expandTargets 实际产出的目标数**恒等**（那边也只排除这两个地址），
// 于是 requested 与 len(targets) 在未触顶时必然相等 ——
// "假截断"（未达上限却提示只扫了前 N 个）因此不可能发生。
func hostCount(ones int) int {
	if ones <= 0 || ones >= 32 {
		return 0
	}
	return 1<<(32-ones) - 2
}

// normalizeTargetMode 归一化插件的 TargetMode 声明：留空按 unicast
// （driver.TargetMode 的文档如此规定），无法识别的值同样按 unicast 处理。
//
// 在这里兜底而不是信任插件：driversdk.Meta.Validate 已经把非法值挡在插件启动期，
// 但这个函数还要面对测试与将来可能的其它 SpecSource 实现，多一层归一化是免费的。
func normalizeTargetMode(mode driver.TargetMode) driver.TargetMode {
	switch mode {
	case driver.TargetModeBroadcast, driver.TargetModeBoth:
		return mode
	default:
		return driver.TargetModeUnicast
	}
}

// subnetOnes 解析 CIDR 的掩码长度。非 IPv4 或解析不了时返回 false。
func subnetOnes(cidr string) (int, bool) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, false
	}
	ones, bits := ipNet.Mask.Size()
	if bits != 32 {
		return 0, false
	}
	return ones, true
}

// broadcastable 这个掩码长度有没有一个有意义的子网广播地址。
//
// 与 discovery.eachUsableIPv4 的掩码过滤区间一致（[1,30]）：/31 与 /32 没有
// 广播地址的概念（点对点链路与单主机路由），对着它们算出来的 ip|^mask 就是
// 地址本身或全 1，发出去要么无人应答、要么打到别的网段上。
func broadcastable(ones int) bool { return ones >= 1 && ones <= 30 }

// expandTargets 把网段展开成**第一批**目标列表，最多 maxTargets 个。
//
// 是 expandTargetsPage 在 offset=0 时的薄包装，保留给只关心第一批的调用方与
// 既有测试。续扫走 expandTargetsPage —— 两处必须是同一次展开的不同切片，
// 各写一份就会在批次交界处漏掉或重复地址，而这种错误只会表现为"某台设备扫不到"。
func expandTargets(subnets []SubnetInfo, port int, maxTargets int, mode driver.TargetMode) (targets []driver.ScanTarget, requested int, truncated bool) {
	targets, requested, nextOffset, _ := expandTargetsPage(subnets, port, maxTargets, 0, mode)
	return targets, requested, nextOffset < requested
}

// expandTargetsPage 展开虚拟目标列表里 [offset, offset+limit) 这一段。
//
// 产出什么目标由插件的 TargetMode 决定（见 driver.TargetMode）：
//
//	unicast（或留空）  逐主机地址，一个目标一台候选设备
//	broadcast          每个网段一个**子网广播地址**，一个目标可能产出多台设备
//	both               两者都要
//
// TargetMode 不是可有可无的装饰：EtherNet/IP 的 ListIdentity 与 FINS/UDP 的节点
// 查询都是广播型协议，逐主机单播只是把同一个问题问 254 遍（还要求设备愿意逐个
// 应答单播），而广播一次就能拿回整个网段的答案。反之 S7/Modbus 这类没有发现
// 报文的协议只能逐主机拨号 —— 所以这件事必须由插件说、网关照做，不能取其一。
//
// 虚拟列表的次序是固定的：**广播目标在前**（按网段顺序），然后是各网段的逐主机
// 地址。offset/limit 数的是**目标序号**（广播也计入），因此同一组网段 + 同一模式
// 下任何一次调用的第 N 个目标都相同 —— 这是分批续扫不会漏扫/重扫的前提。
//
// 返回 requested 是展开前的目标总数（不受 limit 影响），nextOffset 是下一批的
// 起点（= offset + len(targets)，不超过 requested），truncated 表示还有未扫的
// 目标。三者都要回给前端：静默截断会让用户从"没扫到设备"得出"这个网段没有
// 设备"的错误结论，而实际原因只是单批上限只扫了前 1024 个地址。
//
// 单播展开只排除网络地址与广播地址（它们不是主机）。**本机地址照扫** ——
// 与网关同机的设备是真实存在的（网关与 PLC 仿真器同机调试、边缘一体机上
// 另跑着采集服务），而"静默跳过某一个地址"恰恰会让用户从"没扫到"得出
// "那里没有设备"的错误结论，代价却只是多一次探测。
func expandTargetsPage(subnets []SubnetInfo, port int, limit, offset int,
	mode driver.TargetMode) (targets []driver.ScanTarget, requested, nextOffset int, truncated bool) {
	mode = normalizeTargetMode(mode)
	wantUnicast := mode == driver.TargetModeUnicast || mode == driver.TargetModeBoth
	wantBroadcast := mode == driver.TargetModeBroadcast || mode == driver.TargetModeBoth

	// 先单独算一遍总量：它**不受 limit 影响**，是前端渲染"还有 N 个未扫"的依据。
	// 不能边展开边累加 —— 一旦本批装满就跳出循环，后面那些没走到（却确实存在
	// 于用户所选网段里）的目标数就漏掉了，提示出来的数字比实际小。
	for _, s := range subnets {
		ones, ok := subnetOnes(s.CIDR)
		if !ok {
			continue
		}
		if wantBroadcast && broadcastable(ones) {
			requested++
		}
		if wantUnicast {
			if n := hostCount(ones); n > 0 {
				requested += n
			}
		}
	}

	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = 0
	}
	if limit == 0 {
		return nil, requested, offset, offset < requested
	}

	// idx 是虚拟列表里下一个目标的序号；只有落在 [offset, offset+limit) 里的
	// 才物化成 ScanTarget。这样 /16 续扫到最后一页时也不会先物化六万个字符串。
	idx := 0
	full := false
	emit := func(t driver.ScanTarget) {
		if idx >= offset && len(targets) < limit {
			targets = append(targets, t)
		}
		idx++
		if len(targets) >= limit {
			full = true
		}
	}

	// 广播目标**先**产出，这个顺序是有意的两件事：
	//
	//  ① 广播目标一个就覆盖整个网段，把它排在两百多个逐主机目标之后，
	//     一个大网段触顶时就可能永远轮不到它 —— 而它恰恰是最廉价、最可能
	//     先出结果的那一个。前端因此能立刻看到"扫到了什么"，而不是先等一堆
	//     不可达地址各跑满一次超时；
	//  ② ScanTarget.LocalIP 是广播的源地址绑定（多网卡机器上不绑就只会从
	//     默认路由那张网卡出去，一台设备也发现不了，见 driver.ScanTarget）。
	if wantBroadcast && !full {
		for _, s := range subnets {
			if full {
				break
			}
			ones, ok := subnetOnes(s.CIDR)
			if !ok || !broadcastable(ones) {
				continue
			}
			_, ipNet, err := net.ParseCIDR(s.CIDR)
			if err != nil {
				continue
			}
			bcast := broadcastOf(ipNet.IP, ipNet.Mask)
			if bcast == nil {
				continue
			}
			emit(driver.ScanTarget{
				Host:      bcast.String(),
				Port:      port,
				LocalIP:   s.LocalIP,
				Subnet:    s.CIDR,
				Broadcast: true,
			})
		}
	}

	if wantUnicast && !full {
	expand:
		for _, s := range subnets {
			_, ipNet, err := net.ParseCIDR(s.CIDR)
			if err != nil {
				continue
			}
			bcast := broadcastOf(ipNet.IP, ipNet.Mask)

			// 逐地址累加而不是先物化整个网段再截断：/16 展开是六万多个字符串，
			// 而默认单批上限只有 1024。
			ip := nextIP(ipNet.IP)
			for ipNet.Contains(ip) && (bcast == nil || !ip.Equal(bcast)) {
				emit(driver.ScanTarget{
					Host:    ip.String(),
					Port:    port,
					LocalIP: s.LocalIP,
					Subnet:  s.CIDR,
				})
				if full {
					break expand
				}
				ip = nextIP(ip)
			}
		}
	}

	nextOffset = offset + len(targets)
	if nextOffset > requested {
		nextOffset = requested
	}
	return targets, requested, nextOffset, nextOffset < requested
}

// broadcastOf 计算子网广播地址（ip | ^mask）。掩码长度不是 4 时返回 nil。
func broadcastOf(ip net.IP, mask net.IPMask) net.IP {
	ip4 := ip.To4()
	if ip4 == nil || len(mask) != 4 {
		return nil
	}
	out := make(net.IP, 4)
	for i := range out {
		out[i] = ip4[i] | ^mask[i]
	}
	return out
}

// nextIP 返回 ip+1，保持 4 字节形式。
func nextIP(ip net.IP) net.IP {
	out := make(net.IP, len(ip))
	copy(out, ip)
	for i := len(out) - 1; i >= 0; i-- {
		out[i]++
		if out[i] != 0 {
			break
		}
	}
	return out
}

// selectSubnets 从本机网段里挑出用户选中的那些。
//
// 只允许选**本机已探测到的网段**，不接受任意 CIDR：那会把网关变成一台可以
// 扫任何地址的通用端口扫描器。运维真要扫别的网段，正确做法是在那台机器上
// 部署网关（或者把网段配到本机路由表上，那样它自然就出现在列表里了）。
//
// 空选择 = 全部本机网段。
func selectSubnets(all []SubnetInfo, wanted []string) ([]SubnetInfo, error) {
	if len(wanted) == 0 {
		return all, nil
	}
	byCIDR := make(map[string]SubnetInfo, len(all))
	for _, s := range all {
		byCIDR[s.CIDR] = s
	}
	out := make([]SubnetInfo, 0, len(wanted))
	for _, w := range wanted {
		s, ok := byCIDR[w]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownSubnet, w)
		}
		out = append(out, s)
	}
	return out, nil
}
