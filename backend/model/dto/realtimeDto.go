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
