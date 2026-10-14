// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gilogging "github.com/iceisfun/goindustrial/logging"
)

// encapTap 从库的 Warn 文案里截出封装层状态码（0x006F/0x0070 被拒时那条）。
//
// **为什么只能从日志文案里截**：库的 do() 把封装层拒绝归为传输错误，先
// transport.Reset 掉会话、再用 logger.Warn 打出真实原因，最终上抛的只是
// "operation failed after N retries" —— 状态码根本没进错误链，唯一带着它的地方
// 就是这条 Warn。桥接器本来就是为接住这类被吞掉的原因而存在的（见 giLevelBridge
// 的说明），这里只是把"接住"从"给人看"推进到"给代码用"（驱动据此把封装层拒绝与
// 真正的网络故障分开，见 cipError.go 的 encapError）。
//
// **代价与退路**：这耦合了库的文案格式。库改了措辞就截不到 —— 那时 encapError
// 不会出现，驱动退回今天的行为（照旧上抛错误、采集侧照旧判断联），只是少一条更准
// 的解释。方向是刻意选的：**宁可认不出来，也不能认错** —— 把一次真正的网络故障
// 说成"设备在线但拒绝了命令"，会让运维去查错地方（正是本仓库反复挡过的那类错）。
// 回归见 bridgelogger_test.go。
type encapTap struct {
	// last 最近一次截到的状态码，0 表示没截到。用 Swap 取走（见 take），
	// 免得上一次的拒绝去解释下一次的失败。
	last atomic.Uint32
}

// observe 从一条已格式化的库日志里截取状态码，截到则返回它。
//
// 认的是库的这句原文（session.go:134）：
//
//	RRData command failed with status: 0x%08X
func (t *encapTap) observe(msg string) (uint32, bool) {
	const marker = "RRData command failed with status: 0x"
	i := strings.Index(msg, marker)
	if i < 0 {
		return 0, false
	}
	digits := msg[i+len(marker):]
	if len(digits) < 8 {
		return 0, false
	}
	v, err := strconv.ParseUint(digits[:8], 16, 32)
	if err != nil {
		return 0, false
	}
	t.last.Store(uint32(v))
	return uint32(v), true
}

// take 取走最近一次的状态码并清零。0 视为"没有过"（封装层状态 0 是成功，
// 永远不该出现在这条 Warn 里）。
func (t *encapTap) take() (uint32, bool) {
	v := t.last.Swap(0)
	return v, v != 0
}

// giLevelBridge 把 goindustrial 日志桥接到插件日志（driversdk.Logf → stderr）。
//
// goindustrial 的 do() 重试循环在传输错误时只通过内部 logger.Warn 输出真实原因
// （如 i/o timeout、响应解析失败），最终上抛的仅是 "operation failed after N retries"
// 兜底文案；默认 NopLogger 会把关键诊断信息全部丢弃。接入桥接后，库内 Warn/Error
// 直接进插件日志，可定位读标签失败的根因。
//
// 级别映射（插件侧只有两个出口，见 log.go）：
//
//	Trace → 丢弃（库的帧级 hexdump，量太大）
//	Debug → logDebug（受 Debug 开关门控）
//	Info  → logDebug（**也受开关门控**，理由见下）
//	Warn  → logf（受 warnLimit 限流，见 warnLimiter）
//	Error → logf
//
// **Info 为什么归到开关后面**：库的 Info 全是"每建一次连接"的握手流水
// （Sending RegisterSession / Session registered / Sending UnregisterSession /
// Max Symbol Instance）。设备一旦进入"每轮重连"的状态，这几行就按轮询周期无限重复
// —— 而本插件偏偏很容易落进那种状态：封装层拒绝会让库 Reset 掉会话（见 cipError.go
// 的 encapError），下一轮 ensureConnected 又重连，如此往复。它们讲的事（"连上了"）
// 采集侧也已经有一行 `device reconnected` 覆盖（collector/task.go）。要看握手细节
// 就把 Debug 开关打开。
//
// Warn/Error 留在开关外面：那些是被库吞掉、别处拿不到的原因，静音它们等于让现场
// 失去唯一的诊断入口。但 Warn 会**限流**（见 warnLimiter）—— 同一个原因每轮重复
// 一遍同样是刷屏，只是不能因此把它彻底关掉。
type giLevelBridge struct {
	// tap 可选：非 nil 时顺便把封装层拒绝的状态码截给驱动用（见 encapTap）。
	tap *encapTap
	// addr 本连接的目标地址（host:port）。只用于给限流当键：库的文案里不一定含
	// 地址，两台设备打出同一句话时不能互相吃掉。
	addr string
}

// newGILevelBridge 创建 goindustrial → 插件日志的桥接器。
// tap 传 nil 表示只做日志转录、不截状态码。
func newGILevelBridge(addr string, tap *encapTap) gilogging.Logger {
	return &giLevelBridge{tap: tap, addr: addr}
}

