// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"context"
	"sync/atomic"
)

// pluginLink 是 remoteDriver 与「当前插件进程实例」之间的稳定间接层。
//
// 为什么必须有它：remoteDriver.proc 在构造时固定且不可变，而 Manager.register
// 的工厂闭包按值捕获进程指针。闭包若直接捕获 *pluginProcess，替换 loadedPlugin
// 的字段对已注册的工厂、以及对在途的 remoteDriver 毫无影响 —— 二进制替换永远
// 不会生效。把可变量放进被捕获对象的**内部**（本类型的原子指针），工厂闭包与
// 驱动实例就都无需改动。
//
// 这正是热升级不需要 collector.Refresh() 的原因：换入新实例时新一代的世代号
// 严格大于任何驱动可能持有的 epochAt（基数在 retireAndSwap 里取旧实例的终值，
// 见该方法的注释），于是每个既有驱动的 stale() 都翻为 true、
// IsConnected() 返回 false，采集引擎既有的 1s→60s 重连退避自然把设备接到
// 新进程上，其它协议与内置驱动完全不受影响。
//
// 不变量：
//   - cur 永不为 nil（构造时即持有一个活实例，之后只 Store 非 nil）；
//   - cur 只被整体替换、从不原地修改，替换由 Manager.scanOnce 串行化
//     （Manager.scanMu），因此不存在两个升级交错的窗口。
type pluginLink struct {
	path string
	cur  atomic.Pointer[pluginProcess]
}

var _ processLink = (*pluginLink)(nil)

// newPluginLink 绑定一个已拉起的进程实例。
func newPluginLink(path string, p *pluginProcess) *pluginLink {
	l := &pluginLink{path: path}
	l.cur.Store(p)
	return l
}

// 以下三个方法即 processLink 的全部契约。三个方法共用一个原子指针，
// 读侧不可能看到「新世代号 + 旧客户端」的撕裂视图 —— 拆成两个原子量恰好
// 会制造出让陈旧会话 id 看起来新鲜的窗口。

func (l *pluginLink) acquire(ctx context.Context) (*client, error) {
	return l.cur.Load().acquire(ctx)
}

func (l *pluginLink) isUp() bool { return l.cur.Load().isUp() }

func (l *pluginLink) epoch() uint64 { return l.cur.Load().epoch() }

// current 返回当前进程实例，供诊断（spawned/currentCmd）与测试使用。
func (l *pluginLink) current() *pluginProcess { return l.cur.Load() }

// retireCurrent 退役当前实例但不换入新的。
// 供 Manager.Stop() 与「插件文件消失」两条收尾路径使用，二者都不再有后继。
func (l *pluginLink) retireCurrent() {
	l.cur.Load().retire()
}

// retireAndSwap 退役当前实例并换入 next。顺序不可颠倒，两个理由：
//
//  1. 串口独占。换入之后采集引擎下一轮就会重连并打开设备连接。旧进程若仍
//     持有 RTU 串口，插件侧 open 在 Windows 上直接失败（串口独占），在 Linux
//     上**可能成功** —— 两个进程同时写 RS-485，帧交错，是静默的数据损坏。
//     retire() 同步等进程被回收（stop 等 supervisor 的 wg，其中已含
//     cmd.Wait），返回即确认句柄已释放。
//
//  2. 唤醒语义。retire 关闭旧实例的 stopCh，阻塞在旧实例 acquire 上的采集
//     协程立即返回；反过来它们要多等一个退役时长（最多 2×KillGrace）才被
//     唤醒，而新进程明明已经就绪可用。
//
// next 必须是已完成 describe 握手的新实例。握手期不打开任何设备连接
// （driversdk.Serve 只校验 Meta 与工厂表），所以「先拉起 next、后退役旧实例」
// 不会造成两个进程同时占用设备。
//
// **世代号基数在这里才确定（setEpochBase），不在 spawnAndDescribe 里。**
// 从「创建 next」到「换入」之间是拉起进程 + 握手的窗口（可达数十秒），旧实例
// 的监管协程在这期间仍可能崩溃重启一次、把它的 epoch 推高 1；若在构造期就
// 把基数定成当时读到的旧 epoch，新实例 markUp 后的世代号恰好与重启后的旧实例
// 相同 —— 于是重连到旧实例的驱动在换代后 stale() 为假，继续把旧会话 id 发进
// 新进程（该 id 可能属于新进程里另一台设备的会话，静默读到别的设备）。
// retire() 返回后旧实例的 epochNum 不可能再变，此刻读到的才是它的终值。
//
// 调用方必须持有 Manager.scanMu，保证这里不会与另一次升级交错。
func (l *pluginLink) retireAndSwap(next *pluginProcess) {
	old := l.cur.Load()
	old.retire()
	next.setEpochBase(old.epoch())
	l.cur.Store(next)
}
