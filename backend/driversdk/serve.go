// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driversdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
	"iot-gateway/pluginwire"
)

// Factory 创建一个新的驱动实例。与 driver.NewFunc 同形，
// 一个会话（= 网关侧一个设备）对应一次调用。
type Factory = driver.NewFunc

// Factories 协议名 → 工厂。**按协议名索引**：一个插件可以提供多个协议
// （如 Modbus 的 TCP 与 RTU，两者实现不同但共用类型表），子进程依据
// connect/ping 请求里的协议名选择对应工厂。
// 键必须与 Meta.Protocols 中的 Name 完全一致。
type Factories map[string]Factory

// ProbeRequest 一次局域网扫描探测的全部输入。
type ProbeRequest struct {
	// Protocol 目标协议名（= Meta.Protocols[].Name），一个插件有多个 prober 时用它区分。
	Protocol string

	// Options 用户在扫描页填的扫描选项原文，形状由本协议 DiscoverySpec.FormJSON 定义。
	// 未填时长度为 0（不是空 JSON 串）—— 实现里用 len(Options) == 0 判定，直接
	// json.Unmarshal 一个长度为 0 的 RawMessage 会报 "unexpected end of JSON input"。
	Options json.RawMessage

	// Target 本次要探测的单个目标。
	Target driver.ScanTarget

	// Budget 本次探测必须返回的时间上限，取「本协议声明的 MaxBudgetMs」与
	// 「网关请求值」的较小者。
	Budget time.Duration
}

// ProbeFunc 探测单个目标。契约有五条，全部与网关侧的可用性直接相关：
//
//  1. **返回 (nil, nil) 表示"这不是我的设备"** —— "没扫到"不是错误。
//     目标上跑着别的服务（502 端口上开着 HTTP）、或者压根没有设备，都属于
//     正常结果。把这种情况当 error 会让一次 /24 扫描产生两百多条错误。
//  2. error 只用于探测本身失败（选项解析不出来、协议内部错误）。
//  3. **必须在 req.Budget 内返回**。超预算会撞网关侧的 probe RPC 超时，
//     并在 pluginhost 的卡死计数上累加 —— 连续几次会把整个插件进程连同
//     它下面的所有协议、所有设备一起重启。预算不够时的正确做法是**带上
//     已完成的部分结果正常返回**（如站号只扫了一半）。
//  4. **不得分配会话、不得持有跨调用资源**。probe 没有 session，
//     调用方永远不会为它调 Close（理由同 PingParams）。
//  5. panic 会被 handle 的 recover 兜住并降级成一次 RPC 错误，但这意味着
//     那一次探测白做了 —— 边界（空切片、越界）自己判。
type ProbeFunc func(req ProbeRequest) ([]driver.DiscoveredDevice, error)

// Probers 协议名 → prober。键必须与 Meta.Protocols[].Name 一致。
//
// Meta 里声明了 Discovery 的协议**必须**在这里有对应项，否则 Serve 启动即失败
// （与"每个协议都必须有工厂"同一条校验）。不声明扫描能力的协议放进来的
// prober 永远不会被调用，只会有一条 WARN。
//
// 不需要扫描能力时传 nil 即可。
type Probers map[string]ProbeFunc

// SessionAccess 让 finder 在一个**不属于自己**的驱动实例上执行读取。
//
// 为什么不让 finder 直接拿 driver.Driver 自己调 Read —— 三条理由，每条都能致命：
//
//  1. **驱动自己的 Read 不是为扫描设计的**。它读设备的 cfg 做区间规划：Modbus 的
//     groupAddrsByFC / CalcReadRanges 都以"配置模式的 startAddress/functionCode"
//     优先。配置模式（startAddress > 0）下扫描按线圈区生成的地址会被当保持寄存器
//     读、区间规划还会因为 addr < startAddress 直接报错，而解析是越界安全的 ——
//     于是**不 panic、不报错，只是静默给出错位的值**。更糟的是这些地址一旦建成
//     device_address，那个点位会**永远取到别处的数据**（在时序库里表现为一条完全
//     合理的曲线，看不出异常），而错在哪一处无从追。
//  2. **会话是共享资源**。Do 每次进入才取锁、fn 返回即放锁，因此扫描可以在块与块
//     之间把锁让出去。一次 Do 覆盖整段扫描 = 把采集卡死几十秒（与 pingSessionBudget
//     同一条理由）。
//  3. **复用时那个实例正在采集**。它是采集引擎持有的对象，finder 不得 Close/Connect。
type SessionAccess interface {
	// Do 把 fn 交给一个驱动实例执行，fn 返回即释放会话。
	//
	// 契约：
	//   - **每次 Do 都可能换一个实例**（复用路径下尤其如此，块与块之间锁是放开的），
	//     所以不要缓存 drv 指针，块间重新调用 Do；
	//   - fn 内部**不得**调用 drv.Close / drv.Connect —— 复用路径下那是正在采集的
	//     实例，关掉它等于把设备打成离线；
	//   - fn 在会话锁下执行，**必须尽快返回**；持锁期间采集侧读不到这台设备。
	Do(fn func(drv driver.Driver) error) error
}

// FindPointsRequest 一次点位范围扫描的全部输入。
//
// 与 ProbeRequest 的两处关键差异：**带连接配置**（目标是网关上一台已存在的设备，
// 而不是任意一个 IP）与**带 Session**（要读那台设备，串口独占时只有它自己的
// 采集会话能读）。
type FindPointsRequest struct {
	// Protocol 目标协议名（= Meta.Protocols[].Name），一个插件有多个 finder 时用它区分。
	Protocol string

	// Options 用户在弹窗里填的扫描选项原文，形状由本协议 PointDiscoverySpec.FormJSON 定义。
	// 未填时长度为 0（不是空 JSON 串）—— 实现里用 len(Options) == 0 判定，
	// 直接 json.Unmarshal 一个长度为 0 的 RawMessage 会报 "unexpected end of JSON input"。
	//
	// **必须由 finder 自己解析**：网关不认识任何协议的扫描选项，只做透传。
	Options json.RawMessage

	// Budget 本次扫描必须返回的时间上限，取「本协议声明的 MaxBudgetMs」与
	// 「网关请求值」的较小者。
	Budget time.Duration

	// Deadline 本次扫描的绝对截止时刻，等于调用时刻 + Budget。
	//
	// 它与 Budget 是同一件事的两种形态，这里都给是为了让 finder 好写：
	// 主循环用 Deadline 判"还有没有时间"（`time.Now().After(req.Deadline)`），
	// 而单次读写的超时则要从剩余时间里夹出来。**每块之前判一次**，
	// 不要在块中途才开始判 —— 那样最后一块可能跑过截止时刻。
	Deadline time.Time

	// Session 读取驱动实例的唯一入口，见 SessionAccess。
	//
	// 不调 Do 也是合法的：**原生枚举**类实现（OPC UA 的 Browse、ABB 的信号表、
	// DLT645 的数据标识字典）根本不需要读设备，它们只用 Options 就能列出点位。
	// 这条留白就是本框架为它们留的位置。
	Session SessionAccess
}

