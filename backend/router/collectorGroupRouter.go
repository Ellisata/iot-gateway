// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// CollectorGroupRoutes 采集组路由
func CollectorGroupRoutes(r gin.IRouter, cgc *controller.CollectorGroupController) {
	group := r.Group("/collectorGroup")
	{
		group.POST("/createCollectorGroup", cgc.CreateCollectorGroup)
		group.POST("/updateCollectorGroup", cgc.UpdateCollectorGroup)
		group.GET("/deleteCollectorGroup/:id", cgc.DeleteCollectorGroup)
		group.GET("/getCollectorGroupById/:id", cgc.GetCollectorGroupByID)
		group.GET("/pageCollectorGroup", cgc.PageCollectorGroup)
		group.GET("/listCollectorGroups", cgc.ListCollectorGroups)
	}
}
