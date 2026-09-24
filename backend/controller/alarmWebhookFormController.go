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

// AlarmWebhookFormController 报警 Webhook 表单控制器
type AlarmWebhookFormController struct {
	alarmWebhookFormService *service.AlarmWebhookFormService
}

// NewAlarmWebhookFormController 创建报警 Webhook 表单控制器
func NewAlarmWebhookFormController(alarmWebhookFormService *service.AlarmWebhookFormService) *AlarmWebhookFormController {
	return &AlarmWebhookFormController{alarmWebhookFormService: alarmWebhookFormService}
}

// CreateAlarmWebhookForm 创建报警 Webhook 表单
func (ctr *AlarmWebhookFormController) CreateAlarmWebhookForm(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateAlarmWebhookFormDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.alarmWebhookFormService.CreateAlarmWebhookForm(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateAlarmWebhookForm 更新报警 Webhook 表单
func (ctr *AlarmWebhookFormController) UpdateAlarmWebhookForm(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateAlarmWebhookFormDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.alarmWebhookFormService.UpdateAlarmWebhookForm(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetAlarmWebhookFormByName 根据名称查询报警 Webhook 表单
func (ctr *AlarmWebhookFormController) GetAlarmWebhookFormByName(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.GetAlarmWebhookFormByNameDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.alarmWebhookFormService.GetAlarmWebhookFormByName(ctx, req.Name)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
