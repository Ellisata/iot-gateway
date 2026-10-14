// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// RealtimeRoutes 设备实时数据路由
func RealtimeRoutes(r gin.IRouter, rc *controller.RealtimeController) {
	rt := r.Group("/realtime")
	{
		rt.GET("/snapshot", rc.Snapshot)
	}
}
