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

// PushChannelController 数据推送通道控制器
type PushChannelController struct {
	pushChannelService *service.PushChannelService
}

// NewPushChannelController 创建数据推送通道控制器
func NewPushChannelController(pushChannelService *service.PushChannelService) *PushChannelController {
	return &PushChannelController{pushChannelService: pushChannelService}
}

// CreatePushChannel 创建数据推送通道
func (ctr *PushChannelController) CreatePushChannel(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreatePushChannelDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.pushChannelService.CreatePushChannel(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdatePushChannel 更新数据推送通道
func (ctr *PushChannelController) UpdatePushChannel(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdatePushChannelDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.pushChannelService.UpdatePushChannel(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetPushChannelById 根据ID查询数据推送通道
func (ctr *PushChannelController) GetPushChannelById(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	data, err := ctr.pushChannelService.GetPushChannelById(ctx, id)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// DeletePushChannel 删除数据推送通道
func (ctr *PushChannelController) DeletePushChannel(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if err := ctr.pushChannelService.DeletePushChannel(ctx, id); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// TestPushChannel 测试推送通道连通性（不保存配置，仅校验配置并建立连接探测）
func (ctr *PushChannelController) TestPushChannel(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.TestPushChannelDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.pushChannelService.TestPushChannel(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// ListPushChannelTypes 已注册的通道类型（含是否消息类、可选格式、能力位）
func (ctr *PushChannelController) ListPushChannelTypes(c *gin.Context) {
	c.JSON(200, response.Success(ctr.pushChannelService.ListPushChannelTypes()))
}

// PreviewPushChannel 预览出站载荷（不发送、不落库）
func (ctr *PushChannelController) PreviewPushChannel(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PreviewPushChannelDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(ctr.pushChannelService.PreviewPushChannel(ctx, &req)))
}

// PreviewPushChannelMapping 只试编码出站映射（不依赖通道类型/连接配置，不发送）
func (ctr *PushChannelController) PreviewPushChannelMapping(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PreviewPushChannelMappingDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(ctr.pushChannelService.PreviewPushChannelMapping(ctx, &req)))
}

// TestSendPushChannel 测试发送（会真的向配置的下游发请求）
func (ctr *PushChannelController) TestSendPushChannel(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.TestSendPushChannelDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(ctr.pushChannelService.TestSendPushChannel(ctx, &req)))
}

// PagePushChannel 数据推送通道分页查询（名称模糊检索）
func (ctr *PushChannelController) PagePushChannel(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PagePushChannelDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.pushChannelService.PagePushChannel(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
