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

// DeviceController 设备控制器
type DeviceController struct {
	deviceService *service.DeviceService
}

// NewDeviceController 创建设备控制器
func NewDeviceController(deviceService *service.DeviceService) *DeviceController {
	return &DeviceController{deviceService: deviceService}
}

// CreateDevice 创建设备
func (ctr *DeviceController) CreateDevice(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateDeviceDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.deviceService.CreateDevice(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateDevice 更新设备
func (ctr *DeviceController) UpdateDevice(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateDeviceDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.deviceService.UpdateDevice(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// DeleteDevice 删除设备
func (ctr *DeviceController) DeleteDevice(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if err := ctr.deviceService.DeleteDevice(ctx, id); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetDeviceByID 根据ID查询设备
func (ctr *DeviceController) GetDeviceByID(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	data, err := ctr.deviceService.GetDeviceByID(ctx, id)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// TestDeviceConnection 测试设备连通性
func (ctr *DeviceController) TestDeviceConnection(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.TestDeviceConnectionDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.deviceService.TestDeviceConnection(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetDeviceOverview 设备在线情况统计（只读聚合）
func (ctr *DeviceController) GetDeviceOverview(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.deviceService.GetDeviceOverview(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageDevice 设备分页查询
func (ctr *DeviceController) PageDevice(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageDeviceDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.deviceService.PageDevice(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ImportDevice 批量导入设备（Excel .xlsx，上传字段名 file）
func (ctr *DeviceController) ImportDevice(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := readXlsxUpload(c, "file")
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	result, err := ctr.deviceService.ImportDevices(ctx, data)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(result))
}

// ImportTemplate 下载设备导入模板（.xlsx）
func (ctr *DeviceController) ImportTemplate(c *gin.Context) {
	ctx := c.Request.Context()
	buf, err := ctr.deviceService.GenerateImportTemplate()
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", `attachment; filename="device-import-template.xlsx"`)
	c.Data(200, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf.Bytes())
}
