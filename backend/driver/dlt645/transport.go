// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"fmt"
	"time"
)

// dlt645Transport 底层字节流传输。
//
// DL/T 645 本身没有传输层语义，串口与 TCP（串口服务器 / DTU 透传）拿到的是
// 同一条半双工字节流，帧定界完全由应用层自建，因此两种传输共用 codec.go 的
// 全部逻辑，只需要各自提供「带超时的读写 + 清缓冲」。
type dlt645Transport interface {
	// Lock/Unlock 串行化一次完整的「清缓冲 → 发送 → 读应答」交互。
	// TCP 传输是非独占的（采集引擎允许并发 Read），必须由传输层保证
	// 同一连接上的请求-应答不被交错，否则回显与迟到应答会互相错配。
	Lock()
	Unlock()

	// Write 发送完整帧（含前导唤醒字节）。
	Write(p []byte) (int, error)
	// Read 读取已到达的字节；无数据时按底层超时返回超时错误。
	// 返回的切片长度可变（串口与 TCP 都只保证「至少 1 字节」）。
	Read(p []byte) (int, error)
	// SetReadDeadline 设置本次读的截止时间（TCP 生效；串口为 no-op，
	// 由底层短超时 + codec 的截止时间循环共同兜底）。
	SetReadDeadline(t time.Time) error
	// Drain 丢弃接收缓冲中的残留字节（上一轮迟到的应答）。
	// 实现可以确认无残留时零成本返回：TCP 直接查 socket 缓冲；
	// 串口查不了，靠 MarkDirty 的标记跳过干净轮次。
	Drain()
	// MarkDirty 标记「本轮交互没有干净收尾，链路上可能还有在途字节」，
	// 使下一次 Drain 真正去清缓冲。调用点在 codec.go 的失败与噪声路径。
	//
	// 迟到的应答只可能在某轮**没按时收到应答**时才在途：干净收尾的轮次，
	// 对方已经答完了，不会有东西再飘过来。所以只在这些路径记账是充分的，
	// 而它换来的收益是串口每帧省下 serialReadTimeout 的空读。
	MarkDirty()

	IsConnected() bool
	Close() error
}

// newTransport 按配置的传输层创建传输实例。
func newTransport(cfg *DLT645Config) (dlt645Transport, error) {
	switch cfg.Transport {
	case TransportSerial:
		return newSerialClient(cfg)
	case TransportTCP:
		return newTCPClient(cfg)
	default:
		return nil, fmt.Errorf("dlt645: 未知传输层 %q", cfg.Transport)
	}
}
