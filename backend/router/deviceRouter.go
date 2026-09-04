// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// DeviceRoutes 设备路由
func DeviceRoutes(r *gin.Engine, dc *controller.DeviceController) {
	device := r.Group("/device")
	{
		device.POST("/createDevice", dc.CreateDevice)
		device.POST("/updateDevice", dc.UpdateDevice)
		device.GET("/deleteDevice/:id", dc.DeleteDevice)
		device.GET("/getDeviceById/:id", dc.GetDeviceByID)
		device.POST("/testDeviceConnection", dc.TestDeviceConnection)
		device.GET("/pageDevice", dc.PageDevice)
		device.GET("/overview", dc.GetDeviceOverview)
		device.POST("/importDevice", dc.ImportDevice)
		device.GET("/importTemplate", dc.ImportTemplate)
	}
}