// FindPointsFunc 扫描一台已存在设备上的点位。契约：
//
//  1. **必须在 req.Budget 内返回**。超预算会撞网关侧的 RPC 超时，并在 pluginhost
//     的卡死计数上累加 —— 连续几次会把整个插件进程连同它下面所有协议、所有设备
//     一起重启。预算不够时的正确做法是**带上已完成的部分结果正常返回**，
//     并把 PointScanResult.Incomplete 置位（不是返回 error）。
//  2. **空结果不是错误**。范围里一个地址都读不到是完全正常的（设备只实现了其中
//     一小段、或者用户给的范围不对），报 error 会让用户以为功能坏了。
//  3. **error 只用于"这次扫描根本没法进行"**（选项解析不出来、设备配置处于
//     不支持的形态、连接直接断开）。一旦出现这种情况，把已经拿到的部分结果
//     一起返回比丢掉它们有用 —— 网关侧两者都会透给用户。
//  4. **不得 Close/Connect req.Session 里的驱动**（见 SessionAccess）。
//  5. panic 会被 handle 的 recover 兜住并降级成一次 RPC 错误，但那一次扫描
//     白做了 —— 边界（空切片、越界）自己判。
type FindPointsFunc func(req FindPointsRequest) (*driver.PointScanResult, error)

// PointFinders 协议名 → finder。键必须与 Meta.Protocols[].Name 一致。
//
// 与 Probers 并行的一套能力表，唯一区别是它面向**已存在的设备**而不是网段。
// Meta 里声明了 PointDiscovery 的协议**必须**在这里有对应项，否则 Serve 启动即失败。
type PointFinders map[string]FindPointsFunc

// Capabilities 插件声明实现的能力实现。
//
// 刻意用结构体而不是「再加一个裸参数」：第三种能力（原生枚举）已在
// FindPointsRequest.Session 的注释里预告过，而 Main/Serve/ServeConn 是本套件
// 对外的三个入口 —— 每加一种能力就把改动重复三遍，且现有 25 处调用点
// （9 个插件 main + 16 处测试）都要再动一次。字段零值即"没有这个能力"，
// 与 Meta 里对应的声明为 nil 是一回事，二者必须一致。
type Capabilities struct {
	// Probers 局域网设备扫描探测（Meta.Protocols[].Discovery）。
	Probers Probers

	// PointFinders 点位范围扫描（Meta.Protocols[].PointDiscovery）。
	PointFinders PointFinders
}

// Main 是 main() 的便捷封装：Serve 返回错误时打印到 stderr 并 os.Exit(1)。
//
// caps 的两个字段为零值表示本插件不提供对应能力 —— 这与 Meta 里各协议的
// Discovery / PointDiscovery 为 nil 是一回事，二者必须一致。
func Main(factories Factories, meta Meta, caps Capabilities) {
	if err := Serve(factories, meta, caps); err != nil {
		fmt.Fprintf(os.Stderr, "iot-gateway driver plugin: %v\n", err)
		os.Exit(1)
	}
}

// Serve 在 stdin/stdout 上运行插件主循环，阻塞直到 stdin EOF（父进程退出）
// 或收到 shutdown，返回 nil 表示正常退出。
//
// Serve 会：
//  1. 保存真实 stdout 用于写帧，并把 os.Stdout 重定向到 stderr，
//     使插件代码里任何 fmt.Println 都不会破坏帧流（网关会捕获 stderr 转自己的日志）；
//  2. 校验 Meta 可序列化，把"漏了 json:\"-\" 标签"变成明确的启动失败；
//  3. 把 Meta.Types 注册进本进程内的 TypeRegistry（ForProtocol(Meta.Scope)），
//     使插件在 Read 内部可以按 addr.DataType 取到 Kind/Size；
//  4. 为每个请求起一个 goroutine 处理，慢速 Read 不阻塞其它设备的 connect/read；
//  5. 同一会话内的调用串行化（见 session 的注释）。
//
// meta.Protocols 中的每个协议都必须在 factories 里有对应工厂，声明了 Discovery /
// PointDiscovery 的还必须在 caps 里有对应实现，否则启动即失败（宁可启动期报错，
// 也不要等到某个设备采集时、或运维点开页面时才失败）。
//
// 注意：Serve 不调用 driver.Register —— 驱动注册由网关侧依据 Meta 完成。
func Serve(factories Factories, meta Meta, caps Capabilities) error {
	// 保留真实 stdout 用于写帧。此后插件代码里对 os.Stdout 的写入
	// （fmt.Println 在调用时解析 os.Stdout）一律落到 stderr。
	realOut := os.Stdout
	os.Stdout = os.Stderr

	return serve(os.Stdin, realOut, os.Stdin, factories, meta, caps)
}

// ServeConn 同 Serve，但在任意 io.ReadWriteCloser 上运行。
//
// 供测试以 net.Pipe() 在进程内跑完整协议栈（无需子进程），
// 也可用于把插件嵌入宿主程序。
func ServeConn(rw io.ReadWriteCloser, factories Factories, meta Meta, caps Capabilities) error {
	return serve(rw, rw, rw, factories, meta, caps)
}

