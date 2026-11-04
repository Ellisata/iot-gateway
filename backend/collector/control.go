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
	// ErrDeviceBusy 在 ctx 截止前没等到设备锁：设备正被采集轮询、重连或
	// 另一次读写占用。此时**尚未发生设备 I/O**，本次操作可安全视为未执行。
	// 触发规则据此记录 timeout，而不是把"排队太久"误报成读取失败。
	ErrDeviceBusy = errors.New("collector: device is busy")
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
	// 等待带 ctx：慢采集占着设备时按调用方超时放弃，而不是无限挂住。
	dm := e.task.deviceLock(deviceID)
	if err := dm.LockContext(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDeviceBusy, err)
	}
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
	results, _, _, err := e.readPointsWithTask(ctx, deviceID, addrs)
	return results, err
}

// ReadRecords 与 ReadPoints 相同的锁与连接语义，但把结果转换成采集记录
// （复用 gatewayTask.toRecords：设备/点位名称、协议、物模型编码、采集组字段
// 与正常采集逐字段一致），供事件触发规则读取动作点位后直接推送。
//
// 返回的 task/dev 在锁释放后仍可安全使用：任务构建完成后这些映射是只读的，
// 采集协程本身也在无锁状态下调用 toRecords。
//
// **禁止在 collector.RecordSink / PointValueSink 等采集回调中同步调用本方法**：
// 串口设备的整轮轮询持有设备锁，回调里再读会自锁；非串口设备也可能与
// Refresh/Stop 的写锁互等。事件触发规则必须把本方法放到独立 worker 里异步调用。
func (e *Engine) ReadRecords(ctx context.Context, deviceID string, addrs []po.DeviceAddress) ([]CollectedRecord, error) {
	results, task, dev, err := e.readPointsWithTask(ctx, deviceID, addrs)
	if err != nil {
		return nil, err
	}
	if task == nil || dev == nil {
		return nil, ErrNotRunning
	}
	return task.toRecords(dev, addrs, results), nil
}

// readPointsWithTask 是 ReadPoints / ReadRecords 共用的加锁与读取段。
// 锁序与 ReadPoints 的历史实现完全一致：Engine.mu.RLock → task.mu → deviceLock。
func (e *Engine) readPointsWithTask(ctx context.Context, deviceID string, addrs []po.DeviceAddress) (
	[]driver.ReadResult, *gatewayTask, *po.Device, error,
) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.task == nil || !e.running {
		return nil, nil, nil, ErrNotRunning
	}
	task := e.task
	dev := task.findDevice(deviceID)
	if dev == nil {
		return nil, nil, nil, fmt.Errorf("%w: %s", ErrDeviceNotActive, deviceID)
	}
	drv := task.GetDriver(deviceID)
	if drv == nil {
		return nil, nil, nil, fmt.Errorf("%w: %s", ErrDeviceNotActive, deviceID)
	}
	if !drv.IsConnected() {
		return nil, nil, nil, fmt.Errorf("%w: %s", ErrDeviceOffline, dev.Name)
	}

	dm := task.deviceLock(deviceID)
	if err := dm.LockContext(ctx); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %w", ErrDeviceBusy, err)
	}
	defer dm.Unlock()
	if !drv.IsConnected() {
		return nil, nil, nil, fmt.Errorf("%w: %s", ErrDeviceOffline, dev.Name)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	results, err := drv.Read(driveradapter.FromPOs(addrs))
	if err != nil {
		return nil, nil, nil, err
	}
	return results, task, dev, nil
}
