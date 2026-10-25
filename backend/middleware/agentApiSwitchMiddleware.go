// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"iot-gateway/enums"
	"iot-gateway/response"
	"iot-gateway/runtimecfg"
)

// AgentApiSwitchMiddleware 按运行期开关 agentApi.enabled 放行 /agentApi 分组。
//
// 为什么靠中间件而不是"关闭时不挂载路由"：gin 的路由表在启动后不可增删，
// 而本开关要求**运行期**切换。故路由始终注册，每次请求现读开关
// （runtimecfg.Manager 读的是原子快照，无锁、无 IO），关闭时直接 403。
//
// nil reader 一律按"关闭"处理：接线漏了时宁可整组不可达，也不能让无鉴权的
// 写接口在没人打算打开的情况下可达（fail closed）。
//
// 与 AuthMiddleware 的分工：/agentApi 仍在 isPublicPath 里免 JWT，所以每个
// 打到分组的请求都会先经过这里。关闭时的 403（功能没开）与缺 token 的 401
// （你没登录）是两件事，不要合并 —— dsh 看到后者会去要凭据，而凭据在这里没有用。
func AgentApiSwitchMiddleware(cfg runtimecfg.Reader) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cfg == nil || !cfg.Bool(runtimecfg.KeyAgentApiEnabled) {
			c.AbortWithStatusJSON(http.StatusForbidden, response.Fail(
				enums.AgentApiDisabledEnum.GetCode(),
				enums.AgentApiDisabledEnum.GetMessageCtx(c.Request.Context()),
			))
			return
		}
		c.Next()
	}
}
