package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// AlarmRoutes 设备断联报警路由
func AlarmRoutes(r *gin.Engine, ac *controller.AlarmController) {
	alarm := r.Group("/alarm")
	{
		alarm.GET("/pageAlarm", ac.PageAlarm)
		alarm.GET("/listActive", ac.ListActiveAlarm)
	}
}
