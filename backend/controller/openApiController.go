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

// OpenApiController 开放接口控制器（供外部系统凭密钥调用，只读）
type OpenApiController struct {
	openApiService *service.OpenApiService
}

// NewOpenApiController 创建开放接口控制器
func NewOpenApiController(openApiService *service.OpenApiService) *OpenApiController {
	return &OpenApiController{openApiService: openApiService}
}

// PageDevices 对外设备分页查询
func (ctr *OpenApiController) PageDevices(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageDeviceDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.PageOpenApiDevices(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageDeviceAddresses 对外设备地址分页查询（按设备ID）
func (ctr *OpenApiController) PageDeviceAddresses(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageDeviceAddressDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.PageOpenApiDeviceAddresses(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
