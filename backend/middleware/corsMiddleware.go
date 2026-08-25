package middleware

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// CorsMiddleware 返回配置好的 CORS 中间件
func CorsMiddleware() gin.HandlerFunc {
	return cors.New(cors.Config{
		// 允许所有前端来源（开发环境）
		// 生产环境可限制为具体域名
		AllowAllOrigins: true,
		// 允许的 HTTP 方法
		AllowMethods: []string{
			"GET",
			"POST",
			"PUT",
			"PATCH",
			"DELETE",
			"OPTIONS",
		},
		// 允许的请求头
		AllowHeaders: []string{
			"Origin",
			"Content-Type",
			"Accept",
			"Authorization",
			"X-Requested-With",
			"X-Access-Token",
			"Area",
		},
		// 允许浏览器携带认证信息（Cookie、Authorization 头等）
		AllowCredentials: true,
		// 预检请求缓存时间（秒），减少 OPTIONS 请求次数
		MaxAge: 12 * time.Hour,
	})
}
