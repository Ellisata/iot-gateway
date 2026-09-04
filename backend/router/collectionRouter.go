// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// CollectionRoutes 采集控制路由
func CollectionRoutes(r *gin.Engine, cc *controller.CollectionController) {
	col := r.Group("/collection")
	{
		col.POST("/start", cc.Start)
		col.POST("/stop", cc.Stop)
		col.POST("/refresh", cc.Refresh)
		col.GET("/status", cc.Status)
	}
}
