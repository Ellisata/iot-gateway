// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// MqttSubscriptionRoutes MQTT 订阅管理路由
func MqttSubscriptionRoutes(r gin.IRouter, mc *controller.MqttSubscriptionController) {
	sub := r.Group("/mqttSubscription")
	{
		sub.POST("/createMqttSubscription", mc.CreateMqttSubscription)
		sub.POST("/updateMqttSubscription", mc.UpdateMqttSubscription)
		sub.GET("/getMqttSubscriptionById/:id", mc.GetMqttSubscriptionById)
		sub.GET("/deleteMqttSubscription/:id", mc.DeleteMqttSubscription)
		sub.GET("/pageMqttSubscription", mc.PageMqttSubscription)
		sub.GET("/pageMqttSubscriptionDevicePoint", mc.PageMqttSubscriptionDevicePoint)
		sub.POST("/createMqttSubscriptionDevicePoint", mc.CreateMqttSubscriptionDevicePoint)
		sub.POST("/updateMqttSubscriptionDevicePoint", mc.UpdateMqttSubscriptionDevicePoint)
		sub.GET("/deleteMqttSubscriptionDevicePoint", mc.DeleteMqttSubscriptionDevicePoint)
		sub.POST("/restoreMqttSubscriptionDevicePointAuto", mc.RestoreMqttSubscriptionDevicePointAuto)
		sub.POST("/batchDeleteMqttSubscriptionDevicePoint", mc.BatchDeleteMqttSubscriptionDevicePoint)
		sub.POST("/importMqttSubscriptionDevicePoint", mc.ImportMqttSubscriptionDevicePoint)
		sub.GET("/importMqttSubscriptionDevicePointTemplate", mc.ImportMqttSubscriptionDevicePointTemplate)
		sub.POST("/testConnectivity", mc.TestMqttSubscription)
		sub.POST("/preview", mc.PreviewMqttSubscription)
		sub.GET("/status", mc.MqttSubscriptionStatus)
		sub.GET("/recentMessages", mc.MqttSubscriptionRecentMessages)
	}
}
