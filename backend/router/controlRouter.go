// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// ControlRoutes 控制下发路由。
//
// write 是**高风险管理动作**：无论前端是否二次确认，服务端都必须独立校验
// 总开关、协议能力与点位 rw_permission（见 service.ControlService）。
func ControlRoutes(r gin.IRouter, cc *controller.ControlController) {
	g := r.Group("/control")
	{
		// 能力查询：前端据此决定是否显示写操作入口。
		g.GET("/capability", cc.Capability)
		// 执行下发。只接受点位 id，不接受原始地址。
		g.POST("/write", cc.Write)
		// 审计查询（只读）。
		g.GET("/pageControlLog", cc.PageLogs)
	}
}
