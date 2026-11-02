// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package controller

import (
	"strings"

	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/model/dto"
	"iot-gateway/response"
	"iot-gateway/service"
)

// ControlController 控制下发控制器。
type ControlController struct {
	controlService *service.ControlService
}

// NewControlController 创建控制下发控制器。
func NewControlController(controlService *service.ControlService) *ControlController {
	return &ControlController{controlService: controlService}
}

// Capability 查询设备是否支持控制下发。
func (ctr *ControlController) Capability(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.ControlCapabilityDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.controlService.Capability(ctx, req.DeviceID)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageLogs 分页查询控制下发审计。
func (ctr *ControlController) PageLogs(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageControlLogDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.controlService.PageLogs(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// Write 执行控制下发。
func (ctr *ControlController) Write(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.ControlDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	// 同一个控制器同时挂在 /control 与 /agentApi 下：按路径区分审计来源。
	// /agentApi 不走 JWT，拿不到用户名；操作者记为 "agent"，不记录任何凭据。
	source := "web"
	operator := c.GetString("username")
	if strings.HasPrefix(c.Request.URL.Path, "/agentApi/") {
		source = "agent"
		if operator == "" {
			operator = "agent"
		}
	}
	data, err := ctr.controlService.Write(ctx, &req, source, operator)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
