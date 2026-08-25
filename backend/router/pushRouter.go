package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// PushRoutes 数据推送路由
func PushRoutes(r *gin.Engine, pc *controller.PushController) {
	group := r.Group("/push")
	{
		group.GET("/status", pc.Status)
		group.POST("/refresh", pc.Refresh)
	}
}
