// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iceisfun/goindustrial/protocol/ethernetip"
	"github.com/iceisfun/goindustrial/protocol/ethernetip/cip"
)

// rockwellTransport 传输层接口。供 rockwellDriver 持有，测试可注入 mock。
type rockwellTransport interface {
	// ReadTag 读取单个标签，返回 0x4C 响应数据区原始字节（含 2 字节类型码前缀，
	// 类型码剥离由 parser.go 的 ParseRockwellValue 统一处理）。
	// 通用状态错误返回 GeneralStatusError（连接是通的，设备拒绝，如标签不存在）；
	// 网络/超时错误返回普通 error。
	ReadTag(tagName string) ([]byte, error)
	// ReadTagElements 读取标签的 count 个连续元素（0x4C 带 ElementCount），
	// 返回响应数据区原始字节（2 字节类型码前缀 + count 个元素的连续小端数据）。
	// 用于数组区间合并读取，错误语义同 ReadTag。
	ReadTagElements(tagName string, count uint16) ([]byte, error)
	// IsConnected 返回连接状态
	IsConnected() bool
	// Close 关闭连接释放资源
	Close() error
}

// goindustrialClient 基于 github.com/iceisfun/goindustrial EtherNet/IP 客户端的适配器。
//
// 连接生命周期：TCP 连接（44818）→ Register Session（0x0065）→ 每次读取
// SendRRData（0x006F）携带一个 0x4C Read Tag 服务。库内部 TCPConn 写串行化，
// 适配层另持互斥锁保证请求-响应配对，并发 ReadTag 安全。
//
// 重连策略：不做库内自动重连（NewReconnectingClient），断连语义与 omron/cip 对齐——
// 网络/超时错误标记连接失效并上抛，由 collector 引擎的 ensureConnected 指数退避重连。
type goindustrialClient struct {
	mu      sync.Mutex
	client  *ethernetip.Client
	timeout time.Duration
	// tap 接住库从日志里带出来、却没进错误链的封装层状态码（见 bridgelogger.go）。
	tap *encapTap
	// connected 本地连接状态。库 IsConnected() 依赖 transport.Peeker 探测
	//（未读时无数据可探视为真），无法区分半开连接，故网络错误后由此标记失效，
	// 交给 collector 重连（与 omron/cip 的 connected 标记语义一致）。
	//
	// **必须是原子的**：Close() 刻意不取 mu（见 Close 的注释），它会与
	// ListTags 的调用方并发读写这一个字段。今天唯一会并发读它的调用方是
	// pointscan.go 的枚举守卫 —— 那次枚举可能一直阻塞在 socket 上，
	// 而收尾的 Close 只能绕过 mu 才能把套接字关掉、把阻塞解开。
	// 把这里改回 bool 或者给 Close 加锁，都会让那条路径重新变成数据竞争
	// 或死锁（见 client_test.go 的 TestClientListTagsUnblocksOnClose）。
	connected atomic.Bool
}

