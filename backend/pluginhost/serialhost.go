// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

// ==================== 网关侧串口代理 ====================
//
// 两个**不同插件进程**配到同一个物理串口时（例如 DLT645.Serial 与 Modbus.RTU），
// 各自打开一个 fd 会同时踩三个坑（详见 SDK 的 driversdk/serialbus 包注释）：
// 应答字节被另一个读端瓜分、select 竞争失败方拿到 EAGAIN、任一方 Close 恢复
// termios 打断另一方。进程内共享句柄修不了这一类 —— fd 属于不同的进程。
//
// 因此网关主进程持有**唯一**的物理句柄，插件经 serial.* RPC 收发字节：
//
//	serial.open   按「归一端口 + 串口参数」共享同一个 fd，返回句柄 id
//	serial.lock   取得跨进程总线锁（一次完整请求-应答全程持有）
//	serial.read   最多等 N ms，有字节就返回；无字节且超时则 timedOut=true
//	serial.write  发送字节
//	serial.unlock 释放总线锁
//	serial.close  释放句柄；进程断开时网关也会自动清理
//
// 本文件属于**驱动侧适配层**（与 adapter_driver.go / remote.go 同级）：
// client.go 只提供一个通用的反向请求钩子，不认识 serial.* 这些方法。
//
// 代理只在「同端口上全部协议都声明了 SerialProxy」时启用（见 collector 的
// configureSerialBusSharing）。旧插件、旧网关都不会走到这里。

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goburrow/serial"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/driversdk/serialproxy"
	"github.com/Ellisata/iot-gateway-sdk/pluginwire"

	"iot-gateway/logger"
)

// hostSerialReadSlice 宿主侧单次 Read 的阻塞上限。
//
// serial.read 的调用方给出的是**整次读**的上限（可能是 5s），而 goburrow/serial
// 的读超时在打开时固定。这里用一个小切片反复读，凑满调用方给的截止时间 ——
// 既有"有字节立即返回"的语义，又不会为一次长超时开一个无法中断的阻塞读。
const hostSerialReadSlice = 50 * time.Millisecond

// hostSerialMaxRead 单次 serial.read 允许请求的最大字节数（防御性上界）。
const hostSerialMaxRead = 64 << 10

// openHostSerialPort 打开物理端口。变量形式供测试注入假端口 ——
// 句柄共享、跨进程互斥与超时措辞都要能脱离硬件验证。
var openHostSerialPort = func(cfg *serial.Config) (io.ReadWriteCloser, error) {
	return serial.Open(cfg)
}

// serialKey 一个物理句柄的身份：归一端口名 + 影响 tty 配置的参数。
type serialKey struct {
	port     string
	baud     int
	dataBits int
	stopBits int
	parity   string
}

func (k serialKey) params() string {
	return fmt.Sprintf("baud=%d,data=%d,stop=%d,parity=%s",
		k.baud, k.dataBits, k.stopBits, k.parity)
}

// serialPortEntry 一个物理端口。sem 是跨进程总线锁：容量 1 的信号量，
// holder 记录当前持锁的句柄 id（0 = 空闲）。
type serialPortEntry struct {
	key    serialKey
	port   io.ReadWriteCloser
	sem    chan struct{}
	holder atomic.Uint64
	refs   int
	alive  atomic.Bool
	closed bool // 受 serialHost.mu 保护
}

// serialHandle 一个插件会话对物理端口的引用。owner 是插件进程的连接，
// 进程断开时 closeOwner 会把这些句柄全部关掉。
type serialHandle struct {
	id    uint64
	owner *client
	entry *serialPortEntry
}

// serialHost 网关进程内的串口代理注册表。单例（见 driverSerialHost）。
type serialHost struct {
	mu      sync.Mutex
	byKey   map[serialKey]*serialPortEntry
	byPort  map[string]map[serialKey]*serialPortEntry
	handles map[uint64]*serialHandle
	nextID  uint64
}

func newSerialHost() *serialHost {
	return &serialHost{
		byKey:   make(map[serialKey]*serialPortEntry),
		byPort:  make(map[string]map[serialKey]*serialPortEntry),
		handles: make(map[uint64]*serialHandle),
	}
}

