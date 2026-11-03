// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/model/dto"
	"iot-gateway/model/vo"
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

// RecentMessages 查询某个通道最近推送的报文（仅内存，最近 100 条）。
func (ctr *PushController) RecentMessages(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.RecentPushMessagesDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	msgs, active := ctr.pushEngine.RecentMessages(req.ChannelID, req.Limit)
	out := &vo.PushRecentMessagesVO{
		Active:   active,
		Messages: make([]vo.PushRecentMessageVO, 0, len(msgs)),
	}
	for _, m := range msgs {
		// 推送频率可能远高于订阅，毫秒精度能区分同一秒内的多条记录。
		sentAt := ""
		if !m.At.IsZero() {
			sentAt = m.At.Format("2006-01-02 15:04:05.000")
		}
		out.Messages = append(out.Messages, vo.PushRecentMessageVO{
			Seq:        m.Seq,
			Time:       sentAt,
			Target:     m.Target,
			DeviceCode: m.DeviceCode,
			GroupCode:  m.GroupCode,
			Records:    m.Records,
			StatusCode: m.StatusCode,
			DurationMs: m.DurationMs,
			Size:       m.Size,
			Payload:    m.Payload,
			Truncated:  m.Truncated,
			Error:      m.Error,
			FromDrain:  m.FromDrain,
		})
	}
	c.JSON(200, response.Success(out))
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
