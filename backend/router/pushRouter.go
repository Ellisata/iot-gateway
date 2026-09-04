// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// PushRoutes 数据推送路由
func PushRoutes(r *gin.Engine, pc *controller.PushController) {
	group := r.Group("/push")
	{
		group.GET("/status", pc.Status)
		group.POST("/refresh", pc.Refresh)
	}
}
