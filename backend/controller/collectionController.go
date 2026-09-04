// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/response"
	"iot-gateway/service"
)

// CollectionController 采集控制控制器
type CollectionController struct {
	collectionService *service.CollectionService
}

// NewCollectionController 创建采集控制控制器
func NewCollectionController(collectionService *service.CollectionService) *CollectionController {
	return &CollectionController{collectionService: collectionService}
}

// Start 启动采集
func (ctr *CollectionController) Start(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctr.collectionService.StartCollection(ctx); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// Stop 停止采集
func (ctr *CollectionController) Stop(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctr.collectionService.StopCollection(ctx); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// Refresh 热刷新采集配置（新增设备/点位无需重启应用）
func (ctr *CollectionController) Refresh(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctr.collectionService.RefreshCollection(ctx); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// Status 获取采集状态
func (ctr *CollectionController) Status(c *gin.Context) {
	ctx := c.Request.Context()
	data := ctr.collectionService.GetCollectionStatus(ctx)
	c.JSON(200, response.Success(data))
}
