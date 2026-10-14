// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package pluginhost 实现进程外协议驱动插件：目录扫描、进程监管、
// 向 driver 注册表注入插件协议、以及把插件进程内的驱动代理为 driver.Driver。
package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"iot-gateway/pluginwire"
)

// ErrProcessDown 插件进程已退出或帧流损坏，本次调用未能完成。
var ErrProcessDown = errors.New("pluginhost: plugin process is down")

// ErrNotConnected 驱动尚未建立连接，无法执行该操作。
var ErrNotConnected = errors.New("pluginhost: driver is not connected")

// 卡死判定阈值：连续这么多次 RPC 超时、且已这么久没有任何成功调用，
// 才认定"插件还活着但卡死"。见 client.stuck。
const (
	stuckTimeoutStreak = 3
	stuckAfter         = 30 * time.Second
)

// Timeouts RPC 各方法的超时。
//
// 硬规则：Read 必须大于 protocolJSON 里配置的协议自身超时，
// 否则网关会放弃插件仍在执行的调用，重试会在串行总线上堆积。
type Timeouts struct {
	Handshake time.Duration // describe 握手（含进程启动与 Go runtime 初始化）
	Connect   time.Duration // connect
	Ping      time.Duration // ping（临时实例，与 connect 同量级）
	Read      time.Duration // read（2000 点位批量在慢速串行链路上可能很久）
	Close     time.Duration // close（尽力而为，绝不能拖住停机）
}

// DefaultTimeouts 默认超时，与 default.yaml 的 driver 段默认值一致。
func DefaultTimeouts() Timeouts {
	return Timeouts{
		Handshake: 10 * time.Second,
		Connect:   30 * time.Second,
		Ping:      30 * time.Second,
		Read:      60 * time.Second,
		Close:     2 * time.Second,
	}
}

// client 是单个插件进程的连接：帧流 + 请求多路复用。
//
// 采集引擎对非独占驱动只对单次 Read 持设备锁，因此同一进程上可以有
// 多个设备的 Read 并发在途 —— 必须按关联 ID 多路复用，不能假设一问一答。
type client struct {
	rw io.ReadWriteCloser

	// wmu 串行化整帧写入，避免多个请求的字节交错。
	wmu sync.Mutex

	// mu 保护 pending 与 closeErr。
	mu       sync.Mutex
	pending  map[uint64]chan pluginwire.Frame
	closeErr error

	nextID    atomic.Uint64
	closeOnce sync.Once
	closed    chan struct{} // 关闭即置位，所有等待者立即返回
	wg        sync.WaitGroup

	// epoch 本客户端所属的**插件进程世代号**，由 pluginProcess 在 markUp 时写入、
	// 之后不再变（本客户端只属于那一个进程实例）。
	//
	// 它的存在是为了消掉一类 TOCTOU：remoteDriver 若用「先读 d.stale()（一次
	// epoch 读）再 proc.acquire()（另一次读）」来判断会话是否有效，两次读之间
	// 恰好换代时，拿到的会是一个**更新一代**的客户端，而手里的会话 id 属于
	// 已退役的旧进程 —— 该 id 在新进程里可能属于另一台设备的会话，既不报错
	// 也察觉不到（静默读到别的设备）。改用「实际对话的这个客户端的世代号」
	// 作判据后，判据与被判的对象同源，不可能撕裂。
	epoch atomic.Uint64

	// 健康统计：供监管判定"插件还活着但卡死"。
	timeoutStreak atomic.Int64
	lastSuccess   atomic.Int64 // UnixNano
	stuckStreak   int64
	stuckAge      time.Duration
}

func newClient(rw io.ReadWriteCloser) *client {
	c := &client{
		rw:          rw,
		pending:     make(map[uint64]chan pluginwire.Frame),
		closed:      make(chan struct{}),
		stuckStreak: stuckTimeoutStreak,
		stuckAge:    stuckAfter,
	}
	c.lastSuccess.Store(time.Now().UnixNano())
	c.wg.Add(1)
	go c.readLoop()
	return c
}

// setStuckThreshold 覆盖卡死判定阈值。生产路径用包级默认值；
// 测试用极小值以在毫秒级验证"插件卡死被重启"这条路径。
func (c *client) setStuckThreshold(streak int64, age time.Duration) {
	if streak > 0 {
		c.stuckStreak = streak
	}
	if age > 0 {
		c.stuckAge = age
	}
}

// readLoop 是唯一的读协程：把应答投递给对应的等待者。
func (c *client) readLoop() {
	defer c.wg.Done()
	for {
		f, err := pluginwire.ReadFrame(c.rw)
		if err != nil {
			c.fail(fmt.Errorf("%w: %v", ErrProcessDown, err))
			return
		}
		c.dispatch(f)
	}
}

