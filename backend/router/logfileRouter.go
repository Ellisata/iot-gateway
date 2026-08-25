package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
	"iot-gateway/middleware"
)

// LogFileRoutes 日志查看路由。
// 日志含现场设备数据，全部需要 JWT 认证（同 userRouter 的鉴权写法）。
func LogFileRoutes(r *gin.Engine, lc *controller.LogFileController) {
	authed := r.Group("", middleware.AuthMiddleware())
	{
		authed.GET("/log/listFiles", lc.ListFiles)
		authed.GET("/log/readFile", lc.ReadFile)
	}
}
