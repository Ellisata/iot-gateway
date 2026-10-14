// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// ProtocolRoutes 协议路由
func ProtocolRoutes(r gin.IRouter, pc *controller.ProtocolController) {
	protocol := r.Group("/protocol")
	{
		// 没有 createProtocol / deleteProtocol：协议行全部由插件供给
		// （见 pluginhost/provision.go），手工建的行没有驱动与之对应，
		// 而删掉的行会在插件下次加载时被重新插回 —— 两个操作都只有反效果。
		// 替代品是 resetProtocolForm：把表单恢复成插件当前声明的那一份。
		protocol.POST("/updateProtocol", pc.UpdateProtocol)
		protocol.GET("/getProtocolById/:id", pc.GetProtocolById)
		protocol.GET("/getProtocolByName", pc.GetProtocolByName)
		protocol.GET("/getDataTypes", pc.GetProtocolDataTypes)
		protocol.POST("/updateProtocolFormByName", pc.UpdateProtocolFormByName)
		protocol.POST("/updateProtocolFormById", pc.UpdateProtocolFormById)
		protocol.POST("/resetProtocolForm/:id", pc.ResetProtocolForm)
		protocol.GET("/pageProtocol", pc.PageProtocol)
	}
}
