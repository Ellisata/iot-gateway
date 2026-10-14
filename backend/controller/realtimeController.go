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

// RealtimeController 设备实时数据控制器
type RealtimeController struct {
	realtimeService *service.RealtimeService
}

// NewRealtimeController 创建设备实时数据控制器
func NewRealtimeController(realtimeService *service.RealtimeService) *RealtimeController {
	return &RealtimeController{realtimeService: realtimeService}
}

// Snapshot 设备实时数据快照（分页）
func (ctr *RealtimeController) Snapshot(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.RealtimeSnapshotDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.realtimeService.GetDeviceSnapshot(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
