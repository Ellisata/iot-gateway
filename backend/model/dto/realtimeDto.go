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
}
