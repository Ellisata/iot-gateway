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

// SystemConfigController 运行期配置开关控制器
type SystemConfigController struct {
	systemConfigService *service.SystemConfigService
}

// NewSystemConfigController 创建运行期配置控制器
func NewSystemConfigController(systemConfigService *service.SystemConfigService) *SystemConfigController {
	return &SystemConfigController{systemConfigService: systemConfigService}
}

// List 查询全部可动态管理的开关及当前状态。
func (ctr *SystemConfigController) List(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.systemConfigService.List(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// Update 修改一个开关。返回 requiresRestart 告知是否需重启网关。
func (ctr *SystemConfigController) Update(c *gin.Context) {
	ctx := c.Request.Context()

	var req dto.UpdateSystemConfigDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}

	data, err := ctr.systemConfigService.Update(ctx, &req, c.GetString("username"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// Reset 删除覆盖值，恢复 default.yaml 的默认值。
func (ctr *SystemConfigController) Reset(c *gin.Context) {
	ctx := c.Request.Context()

	var req dto.ResetSystemConfigDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}

	data, err := ctr.systemConfigService.Reset(ctx, &req, c.GetString("username"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
