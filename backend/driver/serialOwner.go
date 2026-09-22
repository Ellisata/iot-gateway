// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import (
	"fmt"
	"strings"
	"sync"
)

// SerialOwner 由「底层连接独占串口」的驱动实现。
//
// 存在理由：HTTP「测试连接」只拿得到协议名与 protocol_json，拿不到设备 ID，
// 够不着采集引擎 drivers map 里正在采集的那个实例；而 Windows 的串口是独占的
// （goburrow/serial 以共享模式 0 调 CreateFile），引擎已持句柄时 Ping 再开一个句柄
// 必然报 Access is denied —— 把「设备其实可达」误报成失败。
// 采集引擎据此把自己的实例登记进本包（RegisterSerialOwner），PingDevice 借它找回实例，
// 由该实例的 Ping 复用已有连接完成握手。
//
// 实现约定：**只有串口传输的驱动实现本接口**。TCP 一律不实现 —— TCP 不存在端口独占，
// 复用反而受害：一条 IsConnected() 仍为真、实际早已失效的长连接，会让本可成功的
// 重连测试误判为失败，而新开一条连接的代价为零。
type SerialOwner interface {
	// MatchConnection 本实例当前持有的连接能否直接服务 protocolJSON 描述的这次测试。
	//
	// 只做只读比较，不得有副作用；只作候选筛子，不能用来判定可达性 ——
	// 可达性由 Ping 真正收发一帧来判定。
	// 参数必须逐项一致才可复用：表号 / 从站号已固化在连接里，
	// 复用一条属于别的设备的链路，会把请求发到别的表上。
	MatchConnection(protocolJSON string) bool

	// SerialResource 本实例当前独占的串口名（如 "COM1"）；未持有串口时返回空串。
	//
	// 「持有」以句柄为准，而不是以 IsConnected() 为准：串口读失败只会把连接标记为
	// 断开、并不释放句柄，那个端口在重连之前仍然被本实例占着，
	// 此时 SerialBusyHint 必须能把它指出来。
	SerialResource() string
}

// serialOwnerEntry 一条登记：谁（label）在哪个协议名下持有独占串口。
type serialOwnerEntry struct {
	protocol string
	label    string // 人类可读的持有者（设备名），仅用于错误提示
	drv      Driver
}

// serialOwners 独占串口持有者登记簿。
//
// 由采集引擎显式登记 / 注销，而不是由 Create 顺带登记：Create 只是工厂，
// 让它接管实例生命周期会把各驱动的测试里 Create 出来的临时实例一起卷进来，
// 而「哪台设备正占着 COM1」是只有引擎知道的事实。
//
// 锁序铁律：**本锁永不与任何驱动自身的锁嵌套**。
// findLiveSerialOwner / SerialBusyHint 一律先拷贝快照、放锁，再去调驱动方法。
// 否则与 gatewayTask.Stop 构成 AB-BA：Ping 侧持本锁等 d.mu，
// Stop 侧在 Close 里持 d.mu、随后注销又要本锁，双双挂死。
// 这不是优化，是正确性前提。
var (
	serialOwnerMu      sync.RWMutex
	serialOwnerEntries []serialOwnerEntry
)

// RegisterSerialOwner 登记一个由采集引擎持有、可能正占用串口的驱动实例。
//
// protocol 为该实例注册时的协议名（与 Register 的键一致）；label 是用于错误提示的
// 人类可读持有者（采集引擎传设备名），可为空。
// 不实现 SerialOwner 的驱动（TCP 类）会被直接跳过，登记簿规模恒等于串口设备数。
// 重复登记同一实例不产生额外条目。
func RegisterSerialOwner(protocol, label string, d Driver) {
	if protocol == "" || d == nil {
		return
	}
	if _, ok := d.(SerialOwner); !ok {
		return
	}

	serialOwnerMu.Lock()
	defer serialOwnerMu.Unlock()
	for _, e := range serialOwnerEntries {
		if e.drv == d {
			return
		}
	}
	serialOwnerEntries = append(serialOwnerEntries,
		serialOwnerEntry{protocol: protocol, label: label, drv: d})
}

