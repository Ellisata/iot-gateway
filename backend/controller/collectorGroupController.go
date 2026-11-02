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

// CollectorGroupController 采集组控制器
type CollectorGroupController struct {
	collectorGroupService *service.CollectorGroupService
}

// NewCollectorGroupController 创建采集组控制器
func NewCollectorGroupController(collectorGroupService *service.CollectorGroupService) *CollectorGroupController {
	return &CollectorGroupController{collectorGroupService: collectorGroupService}
}

// CreateCollectorGroup 创建采集组
func (ctr *CollectorGroupController) CreateCollectorGroup(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateCollectorGroupDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.collectorGroupService.CreateCollectorGroup(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateCollectorGroup 更新采集组
func (ctr *CollectorGroupController) UpdateCollectorGroup(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateCollectorGroupDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.collectorGroupService.UpdateCollectorGroup(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// DeleteCollectorGroup 删除采集组（仍有点位绑定时拒绝）
func (ctr *CollectorGroupController) DeleteCollectorGroup(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctr.collectorGroupService.DeleteCollectorGroup(ctx, c.Param("id")); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetCollectorGroupByID 查询采集组详情
func (ctr *CollectorGroupController) GetCollectorGroupByID(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.collectorGroupService.GetCollectorGroupByID(ctx, c.Param("id"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageCollectorGroup 采集组分页查询
func (ctr *CollectorGroupController) PageCollectorGroup(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageCollectorGroupDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.collectorGroupService.PageCollectorGroup(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListCollectorGroups 采集组下拉列表（deviceId 可选；不传返回全部设备的组）
func (ctr *CollectorGroupController) ListCollectorGroups(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.ListCollectorGroupsDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.collectorGroupService.ListCollectorGroups(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
