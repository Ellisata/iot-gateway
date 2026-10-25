// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

// ==================== 驱动插件适配层：局域网扫描发现 ====================
//
// 本文件与 adapter_driver.go（describe → driver 注册表）、provision.go（协议行供给）、
// remote.go（RPC → driver.Driver）同属「认识驱动这个概念」的那一组，管的是
// probe 方法 —— connect/read 走 remoteDriver，probe 无会话、无驱动实例，因此
// 直接从 Manager 出口。
//
// 通用核心（process.go / upgrade.go / client.go / link.go）不认识这里任何东西。

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

// ErrDiscoveryUnsupported 当前没有任何已加载的插件为该协议提供扫描能力。
//
// 与 provision.go 的 ErrProtocolNotProvisioned 是两回事：那个是"没有插件提供
// 这个协议"，这个是"有插件提供协议、但它没声明扫描能力"。分开是因为处置不同 ——
// 前者要看插件目录，后者要去换一个带扫描能力的插件版本。
var ErrDiscoveryUnsupported = errors.New("pluginhost: 该协议不支持局域网扫描")

// probeRPCGrace probe RPC 超时相对插件自报预算的余量。
//
// 插件的 MaxBudgetMs 管的是「探测」本身，RPC 还要加上帧编解码、插件侧调度与
// 本地管道往返 —— 这点余量留给它们。取 3s 而非几百毫秒：插件预算是秒级的，
// 余量给它一个数量级以下的固定值即可，不必精细。
const probeRPCGrace = 3 * time.Second

// DiscoverySpecs 返回当前已加载插件声明的全部扫描能力（按协议名排序）。
//
// **每次现算，不维护缓存表。** 热升级只替换 lp.meta（见 upgrade.go），而
// registrationSig 只含 scope / protocols / types / typeMap，**不含 Discovery** ——
// 于是一个只改了 Discovery 的新二进制升级后不会触发重注册。若把能力另存一张表，
// 它会停留在旧值上，症状是"插件明明加了扫描能力、前端却一直没有"，且两边日志
// 都写着成功。遍历 m.plugins 读 lp.meta 则一致性自动成立，unregister 也无需清理。
//
// 返回的 spec 是副本（含 FormJSON 的独立底层数组）：调用方会把它序列化给前端，
// 不该让一次改写污染已加载插件的 meta。
func (m *Manager) DiscoverySpecs() []driver.ProtocolDiscovery {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []driver.ProtocolDiscovery
	for _, lp := range m.plugins {
		for i := range lp.meta.Protocols {
			p := &lp.meta.Protocols[i]
			if p.Discovery == nil {
				continue
			}
			spec := *p.Discovery
			spec.FormJSON = append([]byte(nil), spec.FormJSON...)
			out = append(out, driver.ProtocolDiscovery{Protocol: p.Name, Spec: spec})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Protocol < out[j].Protocol })
	return out
}

// scanSpecFor 找到提供该协议的插件的链接与它声明的扫描规格。
//
// 返回 link 而不是进程指针：与 adapter_driver.go 里工厂闭包的取舍相同 ——
// 热升级要能把调用从旧进程挪到新进程，闭包/调用方持有的必须是那个稳定的间接层。
func (m *Manager) scanSpecFor(protocol string) (*pluginLink, driver.DiscoverySpec, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, lp := range m.plugins {
		for i := range lp.meta.Protocols {
			p := &lp.meta.Protocols[i]
			if p.Name != protocol || p.Discovery == nil {
				continue
			}
			return lp.link, *p.Discovery, true
		}
	}
	return nil, driver.DiscoverySpec{}, false
}

// ProbeTargets 把一个目标交给提供 protocol 的插件进程探测。
//
// options 是用户在扫描页填的扫描选项原文（形状由该协议 DiscoverySpec.FormJSON
// 定义），原样透传；为空时插件侧看到的是未设置（不是空 JSON 串）。
//
// **无会话**：probe 不分配插件侧会话、不持有跨调用资源，因此不需要 remote.go
// 里那种「acquire 之后再拿 cl.epoch 复核一次」的检查 —— 那道检查防的是
// "陈旧会话 id 打到新进程里的另一台设备的会话"，而 probe 根本没有会话 id。
// 换代后 acquire 拿到的就是新进程的 client，探测在新进程上照做即可。
//
// 超时由插件自报的 MaxBudgetMs 推导（+probeRPCGrace），**不是一个全局配置值**：
// 预算是每协议的属性（UDP 广播探测天然比 TCP 拨号慢），配一个全局旋钮的话，
// 它配小了就是"RPC 先超时、插件还在跑、网关拿到空结果、用户看到扫不到设备"，
// 而日志里只有一条 timeout —— 典型的"配错了就静默不工作"。取值区间已由
// driversdk.Meta.Validate 限死在 [500, 60000] ms，超时值因此自然有界。
//
// 调用方传入的 ctx 被取消时，本次探测立即返回（client.call 在 ctx.Done 上返回，
// 迟到的应答被 dispatch 静默丢弃）。注意这**不等于**插件立刻停止探测：它会在
// 自己的预算耗尽后才返回，那个结果已无人接收。
func (m *Manager) ProbeTargets(ctx context.Context, protocol string, options json.RawMessage,
	target driver.ScanTarget) (*driversdk.ProbeReply, error) {

	link, spec, ok := m.scanSpecFor(protocol)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrDiscoveryUnsupported, protocol)
	}

	timeout := time.Duration(spec.MaxBudgetMs)*time.Millisecond + probeRPCGrace
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cl, err := link.acquire(ctx)
	if err != nil {
		return nil, err
	}

	var reply driversdk.ProbeReply
	// Options 过线用 string（理由见 driversdk.ProbeParams）：这里把 nil 与空串
	// 都变成 ""，插件侧再转回 nil，两端对"没填选项"的判定因此一致。
	if err := cl.call(ctx, pluginwire.MethodProbe, driversdk.ProbeParams{
		Protocol: protocol,
		Options:  string(options),
		Target:   target,
		BudgetMs: spec.MaxBudgetMs,
	}, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}
