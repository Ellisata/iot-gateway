// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// PluginRoutes 协议插件安装路由
func PluginRoutes(r gin.IRouter, pc *controller.PluginController) {
	plugin := r.Group("/plugin")
	{
		plugin.GET("/info", pc.PluginInfo)
		plugin.POST("/importPlugin", pc.ImportPlugin)
	}
}
