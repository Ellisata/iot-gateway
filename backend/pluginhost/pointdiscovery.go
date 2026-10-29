// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

// ==================== 驱动插件适配层：点位范围扫描 ====================
//
// 本文件与 discovery.go 逐函数同构，管的是 discoverPoints 方法。两者都是
// 「扫描发现」，但支点相反：
//
//	discovery.go  扫**网络**：无会话、一次 RPC 一个目标、可取消。
//	本文件         扫**设备**：目标是一台已存在的设备，插件侧要复用它的采集会话
//	                （块间释放锁），一次 RPC 覆盖整段，**不可取消**。
//
// 会话怎么复用、串口为什么不回退、预算为什么还要再钳一次，全部在插件侧
// （driversdk.handleDiscoverPoints）与 specs/point-discovery.md 里；宿主这一层
// 只负责三件事：把能力声明给前端、把请求发给对的进程、给这次同步 RPC 定超时。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"github.com/Ellisata/iot-gateway-sdk/pluginwire"
)

// ErrPointDiscoveryUnsupported 当前没有任何已加载的插件为该协议提供点位扫描能力。
//
// 与 discovery.go 的 ErrDiscoveryUnsupported 是两回事（同一个协议完全可能支持
// 局域网扫描却不支持点位扫描）：处置也不同 —— 那个要去换一个带 Discovery 的
// 插件版本，这个要去看该协议的 PointDiscovery 声明。
var ErrPointDiscoveryUnsupported = errors.New("pluginhost: 该协议不支持点位扫描")

// 点位扫描的超时预算。这两个常量与 **main.go 的关机宽限期互相耦合**，
// 改一个必须同时看另一个：
//
//	main.go  srv.Shutdown 用 10s 超时
//	          ↓ http.Server.Shutdown 会**等所有在飞 handler 跑完**
//	本文件    discoverPoints RPC 超时 = min(插件声明, 7000ms) + 2s → 最坏 9s < 10s
//
// 超过 10s 的后果不是"报个错"：Shutdown 会卡满一整个超时周期，然后打一条
// context deadline exceeded —— 在 Windows 服务管理器 / systemd 里表现为
// "网关停不掉"，运维只能强杀。这条链路是**同步 HTTP**（与设备扫描的异步任务
// 不同，那里登记即返回），所以必须在网关侧再钳一道，不能只信插件自报的值：
// 插件声明的区间上限是 30s（driversdk 的 pointBudgetCeiling），那是留给串口
// 大范围扫描的，而它只在使用插件协议直连时才安全。
const (
	// pointScanGatewayBudgetCapMs 网关要求插件的整段扫描预算上限（毫秒）。
	//
	// 7000 的来历：留 3s 给 [RPC 余量 2s] + [HTTP 层与前端收尾]，凑不满 10s。
	// 取更小（如 3s）会频繁截断串口大范围扫描、把 Incomplete 变成常态。
	pointScanGatewayBudgetCapMs = 7000

	// pointScanRPCGrace RPC 超时相对插件实际预算的余量。
	//
	// 插件的 MaxBudgetMs 管的是「扫描」本身，RPC 还要加上帧编解码、插件侧调度
	// 与本地管道往返 —— 这点余量留给它们。取 2s 而非几百毫秒：扫描结果是
	// 一次性的大载荷（MaxPoints 上限下有几百 KB 的量级），编码与过管道本身
	// 就要花掉可观的时间，而这部分不受插件预算约束。
	//
	// 比 probeRPCGrace 的 3s 小 1s，是为了让最坏值落在 9s：probe 走的是异步任务、
	// 超时不会拖住停机，这里不行。
	pointScanRPCGrace = 2 * time.Second
)

// PointDiscoverySpecs 返回当前已加载插件声明的全部点位扫描能力（按协议名排序）。
//
// **每次现算，不维护缓存表。** 热升级只替换 lp.meta（见 upgrade.go），而
// registrationSig 只含 scope / protocols / types / typeMap，**不含 PointDiscovery** ——
// 于是一个只改了 PointDiscovery 的新二进制升级后不会触发重注册。若把能力另存
// 一张表，它会停留在旧值上，症状是"插件明明加了点扫能力、前端却一直没有"，
// 且两边日志都写着成功。遍历 m.plugins 读 lp.meta 则一致性自动成立，
// unregister 也无需清理。（与 DiscoverySpecs 是同一个坑、同一条取舍。）
//
// 返回的 spec 是副本（含 FormJSON 的独立底层数组）：调用方会把它序列化给前端，
// 不该让一次改写污染已加载插件的 meta。
func (m *Manager) PointDiscoverySpecs() []driver.ProtocolPointDiscovery {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []driver.ProtocolPointDiscovery
	for _, lp := range m.plugins {
		for i := range lp.meta.Protocols {
			p := &lp.meta.Protocols[i]
			if p.PointDiscovery == nil {
				continue
			}
			spec := *p.PointDiscovery
			spec.FormJSON = append([]byte(nil), spec.FormJSON...)
			spec.TitleI18n = cloneStringMap(spec.TitleI18n)
			spec.DescriptionI18n = cloneStringMap(spec.DescriptionI18n)
			out = append(out, driver.ProtocolPointDiscovery{Protocol: p.Name, Spec: spec})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Protocol < out[j].Protocol })
	return out
}

