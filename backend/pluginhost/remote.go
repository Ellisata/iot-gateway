// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"github.com/Ellisata/iot-gateway-sdk/pluginwire"
)

// processLink 抽象 remoteDriver 对底层插件进程的依赖，便于测试注入替身。
//
// epoch 是插件进程实例的世代号：每次成功拉起（含崩溃后重启）自增。
// remoteDriver 用它识别"会话属于已退役的进程实例"——见 stale 的注释。
type processLink interface {
	// acquire 返回当前可用的客户端；进程处于退避窗口内时返回错误。
	acquire(ctx context.Context) (*client, error)
	// isUp 报告进程当前是否可用。
	isUp() bool
	// epoch 返回当前进程实例的世代号（首次拉起前为 0）。
	epoch() uint64
}

// remoteDriver 把一个插件进程中的驱动实例代理为 driver.Driver。
// 每个实例对应插件进程内的一个会话，与 driver.Create 调用一一对应（即一个设备）。
type remoteDriver struct {
	proc     processLink
	protocol string
	serial   bool
	to       Timeouts

	mu      sync.Mutex
	session uint64 // 0 = 未分配

	connected atomic.Bool
	// epochAt 建立当前会话时**实际对话的那个客户端**的世代号。
	//
	// 必须取自 client.epoch，而不是 Connect 返回后重新读一次 d.proc.epoch()：
	// 后者读到的可能是"应答已到达"与"这行执行"之间发生的那次换代之后的值，
	// 于是把一个属于已退役进程的会话登记成新鲜会话。判据取自已建立连接的对象，
	// 才不会与事实脱节。
	epochAt atomic.Uint64
}

var (
	_ driver.Driver          = (*remoteDriver)(nil)
	_ driver.SerialExclusive = (*remoteDriver)(nil)
)

func newRemoteDriver(proc processLink, protocol string, serial bool, to Timeouts) *remoteDriver {
	return &remoteDriver{proc: proc, protocol: protocol, serial: serial, to: to}
}

// SerialExclusive 由插件 describe 声明，语义见 driver.SerialExclusive。
func (d *remoteDriver) SerialExclusive() bool { return d.serial }

// stale 报告当前会话是否属于已退役的插件进程实例。
//
// 用世代号对比而非"进程下线回调"：回调需要在每次 driver.Create 时注册，
// 而 Refresh 会重建全部驱动，注册列表会无界增长；世代号是零状态的。
func (d *remoteDriver) stale() bool {
	return d.epochAt.Load() != d.proc.epoch()
}

// IsConnected 返回缓存的连接状态。
//
// 这是本设计最要紧的一处语义：采集引擎每次轮询每设备都会调用它，
// 且**只在它返回 false 时才触发重连**（Read 报错本身不触发）。
// 缓存由三类更新源维护，见 Read 的注释。
//
// 不走 RPC：规模下（数千设备 × 每秒轮询）不可接受。
func (d *remoteDriver) IsConnected() bool {
	if !d.connected.Load() {
		return false
	}
	if d.stale() {
		// 进程已重启，旧会话作废 → 必须返回 false 让采集引擎重连。
		// 否则会向新进程发陈旧会话 id，永远收到 "unknown session" 却不重连。
		return false
	}
	return d.proc.isUp()
}

func (d *remoteDriver) Connect(protocolJSON string) error {
	// 重连时先释放旧会话，避免插件进程内的会话随重连次数无界增长。
	d.closeSession(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), d.to.Connect)
	defer cancel()

	cl, err := d.proc.acquire(ctx)
	if err != nil {
		d.connected.Store(false)
		return err
	}

	var res driversdk.ConnectResult
	if err := cl.call(ctx, pluginwire.MethodConnect, driversdk.ConnectParams{
		Protocol:     d.protocol,
		ProtocolJSON: protocolJSON,
	}, &res); err != nil {
		d.connected.Store(false)
		return fmt.Errorf("pluginhost: connect %s: %w", d.protocol, err)
	}

	d.mu.Lock()
	d.session = res.Session
	// 世代号取自**刚刚对话的这个客户端**，而不是再读一次 d.proc.epoch()：
	// 若在"应答到达"与"这里执行"之间发生换代，后者会把新世代号配到旧进程的
	// 会话上，使一个已作废的会话看起来是新鲜的。
	d.epochAt.Store(cl.epoch.Load())
	d.mu.Unlock()
	d.connected.Store(res.Connected)
	return nil
}

