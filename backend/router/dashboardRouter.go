// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// DashboardRoutes 大盘聚合数据路由。
func DashboardRoutes(r gin.IRouter, dc *controller.DashboardController) {
	dashboard := r.Group("/dashboard")
	{
		dashboard.GET("/trend", dc.GetTrend)
	}
}