// UnregisterSerialOwner 注销实例，由采集引擎在关闭该实例后调用。
// 实例未登记或已注销时为无操作，允许重复调用。
func UnregisterSerialOwner(d Driver) {
	if d == nil {
		return
	}

	serialOwnerMu.Lock()
	defer serialOwnerMu.Unlock()

	kept := serialOwnerEntries[:0]
	for _, e := range serialOwnerEntries {
		if e.drv != d {
			kept = append(kept, e)
		}
	}
	// 清掉尾部残留的元素，否则被移出的条目仍占着数组槽、拖住已注销的驱动不让 GC 回收
	for i := len(kept); i < len(serialOwnerEntries); i++ {
		serialOwnerEntries[i] = serialOwnerEntry{}
	}
	serialOwnerEntries = kept
}

// SerialOwnerCount 返回当前登记的实例数（诊断与测试用）。
func SerialOwnerCount() int {
	serialOwnerMu.RLock()
	defer serialOwnerMu.RUnlock()
	return len(serialOwnerEntries)
}

// snapshotSerialOwners 拷贝一份登记快照。protocol 为空表示不过滤。
//
// 返回拷贝而不是持锁迭代，是因为本锁不得与驱动自身的锁嵌套（见 serialOwners 的锁序说明）。
func snapshotSerialOwners(protocol string) []serialOwnerEntry {
	serialOwnerMu.RLock()
	defer serialOwnerMu.RUnlock()

	out := make([]serialOwnerEntry, 0, len(serialOwnerEntries))
	for _, e := range serialOwnerEntries {
		if protocol == "" || e.protocol == protocol {
			out = append(out, e)
		}
	}
	return out
}

// findLiveSerialOwner 查找能服务 (protocol, protocolJSON) 的已连接实例，找不到返回 nil。
//
// 倒序查找（最后登记的优先）：热刷新会先建新任务再停旧任务，窗口期新旧两个实例同时在册，
// 新的那个更贴近当前配置。
// 判定顺序是「先接口断言、再 IsConnected」而不是反过来：不实现 SerialOwner 的驱动
// 连 IsConnected 都不会被调到，把并发读面收敛到最小。
func findLiveSerialOwner(protocol, protocolJSON string) Driver {
	entries := snapshotSerialOwners(protocol)
	for i := len(entries) - 1; i >= 0; i-- {
		d := entries[i].drv
		owner, ok := d.(SerialOwner)
		if !ok || !d.IsConnected() {
			continue
		}
		if owner.MatchConnection(protocolJSON) {
			return d
		}
	}
	return nil
}

// SerialBusyHint 返回「指定串口被谁占用」的提示片段，无人占用时返回空串。
//
// 只用于把 Windows 那句毫无指向性的 Access is denied 翻译成人能照做的动作。
// 按**串口名**查而不是按协议名查：出问题的恰恰是「别的协议正占着 COM1，
// 却去测 DLT645」，按协议查会查不到。
//
// 这里刻意不要求 IsConnected()：串口读失败只标记断开、不释放句柄，
// 那个端口在重连之前仍然被占着，而这正是用户最需要被告知的时刻。
func SerialBusyHint(port string) string {
	if port == "" {
		return ""
	}

	entries := snapshotSerialOwners("")
	for i := len(entries) - 1; i >= 0; i-- {
		d := entries[i].drv
		owner, ok := d.(SerialOwner)
		if !ok || !strings.EqualFold(owner.SerialResource(), port) {
			continue
		}
		who := "本进程内另一个采集任务"
		if entries[i].label != "" {
			who = fmt.Sprintf("设备「%s」的采集任务", entries[i].label)
		}
		return fmt.Sprintf("（%s 已被%s占用；Windows 下串口独占，请先停用该设备再测试）", port, who)
	}
	return ""
}
