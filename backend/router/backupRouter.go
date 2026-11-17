// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// BackupRoutes 数据备份与恢复路由（管理员 JWT）。
//
// 刻意只挂在管理端：恢复是覆盖配置的高风险动作，不能出现在 /openApi
// （X-Api-Key 可调）或 /agentApi（免鉴权）上。
func BackupRoutes(r gin.IRouter, bc *controller.BackupController) {
	g := r.Group("/backup")
	{
		g.GET("/list", bc.List)
		g.GET("/status", bc.Status)
		g.POST("/create", bc.Create)
		g.GET("/download", bc.Download)
		g.DELETE("/delete", bc.Delete)
		// 恢复只做暂存，重启后由启动流程应用；没有“立即恢复”接口。
		g.POST("/stage", bc.Stage)
		g.POST("/upload", bc.Upload)
		g.POST("/cancelPending", bc.CancelPending)
	}
}
