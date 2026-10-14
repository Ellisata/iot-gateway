// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package alarm

import (
	"gorm.io/gorm"

	"iot-gateway/driver"
)

// 质量码文案（进报警内容，如「点位 1#炉.温度 采集质量异常（地址无效）」）。
// 只覆盖会触发点位报警的那两个子码，见 PointEngine 的策略说明。
const (
	causeTextAddress = "地址无效"
	causeTextParse   = "解析失败"
)

// PointEngine 点位采集质量报警引擎。
//
// 实现 collector.PointStateSink：采集引擎每轮把**已定性**的点位质量交给
// ReportPointQuality，本引擎转发给共享 Tracker（PointSpec）完成状态机判定与落库。
// 状态机（连续失败去抖 / 边沿触发 / 节流 / 落库 / 通知）全在 Tracker，见 tracker.go。
//
// # 判定策略（本引擎唯一的判断，其余都交给状态机）
//
// 只有「设备是好的，这个点位本身有问题」的质量码才计入失败：
//
//	QualityBadAddress  地址无效：本地就没法把它放进计划（写法解析不出、类型/长度放不进
//	                   单请求、或对不上 startAddress），或设备已确认它不存在。永不自我恢复。
//	QualityBadParse    设备应答了但本地解码失败（类型不符、字节数不足）。同样永不自我恢复。
//
// 其余一律**不构成证据**，既不报成功也不报失败 —— 不调用 Report 就是"中性"，
// 这是状态机天然支持的语义：
//
//	QualityBadComms    整段被设备以可恢复状态拒绝。按定义是瞬时的，且**成组出现**
//	                   （整段被拒时该段每个点位一起拿到它）—— 计入失败会让一次
//	                   区间拒绝同时点亮上百个点位报警。
//	QualityBad         坏，未归类：认不出来的设备状态码（不猜），以及**预算耗尽被延迟**
//	                   的点位。后者每轮都在同一处被延迟，计入失败就是恒定误报。
//	QualityUncertain   ABB 的"不确定"（信号被勾了仿真，值可用但标注质量），不是故障。
//
// 中性还有一个免费的好处：设备离线时 drv.Read 直接返 error、一条记录都不产生，
// 于是点位压根不会被上报 —— 状态机原地冻结，既不新报也不误恢复。
// 「与设备离线去重」不需要任何额外代码。
type PointEngine struct {
	tr *Tracker
}

// NewPointEngine 创建点位质量报警引擎（默认连续失败 DefaultConsecutiveFailures 次判异常）。
// sink 为报警通知出口，传 nil 表示只落库不通知。
func NewPointEngine(db *gorm.DB, sink NotifySink) *PointEngine {
	return &PointEngine{tr: NewTracker(db, PointSpec, sink)}
}

// ReportPointQuality 实现 collector.PointStateSink。
// 由采集协程同步调用（每点位每轮一次，海量点位下即热点路径），内部持锁快速完成。
//
// pointID 为 device_address.id（全局唯一，状态机的键，也是报警行的 target_id）；
// pointName 为可读名（调用方已给的「设备名.点位名」）；deviceID 记进报警行供
// 清理时反查（见 po.Alarm.DeviceID）。
//
// 质量码不构成证据时本方法直接返回，不产生任何上报 —— 见上面「判定策略」。
func (e *PointEngine) ReportPointQuality(deviceID, deviceName, pointID, pointName string, quality int) {
	switch quality {
	case driver.QualityGood:
		e.tr.ReportQuality(pointID, deviceName+"."+pointName, "", deviceID, true)
	case driver.QualityBadAddress:
		e.tr.ReportQuality(pointID, deviceName+"."+pointName, causeTextAddress, deviceID, false)
	case driver.QualityBadParse:
		e.tr.ReportQuality(pointID, deviceName+"."+pointName, causeTextParse, deviceID, false)
	}
}
