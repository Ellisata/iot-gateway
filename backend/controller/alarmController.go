// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/model/dto"
	"iot-gateway/response"
	"iot-gateway/service"
)

// AlarmController 设备断联报警控制器
type AlarmController struct {
	alarmService *service.AlarmService
}

// NewAlarmController 创建报警控制器
func NewAlarmController(alarmService *service.AlarmService) *AlarmController {
	return &AlarmController{alarmService: alarmService}
}

// PageAlarm 报警历史分页查询
func (ctr *AlarmController) PageAlarm(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageAlarmDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.alarmService.PageAlarms(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListActiveAlarm 当前活跃离线报警（= 离线设备列表）
func (ctr *AlarmController) ListActiveAlarm(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.alarmService.ListActiveAlarms(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
