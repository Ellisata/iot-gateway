// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// CollectorGroupVO 采集组响应。
type CollectorGroupVO struct {
	ID          string `json:"id"`
	DeviceID    string `json:"deviceId"`
	DeviceName  string `json:"deviceName"` // 展示用冗余字段
	Name        string `json:"name"`
	IntervalMs  int    `json:"intervalMs"`
	Description string `json:"description"`
	Status      int    `json:"status"` // 1 启用 / 0 停用
	// PointCount 绑定到本组的点位数（含停用点位），删除守卫与列表页共用。
	PointCount int    `json:"pointCount"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// BatchAssignCollectorGroupResultVO 批量分配采集组的结果。
//
// Assigned 由实际更新行数得出，可能小于 Requested：勾选后被删掉或不属于
// 该设备的点位不会被更新，这种情况是正常的（与批量删除语义一致）。
type BatchAssignCollectorGroupResultVO struct {
	Requested int   `json:"requested"`
	Assigned  int64 `json:"assigned"`
}
