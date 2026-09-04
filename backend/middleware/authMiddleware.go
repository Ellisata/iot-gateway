// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"iot-gateway/enums"
	"iot-gateway/response"
	"iot-gateway/utils"
)

// publicPaths 免认证路径（精确匹配）：除登录外，其余 API 均需 JWT 认证。
// /health 供健康检查；/admin 为内嵌前端静态资源入口（登录页本身无需认证）。
var publicPaths = map[string]bool{
	"/health":     true,
	"/user/login": true,
}

// isPublicPath 判断请求路径是否免认证（含 /admin 前缀的前端静态资源）。
// 内嵌 Vue 前端的 index.html 与 assets/* 由浏览器直接加载，不会携带 token；
// /openApi/* 走独立的 X-Api-Key 密钥校验（openApiAuthMiddleware），同样不走 JWT。
func isPublicPath(path string) bool {
	if publicPaths[path] {
		return true
	}
	if path == "/admin" || strings.HasPrefix(path, "/admin/") {
		return true
	}
	return strings.HasPrefix(path, "/openApi/")
}

// AuthMiddleware JWT 认证中间件。由 SetupRouter 全局挂载，
// 除免认证路径（登录、健康检查、前端静态资源）外统一校验 token。
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 免认证路径直接放行
		if isPublicPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, response.Fail(enums.TokenEmptyErr.GetCode(), enums.TokenEmptyErr.GetMessageCtx(ctx)))
			c.Abort()
			return
		}

		// Bearer token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, response.Fail(enums.TokenEmptyErr.GetCode(), enums.TokenEmptyErr.GetMessageCtx(ctx)))
			c.Abort()
			return
		}

		tokenStr := parts[1]
		userId, err := utils.GetUserIdFromToken(tokenStr)
		if err != nil {
			c.JSON(http.StatusUnauthorized, response.Fail(enums.TokenInvalidErr.GetCode(), enums.TokenInvalidErr.GetMessageCtx(ctx)))
			c.Abort()
			return
		}

		// 注入 userId 到 Gin Context
		c.Set("userId", userId)

		// 同时注入到 Go Context
		ctx = utils.SetUserIdToCtx(ctx, userId)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