func (d *remoteDriver) Read(addrs []driver.DeviceAddress) ([]driver.ReadResult, error) {
	d.mu.Lock()
	sess := d.session
	d.mu.Unlock()

	if sess == 0 || d.stale() {
		// 从未连接，或进程已重启导致会话作废。标记未连接，
		// 采集引擎下一轮 ensureConnected 会看到 false 并重连。
		d.connected.Store(false)
		return nil, ErrNotConnected
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.to.Read)
	defer cancel()

	cl, err := d.proc.acquire(ctx)
	if err != nil {
		d.connected.Store(false)
		return nil, err
	}
	// 拿到客户端之后**再核一次世代号**：上面那次 stale() 与这里的 acquire 是
	// 两次独立的原子读，中间换代时会拿到一个属于新进程的客户端，而手里的会话 id
	// 属于已退役的旧进程。该 id 在新进程里可能恰好属于另一台设备的会话 ——
	// 既不报错也察觉不到，表现为静默读到别的设备的数据。
	// 用客户端自带的世代号（与"我正要跟谁说话"同源）比对，这个窗口才真正关闭。
	if cl.epoch.Load() != d.epochAt.Load() {
		d.connected.Store(false)
		return nil, ErrNotConnected
	}

	wa := make([]driversdk.WireAddr, len(addrs))
	for i, a := range addrs {
		wa[i] = driversdk.WireAddr{ID: a.ID, Name: a.Name, DataType: a.DataType}
	}

	var reply driversdk.ReadReply
	callErr := cl.call(ctx, pluginwire.MethodRead, driversdk.ReadParams{Session: sess, Addrs: wa}, &reply)

	var driverErr *pluginwire.RemoteError
	switch {
	case callErr == nil || errors.As(callErr, &driverErr):
		// 帧完好（成功，或驱动自身报错）：应答携带的 connected 是权威值。
		// 这是链路掉线被感知的唯一通道 —— 驱动 Read 报错且 IsConnected()==false
		// 时，下一轮 ensureConnected 据此重连。
		d.connected.Store(reply.Connected)

	case errors.Is(callErr, context.DeadlineExceeded):
		// RPC 层超时：代理本身没能完成调用，驱动状态未知 → 保守地判为未连接，
		// 交由采集引擎的重连退避去处理（其 1s→60s 退避与插件的重启退避自然叠加）。
		d.connected.Store(false)
		return nil, callErr

	default:
		// 进程已下线/帧流损坏：驱动必然不可用。
		d.connected.Store(false)
		return nil, callErr
	}

	if callErr != nil {
		return nil, callErr
	}

	// 采集引擎的 toRecords 依赖结果与 addrs 按索引一一对应（与内置驱动契约一致），
	// 插件违反时直接报错，避免记录错位。
	if len(reply.Results) != len(addrs) {
		return nil, fmt.Errorf("pluginhost: %s returned %d results for %d addresses",
			d.protocol, len(reply.Results), len(addrs))
	}

	// 光比数量挡不住两类错误：顺序错位，以及**结果里的 id 张冠李戴**（例如陈旧
	// 会话恰好命中了新进程里另一台设备的会话）。后者会让采集引擎按 id 兜底时
	// 查不到名称，却仍带着别的设备的 addressID 推下去 —— 落到下游就是一条
	// 记错设备的记录。这里按 id 多重集校验：每个请求的点位恰好被回一次。
	// 顺序不做要求：toRecords 本就是按 id 兜底的（见 collector/task.go）。
	want := make(map[string]int, len(addrs))
	for _, a := range addrs {
		want[a.ID]++
	}
	for i := range reply.Results {
		id := reply.Results[i].DeviceAddressID
		n, ok := want[id]
		if !ok {
			return nil, fmt.Errorf("pluginhost: %s returned result for unknown address id %q", d.protocol, id)
		}
		if n == 0 {
			return nil, fmt.Errorf("pluginhost: %s returned address id %q more than once", d.protocol, id)
		}
		want[id] = n - 1
	}
	return reply.Results, nil
}

func (d *remoteDriver) Ping(protocolJSON string) error {
	ctx, cancel := context.WithTimeout(context.Background(), d.to.Ping)
	defer cancel()

	cl, err := d.proc.acquire(ctx)
	if err != nil {
		return err
	}
	// 无会话：插件侧自行决定用临时实例（Ping 后立即销毁）还是复用某个已存在的
	// 会话 —— 后者是串口场景的必需路径（端口独占，二次打开必失败）。
	// 无论走哪条，Ping 都不会分配新会话，也不会被 Close（PingDevice 从不 Close）。
	return cl.call(ctx, pluginwire.MethodPing, driversdk.PingParams{
		Protocol:     d.protocol,
		ProtocolJSON: protocolJSON,
	}, nil)
}

// Close 关闭连接。
//
// 进程已死或本就未连接时立即返回 nil —— 必须如此：采集任务的 Stop()
// 会逐个驱动调用 Close()，任何阻塞都会卡死停机。
func (d *remoteDriver) Close() error {
	d.closeSession(context.Background())
	return nil
}

// closeSession 释放当前会话（best-effort），并把本驱动标记为未连接。
func (d *remoteDriver) closeSession(parent context.Context) {
	d.mu.Lock()
	sess := d.session
	d.session = 0
	d.mu.Unlock()
	d.connected.Store(false)

	if sess == 0 || d.stale() || !d.proc.isUp() {
		return
	}

	// 整个清理过程（含 acquire 等待）受 Close 超时约束，绝不拖住停机。
	ctx, cancel := context.WithTimeout(parent, d.to.Close)
	defer cancel()

	cl, err := d.proc.acquire(ctx)
	if err != nil {
		return
	}
	// 同 Read：acquire 拿到的客户端可能已属于新一代进程，而 sess 是旧进程的
	// 会话 id —— 发过去可能关掉新进程里另一台设备的会话。宁可留着不关。
	if cl.epoch.Load() != d.epochAt.Load() {
		return
	}
	// 尽力而为：忽略错误。
	_ = cl.call(ctx, pluginwire.MethodClose, driversdk.SessionParams{Session: sess}, nil)
}