// warnLimiter 给库的 Warn 限流：同一台设备的同一句话在窗口内只输出一次。
//
// **为什么限流而不是"只打一次"，也不是直接静音**：静音是把这个失败从现场视野里
// 抹掉（与"把设备判成在线、点位标中性质量码"是同一个错误 —— 见 driver.go 的
// tagReadError），而"只打一次"在设备好了又坏时再也不出声。限流两头都占：一直坏就
// 每隔一个窗口露一次头，换了原因立刻出声。
//
// **为什么状态挂在包级、而不是桥接器上**：桥接器与客户端是**按连接**创建的
// （newRockwellClient 每次重连都新建一份），而这里要挡的恰恰是"每轮重连、每轮同一句"
// —— 状态挂在桥上等于每轮清零，一句也挡不住。插件进程是按协议常驻的，进程级状态
// 正好对上"这台设备一直这样"这个事实。
//
// 窗口取 60s，与采集侧自己的 reconnectMaxDelay 同量级（collector/task.go）：
// 比它短则挡不住，比它长则设备恢复正常前的最后一条原因会留得太久。
const warnRepeatInterval = 60 * time.Second

type warnLimiter struct {
	mu   sync.Mutex
	last map[string]warnEntry // 键 = 设备地址，容量与设备数同阶，不会随失败次数增长
}

type warnEntry struct {
	msg string
	at  time.Time
}

// allow 判断这句话现在该不该输出。
func (l *warnLimiter) allow(addr, msg string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.last == nil {
		l.last = make(map[string]warnEntry)
	}
	now := time.Now()
	if prev, ok := l.last[addr]; ok && prev.msg == msg && now.Sub(prev.at) < warnRepeatInterval {
		return false
	}
	l.last[addr] = warnEntry{msg: msg, at: now}
	return true
}

// warnLimit 插件进程内共享的限流器（理由见 warnLimiter 的注释）。
var warnLimit = &warnLimiter{}

func (b *giLevelBridge) Trace(ctx context.Context, format string, args ...any) {
	// 库的帧级 hexdump 跟踪日志，量太大，不转发
}

func (b *giLevelBridge) Debug(ctx context.Context, format string, args ...any) {
	logDebug("goindustrial: "+format, args...)
}

func (b *giLevelBridge) Info(ctx context.Context, format string, args ...any) {
	// 握手流水，每重连一次就刷一遍，归到 Debug 开关后面（理由见类型注释）。
	logDebug("goindustrial: "+format, args...)
}

func (b *giLevelBridge) Warn(ctx context.Context, format string, args ...any) {
	// 先格式化再输出，而不是把 format 直接喂给 logf：库的消息里可能带 %，
	// 当 format 用会被 fmt 二次解释（例如 %08X 已填成 0x00000001 后才轮到这里）。
	msg := fmt.Sprintf(format, args...)
	if b.tap != nil {
		if st, ok := b.tap.observe(msg); ok {
			// 顺手把码翻译成人话：这一行是现场唯一能看到真实原因的地方。
			//
			// **observe 必须在限流之前**：限流只压"输出"，不能压"截码" ——
			// 否则被压掉的那些轮次里驱动就拿不到状态码，封装层拒绝会退化成
			// 一句 operation failed after N retries（正是本轮改动要消灭的东西）。
			msg = fmt.Sprintf("%s（%s）", msg, describeEncapStatus(st))
		}
	}
	if !warnLimit.allow(b.addr, msg) {
		return
	}
	logf("goindustrial: %s", msg)
}

func (b *giLevelBridge) Error(ctx context.Context, format string, args ...any) {
	logf("goindustrial: "+format, args...)
}

// WithFields 桥接器无结构化字段支持，返回自身（字段仅影响库内部展示格式）
func (b *giLevelBridge) WithFields(fields map[string]any) gilogging.Logger {
	return b
}

// GetLevel 恒返回 LevelDebug：库据此决定**要不要把 Debug 级日志送过来**。
//
// 这是刻意的取舍，不是疏漏：Debug 档现在由插件侧的 logDebug（Debug 开关）门控，
// 库这一头必须继续把 Debug 日志送过来，否则"源码级开关打开后仍看不到库内
// Debug 日志"。若把返回值改成 LevelInfo/LevelWarn 之类"看起来更贴合新映射"的
// 级别，库会在源头把 Debug 日志整批丢掉，且没有任何编译期或运行期提示。
//
// 代价是默认配置下库内 Debug 日志到了插件侧仍要过一次 logDebug —— 那只是一次
// bool 判断，不产生 I/O（Debug 为 false 时不格式化、不写 stderr）。
func (b *giLevelBridge) GetLevel() gilogging.Level {
	return gilogging.LevelDebug
}

func (b *giLevelBridge) SetLevel(level gilogging.Level) {
	// 级别由插件侧的 Debug 开关统一管理，不接受库内调整
}

// 编译期保证桥接器实现 goindustrial Logger 接口
var _ gilogging.Logger = (*giLevelBridge)(nil)
