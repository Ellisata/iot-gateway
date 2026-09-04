// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"iot-gateway/enums"
	"iot-gateway/response"
	"iot-gateway/service"
)

// OpenApiKeyHeader 开放接口密钥请求头，外部系统调用 /openApi/* 时携带
const OpenApiKeyHeader = "X-Api-Key"

// OpenApiAuthMiddleware 开放接口密钥校验中间件。
// 外部系统通过 X-Api-Key 请求头携带密钥，校验其在 open_api_secret 表中的
// 存在性与正确性，校验通过方可继续调用后续接口。
func OpenApiAuthMiddleware(secretSvc *service.OpenApiSecretService) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader(OpenApiKeyHeader)
		if key == "" {
			c.JSON(http.StatusUnauthorized, response.Fail(
				enums.OpenApiSecretKeyEmptyEnum.GetCode(),
				enums.OpenApiSecretKeyEmptyEnum.GetMessageCtx(c.Request.Context()),
			))
			c.Abort()
			return
		}

		exists, err := secretSvc.ExistsOpenApiSecretKey(c.Request.Context(), key)
		if err != nil || !exists {
			// 存在性或正确性任一不满足，统一返回无效，避免泄露密钥是否存在
			c.JSON(http.StatusUnauthorized, response.Fail(
				enums.OpenApiSecretInvalidEnum.GetCode(),
				enums.OpenApiSecretInvalidEnum.GetMessageCtx(c.Request.Context()),
			))
			c.Abort()
			return
		}

		c.Next()
	}
}
