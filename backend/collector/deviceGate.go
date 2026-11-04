// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import "context"

// deviceGate 设备级互斥门：同一设备的采集、重连、控制下发与触发规则读取
// 串行执行。
//
// 与 sync.Mutex 的唯一区别是支持带 context 的等待：触发规则/控制下发在
// 「慢采集正占着设备」时可以按规则超时放弃等待，而不是无限挂住 worker。
// 采集轮询继续用 Lock()，语义与 Mutex 完全一致。
//
// 零值不可用，必须通过 newDeviceGate 创建。
type deviceGate struct {
	ch chan struct{}
}

func newDeviceGate() *deviceGate {
	g := &deviceGate{ch: make(chan struct{}, 1)}
	g.ch <- struct{}{} // 初始持有令牌 = 未加锁
	return g
}

// Lock 阻塞直到拿到门（采集轮询路径，无超时）。
func (g *deviceGate) Lock() {
	<-g.ch
}

// LockContext 带 context 地等待门。返回 ctx.Err() 时未拿到门，
// 调用方不得解锁。
func (g *deviceGate) LockContext(ctx context.Context) error {
	select {
	case <-g.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Unlock 释放门。
func (g *deviceGate) Unlock() {
	g.ch <- struct{}{}
}
