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

// agentApiPrefix 为 dsh（AI Agent）调用面前缀，**整体免鉴权**。
//
// 与另两条免认证路径的性质不同，务必分清：
//   - /admin/* 免认证是因为它只是静态资源（登录页本身要能打开）；
//   - /openApi/* 免 JWT，但**仍要 X-Api-Key**（走 openApiAuthMiddleware），
//     在此被放行只是"不走 JWT"，不代表性校验被跳过；
//   - /agentApi/* 是**真的没有任何凭据校验** —— 任何能访问到网关端口的主机都能
//     调用，包括删除设备、上传并加载插件（可执行文件）、修改推送配置等写操作。
//
// 这是刻意的设计选择（前提：隔离内网，或 dsh 与网关同机），不是遗漏。
// 取舍与完整暴露清单见 router/agentApiRouter.go 的文件头注释。
//
// **要收回这个口子，删掉本常量与下面那一行 HasPrefix 即可** —— 无需改动任何路由注册。
//
// 写成常量而非字面量：router 包已用同一前缀建组（router.AgentApiPrefix），
// 两处必须一致，钉在这一个值上。
const agentApiPrefix = "/agentApi"

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
	// /agentApi/* 免鉴权放行，理由与风险见 agentApiPrefix 的注释
	if path == agentApiPrefix || strings.HasPrefix(path, agentApiPrefix+"/") {
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
		userId, username, err := utils.GetUserFromToken(tokenStr)
		if err != nil {
			c.JSON(http.StatusUnauthorized, response.Fail(enums.TokenInvalidErr.GetCode(), enums.TokenInvalidErr.GetMessageCtx(ctx)))
			c.Abort()
			return
		}

		// 注入 userId / username 到 Gin Context
		c.Set("userId", userId)
		c.Set("username", username)

		// 同时注入到 Go Context
		ctx = utils.SetUserIdToCtx(ctx, userId)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