// newRockwellClient 创建并建立 Rockwell EtherNet/IP 连接（TCP + Register Session）。
func newRockwellClient(cfg *RockwellConfig) (rockwellTransport, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("rockwell: host is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	// 连接建立（dial + Register Session）受 ctx 超时约束
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 桥接库内部日志：do() 重试吞掉的传输错误细节（i/o timeout 等）只经
	// logger.Warn 输出，不接入则最终上抛的只有 "operation failed after N retries" 兜底文案。
	//
	// tap 一并交给桥接器：库把封装层状态码吞在那条 Warn 里、没放进错误链，桥接器是
	// 唯一能接住它的地方（见 bridgelogger.go 的 encapTap）。addr 只用于给库日志限流
	// 当键（同一句话按设备分别判断）。
	tap := &encapTap{}
	client, err := ethernetip.Connect(ctx, addr,
		ethernetip.WithDialTimeout(timeout),
		ethernetip.WithLogger(newGILevelBridge(addr, tap)))
	if err != nil {
		return nil, fmt.Errorf("rockwell: connect %s failed: %w", addr, err)
	}

	c := &goindustrialClient{
		client:  client,
		timeout: timeout,
		tap:     tap,
	}
	c.connected.Store(true)
	return c, nil
}

// wrapReadError 在**持锁的调用点**把封装层状态码裹进错误。
//
// 必须在这里取走 encapTap：库的 Warn 是在本次调用内打出来的，出了这把锁状态码就
// 可能被并发的另一次 Read 抢走（那多半也是同一个设备级原因，但不该依赖这个巧合）。
func (c *goindustrialClient) wrapReadError(err error) error {
	if err == nil || c.tap == nil {
		return err
	}
	if st, ok := c.tap.take(); ok {
		return &encapError{status: st, err: err}
	}
	return err
}

// ReadTag 读取单个标签（0x4C），返回响应数据区（2 字节类型码前缀 + 元素数据）。
func (c *goindustrialClient) ReadTag(tagName string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil || !c.connected.Load() {
		return nil, fmt.Errorf("rockwell: not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	data, err := c.client.ReadTag(ctx, tagName)
	if err != nil {
		err = c.wrapReadError(err)
		// CIP 状态错误（设备响应但拒绝）连接仍通，不置 connected=false；
		// 网络/超时错误视为连接失效，由 collector 重连。
		if !isCIPStatusError(err) {
			c.connected.Store(false)
		}
		return nil, err
	}
	return data, nil
}

// ReadTagElements 读取标签的 count 个连续元素（0x4C 带 ElementCount）。
// 数组合并读取路径：响应数据区为 2 字节类型码 + count 个元素的连续小端数据。
func (c *goindustrialClient) ReadTagElements(tagName string, count uint16) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil || !c.connected.Load() {
		return nil, fmt.Errorf("rockwell: not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	data, err := c.client.ReadTagElements(ctx, tagName, count)
	if err != nil {
		// 错误语义与 ReadTag 一致：CIP 状态错误连接仍通，网络/超时错误标记失效
		err = c.wrapReadError(err)
		if !isCIPStatusError(err) {
			c.connected.Store(false)
		}
		return nil, err
	}
	return data, nil
}

// ListTags 枚举控制器上的全部标签（CIP Symbol Object，类 0x6B）。
//
// 用库自己的实现（ethernetip.Client.ListTags）而不是照着手册另写一份请求/应答
// 编解码：那份布局在本仓库里**唯一的依据就是它**，而"没有第二处依据就不猜"
// 是本仓库反复挡过的事（见 specs/point-discovery.md §3 对 Omron.CIP 的说明）。
//
// **不进 rockwellTransport 接口**：那个接口是采集路径的契约，加一个只有扫描用的
// 方法会逼着每一个测试桩都去实现它、并让"驱动能读标签"与"驱动能枚举标签"这两件
// 不同的能力在类型上混为一谈。调用方走 pointscan.go 的 tagEnumerator 类型断言。
//
// 与 ReadTag 一样全程持 c.mu：库内部是"一次请求配一次响应"，不串行化就会串帧。
// 也正因如此，**这个调用可能长时间不返回**（库的 TCP 传输不设读超时），调用方
// 必须自己用 deadline 兜住 —— 见 pointscan.go 的枚举守卫。
func (c *goindustrialClient) ListTags(ctx context.Context) ([]cip.SymbolInstance, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil || !c.connected.Load() {
		return nil, fmt.Errorf("rockwell: not connected")
	}

	tags, err := c.client.ListTags(ctx)
	if err != nil {
		// 错误语义与 ReadTag 一致：CIP 状态错误连接仍通，网络/超时错误标记失效。
		// 封装层拒绝也在这里裹上状态码 —— 点位扫描的窗口要能看出"设备在线但拒了"，
		// 而不是一句 "operation failed after N retries"。
		err = c.wrapReadError(err)
		//
		// 注意这里**只清理标志、不重连**：枚举用的是一次性实例（串口协议才需要
		// 复用采集会话，而 Rockwell 只有 TCP），它的生命周期到这次扫描为止。
		if !isCIPStatusError(err) {
			c.connected.Store(false)
		}
		return nil, err
	}
	return tags, nil
}

// IsConnected 返回连接状态
func (c *goindustrialClient) IsConnected() bool {
	return c != nil && c.connected.Load() && c.client != nil && c.client.IsConnected()
}

// Close 关闭连接：尽力注销会话后关闭 TCP。
//
// **刻意不取 mu**：点位扫描的枚举可能一直阻塞在 socket 上（库的 TCP 传输不设读
// 超时，见 ListTags），而枚举的收尾正是靠这里把套接字关掉、让那次阻塞的 Receive
// 立刻返回错误。取 mu 就等于等一个不会自己结束的调用 —— 死锁。
//
// 代价是 connected 必须原子读写（见该字段的注释）。
func (c *goindustrialClient) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	// 尽力注销会话并关闭连接，忽略错误（会话由 PLC 超时回收）
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	_ = c.client.Disconnect(ctx)
	c.connected.Store(false)
	return nil
}

// isCIPStatusError 判断错误是否为 CIP 通用状态错误（设备响应但拒绝请求）。
// goindustrial 把 CIP 状态错误以 cip.Error（值类型）包装返回，网络/超时错误
// 为普通 error。路径段错误/目标不存在等属正常业务拒绝，连接仍然可用。
func isCIPStatusError(err error) bool {
	if err == nil {
		return false
	}
	var cipErr cip.Error
	if asCIPError(err, &cipErr) {
		return true
	}
	// 兼容库版本差异：cipError 为内部包装类型，经 Unwrap 链可能直接暴露 cip.Error
	return false
}

// asCIPError 在错误链上查找 cip.Error（值类型，兼容 value / pointer 两种包装）。
func asCIPError(err error, target *cip.Error) bool {
	for err != nil {
		if e, ok := err.(cip.Error); ok {
			*target = e
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// stripTypeCodeHeader 剥离 0x4C 响应数据区的类型码头部，返回纯数据区。
//
// Read Tag 响应数据区格式：2 字节 LE 类型码；类型码 ≥ TypeSTRUCT(0x02A0) 时
// 额外带 2 字节成员数（共 4 字节头，如 Logix STRING 0x00D0）。
func stripTypeCodeHeader(data []byte) ([]byte, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("rockwell: response too short for type code header: %d bytes", len(data))
	}
	code := binary.LittleEndian.Uint16(data[0:2])
	hdrLen := 2
	if code >= uint16(cip.TypeSTRUCT) {
		hdrLen = 4
	}
	if len(data) < hdrLen {
		return nil, fmt.Errorf("rockwell: response too short for type header: %d bytes", len(data))
	}
	return data[hdrLen:], nil
}
