// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// LogFileRoutes 日志查看路由。
// 日志含现场设备数据，全部需要 JWT 认证（由 SetupRouter 全局 AuthMiddleware 覆盖）。
func LogFileRoutes(r *gin.Engine, lc *controller.LogFileController) {
	log := r.Group("/log")
	{
		log.GET("/listFiles", lc.ListFiles)
		log.GET("/readFile", lc.ReadFile)
	}

}
