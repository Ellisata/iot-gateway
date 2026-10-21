// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// DashboardTrendDTO 大盘趋势查询参数。
// Hours 为空或越界时由服务端收敛到 6 / 1..24，前端不需要重复校验。
type DashboardTrendDTO struct {
	Hours int `form:"hours" json:"hours"`
}
