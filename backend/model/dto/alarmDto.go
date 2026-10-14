// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// ==================== 报警 DTO ====================

// PageAlarmDTO 报警历史分页查询（目标名 / 类型 / 状态过滤）
type PageAlarmDTO struct {
	Page       int    `form:"page" binding:"required,min=1"`
	Size       int    `form:"size" binding:"required,min=1,max=100"`
	TargetName string `form:"targetName"` // 目标名称（模糊查询）
	TargetType string `form:"targetType"` // device | channel | point | point_threshold，空查全部
	AlarmType  string `form:"alarmType"`  // offline | quality | threshold | recover
	Status     string `form:"status"`     // active | cleared
}

// PageThresholdRecordDTO 越限记录分页查询（越限样本明细，见 po.PointThresholdRecord）。
type PageThresholdRecordDTO struct {
	Page     int    `form:"page" binding:"required,min=1"`
	Size     int    `form:"size" binding:"required,min=1,max=100"`
	DeviceID string `form:"deviceId"` // 所属设备，空 = 全部
	Name     string `form:"name"`     // 点位名称（模糊查询）
}

// ==================== 可靠性统计 DTO ====================
//
// 两个端点的窗口参数相同：from/to 是**日期**（YYYY-MM-DD），两端都含当天。
//
// **刻意必填、不给默认值**：一是让调用方对齐自己的统计周期，二是避免服务端悄悄
// 替它选一个窗口 —— 统计口径藏在默认值里，报表对不上时最难查。
// 上限（见 service 层的 maxReliabilityWindowDays）是为了护住那唯一的 SQLite 连接：
// alarm 表**永不清理**，不设上限的一次全表聚合会把采集协程卡住。

// DeviceReliabilityDTO 设备维度可靠性统计。
type DeviceReliabilityDTO struct {
	From string `form:"from" binding:"required"` // YYYY-MM-DD，含当天起点
	To   string `form:"to" binding:"required"`   // YYYY-MM-DD，含当天终点
	// Limit 按可用率升序只取前 N 名（1 = 最差那台）。
	//
	// **省略或 <=0 = 不截断**，与 PointAbnormalDTO 的默认值语义**刻意相反**：
	// 设备是天然有限的维度（比点位少 1~2 个数量级），而截断后的设备列表很容易被
	// 误读成"全网设备清单"—— 一个看着完整实则残缺的答案比长响应危险得多。
	// 想按别的字段（故障段数 / MTTR）在客户端重排的调用方，不传它即可拿到全量。
	Limit int `form:"limit" binding:"omitempty,min=1,max=500"`
}

// PointAbnormalDTO 点位维度异常 top-N。
//
// deviceId 与 limit 都是可选的，且可以一起用：
//
//	都不传      全厂点位按异常段数排序
//	只传 deviceId  那台设备下的全部点位（此时 limit 可用来截断）
//	只传 limit     全厂前 N（limit 省略时服务端默认一个值）
type PointAbnormalDTO struct {
	DeviceID string `form:"deviceId"` // 空 = 全部设备
	From     string `form:"from" binding:"required"`
	To       string `form:"to" binding:"required"`
	// Limit 省略或 <=0 时服务端兜底（见 defaultPointAbnormalLimit），上限见 maxPointAbnormalLimit。
	Limit int `form:"limit" binding:"omitempty,min=1,max=500"`
}
