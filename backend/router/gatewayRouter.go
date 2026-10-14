// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// GatewayRoutes 网关自身身份路由。
//
// 走全局 JWT 认证（authMiddleware.go 的 publicPaths 只白名单 /health、/user/login
// 与 /admin、/openApi/ 两个前缀，不包含 /gateway），因此这里不做额外处理。
func GatewayRoutes(r gin.IRouter, gc *controller.GatewayController) {
	gateway := r.Group("/gateway")
	{
		gateway.GET("/get", gc.GetGateway)
		gateway.POST("/update", gc.UpdateGateway)
	}
}
