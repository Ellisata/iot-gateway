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

// AlarmWebhookController 报警 Webhook 通知控制器
type AlarmWebhookController struct {
	alarmWebhookService *service.AlarmWebhookService
}

// NewAlarmWebhookController 创建报警 Webhook 控制器
func NewAlarmWebhookController(alarmWebhookService *service.AlarmWebhookService) *AlarmWebhookController {
	return &AlarmWebhookController{alarmWebhookService: alarmWebhookService}
}

// CreateAlarmWebhook 创建报警 Webhook
func (ctr *AlarmWebhookController) CreateAlarmWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateAlarmWebhookDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.alarmWebhookService.CreateAlarmWebhook(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateAlarmWebhook 更新报警 Webhook
func (ctr *AlarmWebhookController) UpdateAlarmWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateAlarmWebhookDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.alarmWebhookService.UpdateAlarmWebhook(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetAlarmWebhookById 根据 ID 查询报警 Webhook
func (ctr *AlarmWebhookController) GetAlarmWebhookById(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.alarmWebhookService.GetAlarmWebhookById(ctx, c.Param("id"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// DeleteAlarmWebhook 删除报警 Webhook
func (ctr *AlarmWebhookController) DeleteAlarmWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctr.alarmWebhookService.DeleteAlarmWebhook(ctx, c.Param("id")); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// PageAlarmWebhook 报警 Webhook 分页查询
func (ctr *AlarmWebhookController) PageAlarmWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageAlarmWebhookDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.alarmWebhookService.PageAlarmWebhook(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// TestSendAlarmWebhook 发送一条测试报警（同步单次，不重试）
func (ctr *AlarmWebhookController) TestSendAlarmWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.TestSendAlarmWebhookDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.alarmWebhookService.TestSendAlarmWebhook(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// StatusAlarmWebhook 报警通知器运行状况
func (ctr *AlarmWebhookController) StatusAlarmWebhook(c *gin.Context) {
	c.JSON(200, response.Success(ctr.alarmWebhookService.StatusAlarmWebhook()))
}

// RefreshAlarmWebhook 手动触发热刷新（改完配置立即生效，无需等 10s 巡检）
func (ctr *AlarmWebhookController) RefreshAlarmWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctr.alarmWebhookService.RefreshAlarmWebhook(); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// ListAlarmWebhookTypes 支持的 Webhook 类型及元信息
func (ctr *AlarmWebhookController) ListAlarmWebhookTypes(c *gin.Context) {
	c.JSON(200, response.Success(ctr.alarmWebhookService.ListAlarmWebhookTypes()))
}
