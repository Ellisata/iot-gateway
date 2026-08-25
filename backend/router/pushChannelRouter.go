package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// PushChannelRoutes 数据推送通道路由
func PushChannelRoutes(r *gin.Engine, pc *controller.PushChannelController) {
	channel := r.Group("/pushChannel")
	{
		channel.POST("/createPushChannel", pc.CreatePushChannel)
		channel.POST("/updatePushChannel", pc.UpdatePushChannel)
		channel.GET("/getPushChannelById/:id", pc.GetPushChannelById)
		channel.GET("/deletePushChannel/:id", pc.DeletePushChannel)
		channel.GET("/pagePushChannel", pc.PagePushChannel)
		channel.POST("/testConnectivity", pc.TestPushChannel)
	}
}
