// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// OpenApiSecretRoutes 开放接口密钥路由
func OpenApiSecretRoutes(r *gin.Engine, oasc *controller.OpenApiSecretController) {
	openApiSecret := r.Group("/openApiSecret")
	{
		// 开放接口说明文档（管理端页面展示用，走全局 JWT 认证）
		openApiSecret.GET("/doc", oasc.GetOpenApiDoc)
		openApiSecret.POST("/create", oasc.CreateOpenApiSecret)
		openApiSecret.POST("/updateName", oasc.UpdateOpenApiSecretName)
		openApiSecret.GET("/list", oasc.ListOpenApiSecret)
		openApiSecret.GET("/delete/:id", oasc.DeleteOpenApiSecret)
	}
}
