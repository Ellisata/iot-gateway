// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/response"
	"iot-gateway/service"
)

// DeviceAddressController 设备地址控制器
type DeviceAddressController struct {
	deviceAddressService *service.DeviceAddressService
}

// NewDeviceAddressController 创建设备地址控制器
func NewDeviceAddressController(deviceAddressService *service.DeviceAddressService) *DeviceAddressController {
	return &DeviceAddressController{deviceAddressService: deviceAddressService}
}

// CreateDeviceAddress 创建设备地址
func (ctr *DeviceAddressController) CreateDeviceAddress(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateDeviceAddressDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.deviceAddressService.CreateDeviceAddress(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateDeviceAddress 更新设备地址
func (ctr *DeviceAddressController) UpdateDeviceAddress(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateDeviceAddressDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.deviceAddressService.UpdateDeviceAddress(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// DeleteDeviceAddress 删除设备地址
func (ctr *DeviceAddressController) DeleteDeviceAddress(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if err := ctr.deviceAddressService.DeleteDeviceAddress(ctx, id); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetDeviceAddressByID 根据ID查询设备地址
func (ctr *DeviceAddressController) GetDeviceAddressByID(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	data, err := ctr.deviceAddressService.GetDeviceAddressByID(ctx, id)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageDeviceAddress 设备地址分页查询
func (ctr *DeviceAddressController) PageDeviceAddress(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageDeviceAddressDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.deviceAddressService.PageDeviceAddress(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ImportDeviceAddress 批量导入设备地址（Excel .xlsx，上传字段名 file，deviceId 指定所属设备）
func (ctr *DeviceAddressController) ImportDeviceAddress(c *gin.Context) {
	ctx := c.Request.Context()
	deviceID := c.PostForm("deviceId")
	if deviceID == "" {
		c.JSON(200, appError.HandleErrorCtx(ctx, appError.NewAppError(enums.ParamValidEnum.GetCode(), "缺少参数 deviceId")))
		return
	}
	data, err := readXlsxUpload(c, "file")
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	result, err := ctr.deviceAddressService.ImportDeviceAddresses(ctx, deviceID, data)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(result))
}

// ImportDeviceAddressTemplate 下载设备地址导入模板（.xlsx）
func (ctr *DeviceAddressController) ImportDeviceAddressTemplate(c *gin.Context) {
	ctx := c.Request.Context()
	buf, err := ctr.deviceAddressService.GenerateDeviceAddressImportTemplate()
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", `attachment; filename="device-address-import-template.xlsx"`)
	c.Data(200, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf.Bytes())
}
