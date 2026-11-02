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

// OpenApiController 开放接口控制器（供外部系统凭密钥调用）。
//
// 除控制下发外均为只读查询。控制下发仍受 control.enabled、协议 Writable、
// 点位 W 权限三层约束，见 service.ControlService。
type OpenApiController struct {
	openApiService *service.OpenApiService
	controlService *service.ControlService
}

// NewOpenApiController 创建开放接口控制器
func NewOpenApiController(openApiService *service.OpenApiService, controlService *service.ControlService) *OpenApiController {
	return &OpenApiController{openApiService: openApiService, controlService: controlService}
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

// ListDeviceNames 批量查询设备名称（JSON Body 传设备ID数组）
func (ctr *OpenApiController) ListDeviceNames(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiDeviceNamesDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.ListOpenApiDeviceNames(ctx, req.IDs)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListAddressLabels 批量查询点位标签（JSON Body 传设备ID数组 + 点位ID数组）
func (ctr *OpenApiController) ListAddressLabels(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiAddressLabelsDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.ListOpenApiAddressLabels(ctx, req.DeviceIDs, req.AddressIDs)
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

// SyncDevicePoints 对外「设备 + 点位」游标分批同步查询（供外部系统全量遍历）
func (ctr *OpenApiController) SyncDevicePoints(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiSyncDevicePointsDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.SyncDevicePoints(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// DeviceReliability 对外设备维度可靠性统计（可用率 / 故障段数 / 停机时长 / MTTR）
func (ctr *OpenApiController) DeviceReliability(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.DeviceReliabilityDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.DeviceReliability(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PointAbnormal 对外点位维度异常 top-N（采集质量异常与超限分开计数）
func (ctr *OpenApiController) PointAbnormal(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PointAbnormalDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.PointAbnormal(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// WriteControl 对外控制下发。
//
// 这是开放接口里**唯一会改变现场设备状态**的端点。仍受 control.enabled、
// 协议 Writable、点位 rw_permission 三层约束；审计 source 记为 "openapi"。
// X-Api-Key 中间件只做存在性校验，不注入操作者名称，因此 operator 固定为
// "openapi"（不记录密钥本身，避免审计表泄露凭据）。
func (ctr *OpenApiController) WriteControl(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.ControlDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.controlService.Write(ctx, &req, "openapi", "openapi")
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