// session 一个设备连接。
//
// mu 串行化本会话内的驱动调用。采集引擎自身已按设备串行化（独占驱动整轮持锁，
// 非独占驱动只对单次 Read 持锁），这里再加一层是不依赖调用方约定的保险：
// driver.Driver 的 Connect/Read/Close 本身就不是为并发调用设计的。
type session struct {
	mu  sync.Mutex
	drv driver.Driver

	// protocol 会话所属的协议名。
	//
	// 在 handleConnect 里于写入 sessions 之前赋值，此后**不可变** ——
	// sessionsForProtocol 在 s.mu 下读它，这条"发布后不可变"的不变量
	// 是那个读操作成立的前提。
	//
	// 必须按协议过滤候选：一个插件可以提供多个协议（Modbus 的 TCP 与 RTU
	// 共处一进程），不过滤会让一次 RTU 的 ping 命中 TCP 会话，把一份串口
	// JSON 交给 TCP 驱动，报出完全指错方向的错误。
	protocol string

	// closed 驱动已被 Close/停机拆解。仅在持 mu 时读写。
	//
	// 存在的理由：「查会话表」与「拿 sess.mu」是两个动作，在途的 read 可能在
	// pluginwire.MethodClose 完成之后才拿到锁 —— 那会在一个已经 Close 的驱动上执行读取，
	// 拿回上一轮的缓存值当成新样本上报（或不巧触发 nil 解引用，把整个插件进程
	// 带走）。拿到锁后必须先看这个标记。
	closed bool
}

// server 插件侧的 RPC 服务端。
type server struct {
	r         io.Reader
	w         io.Writer
	closer    io.Closer
	factories Factories
	probers   Probers
	finders   PointFinders
	meta      Meta

	// probeBudgets 协议名 → 该协议声明的单目标探测预算。
	// 在 serve() 的启动校验里预计算，避免每次 probe 请求都扫一遍 Meta.Protocols。
	probeBudgets map[string]time.Duration

	// pointBudgets 协议名 → 该协议声明的整段扫描预算。理由同 probeBudgets。
	pointBudgets map[string]time.Duration

	// serialProtocols 哪些协议独占串行总线（Meta.Protocols[].SerialExclusive）。
	//
	// 点位扫描的回退规则需要它：串口协议在"有会话但复用了不"时**绝不能**回落到
	// 临时实例（见 handleDiscoverPoints 的说明），而 TCP 协议回落无害。
	// 与 probeBudgets 一样在启动期预计算，避免每次请求扫一遍 Meta。
	serialProtocols map[string]bool

	// wmu 串行化整帧写入，避免多个应答字节交错。
	wmu sync.Mutex

	mu       sync.Mutex
	sessions map[uint64]*session
	nextID   atomic.Uint64

	done     chan struct{}
	stopOnce sync.Once
	closed   atomic.Bool
}

func serve(r io.Reader, w io.Writer, closer io.Closer, factories Factories, meta Meta, caps Capabilities) error {
	if err := meta.Validate(); err != nil {
		return err
	}
	// 启动期就校验每个协议都有工厂：插件作者漏配一个协议的工厂，
	// 应该在进程启动时报错，而不是等到那个协议的设备来采集才失败。
	for _, p := range meta.Protocols {
		if _, ok := factories[p.Name]; !ok {
			return fmt.Errorf("driversdk: 协议 %q 没有对应的工厂（factories 的键必须与 Meta.Protocols[].Name 一致）", p.Name)
		}
	}

	// 同上：声明了扫描能力的协议必须有 prober。漏配在这里报错，
	// 而不是等运维在前端点开扫描页、看到"扫不到设备"才去猜。
	// 校验的同时把预算表算出来（Meta.Validate 已保证 MaxBudgetMs > 0）。
	probeBudgets := make(map[string]time.Duration)
	for _, p := range meta.Protocols {
		if p.Discovery == nil {
			continue
		}
		if _, ok := caps.Probers[p.Name]; !ok {
			return fmt.Errorf("driversdk: 协议 %q 声明了 Discovery 但没有对应的 prober"+
				"（Probers 的键必须与 Meta.Protocols[].Name 一致）", p.Name)
		}
		probeBudgets[p.Name] = time.Duration(p.Discovery.MaxBudgetMs) * time.Millisecond
	}
	// 多余的 prober 键只警告，不拒绝 —— 与工厂表的取舍对称（那里也只拒绝缺失）。
	// 这类 prober 永远不会被调用（网关只按自己看到的能力派发），说一声即可。
	for name := range caps.Probers {
		if _, ok := probeBudgets[name]; !ok {
			Logf("driversdk: prober %q 未在 Meta 中声明 Discovery，该 prober 不会被调用", name)
		}
	}

	// 点位扫描同上，并把两条扫描路径要用的表一并算出来。
	pointBudgets := make(map[string]time.Duration)
	serialProtocols := make(map[string]bool)
	for _, p := range meta.Protocols {
		if p.SerialExclusive {
			serialProtocols[p.Name] = true
		}
		if p.PointDiscovery == nil {
			continue
		}
		if _, ok := caps.PointFinders[p.Name]; !ok {
			return fmt.Errorf("driversdk: 协议 %q 声明了 PointDiscovery 但没有对应的 finder"+
				"（PointFinders 的键必须与 Meta.Protocols[].Name 一致）", p.Name)
		}
		pointBudgets[p.Name] = time.Duration(p.PointDiscovery.MaxBudgetMs) * time.Millisecond
	}
	for name := range caps.PointFinders {
		if _, ok := pointBudgets[name]; !ok {
			Logf("driversdk: finder %q 未在 Meta 中声明 PointDiscovery，该 finder 不会被调用", name)
		}
	}

	// 本进程内注册类型，供作者的 Read 内部解析 addr.DataType（与内置驱动同构）。
	for _, dt := range meta.Types {
		driver.GetTypeRegistry().ForProtocol(meta.Scope).Register(dt)
	}

	s := &server{
		r:               r,
		w:               w,
		closer:          closer,
		factories:       factories,
		probers:         caps.Probers,
		finders:         caps.PointFinders,
		meta:            meta,
		probeBudgets:    probeBudgets,
		pointBudgets:    pointBudgets,
		serialProtocols: serialProtocols,
		sessions:        make(map[uint64]*session),
		done:            make(chan struct{}),
	}
	return s.run()
}

func (s *server) run() error {
	frames := make(chan pluginwire.Frame)
	readErr := make(chan error, 1)

	go func() {
		defer close(frames)
		for {
			f, err := pluginwire.ReadFrame(s.r)
			if err != nil {
				readErr <- err
				return
			}
			select {
			case frames <- f:
			case <-s.done:
				return
			}
		}
	}()

	for f := range frames {
		if f.Method == pluginwire.MethodShutdown {
			s.reply(f.ID, struct{}{}, nil)
			s.shutdown()
			return nil
		}
		go s.handle(f)
	}

	// 帧通道关闭：读端结束（stdin EOF 或流损坏）。
	err := <-readErr
	s.shutdown()
	if errors.Is(err, io.EOF) {
		// 父进程退出（管道关闭），这是进程被强杀时的正常退出路径。
		return nil
	}
	return err
}

