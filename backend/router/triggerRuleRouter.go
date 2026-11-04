// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// TriggerRuleRoutes 事件触发采集规则路由
func TriggerRuleRoutes(r gin.IRouter, trc *controller.TriggerRuleController) {
	group := r.Group("/triggerRule")
	{
		group.POST("/createTriggerRule", trc.CreateTriggerRule)
		group.POST("/updateTriggerRule", trc.UpdateTriggerRule)
		group.GET("/deleteTriggerRule/:id", trc.DeleteTriggerRule)
		group.GET("/getTriggerRuleById/:id", trc.GetTriggerRuleByID)
		group.GET("/pageTriggerRule", trc.PageTriggerRule)
		group.GET("/pageTriggerRuleRecord", trc.PageTriggerRuleRecord)
		group.GET("/recentTriggers", trc.RecentTriggers)
	}
}
