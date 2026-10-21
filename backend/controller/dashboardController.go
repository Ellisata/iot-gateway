// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/model/dto"
	"iot-gateway/response"
	"iot-gateway/service"
)

// DashboardController 大盘聚合数据控制器。
type DashboardController struct {
	dashboardService *service.DashboardService
}

// NewDashboardController 创建大盘控制器。
func NewDashboardController(dashboardService *service.DashboardService) *DashboardController {
	return &DashboardController{dashboardService: dashboardService}
}

// GetTrend 获取在线率与采集吞吐趋势。
// hours 默认 6，越界时收敛到 1..24；非法值不报错，避免前端因为一个参数丢掉整张图。
func (ctr *DashboardController) GetTrend(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.DashboardTrendDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if req.Hours == 0 {
		req.Hours = 6
	}
	if req.Hours < 1 {
		req.Hours = 1
	}
	if req.Hours > 24 {
		req.Hours = 24
	}
	c.JSON(200, response.Success(ctr.dashboardService.GetTrend(req.Hours)))
}
