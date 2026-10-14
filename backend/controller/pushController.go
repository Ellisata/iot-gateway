// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/push"
	"iot-gateway/response"
)

// PushController 数据推送控制器
type PushController struct {
	pushEngine *push.Engine
}

// NewPushController 创建数据推送控制器
func NewPushController(pushEngine *push.Engine) *PushController {
	return &PushController{pushEngine: pushEngine}
}

// Status 获取推送通道运行状态
func (ctr *PushController) Status(c *gin.Context) {
	c.JSON(200, response.Success(ctr.pushEngine.GetStatus()))
}

// Refresh 热刷新推送配置（新增/修改/删除通道无需重启应用）
func (ctr *PushController) Refresh(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctr.pushEngine.Refresh(); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}
