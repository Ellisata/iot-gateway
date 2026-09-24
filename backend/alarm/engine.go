// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package alarm

import (
	"gorm.io/gorm"
)

// Engine 设备断联报警引擎。
//
// 实现 collector.DeviceStateSink：采集引擎每轮对单台设备轮询结束调用
// ReportDevicePoll，本引擎将信号转发给共享 Tracker（targetType=device）
// 完成状态机判定与落库。逻辑见 Tracker。
type Engine struct {
	tr *Tracker
}

// NewEngine 创建设备断联报警引擎（默认连续失败 3 次判离线）。
// sink 为报警通知出口（推送钉钉/企微/飞书等），传 nil 表示只落库不通知。
func NewEngine(db *gorm.DB, sink NotifySink) *Engine {
	return &Engine{tr: NewTracker(db, TypeDevice, "设备", sink)}
}

// ReportDevicePoll 实现 collector.DeviceStateSink。
// 由采集协程同步调用，内部持锁快速完成状态迁移与落库，不阻塞采集。
func (e *Engine) ReportDevicePoll(deviceID, deviceName string, ok bool) {
	e.tr.Report(deviceID, deviceName, ok)
}
