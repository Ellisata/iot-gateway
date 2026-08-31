package cip

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"sync"
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
	// connected 本地连接状态。库 IsConnected() 依赖 transport.Peeker 探测
	//（未读时无数据可探视为真），无法区分半开连接，故网络错误后由此标记失效，
	// 交给 collector 重连（与 omron/cip 的 connected 标记语义一致）。
	connected bool
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
	// logger.Warn 输出，不接入则最终上抛的只有 "operation failed after N retries" 兜底文案
	client, err := ethernetip.Connect(ctx, addr,
		ethernetip.WithDialTimeout(timeout),
		ethernetip.WithLogger(newGILevelBridge()))
	if err != nil {
		return nil, fmt.Errorf("rockwell: connect %s failed: %w", addr, err)
	}

	return &goindustrialClient{
		client:    client,
		timeout:   timeout,
		connected: true,
	}, nil
}

// ReadTag 读取单个标签（0x4C），返回响应数据区（2 字节类型码前缀 + 元素数据）。
func (c *goindustrialClient) ReadTag(tagName string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil || !c.connected {
		return nil, fmt.Errorf("rockwell: not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	data, err := c.client.ReadTag(ctx, tagName)
	if err != nil {
		// CIP 状态错误（设备响应但拒绝）连接仍通，不置 connected=false；
		// 网络/超时错误视为连接失效，由 collector 重连。
		if !isCIPStatusError(err) {
			c.connected = false
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

	if c.client == nil || !c.connected {
		return nil, fmt.Errorf("rockwell: not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	data, err := c.client.ReadTagElements(ctx, tagName, count)
	if err != nil {
		// 错误语义与 ReadTag 一致：CIP 状态错误连接仍通，网络/超时错误标记失效
		if !isCIPStatusError(err) {
			c.connected = false
		}
		return nil, err
	}
	return data, nil
}

// IsConnected 返回连接状态
func (c *goindustrialClient) IsConnected() bool {
	return c != nil && c.connected && c.client != nil && c.client.IsConnected()
}

// Close 关闭连接：尽力注销会话后关闭 TCP。
func (c *goindustrialClient) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	// 尽力注销会话并关闭连接，忽略错误（会话由 PLC 超时回收）
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	_ = c.client.Disconnect(ctx)
	c.connected = false
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
