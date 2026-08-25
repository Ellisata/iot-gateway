package middleware

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/utils"
)

// AreaMiddleware 区域设置中间件
func AreaMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		area := c.GetHeader("Area")
		if area == "" {
			area = "zh"
		}

		// 注入到 Gin Context
		c.Set("area", area)

		// 注入到 Go Context
		ctx := c.Request.Context()
		ctx = utils.SetAreaToCtx(ctx, area)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