// driverSerialHost 整个网关进程共享的一张注册表。
var driverSerialHost = newSerialHost()

func (h *serialHost) addLocked(e *serialPortEntry) {
	h.byKey[e.key] = e
	set := h.byPort[e.key.port]
	if set == nil {
		set = make(map[serialKey]*serialPortEntry, 1)
		h.byPort[e.key.port] = set
	}
	set[e.key] = e
}

func (h *serialHost) removeLocked(e *serialPortEntry) {
	if cur, ok := h.byKey[e.key]; ok && cur == e {
		delete(h.byKey, e.key)
	}
	set := h.byPort[e.key.port]
	if set == nil {
		return
	}
	delete(set, e.key)
	if len(set) == 0 {
		delete(h.byPort, e.key.port)
	}
}

func (h *serialHost) liveOnPortLocked(port string) *serialPortEntry {
	for _, e := range h.byPort[port] {
		if e.alive.Load() {
			return e
		}
	}
	return nil
}

// open 取得该配置对应的共享句柄：能复用就复用，否则打开并登记。
func (h *serialHost) open(owner *client, p serialproxy.SerialOpenParams) (uint64, error) {
	key := serialKey{
		port:     driver.CanonicalSerialPort(p.Port),
		baud:     p.BaudRate,
		dataBits: p.DataBits,
		stopBits: p.StopBits,
		parity:   strings.ToUpper(strings.TrimSpace(p.Parity)),
	}
	if key.port == "" {
		return 0, fmt.Errorf("串口名未配置")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if e := h.byKey[key]; e != nil {
		if e.alive.Load() {
			e.refs++
			id := h.newHandleLocked(owner, e)
			logger.Info("pluginhost: 串口 %s 被协议插件复用（%s，共 %d 个插件会话）",
				key.port, key.params(), e.refs)
			return id, nil
		}
		// 句柄已判死：摘登记、关旧句柄，再走新开路径。关闭在锁内做，保证
		// 新 open 一定发生在旧 fd 关闭（termios 已恢复）之后。
		h.removeLocked(e)
		e.closed = true
		_ = e.port.Close()
	}

	// 同端口参数不同：配置错误。同一条总线上的设备必须参数一致。
	if other := h.liveOnPortLocked(key.port); other != nil {
		return 0, fmt.Errorf("串口 %s 已被另一台设备以不同参数打开（%s），"+
			"本设备为（%s）；同一条总线上的设备必须使用相同的串口参数",
			key.port, other.key.params(), key.params())
	}

	port, err := openHostSerialPort(&serial.Config{
		Address:  p.Port,
		BaudRate: key.baud,
		DataBits: key.dataBits,
		StopBits: key.stopBits,
		Parity:   key.parity,
		Timeout:  hostSerialReadSlice,
	})
	if err != nil {
		return 0, fmt.Errorf("打开 %s 失败: %w%s", p.Port, err, driver.SerialOpenFailureHint)
	}

	e := &serialPortEntry{
		key:  key,
		port: port,
		sem:  make(chan struct{}, 1),
		refs: 1,
	}
	e.sem <- struct{}{} // 初始空闲
	e.alive.Store(true)
	h.addLocked(e)

	id := h.newHandleLocked(owner, e)
	logger.Info("pluginhost: 串口 %s 已由网关代理（%s）", key.port, key.params())
	return id, nil
}

func (h *serialHost) newHandleLocked(owner *client, e *serialPortEntry) uint64 {
	h.nextID++
	id := h.nextID
	h.handles[id] = &serialHandle{id: id, owner: owner, entry: e}
	return id
}

// lookup 返回句柄与端口；句柄不存在或已失效时返回错误。
func (h *serialHost) lookup(owner *client, id uint64) (*serialHandle, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	hd, ok := h.handles[id]
	if !ok || hd.owner != owner {
		return nil, fmt.Errorf("无效的串口句柄 %d", id)
	}
	if !hd.entry.alive.Load() {
		return nil, fmt.Errorf("串口句柄 %d 已失效", id)
	}
	return hd, nil
}

// close 释放句柄；最后一个引用才真正关闭端口。幂等。
func (h *serialHost) close(owner *client, id uint64) error {
	h.mu.Lock()
	hd, ok := h.handles[id]
	if !ok || hd.owner != owner {
		h.mu.Unlock()
		return nil // 重复关闭视为成功
	}
	delete(h.handles, id)
	e := hd.entry
	// 关闭时仍未解锁：把令牌还回去，避免整条总线被一个已断开的会话锁死。
	if e.holder.CompareAndSwap(id, 0) {
		e.sem <- struct{}{}
	}
	e.refs--
	if e.refs > 0 {
		h.mu.Unlock()
		return nil
	}
	h.removeLocked(e)
	e.closed = true
	h.mu.Unlock()

	e.alive.Store(false)
	err := e.port.Close()
	logger.Info("pluginhost: 串口 %s 已关闭（最后一个插件会话释放）", e.key.port)
	return err
}

// closeOwner 关闭某个插件进程持有的全部句柄（进程崩溃/退出）。
func (h *serialHost) closeOwner(owner *client) {
	h.mu.Lock()
	ids := make([]uint64, 0)
	for id, hd := range h.handles {
		if hd.owner == owner {
			ids = append(ids, id)
		}
	}
	h.mu.Unlock()

	for _, id := range ids {
		_ = h.close(owner, id)
	}
}

func (h *serialHost) lock(owner *client, id uint64, timeout time.Duration) error {
	hd, err := h.lookup(owner, id)
	if err != nil {
		return err
	}
	e := hd.entry

	// 已持有（驱动重复 Lock）视为成功，不重复占令牌。
	if e.holder.Load() == id {
		return nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-e.sem:
	case <-timer.C:
		return fmt.Errorf("串口 %s 正被另一个插件会话使用，等待 %v 超时", e.key.port, timeout)
	}

	if !e.alive.Load() {
		e.sem <- struct{}{}
		return fmt.Errorf("串口句柄 %d 已失效", id)
	}
	e.holder.Store(id)
	return nil
}

func (h *serialHost) unlock(owner *client, id uint64) error {
	hd, err := h.lookup(owner, id)
	if err != nil {
		return err
	}
	e := hd.entry
	if !e.holder.CompareAndSwap(id, 0) {
		return fmt.Errorf("串口 %s 的总线锁不是由本会话持有", e.key.port)
	}
	e.sem <- struct{}{}
	return nil
}

// write 发送字节。调用方必须持有总线锁。
func (h *serialHost) write(owner *client, id uint64, data []byte) (int, error) {
	hd, err := h.lookup(owner, id)
	if err != nil {
		return 0, err
	}
	e := hd.entry
	if e.holder.Load() != id {
		return 0, fmt.Errorf("串口 %s 未加锁，拒绝写入", e.key.port)
	}
	n, err := e.port.Write(data)
	if err != nil {
		e.alive.Store(false)
		return n, fmt.Errorf("串口 %s 写入失败: %w", e.key.port, err)
	}
	return n, nil
}

// read 读取字节：一有数据就返回；直到 deadline 都没有数据则 timedOut=true。
// 调用方必须持有总线锁。返回的切片最多 max 字节。
func (h *serialHost) read(owner *client, id uint64, max, timeoutMs int) ([]byte, bool, error) {
	hd, err := h.lookup(owner, id)
	if err != nil {
		return nil, false, err
	}
	e := hd.entry
	if e.holder.Load() != id {
		return nil, false, fmt.Errorf("串口 %s 未加锁，拒绝读取", e.key.port)
	}

	if max <= 0 || max > hostSerialMaxRead {
		max = hostSerialMaxRead
	}
	if timeoutMs <= 0 {
		timeoutMs = 1
	}
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	buf := make([]byte, max)

	for {
		n, rerr := e.port.Read(buf)
		if n > 0 {
			return buf[:n], false, nil
		}
		if rerr != nil && !isHostSerialTimeout(rerr) {
			e.alive.Store(false)
			return nil, false, fmt.Errorf("串口 %s 读取失败: %w", e.key.port, rerr)
		}
		if !time.Now().Before(deadline) {
			return nil, true, nil
		}
	}
}

func isHostSerialTimeout(err error) bool {
	return errors.Is(err, serial.ErrTimeout)
}

// handleRequest 处理插件发起的 serial.* 反向请求。
//
// 每个请求都在独立 goroutine 里执行（见 client.dispatch）：一次 5 秒的串口读
// 不能挡住同一插件进程上其它设备的请求。
func (h *serialHost) handleRequest(cl *client, f pluginwire.Frame) {
	result, rerr := h.dispatch(cl, f)

	body, codec, err := pluginwire.EncodeBody(result)
	if err != nil {
		body, codec = nil, pluginwire.CodecJSON
		rerr = &pluginwire.RemoteError{Message: "marshal serial result: " + err.Error()}
	}
	// 应答复用请求的 ID、不带 Method —— 插件侧据此投递给等待者。
	_ = cl.write(pluginwire.Frame{ID: f.ID, Codec: codec, Result: body, Error: rerr})
}

func (h *serialHost) dispatch(cl *client, f pluginwire.Frame) (any, *pluginwire.RemoteError) {
	switch f.Method {
	case pluginwire.MethodSerialOpen:
		var p serialproxy.SerialOpenParams
		if rerr := decodeHostParams(f, &p); rerr != nil {
			return nil, rerr
		}
		id, err := h.open(cl, p)
		if err != nil {
			return nil, pluginwire.NewRemoteError(err)
		}
		return serialproxy.SerialOpenResult{ID: id}, nil

	case pluginwire.MethodSerialClose:
		var p serialproxy.SerialHandleParams
		if rerr := decodeHostParams(f, &p); rerr != nil {
			return nil, rerr
		}
		if err := h.close(cl, p.ID); err != nil {
			return nil, pluginwire.NewRemoteError(err)
		}
		return struct{}{}, nil

	case pluginwire.MethodSerialLock:
		var p serialproxy.SerialLockParams
		if rerr := decodeHostParams(f, &p); rerr != nil {
			return nil, rerr
		}
		timeout := time.Duration(p.TimeoutMs) * time.Millisecond
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		if err := h.lock(cl, p.ID, timeout); err != nil {
			return nil, pluginwire.NewRemoteError(err)
		}
		return struct{}{}, nil

	case pluginwire.MethodSerialUnlock:
		var p serialproxy.SerialHandleParams
		if rerr := decodeHostParams(f, &p); rerr != nil {
			return nil, rerr
		}
		if err := h.unlock(cl, p.ID); err != nil {
			return nil, pluginwire.NewRemoteError(err)
		}
		return struct{}{}, nil

	case pluginwire.MethodSerialWrite:
		var p serialproxy.SerialWriteParams
		if rerr := decodeHostParams(f, &p); rerr != nil {
			return nil, rerr
		}
		n, err := h.write(cl, p.ID, p.Data)
		if err != nil {
			return nil, pluginwire.NewRemoteError(err)
		}
		return serialproxy.SerialWriteReply{N: n}, nil

	case pluginwire.MethodSerialRead:
		var p serialproxy.SerialReadParams
		if rerr := decodeHostParams(f, &p); rerr != nil {
			return nil, rerr
		}
		data, timedOut, err := h.read(cl, p.ID, p.Max, p.TimeoutMs)
		if err != nil {
			return nil, pluginwire.NewRemoteError(err)
		}
		return serialproxy.SerialReadReply{Data: data, TimedOut: timedOut}, nil

	default:
		return nil, &pluginwire.RemoteError{Message: fmt.Sprintf("unknown serial method %q", f.Method)}
	}
}

// decodeHostParams 解析反向请求参数；失败时返回可过线的错误。
func decodeHostParams(f pluginwire.Frame, v any) *pluginwire.RemoteError {
	if len(f.Params) == 0 {
		return nil
	}
	if err := pluginwire.DecodeBody(f.Codec, f.Params, v); err != nil {
		return &pluginwire.RemoteError{Message: "bad serial params: " + err.Error()}
	}
	return nil
}
