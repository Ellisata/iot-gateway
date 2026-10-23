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

// MqttSubscriptionController MQTT 订阅管理控制器
type MqttSubscriptionController struct {
	service *service.MqttSubscriptionService
}

// NewMqttSubscriptionController 创建 MQTT 订阅管理控制器
func NewMqttSubscriptionController(svc *service.MqttSubscriptionService) *MqttSubscriptionController {
	return &MqttSubscriptionController{service: svc}
}

// CreateMqttSubscription 新增 MQTT 订阅
func (ctr *MqttSubscriptionController) CreateMqttSubscription(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateMqttSubscriptionDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.service.CreateMqttSubscription(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateMqttSubscription 编辑 MQTT 订阅
func (ctr *MqttSubscriptionController) UpdateMqttSubscription(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateMqttSubscriptionDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.service.UpdateMqttSubscription(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetMqttSubscriptionById 根据 ID 查询 MQTT 订阅
func (ctr *MqttSubscriptionController) GetMqttSubscriptionById(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.service.GetMqttSubscriptionById(ctx, c.Param("id"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// DeleteMqttSubscription 删除 MQTT 订阅
func (ctr *MqttSubscriptionController) DeleteMqttSubscription(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctr.service.DeleteMqttSubscription(ctx, c.Param("id")); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// PageMqttSubscription MQTT 订阅分页查询
func (ctr *MqttSubscriptionController) PageMqttSubscription(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageMqttSubscriptionDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.service.PageMqttSubscription(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageMqttSubscriptionDevicePoint 订阅设备/点位信息分页查询
func (ctr *MqttSubscriptionController) PageMqttSubscriptionDevicePoint(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageMqttSubscriptionDevicePointDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.service.PageMqttSubscriptionDevicePoint(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// CreateMqttSubscriptionDevicePoint 手工新增订阅设备/点位
func (ctr *MqttSubscriptionController) CreateMqttSubscriptionDevicePoint(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateMqttSubscriptionDevicePointDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.service.CreateMqttSubscriptionDevicePoint(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateMqttSubscriptionDevicePoint 编辑订阅设备/点位元信息
func (ctr *MqttSubscriptionController) UpdateMqttSubscriptionDevicePoint(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateMqttSubscriptionDevicePointDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.service.UpdateMqttSubscriptionDevicePoint(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// ImportMqttSubscriptionDevicePoint 批量导入订阅设备/点位（Excel .xlsx，上传字段名 file）
func (ctr *MqttSubscriptionController) ImportMqttSubscriptionDevicePoint(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := readXlsxUpload(c, "file")
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	result, err := ctr.service.ImportMqttSubscriptionDevicePoints(ctx, data)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(result))
}

// ImportMqttSubscriptionDevicePointTemplate 下载订阅设备/点位导入模板（.xlsx）
func (ctr *MqttSubscriptionController) ImportMqttSubscriptionDevicePointTemplate(c *gin.Context) {
	ctx := c.Request.Context()
	buf, err := ctr.service.GenerateMqttSubscriptionDevicePointImportTemplate()
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", `attachment; filename="mqtt-subscription-device-point-import-template.xlsx"`)
	c.Data(200, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf.Bytes())
}

// TestMqttSubscription 连通性测试（不保存配置）
func (ctr *MqttSubscriptionController) TestMqttSubscription(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.TestMqttSubscriptionDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.service.TestMqttSubscription(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// PreviewMqttSubscription 映射规则预览（不保存配置）
func (ctr *MqttSubscriptionController) PreviewMqttSubscription(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PreviewMqttSubscriptionDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(ctr.service.PreviewMqttSubscription(&req)))
}

// MqttSubscriptionStatus 查询所有活跃订阅的运行状态
func (ctr *MqttSubscriptionController) MqttSubscriptionStatus(c *gin.Context) {
	c.JSON(200, response.Success(ctr.service.MqttSubscriptionStatus()))
}
