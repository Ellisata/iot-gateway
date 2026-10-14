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

// GatewayController 网关自身身份控制器
type GatewayController struct {
	gatewayService *service.GatewayService
}

// NewGatewayController 创建网关身份控制器
func NewGatewayController(gatewayService *service.GatewayService) *GatewayController {
	return &GatewayController{gatewayService: gatewayService}
}

// GetGateway 查询网关自身身份
func (ctr *GatewayController) GetGateway(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.gatewayService.GetGateway(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// UpdateGateway 编辑网关名称与描述
func (ctr *GatewayController) UpdateGateway(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateGatewayDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.gatewayService.UpdateGateway(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}
