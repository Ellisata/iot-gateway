// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// UserRoutes 用户路由。
// 仅登录接口免认证，其余路由由 SetupRouter 全局 AuthMiddleware 统一认证。
func UserRoutes(r *gin.Engine, uc *controller.UserController) {
	// 公共路由
	r.POST("/user/login", uc.Login)

	// 需认证路由（全局 AuthMiddleware 覆盖）
	r.POST("/createUser", uc.CreateUser)
	r.GET("/users/:id", uc.GetUser)
	r.PUT("/users/:id", uc.UpdateUser)
	r.GET("/users", uc.PageUser)
	r.GET("/profile", uc.GetProfile)
	r.PUT("/user/password", uc.ChangePassword)
}