// dispatch 投递应答。
//
// 投递**绝不能阻塞**读协程：通道缓冲为 1，因此即使调用方已超时放弃、
// 无人接收，这里也能立即返回。未知 id 的应答静默丢弃（调用方超时后
// 迟到的应答属正常情况）。
func (c *client) dispatch(f pluginwire.Frame) {
	if f.ID == 0 {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[f.ID]
	if ok {
		delete(c.pending, f.ID)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	ch <- f
}

// fail 幂等地标记连接已死。
//
// 不关闭各 pending 通道（关闭会让接收方收到零值 Frame，看起来像一次
// 成功的空应答）；改为关闭 closed 通道，所有等待者据此立即返回错误。
func (c *client) fail(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closeErr = err
		c.pending = make(map[uint64]chan pluginwire.Frame)
		c.mu.Unlock()

		close(c.closed)
		_ = c.rw.Close()
	})
}

// err 返回连接失效的原因。
func (c *client) err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closeErr != nil {
		return c.closeErr
	}
	return ErrProcessDown
}

// isUp 报告连接是否仍然可用。
func (c *client) isUp() bool {
	select {
	case <-c.closed:
		return false
	default:
		return true
	}
}

// wait 等待读协程退出。进程被 kill 后由监管调用，确保不留下读协程。
func (c *client) wait() { c.wg.Wait() }

// call 发起一次 RPC。
//
// 错误分两大类，调用方必须区分（见 remoteDriver.Read 的注释）：
//   - *pluginwire.RemoteError：驱动自身返回的错误，帧是完好的，
//     Result 仍然有效（read 的 connected 就靠它回传）；
//   - 其余（ctx.Err / ErrProcessDown）：RPC 层失败，代理本身坏了。
//
// result 非 nil 时，即使帧里带 Error 也会尝试解码 Result。
func (c *client) call(ctx context.Context, method string, params, result any) error {
	// 载荷编码由类型决定：实现了 BinaryEncoder 的走二进制，其余走 JSON。
	// 这里刻意不做「哪些方法走哪种编码」的判断 —— 那种对照表迟早与实现漂移。
	body, codec, err := pluginwire.EncodeBody(params)
	if err != nil {
		return fmt.Errorf("pluginhost: encode %s params: %w", method, err)
	}

	id := c.nextID.Add(1)
	ch := make(chan pluginwire.Frame, 1)

	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return c.err()
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.write(pluginwire.Frame{ID: id, Method: method, Codec: codec, Params: body}); err != nil {
		c.drop(id)
		return err
	}

	select {
	case resp := <-ch:
		c.noteSuccess()
		if len(resp.Result) > 0 && result != nil {
			if err := pluginwire.DecodeBody(resp.Codec, resp.Result, result); err != nil {
				return fmt.Errorf("pluginhost: decode %s result: %w", method, err)
			}
		}
		if resp.Error != nil {
			return resp.Error
		}
		return nil

	case <-ctx.Done():
		c.drop(id)
		// **只有真正的超时才算"插件卡死"的证据，主动取消不算。**
		//
		// 这一区分是设备扫描功能的前提：取消一个扫描任务会同时掐掉在飞的
		// 全部 probe 调用，若无条件计数，一次取消就顶满 stuckStreak；而
		// "一台还没建设备的新网关"（运维第一件事就是扫描）这个插件进程上
		// 除 probe 外没有任何成功调用，lastSuccess 早已过期 —— 看门狗随即
		// 杀掉进程，该插件下**所有协议、所有设备**一起断。
		//
		// 对既有调用方零行为变更：Connect/Read/Ping/Close 的超时全部由
		// context.WithTimeout(context.Background(), …) 派生，ctx.Err()
		// 只可能是 DeadlineExceeded。只有扫描任务的 ctx 才可能被取消。
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			c.noteTimeout()
		}
		return fmt.Errorf("pluginhost: %s canceled or timed out: %w", method, ctx.Err())

	case <-c.closed:
		c.drop(id)
		return c.err()
	}
}

func (c *client) write(f pluginwire.Frame) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()

	select {
	case <-c.closed:
		return c.err()
	default:
	}
	if err := pluginwire.WriteFrame(c.rw, f); err != nil {
		// 写失败说明流已不可用（管道破裂等），直接标记下线。
		c.fail(fmt.Errorf("%w: write frame: %v", ErrProcessDown, err))
		return c.err()
	}
	return nil
}

func (c *client) drop(id uint64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *client) noteSuccess() {
	c.timeoutStreak.Store(0)
	c.lastSuccess.Store(time.Now().UnixNano())
}

func (c *client) noteTimeout() { c.timeoutStreak.Add(1) }

// stuck 报告"进程还活着但卡死"：连续多次 RPC 超时且已很久没有成功调用。
//
// 这一判定不可省略：卡死的插件会让 IsConnected() 永远停在 true 且 Read
// 永远超时，而采集引擎只在 IsConnected()==false 时重连 —— 没有它就是一个
// 无恢复的活锁。监管据此杀掉并重启进程。
func (c *client) stuck() bool {
	if c.timeoutStreak.Load() < c.stuckStreak {
		return false
	}
	return time.Since(time.Unix(0, c.lastSuccess.Load())) > c.stuckAge
}