// pointSpecFor 找到提供该协议的插件的链接与它声明的点位扫描规格。
//
// 返回 link 而不是进程指针：热升级要能把调用从旧进程挪到新进程，调用方持有的
// 必须是那个稳定的间接层（同 scanSpecFor）。
func (m *Manager) pointSpecFor(protocol string) (*pluginLink, driver.PointDiscoverySpec, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, lp := range m.plugins {
		for i := range lp.meta.Protocols {
			p := &lp.meta.Protocols[i]
			if p.Name != protocol || p.PointDiscovery == nil {
				continue
			}
			return lp.link, *p.PointDiscovery, true
		}
	}
	return nil, driver.PointDiscoverySpec{}, false
}

// DiscoverPoints 让提供 protocol 的插件进程扫描一台已存在的设备上的点位。
//
// protocolJSON 是该设备的连接配置，**由调用方从 device 行读出后透传**：它决定
// 插件去连哪台设备（以及复用哪条会话），不接受客户端的值。options 是用户在
// 弹窗里填的扫描选项原文，原样透传；为空时插件侧看到的是未设置（不是空 JSON 串）。
//
// **不可取消**：插件侧一次扫描覆盖整段，中途被打断只会丢掉全部已读到的结果
// （它没有"只返回一部分"的入口）。ctx 被取消时本调用立即返回（client.call 在
// ctx.Done 上返回，迟到的应答被 dispatch 静默丢弃），但插件仍会跑完它自己的
// 预算 —— 那部分算力白花，所以前端不做"取消扫描"按钮，只把预算钳到 9s 以内。
//
// 超时 = min(插件自报的 MaxBudgetMs, pointScanGatewayBudgetCapMs) + pointScanRPCGrace。
// 为什么要额外钳一道见那两个常量的说明；插件侧拿到的是同一个被钳过的值
// （DiscoverPointsParams.BudgetMs），因此"插件是必须不超时的那一方"这条不变量
// 依然成立，只是两边用的是同一个上限。
func (m *Manager) DiscoverPoints(ctx context.Context, protocol, protocolJSON string,
	options json.RawMessage) (*driver.PointScanResult, error) {

	link, spec, ok := m.pointSpecFor(protocol)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPointDiscoveryUnsupported, protocol)
	}

	budget := budgetWithinGatewayCap(spec.MaxBudgetMs)
	ctx, cancel := context.WithTimeout(ctx, budget+pointScanRPCGrace)
	defer cancel()

	cl, err := link.acquire(ctx)
	if err != nil {
		return nil, err
	}

	var reply driver.PointScanResult
	// Options 过线用 string（理由见 driversdk.DiscoverPointsParams）：这里把 nil
	// 与空串都变成 ""，插件侧再转回 nil，两端对"没填选项"的判定因此一致。
	params := driversdk.DiscoverPointsParams{
		Protocol:     protocol,
		ProtocolJSON: protocolJSON,
		Options:      string(options),
		BudgetMs:     int(budget / time.Millisecond),
	}
	if err := cl.call(ctx, pluginwire.MethodDiscoverPoints, params, &reply); err != nil {
		// 失败时 reply 可能已经带上了插件的部分结果（插件契约里"带着已完成的部分
		// 结果返回 + error"是允许的），一并交给调用方 —— 丢掉它们对用户没有好处。
		if reply.Scanned > 0 || len(reply.Points) > 0 {
			return &reply, err
		}
		return nil, err
	}
	return &reply, nil
}

// budgetWithinGatewayCap 把插件声明的整段扫描预算钳到网关上限之内。
//
// 独立成函数是为了让它是**可测的纯函数**：这条钳制是"网关停不掉"那个故障的
// 唯一防线，而它的另一半（main.go 的 10s）无法在测试里断言。
func budgetWithinGatewayCap(declaredMs int) time.Duration {
	if declaredMs > pointScanGatewayBudgetCapMs {
		declaredMs = pointScanGatewayBudgetCapMs
	}
	if declaredMs <= 0 {
		// Meta.Validate 已保证 > 0；这里只是防御一个绕过校验的路径，别让它变成 0 预算。
		declaredMs = pointScanGatewayBudgetCapMs
	}
	return time.Duration(declaredMs) * time.Millisecond
}

// BudgetWithinGatewayCapMs 与 budgetWithinGatewayCap 同一条钳制，但以毫秒返回。
//
// 导出是给**服务层**用的：它要把"实际会用的预算"告诉前端。插件自报的
// MaxBudgetMs 与真正生效的值可以差三倍（Modbus 声明 20000，网关钳成 7000），
// 弹窗里显示前者的话，用户会按 20 秒预估、实际 7 秒就停了 —— 而他会去怀疑
// 设备和链路，根因却在网关这条同步 HTTP 的互锁上（见上面那两个常量）。
//
// 钳制只有这一处实现：服务层另写一遍 min(declared, 7000) 就是把那个数字
// 复制到了第二个地方，而这类"两处默认值人工对齐"的约定迟早会漂移。
func BudgetWithinGatewayCapMs(declaredMs int) int {
	return int(budgetWithinGatewayCap(declaredMs) / time.Millisecond)
}
