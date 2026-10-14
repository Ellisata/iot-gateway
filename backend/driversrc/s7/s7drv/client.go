// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package s7

import (
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/robinson/gos7"
)

// maxS7ByteAddr gos7 可寻址的最大字节偏移。
// gos7 在读取请求中用 3 字节编码字节偏移（start<<3），上限 0xFFFFFF>>3 ≈ 2,097,151；
// 超过该值的区间会静默回绕读到错误位置，故在 readRange 入口直接报错。
// 经典 S7 DB/区段 ≤64KB，正常配置不会触发，仅防护超大区间/越界地址。
const maxS7ByteAddr = 1<<21 - 1

// S7Client gos7 TCP 客户端封装。
//
// gos7 的 tcpTransporter.Send 对每次请求+响应持有内部互斥锁，
// 同一连接上并发 Read 是安全的；驱动层再按 modbus 模式做指针快照，
// 因此本结构不需要额外的 I/O 串行化锁。
type S7Client struct {
	handler   *gos7.TCPClientHandler
	client    gos7.Client
	connected atomic.Bool // gos7 不暴露连接状态，由本驱动自维护；原子读写避免并发竞态
}

// newTCPHandler 按连接类型构造 gos7 TCP 处理器。
// connectType 不在 [PG, OP, Basic] 范围时回退 gos7 默认（PG）。
func newTCPHandler(addr string, rack, slot, connectType int, d time.Duration) *gos7.TCPClientHandler {
	var handler *gos7.TCPClientHandler
	if connectType >= ConnectionTypePG && connectType <= ConnectionTypeBasic {
		handler = gos7.NewTCPClientHandlerWithConnectType(addr, rack, slot, connectType)
	} else {
		handler = gos7.NewTCPClientHandler(addr, rack, slot)
	}
	handler.Timeout = d
	handler.IdleTimeout = 0 // 禁用空闲自动断连，保持连接常开
	return handler
}

// NewS7Client 建立到 PLC 的连接（TCP + ISO-on-TCP + S7 PDU 协商握手）。
func NewS7Client(host, port string, rack, slot, connectType int, timeout time.Duration) (*S7Client, error) {
	addr := net.JoinHostPort(host, port)
	handler := newTCPHandler(addr, rack, slot, connectType, timeout)

	if err := handler.Connect(); err != nil {
		return nil, fmt.Errorf("s7: connect %s failed (rack=%d, slot=%d, connectType=%d): %w",
			addr, rack, slot, connectType, err)
	}

	sc := &S7Client{
		handler: handler,
		client:  gos7.NewClient(handler),
	}
	sc.connected.Store(true)
	return sc, nil
}

// IsConnected 返回连接状态
func (c *S7Client) IsConnected() bool {
	return c != nil && c.connected.Load()
}

// MarkDisconnected 标记连接断开（由 Read 遇到网络错误时调用）
func (c *S7Client) MarkDisconnected() {
	if c != nil {
		c.connected.Store(false)
	}
}

// readRange 读取一个区间内的字节。
// buf 由调用方预先分配 size 字节；gos7 内部按 PDU 自动分块。
func (c *S7Client) readRange(r S7Range, buf []byte) error {
	size := r.EndOffset - r.StartOffset
	if size <= 0 {
		return fmt.Errorf("s7: invalid range size %d", size)
	}
	if r.EndOffset > maxS7ByteAddr {
		return fmt.Errorf("s7: range end offset %d exceeds 24-bit address limit %d",
			r.EndOffset, maxS7ByteAddr)
	}

	switch r.Area {
	case S7AreaDB:
		return c.client.AGReadDB(r.DB, r.StartOffset, size, buf)
	case S7AreaM:
		return c.client.AGReadMB(r.StartOffset, size, buf)
	case S7AreaI:
		return c.client.AGReadEB(r.StartOffset, size, buf)
	case S7AreaQ:
		return c.client.AGReadAB(r.StartOffset, size, buf)
	default:
		return fmt.Errorf("s7: unsupported area 0x%02X", r.Area)
	}
}

// Close 关闭连接
func (c *S7Client) Close() error {
	if c == nil || c.handler == nil {
		return nil
	}
	err := c.handler.Close()
	c.connected.Store(false)
	return err
}
