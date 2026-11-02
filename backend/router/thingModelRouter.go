// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// ThingModelRoutes 设备物模型路由
func ThingModelRoutes(r gin.IRouter, tc *controller.ThingModelController) {
	g := r.Group("/thingModel")
	{
		g.POST("/createThingModel", tc.CreateThingModel)
		g.POST("/updateThingModel", tc.UpdateThingModel)
		g.GET("/deleteThingModel/:id", tc.DeleteThingModel)
		g.GET("/getThingModelById/:id", tc.GetThingModelByID)
		g.GET("/pageThingModel", tc.PageThingModel)
		g.GET("/listThingModels", tc.ListThingModels)

		g.GET("/listThingModelProperties", tc.ListThingModelProperties)
		g.GET("/pageThingModelProperties", tc.PageThingModelProperties)
		g.POST("/createThingModelProperty", tc.CreateThingModelProperty)
		g.POST("/updateThingModelProperty", tc.UpdateThingModelProperty)
		g.GET("/deleteThingModelProperty/:id", tc.DeleteThingModelProperty)
	}
}
