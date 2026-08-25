package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// PushChannelFormRoutes 数据推送通道表单路由
func PushChannelFormRoutes(r *gin.Engine, pcf *controller.PushChannelFormController) {
	form := r.Group("/pushChannelForm")
	{
		form.POST("/createPushChannelForm", pcf.CreatePushChannelForm)
		form.POST("/updatePushChannelForm", pcf.UpdatePushChannelForm)
		form.GET("/getPushChannelFormByName", pcf.GetPushChannelFormByName)
	}
}