// shutdown 幂等地关闭全部会话与底层流，唤醒读协程。
func (s *server) shutdown() {
	s.stopOnce.Do(func() {
		s.closed.Store(true)
		close(s.done)

		s.mu.Lock()
		sessions := make([]*session, 0, len(s.sessions))
		for _, sess := range s.sessions {
			sessions = append(sessions, sess)
		}
		s.sessions = make(map[uint64]*session)
		s.mu.Unlock()

		for _, sess := range sessions {
			closeSessionLocked(sess)
		}

		// 关闭底层流以唤醒阻塞在 pluginwire.ReadFrame 的读协程，避免协程泄漏。
		if s.closer != nil {
			_ = s.closer.Close()
		}
	})
}

// newDriver 按协议名创建驱动实例。
func (s *server) newDriver(protocol string) (driver.Driver, error) {
	factory, ok := s.factories[protocol]
	if !ok {
		return nil, fmt.Errorf("unknown protocol %q", protocol)
	}
	return factory(), nil
}

func (s *server) getSession(id uint64) (*session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

// sessionCall 在会话锁下调用驱动，并保证**任何**退出路径都释放锁。
//
// 锁必须在 defer 里放掉，而不是在函数体末尾 Unlock：驱动 panic 时会穿过本帧
// 向上展开到 handle 的 recover，只有 defer 能让锁跟着放掉。锁一旦泄漏，
// shutdown() 会永久阻塞在 sess.mu.Lock() 上（会话表已清空、剩余会话与底层流都
// 没关、进程停不下来），比当初那次 panic 更难排查。
//
// 调用方一律在锁**外**应答：底层是同步管道，持锁写应答会卡住同一会话的其它请求。
func sessionCall[T any](sess *session, fn func() (T, error)) (T, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return fn()
}

// pingSessionBudget 复用路径上等待会话锁的上限。
//
// 这条路径会与采集侧的 Read 争同一把锁，而一次 Read 可能覆盖整批点位、在
// 9600bps 上跑到十几秒。不设上限的话，运维点一次「测试连接」就会挂到网关侧
// 的 ping 超时（默认 30s），既慢又会累积超时计数 —— 连续超时可能触发卡死检测
// （见 pluginhost/client.go 的 stuck），把整个插件进程连同它下面所有协议、
// 所有设备一起重启。宁可 3 秒后回一句"端口正忙"，也不要撞那条链。
//
// 固定常量而非配置项：这是"用户体验的止损"，不是一个运维旋钮。
const pingSessionBudget = 3 * time.Second

// sessionCallWithin 与 sessionCall 相同，但在 sess.mu 上最多等待 budget，
// 拿不到锁时返回 ok=false 且**不调用** fn。
//
// 用 TryLock + 小步退避而不是"带超时的 Lock"：标准库的 sync.Mutex 没有后者，
// 而 select + channel 包装会引入一个新 goroutine 和一次无法回收的竞争。
func sessionCallWithin[T any](sess *session, budget time.Duration, fn func() (T, error)) (v T, err error, ok bool) {
	if !acquireWithin(&sess.mu, budget) {
		var zero T
		return zero, nil, false
	}
	defer sess.mu.Unlock()
	v, err = fn()
	return v, err, true
}

// acquireWithin 在 budget 内尽力获取 mu，成功返回 true。
func acquireWithin(mu *sync.Mutex, budget time.Duration) bool {
	if mu.TryLock() {
		return true
	}
	deadline := time.Now().Add(budget)
	for delay := time.Millisecond; time.Now().Before(deadline); {
		time.Sleep(delay)
		if mu.TryLock() {
			return true
		}
		if delay < 32*time.Millisecond {
			delay *= 2
		}
	}
	return false
}

// sessionsForProtocol 返回本进程内该协议的活跃会话，**按会话 id 降序**（新的在前）。
//
// 快照在 s.mu 内取、在 s.mu 外使用 —— 与本包既有的 shutdown() 同构。
// 这是一条铁律而不是风格选择：现有代码里没有任何一处同时持有 s.mu 与 sess.mu
// （handleConnect 在 s.mu 外调 drv.IsConnected；MethodClose 先经 sessionCall
// 放掉 sess.mu，再在 s.mu 下 delete）。若在本函数里持 s.mu 去调驱动方法，
// 就会与那条路径构成 AB-BA 死锁。
//
// 降序**不是正确性保证**，只是多个候选都匹配时的确定性择优：Refresh 的时序是
// 「建新任务 → 停旧任务」，而新任务的驱动是懒连接的（创建时不 Connect），
// 旧会话在新会话建立之前就已关闭。同一端口出现两个活会话，只可能来自
// 「两台设备配到了同一个串口」这类错配，此时取新登记的更贴近当前配置。
func (s *server) sessionsForProtocol(protocol string) []*session {
	type entry struct {
		id   uint64
		sess *session
	}

	s.mu.Lock()
	entries := make([]entry, 0, len(s.sessions))
	for id, sess := range s.sessions {
		if sess.protocol == protocol {
			entries = append(entries, entry{id: id, sess: sess})
		}
	}
	s.mu.Unlock()

	sort.Slice(entries, func(i, j int) bool { return entries[i].id > entries[j].id })

	out := make([]*session, len(entries))
	for i, e := range entries {
		out[i] = e.sess
	}
	return out
}

// pingAttempt 一次候选会话探测的结果。
type pingAttempt struct {
	// matched 本会话持有的连接正是本次要测的那一条。
	matched bool
	// err matched 为 true 时，是这次 Ping 的结果。
	err error
}

// pingViaSession 尝试把这次 ping 交给一个已存在的会话执行。
//
// 返回 (nil, nil) 表示没有可复用的会话，调用方应建临时实例。
//
// 命中时的错误**原样上抛，不再回落** —— 那时已经确认过"这条连接就是被测的那条"，
// 换临时实例只会再抢一次同一个已被占用的端口，白搭一次必然失败的尝试。
//
// 拿不到会话锁（对方有在途读取）时**跳过该候选而不是报错**：
// 此时无从判断它是不是目标端口（MatchConnection 本身就要拿锁才能调），
// 把"有会话正忙"直接当成失败，会让「测 COM2 时因为 COM1 在读取而失败」这种
// 与被测对象无关的假失败出现 —— 那恰恰是本次要消除的那类体验。
// 跳到临时实例后：端口真的被占就如实报 Access is denied（网关会补上
// 「COM1 当前由设备「xxx」占用」），没被占则这次测试本就该成功。
func (s *server) pingViaSession(protocol, protocolJSON string) (*session, error) {
	for _, cand := range s.sessionsForProtocol(protocol) {
		// 探测回调里**绝不能**触碰 s.mu（见 sessionsForProtocol 的锁序说明），
		// 且必须经 sessionCallWithin 释放锁：驱动 panic 时靠它的 defer 放锁，
		// 自己写 Lock/defer Unlock 一旦漏掉就会让 shutdown() 永久阻塞。
		att, _, locked := sessionCallWithin(cand, pingSessionBudget, func() (pingAttempt, error) {
			// 顺序即优先级：closed 是安全关键且最便宜，类型断言次之，
			// MatchConnection 要解析 JSON 最贵。
			if cand.closed {
				return pingAttempt{}, nil
			}
			m, ok := cand.drv.(driver.ConnectionMatcher)
			if !ok || !m.MatchConnection(protocolJSON) {
				return pingAttempt{}, nil
			}
			return pingAttempt{matched: true, err: cand.drv.Ping(protocolJSON)}, nil
		})
		if !locked {
			continue
		}
		if att.matched {
			return cand, att.err
		}
	}
	return nil, nil
}

// closeSessionLocked 标记并关闭会话，panic 降级为一条日志。
//
// 供停机路径使用：那里不能再往外抛 —— shutdown 已经清空了会话表，半途中断会
// 留下没关的驱动与没关的底层流，而调用方已经无处可退。
func closeSessionLocked(sess *session) {
	defer func() {
		if r := recover(); r != nil {
			Logf("driversdk: 关闭会话时驱动 panic: %v\n%s", r, debug.Stack())
		}
	}()
	_, _ = sessionCall(sess, func() (struct{}, error) {
		sess.closed = true
		return struct{}{}, sess.drv.Close()
	})
}

// handle 处理一个请求。
//
// 顶层 recover 是必需的而非保险：每个请求都在自己的 goroutine 里跑，驱动是
// 手写代码（空批次取 [0]、Close 后解引用、类型断言失败都会 panic），而 Go 的
// panic 不可跨 goroutine 恢复 —— Main 上也接不住。没有它，一个点位的确定性
// panic 会带走整个插件进程：该插件下**所有协议、所有设备**一起断，监管按退避
// 重启，MaxRestarts 次之后被永久放弃（见 pluginhost/process.go）。
// 正确的语义是"这一次调用失败"，这里把它降级为一次 RPC 错误 + 一条带栈的日志。
func (s *server) handle(f pluginwire.Frame) {
	defer func() {
		if r := recover(); r != nil {
			Logf("driversdk: %s 处理中 panic: %v\n%s", f.Method, r, debug.Stack())
			// 若该请求已经应答过，这一帧会因为 id 已被清理而被网关静默丢弃
			// （见 pluginhost/client.go 的 dispatch），不会造成错配。
			s.reply(f.ID, nil, &pluginwire.RemoteError{Message: fmt.Sprintf("driver panic: %v", r)})
		}
	}()

	switch f.Method {
	case pluginwire.MethodDescribe:
		s.reply(f.ID, s.meta, nil)

	case pluginwire.MethodConnect:
		s.handleConnect(f)

	case pluginwire.MethodRead:
		s.handleRead(f)

	case pluginwire.MethodIsConnected:
		var p SessionParams
		if !s.decode(f, &p) {
			return
		}
		sess, ok := s.getSession(p.Session)
		if !ok {
			s.reply(f.ID, nil, &pluginwire.RemoteError{Message: fmt.Sprintf("unknown session %d", p.Session)})
			return
		}
		connected, _ := sessionCall(sess, func() (bool, error) {
			// 已关闭的会话一律报未连接：IsConnected 的语义是"链路可用"，
			// 而一个已拆解的驱动绝不是可用的（返回 true 会让采集引擎永不重连）。
			if sess.closed {
				return false, nil
			}
			return sess.drv.IsConnected(), nil
		})
		s.reply(f.ID, IsConnectedReply{Connected: connected}, nil)

	case pluginwire.MethodClose:
		var p SessionParams
		if !s.decode(f, &p) {
			return
		}
		// 未知会话静默成功：等价于对"从未连接过的进程内驱动"调用 Close。
		sess, ok := s.getSession(p.Session)
		if !ok {
			s.reply(f.ID, CloseReply{Connected: false}, nil)
			return
		}
		// 先置关闭标记再拆：在途的 read/isConnected 拿到锁后会看到它，
		// 不会在已 Close 的驱动上继续读。
		_, err := sessionCall(sess, func() (struct{}, error) {
			sess.closed = true
			return struct{}{}, sess.drv.Close()
		})
		s.mu.Lock()
		delete(s.sessions, p.Session)
		s.mu.Unlock()
		s.reply(f.ID, CloseReply{Connected: false}, pluginwire.NewRemoteError(err))

	case pluginwire.MethodProbe:
		s.handleProbe(f)

	case pluginwire.MethodDiscoverPoints:
		s.handleDiscoverPoints(f)

	case pluginwire.MethodPing:
		var p PingParams
		if !s.decode(f, &p) {
			return
		}
		// 先用已存在的会话试一次。
		//
		// 串口是独占资源：采集任务已打开 COM1 时，临时实例再打开同一个端口
		// 必然失败（Windows 报 Access is denied），把"设备其实可达"误报成错误。
		// 复用同一个句柄既得出正确答案，又不打扰采集链路。
		//
		// 命中的话它的结果就是权威，无论成败都不再回落 —— 回落只会再抢一次
		// 已被占用的端口，白搭一次失败。
		if sess, pingErr := s.pingViaSession(p.Protocol, p.ProtocolJSON); sess != nil {
			s.reply(f.ID, struct{}{}, pluginwire.NewRemoteError(pingErr))
			return
		}

		// 没有可复用的会话：临时实例，Ping 后立即销毁，不分配会话。
		// PingDevice 不会调用 Close，因此这里必须自己收尾 —— 而且要用 defer：
		// Ping 内部 panic 时同样得把临时连接（串口/socket）收掉，否则 panic 被
		// handle 的 recover 咽下之后，资源就永远留在进程里了。
		//
		// 注意这条路径与上面的复用路径在结构上是分开的：复用到的会话**绝不能**
		// 被 Close（那会把正在采集的设备连接掐掉）。
		drv, err := s.newDriver(p.Protocol)
		if err != nil {
			s.reply(f.ID, nil, pluginwire.NewRemoteError(err))
			return
		}
		pingErr := func() error {
			defer func() { _ = drv.Close() }()
			return drv.Ping(p.ProtocolJSON)
		}()
		s.reply(f.ID, struct{}{}, pluginwire.NewRemoteError(pingErr))

	default:
		s.reply(f.ID, nil, &pluginwire.RemoteError{Message: fmt.Sprintf("unknown method %q", f.Method)})
	}
}

// handleProbe 处理一次局域网扫描探测。
//
// 结构与 MethodPing 的**无会话路径**同构：都不查会话表、都不分配会话、
// 都在调用后立即收尾。区别只在收尾对象 —— ping 建的是临时驱动实例（要 Close），
// probe 只有一个纯函数，没有需要释放的东西。
//
// 并发：每个请求本来就跑在自己的 goroutine 里（run 的 `go s.handle(f)`），
// 因此网关侧的 worker 池并发派发 probe 时，插件天然并行处理，不会互相阻塞，
// 也不会挡住同一进程上其它设备的 connect/read。
func (s *server) handleProbe(f pluginwire.Frame) {
	var p ProbeParams
	if !s.decode(f, &p) {
		return
	}

	prober, ok := s.probers[p.Protocol]
	if !ok {
		// 网关只会向「Meta 里声明了 Discovery」的协议派发 probe，所以走到这里
		// 说明两侧对能力的认知不一致（旧网关 + 新插件，或插件被换成了不含该
		// 协议的版本）。如实报错，别静默返回空列表 —— 那会让用户以为"扫不到设备"。
		s.reply(f.ID, nil, &pluginwire.RemoteError{
			Message: fmt.Sprintf("protocol %q does not support discovery", p.Protocol),
		})
		return
	}

	// 未填选项时给 nil 而不是空 RawMessage：实现里用 len(Options)==0 判定"没填"，
	// 而长度为 0 的 RawMessage 直接 Unmarshal 会报 "unexpected end of JSON input"。
	var opts json.RawMessage
	if p.Options != "" {
		opts = json.RawMessage(p.Options)
	}

	devices, err := prober(ProbeRequest{
		Protocol: p.Protocol,
		Options:  opts,
		Target:   p.Target,
		Budget:   s.probeBudget(p.Protocol, p.BudgetMs),
	})
	s.reply(f.ID, ProbeReply{Devices: devices}, pluginwire.NewRemoteError(err))
}

// probeBudget 本次探测实际采用的时间预算。
//
// 取「本协议声明的 MaxBudgetMs」与「网关请求值」的较小者：**插件是必须不超时的
// 那一方**，把这条不变量交给对端给的值，等于让网关的一次配置失误变成"插件被
// RPC 超时打断"，而症状只是"扫不到设备"，完全指不到根因。
//
// 请求值 <= 0 视为未指定，直接用声明值。
func (s *server) probeBudget(protocol string, requestedMs int) time.Duration {
	return clampBudget(s.probeBudgets[protocol], requestedMs)
}

// pointBudget 本次点位扫描实际采用的时间预算。语义与 probeBudget 完全一致。
func (s *server) pointBudget(protocol string, requestedMs int) time.Duration {
	return clampBudget(s.pointBudgets[protocol], requestedMs)
}

// clampBudget 取「插件声明的预算」与「网关请求值」的较小者；请求值 <= 0 视为未指定。
func clampBudget(declared time.Duration, requestedMs int) time.Duration {
	if requestedMs <= 0 {
		return declared
	}
	if want := time.Duration(requestedMs) * time.Millisecond; want < declared {
		return want
	}
	return declared
}

// 点位扫描中"不得回退"的两种情形与"会话已关闭"的收工信号。
//
// 前两条都要原样穿到用户面前（网关侧只会把它们包进一次业务错误），措辞必须是
// 可执行的下一步动作，而不是"扫描失败"这类无处下手的话。
var (
	// errPointScanBusy 命中了本次要用的会话，但 5 秒内拿不到它的锁。
	//
	// 这只可能发生在采集正持锁读取那台设备的时刻 —— 而那条路径可能已经跑了
	// 十几秒（一次 Read 覆盖整批点位），后面还有同一批里的其它区间。继续等下去
	// 会撞网关侧的 RPC 超时并累积插件卡死计数（连续几次重启整个插件进程），
	// 所以宁可立刻告诉用户"设备正忙"。
	errPointScanBusy = errors.New("设备正忙：采集正在读取这台设备，请先停用该设备再扫描")

	// errPointScanForeignSession 该串口协议已有会话，但没有一条是本次要连的那个端口。
	//
	// **绝不能回落到临时实例**：那会打开同一个串口的第二个句柄。一次长扫描持有它
	// 几十秒，采集侧下一次重连就会报 Access is denied 并进入退避 ——
	// 即"点一次扫描把设备打成离线"。
	//
	// 措辞留了余地：端口配给了别的设备、设备掉线（句柄仍被旧 handler 占着）、
	// 串口参数与现有连接不一致，都会走到这里，无从在插件侧分辨。
	errPointScanForeignSession = errors.New(
		"未找到可复用的串口连接（该端口可能配给了别的设备、或现有连接已断开但仍占着端口）。" +
			"为避免抢占串口打断采集，扫描不会新开连接；请先停用占用该串口的设备后重试")

	// errPointScanSessionClosed 复用到的会话在扫描途中被关闭了（设备被停用/热升级）。
	//
	// 不是"错误"而是"到此为止"的信号：调用方应当把已经拿到的部分结果带回去，
	// 由 finder 自己置 Incomplete。
	errPointScanSessionClosed = errors.New("point scan: session closed")
)

// pointScanSessionBudget 复用路径上单次等待会话锁的上限。
//
// 与 pingSessionBudget 是同一条取舍（那里是 3s，这里给到 5s：一次扫描要拿很多次
// 锁，每次只给 3s 会让"采集节奏稍慢"变成频繁的假失败），但**必须是有限值**：
// 无限等待会挂到网关侧的 RPC 超时，并累积插件卡死计数。
const pointScanSessionBudget = 5 * time.Second

// handleDiscoverPoints 处理一次点位范围扫描。
//
// 结构与 handlePing 的**两条路径**同构（不是 handleProbe —— 那条路没有会话）：
// 先看本进程里有没有能复用的会话，有则复用、没有则建一个临时实例。区别在于
// 复用这条路上"拿不到会话"不再是"回落"，而可能是"必须报错"—— 见 selectSession。
func (s *server) handleDiscoverPoints(f pluginwire.Frame) {
	var p DiscoverPointsParams
	if !s.decode(f, &p) {
		return
	}

	finder, ok := s.finders[p.Protocol]
	if !ok {
		// 网关只会向「Meta 里声明了 PointDiscovery」的协议派发 discoverPoints，
		// 走到这里说明两侧对能力的认知不一致（旧网关 + 新插件，或插件被换成了
		// 不含该协议的版本）。如实报错，别静默返回空列表。
		s.reply(f.ID, driver.PointScanResult{}, &pluginwire.RemoteError{
			Message: fmt.Sprintf("protocol %q does not support point discovery", p.Protocol),
		})
		return
	}

	// 未填选项时给 nil 而不是空 RawMessage（理由同 handleProbe）。
	var opts json.RawMessage
	if p.Options != "" {
		opts = json.RawMessage(p.Options)
	}

	budget := s.pointBudget(p.Protocol, p.BudgetMs)
	req := FindPointsRequest{
		Protocol: p.Protocol,
		Options:  opts,
		Budget:   budget,
		Deadline: time.Now().Add(budget),
	}

	sess, err := s.selectSession(p.Protocol, p.ProtocolJSON)
	if err != nil {
		// 不回退：串口抢占与"设备正忙"这两种情形都必须让用户看到原因，
		// 悄悄换一条路只会把问题推到采集侧（下次重连 Access is denied）。
		s.reply(f.ID, driver.PointScanResult{}, pluginwire.NewRemoteError(err))
		return
	}

	if sess != nil {
		// 复用路径。**这里绝不 Close 任何东西** —— sess 归采集引擎所有，
		// 它是那台设备正在用的连接。Do 每次重新取锁、下次调用前就放掉，
		// 因此一次扫描不会把采集卡死几十秒。
		req.Session = &sessionAccess{sess: sess, deadline: req.Deadline}
		result, ferr := finder(req)
		s.reply(f.ID, pointScanValue(result), pluginwire.NewRemoteError(ferr))
		return
	}

	// 临时路径：本进程内没有可复用的会话，新建一个只为本次扫描服务的实例。
	//
	// 与 handlePing 完全同构，包括用 defer 收尾：finder panic 时同样得把临时连接
	// （串口/socket）关掉，否则 panic 被 handle 的 recover 咽下之后，资源就永远
	// 留在进程里了。
	drv, err := s.newDriver(p.Protocol)
	if err != nil {
		s.reply(f.ID, driver.PointScanResult{}, pluginwire.NewRemoteError(err))
		return
	}
	result, ferr := func() (*driver.PointScanResult, error) {
		defer func() { _ = drv.Close() }()
		if err := drv.Connect(p.ProtocolJSON); err != nil {
			return nil, err
		}
		req.Session = &instanceAccess{drv: drv}
		return finder(req)
	}()
	s.reply(f.ID, pointScanValue(result), pluginwire.NewRemoteError(ferr))
}

// pointScanValue 把 finder 的返回值规整成应答载荷。
//
// 统一用**值**而不是指针：nil 指针过线是 JSON null，而网关侧解码进的是值类型，
// 于是"插件报错"与"插件返回了零值结果"在界面上长得一样。
func pointScanValue(r *driver.PointScanResult) driver.PointScanResult {
	if r == nil {
		return driver.PointScanResult{}
	}
	return *r
}

// selectSession 为一次点位扫描选一条会话，或告诉调用方该走哪条回退路径。
//
// 返回值有三种含义：
//
//	(sess, nil)  复用 sess
//	(nil,  nil)  走临时实例（本进程内没有可复用的会话，且回落是安全的）
//	(nil,  err)  **不得回退**，把 err 原样报给用户
//
// 那张回退表（为什么串口与 TCP 不对称）：
//
//	情形                        串口协议            TCP 协议
//	有会话且 MatchConnection 命中  复用（锁预算 5s）    不会命中（Modbus TCP 未实现
//	                                               ConnectionMatcher），等同下一行
//	有会话但不是本次要的那条       报错，不回退         走临时实例（第二个 socket 无害）
//	该协议一个会话都没有           走临时实例          走临时实例
//	有会话、命中但锁不到           报"设备正忙"，不回退  不适用
//
// 串口那两格是本次扫描与 ping **最关键的差异**。ping 的取舍是"拿不到锁就跳过、
// 回落临时实例"—— 那对 ping 是对的（不能让"测 COM2 时因为 COM1 在忙"变成假失败），
// 但一次几十秒的长扫描回落成临时实例会**抢占串口长时句柄**：采集侧下次重连
// 报 Access is denied 并进入退避，用户看到的是"点一次扫描把设备打成离线"。
// 端口按定义是空的时候（一个会话都没有）回落才是安全的。
func (s *server) selectSession(protocol, protocolJSON string) (*session, error) {
	candidates := s.sessionsForProtocol(protocol)
	if len(candidates) == 0 {
		return nil, nil
	}
	serial := s.serialProtocols[protocol]

	for _, cand := range candidates {
		// 探测回调里**绝不能**触碰 s.mu（见 sessionsForProtocol 的锁序说明），
		// 且必须经 sessionCallWithin 释放锁（panic 时靠它的 defer 放锁）。
		matched, _, locked := sessionCallWithin(cand, pointScanSessionBudget, func() (bool, error) {
			// 顺序即优先级：closed 是安全关键且最便宜，类型断言次之，
			// MatchConnection 要解析 JSON 最贵。
			if cand.closed {
				return false, nil
			}
			m, ok := cand.drv.(driver.ConnectionMatcher)
			if !ok || !m.MatchConnection(protocolJSON) {
				return false, nil
			}
			return true, nil
		})
		if !locked {
			// 拿不到锁 = 这个会话正被采集占着。此时**无从判断它是不是目标端口**
			//（MatchConnection 本身就要拿锁），对串口来说这恰恰是最常见的时刻，
			// 必须如实报忙而不是当成"没有会话"去开第二个句柄。
			if serial {
				return nil, errPointScanBusy
			}
			continue
		}
		if matched {
			return cand, nil
		}
	}

	if serial {
		return nil, errPointScanForeignSession
	}
	return nil, nil
}

// sessionAccess 复用采集会话的 SessionAccess。
//
// 每次 Do 都是一次独立的"取锁 → 调用 → 放锁"，块与块之间锁是放开的 ——
// 这正是"扫描不会卡死采集"的全部原因。
type sessionAccess struct {
	sess     *session
	deadline time.Time
}

func (a *sessionAccess) Do(fn func(drv driver.Driver) error) error {
	// 等待预算取「剩余时间」与「单次上限」的较小者：整段扫描已经跑掉的
	// 时间必须从这次等待里扣掉，否则最后一块会拿着整个预算去等锁，
	// 一次扫描的总耗时因此可能翻倍。
	wait := time.Until(a.deadline)
	if wait > pointScanSessionBudget {
		wait = pointScanSessionBudget
	}
	if wait <= 0 {
		return context.DeadlineExceeded
	}

	_, err, ok := sessionCallWithin(a.sess, wait, func() (struct{}, error) {
		// 查表与加锁是两个动作：本请求可能是在 pluginwire.MethodClose 拆完驱动
		// 之后才拿到锁的（见 session.closed）。在已关闭的驱动上继续读会拿回上一轮
		// 的缓存值，或不巧触发 nil 解引用把整个插件进程带走。
		if a.sess.closed {
			return struct{}{}, errPointScanSessionClosed
		}
		return struct{}{}, fn(a.sess.drv)
	})
	if !ok {
		return errPointScanBusy
	}
	return err
}

// instanceAccess 临时实例的 SessionAccess。
//
// 没有第二方会碰这个实例，Do 因此退化成直接调用 —— 加锁只会是纯粹的噪音。
type instanceAccess struct{ drv driver.Driver }

func (a *instanceAccess) Do(fn func(drv driver.Driver) error) error { return fn(a.drv) }

func (s *server) handleConnect(f pluginwire.Frame) {
	var p ConnectParams
	if !s.decode(f, &p) {
		return
	}
	drv, err := s.newDriver(p.Protocol)
	if err != nil {
		s.reply(f.ID, ConnectResult{}, pluginwire.NewRemoteError(err))
		return
	}
	if err := drv.Connect(p.ProtocolJSON); err != nil {
		// 连接失败不留会话，避免半初始化实例堆积。
		_ = drv.Close()
		s.reply(f.ID, ConnectResult{Connected: false}, pluginwire.NewRemoteError(err))
		return
	}

	// protocol 在入表前写定，此后不再改 —— sessionsForProtocol 依赖这条不变量。
	sess := &session{drv: drv, protocol: p.Protocol}
	connected := drv.IsConnected()

	s.mu.Lock()
	// 建连期间可能已收到 shutdown/EOF：shutdown 已经遍历过会话表并清空，
	// 现在插进去的这个会话不会再有人关它 —— 驱动连着的串口/socket 会一直留着，
	// 直到整个进程退出。这里补上这一刀。
	if s.closed.Load() {
		s.mu.Unlock()
		closeSessionLocked(sess)
		s.reply(f.ID, ConnectResult{Connected: false}, &pluginwire.RemoteError{Message: "server is shutting down"})
		return
	}
	id := s.nextID.Add(1) // 从 1 开始，0 保留为"未分配"
	s.sessions[id] = sess
	s.mu.Unlock()

	s.reply(f.ID, ConnectResult{Session: id, Connected: connected}, nil)
}

func (s *server) handleRead(f pluginwire.Frame) {
	var p ReadParams
	if !s.decode(f, &p) {
		return
	}
	sess, ok := s.getSession(p.Session)
	if !ok {
		s.reply(f.ID, nil, &pluginwire.RemoteError{Message: fmt.Sprintf("unknown session %d", p.Session)})
		return
	}

	// 只还原驱动真正读取的字段，其余取零值（见 WireAddr 注释）。
	addrs := make([]po.DeviceAddress, len(p.Addrs))
	for i, a := range p.Addrs {
		addrs[i] = po.DeviceAddress{ID: a.ID, Name: a.Name, DataType: a.DataType}
	}

	// 查表与加锁是两个动作：本请求可能是在 pluginwire.MethodClose 拆完驱动之后才拿到锁的
	// （见 session.closed）。此时读回的是上一轮的缓存值，会被当成新样本上报。
	out, callErr := sessionCall(sess, func() (readOutcome, error) {
		if sess.closed {
			return readOutcome{closed: true}, nil
		}
		results, err := sess.drv.Read(addrs)
		// 成功与驱动报错两种情况下都回传连接状态：链路掉线时驱动返回 error
		// 且 IsConnected()==false，网关据此在下一轮触发重连。
		return readOutcome{results: results, connected: sess.drv.IsConnected()}, err
	})

	if out.closed {
		s.reply(f.ID, nil, &pluginwire.RemoteError{Message: fmt.Sprintf("session %d closed", p.Session)})
		return
	}
	s.reply(f.ID, ReadReply{Results: out.results, Connected: out.connected}, pluginwire.NewRemoteError(callErr))
}

// readOutcome Read 这一次调用在会话锁内得到的结果。
type readOutcome struct {
	results   []driver.ReadResult
	connected bool
	// closed 拿到锁时会话已经被关闭，没有触碰驱动。
	closed bool
}

// decode 解析请求参数；失败时已经应答错误并返回 false。
//
// 载荷编码取自帧上的 Codec（与网关侧 client.call 同源）。本函数不判断哪个
// 方法该用哪种编码 —— 那是载荷类型自己的事（见 driversdk/binary.go）。
func (s *server) decode(f pluginwire.Frame, v any) bool {
	if len(f.Params) == 0 {
		return true
	}
	if err := pluginwire.DecodeBody(f.Codec, f.Params, v); err != nil {
		s.reply(f.ID, nil, &pluginwire.RemoteError{Message: "bad params: " + err.Error()})
		return false
	}
	return true
}

// reply 发送应答。已关闭后静默丢弃（在途请求处理完时进程可能已在退出）。
func (s *server) reply(id uint64, result any, rerr *pluginwire.RemoteError) {
	if s.closed.Load() {
		return
	}
	body, codec, err := pluginwire.EncodeBody(result)
	if err != nil {
		// 编码失败时把 Codec 一并退回 JSON：body 已置空，再留一个
		// "声明二进制、体为空"的帧会把对端引到解码分支上去。
		body, codec = nil, pluginwire.CodecJSON
		rerr = &pluginwire.RemoteError{Message: "marshal result: " + err.Error()}
	}

	s.wmu.Lock()
	defer s.wmu.Unlock()
	_ = pluginwire.WriteFrame(s.w, pluginwire.Frame{ID: id, Codec: codec, Result: body, Error: rerr})
}
