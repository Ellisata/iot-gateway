// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"context"
	"errors"
	"fmt"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/driveradapter"
	"iot-gateway/model/po"
)

// 控制下发的错误分类。服务层据此给出可读提示，并区分"配置问题"与"设备状态"。
var (
	// ErrNotRunning 采集引擎未运行（或正在热刷新）。写不能绕过采集引擎
	// 另开连接，因此直接拒绝。
	ErrNotRunning = errors.New("collector: not running")
	// ErrDeviceNotActive 设备不在当前采集任务里（未启用、协议不支持或刚被删除）。
	ErrDeviceNotActive = errors.New("collector: device is not in the active collection task")
	// ErrDeviceOffline 设备当前没有可用的活连接。写路径**不触发重连**：
	// 重连属于采集侧的职责，控制指令不应为了下发而静默建立一条新连接
	//（串口场景还会抢占正在重连的端口）。设备下一轮采集恢复后即可再试。
	ErrDeviceOffline = errors.New("collector: device is offline")
	// ErrWriteUnsupported 协议/驱动没有声明可写能力。
	ErrWriteUnsupported = errors.New("collector: protocol does not support write")
)

// Write 通过采集引擎持有的活会话执行一次控制下发。
//
// 锁序：Engine.mu.RLock → task.mu（GetDriver）→ deviceLock。
// 持有 Engine.mu.RLock 直到写完，目的是让 Refresh/StopCollection 的写锁
// 等这一次控制完成 —— 否则 Refresh 会关掉正在写的驱动，把一帧写到一半
// 的连接拆掉。代价是慢速串口上的一次控制可能让热刷新多等一个轮询回合，
// 这是刻意的取舍：控制指令不应该被静默丢弃。
func (e *Engine) Write(ctx context.Context, deviceID string, reqs []driver.WriteRequest) ([]driver.WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.task == nil || !e.running {
		return nil, ErrNotRunning
	}
	dev := e.task.findDevice(deviceID)
	if dev == nil {
		return nil, fmt.Errorf("%w: %s", ErrDeviceNotActive, deviceID)
	}
	drv := e.task.GetDriver(deviceID)
	if drv == nil {
		return nil, fmt.Errorf("%w: %s", ErrDeviceNotActive, deviceID)
	}
	writer, ok := drv.(driver.Writer)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrWriteUnsupported, e.task.protocols[deviceID])
	}
	if !drv.IsConnected() {
		return nil, fmt.Errorf("%w: %s", ErrDeviceOffline, dev.Name)
	}

	// 与采集轮询共用设备锁：串口设备会等当前整轮采集结束，TCP 设备只与
	// 重连/在途读写互斥。拿锁后再核一次连接状态，避免等待期间链路掉线。
	dm := e.task.deviceLock(deviceID)
	dm.Lock()
	defer dm.Unlock()
	if !drv.IsConnected() {
		return nil, fmt.Errorf("%w: %s", ErrDeviceOffline, dev.Name)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return writer.Write(reqs)
}

// ReadPoints 用采集引擎的活会话读取指定点位，供控制下发后的回读校验。
//
// 与 Write 相同的锁与连接约束；读不触发重连。调用方负责跳过只有 W 权限的
// 点位（它们本来就读不到）。
func (e *Engine) ReadPoints(ctx context.Context, deviceID string, addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.task == nil || !e.running {
		return nil, ErrNotRunning
	}
	dev := e.task.findDevice(deviceID)
	if dev == nil {
		return nil, fmt.Errorf("%w: %s", ErrDeviceNotActive, deviceID)
	}
	drv := e.task.GetDriver(deviceID)
	if drv == nil {
		return nil, fmt.Errorf("%w: %s", ErrDeviceNotActive, deviceID)
	}
	if !drv.IsConnected() {
		return nil, fmt.Errorf("%w: %s", ErrDeviceOffline, dev.Name)
	}

	dm := e.task.deviceLock(deviceID)
	dm.Lock()
	defer dm.Unlock()
	if !drv.IsConnected() {
		return nil, fmt.Errorf("%w: %s", ErrDeviceOffline, dev.Name)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return drv.Read(driveradapter.FromPOs(addrs))
}
