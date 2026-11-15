// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// RealtimeSnapshotDTO 设备实时数据快照查询。
//
// 服务端分页而非一次返回全部点位：单台设备的点位可达数千个，
// 秒级轮询下全量响应的体积会把网关带宽与前端解析都拖垮。
type RealtimeSnapshotDTO struct {
	Page     int    `form:"page" binding:"required,min=1"`
	Size     int    `form:"size" binding:"required,min=1,max=100"`
	DeviceID string `form:"deviceId" binding:"required"`
	Name     string `form:"name"` // 点位名或说明（模糊查询）
	// AddressIDs 可选：英文逗号分隔的点位 ID。给了就只返回这些点位（page/size 仍需
	// 携带，但被忽略），供「点位配置」页按当前页的十几行精确取实时值。查询仍限定在
	// DeviceID 之下，别台设备的点位 ID 不会因为被人拼进来而泄漏。
	AddressIDs string `form:"addressIds"`
}

// OpenApiRealtimeSnapshotDTO 开放接口的设备实时数据快照查询。
//
// 与管理端接口的区别：外部系统通常只持有设备编码 code（下发自然键），
// 不一定知道网关内部 UUID，因此 deviceId 与 code 二选一，优先 deviceId。
// 点位分页/搜索/AddressIDs 语义与 RealtimeSnapshotDTO 一致。
type OpenApiRealtimeSnapshotDTO struct {
	Page     int    `form:"page" binding:"required,min=1"`
	Size     int    `form:"size" binding:"required,min=1,max=100"`
	DeviceID string `form:"deviceId"`
	Code     string `form:"code"`
	Name     string `form:"name"`
	// AddressIDs 可选：仅用于按当前页精确取值，仍限定在解析出的设备之下。
	AddressIDs string `form:"addressIds"`
}
