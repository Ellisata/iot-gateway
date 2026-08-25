package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"iot-gateway/response"
)

// ClientSecretMiddleware 客户端密钥校验中间件
func ClientSecretMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		clientSecret := c.GetHeader("Client-Secret")
		if clientSecret == "" {
			c.JSON(http.StatusUnauthorized, response.Fail("AUTH_FAILED", "missing client secret"))
			c.Abort()
			return
		}

		// TODO: 校验 clientSecret
		// 注入到 context
		c.Set("clientSecret", clientSecret)
		ctx := c.Request.Context()
		// ctx = context.WithValue(ctx, "clientSecret", clientSecret) // 由 contextUtil 处理
		_ = ctx

		c.Next()
	}
}
