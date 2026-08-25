package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"iot-gateway/enums"
	"iot-gateway/response"
	"iot-gateway/utils"
)

// AuthMiddleware JWT 认证中间件
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
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
		//ctx := c.Request.Context()
		ctx = utils.SetUserIdToCtx(ctx, userId)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
