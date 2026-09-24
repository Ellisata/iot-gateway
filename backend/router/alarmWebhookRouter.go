// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// AlarmWebhookRoutes 报警 Webhook 通知路由
func AlarmWebhookRoutes(r *gin.Engine, awc *controller.AlarmWebhookController) {
	webhook := r.Group("/alarmWebhook")
	{
		webhook.POST("/createAlarmWebhook", awc.CreateAlarmWebhook)
		webhook.POST("/updateAlarmWebhook", awc.UpdateAlarmWebhook)
		webhook.GET("/getAlarmWebhookById/:id", awc.GetAlarmWebhookById)
		webhook.GET("/deleteAlarmWebhook/:id", awc.DeleteAlarmWebhook)
		webhook.GET("/pageAlarmWebhook", awc.PageAlarmWebhook)
		webhook.POST("/testSend", awc.TestSendAlarmWebhook)
		webhook.GET("/status", awc.StatusAlarmWebhook)
		webhook.POST("/refresh", awc.RefreshAlarmWebhook)
		webhook.GET("/listTypes", awc.ListAlarmWebhookTypes)
	}
}

// AlarmWebhookFormRoutes 报警 Webhook 表单路由
func AlarmWebhookFormRoutes(r *gin.Engine, awfc *controller.AlarmWebhookFormController) {
	form := r.Group("/alarmWebhookForm")
	{
		form.POST("/createAlarmWebhookForm", awfc.CreateAlarmWebhookForm)
		form.POST("/updateAlarmWebhookForm", awfc.UpdateAlarmWebhookForm)
		form.GET("/getAlarmWebhookFormByName", awfc.GetAlarmWebhookFormByName)
	}
}
