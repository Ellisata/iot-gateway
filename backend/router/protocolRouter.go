package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// ProtocolRoutes 协议路由
func ProtocolRoutes(r *gin.Engine, pc *controller.ProtocolController) {
	protocol := r.Group("/protocol")
	{
		protocol.POST("/createProtocol", pc.CreateProtocol)
		protocol.POST("/updateProtocol", pc.UpdateProtocol)
		protocol.GET("/getProtocolById/:id", pc.GetProtocolById)
		protocol.GET("/getProtocolByName", pc.GetProtocolByName)
		protocol.GET("/getDataTypes", pc.GetProtocolDataTypes)
		protocol.POST("/updateProtocolFormByName", pc.UpdateProtocolFormByName)
		protocol.POST("/updateProtocolFormById", pc.UpdateProtocolFormById)
		protocol.GET("/deleteProtocol/:id", pc.DeleteProtocol)
		protocol.GET("/pageProtocol", pc.PageProtocol)
	}
}
