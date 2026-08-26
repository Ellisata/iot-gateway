package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// DeviceAddressRoutes 设备地址路由
func DeviceAddressRoutes(r *gin.Engine, ac *controller.DeviceAddressController) {
	addr := r.Group("/deviceAddress")
	{
		addr.POST("/createDeviceAddress", ac.CreateDeviceAddress)
		addr.POST("/updateDeviceAddress", ac.UpdateDeviceAddress)
		addr.GET("/deleteDeviceAddress/:id", ac.DeleteDeviceAddress)
		addr.GET("/getDeviceAddressById/:id", ac.GetDeviceAddressByID)
		addr.GET("/pageDeviceAddress", ac.PageDeviceAddress)
		addr.POST("/importDeviceAddress", ac.ImportDeviceAddress)
		addr.GET("/importDeviceAddressTemplate", ac.ImportDeviceAddressTemplate)
	}
}
