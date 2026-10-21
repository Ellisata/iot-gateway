// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// SystemConfigRoutes 运行期配置开关路由。
//
// 独立于 /gateway：那是网关身份（名称 / 描述），这是进程级行为开关，
// 两者失败语义完全不同（一个改库里的展示信息，一个可能启停局域网套接字）。
func SystemConfigRoutes(r gin.IRouter, scc *controller.SystemConfigController) {
	g := r.Group("/systemConfig")
	{
		g.GET("/list", scc.List)
		g.POST("/update", scc.Update)
		g.POST("/reset", scc.Reset)
	}
}
