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

// TriggerRuleController 事件触发采集规则控制器
type TriggerRuleController struct {
	triggerRuleService *service.TriggerRuleService
}

// NewTriggerRuleController 创建规则控制器
func NewTriggerRuleController(triggerRuleService *service.TriggerRuleService) *TriggerRuleController {
	return &TriggerRuleController{triggerRuleService: triggerRuleService}
}

// CreateTriggerRule 创建规则
func (ctr *TriggerRuleController) CreateTriggerRule(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateTriggerRuleDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.triggerRuleService.CreateTriggerRule(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateTriggerRule 更新规则
func (ctr *TriggerRuleController) UpdateTriggerRule(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateTriggerRuleDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.triggerRuleService.UpdateTriggerRule(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// DeleteTriggerRule 删除规则（历史记录保留）
func (ctr *TriggerRuleController) DeleteTriggerRule(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctr.triggerRuleService.DeleteTriggerRule(ctx, c.Param("id")); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetTriggerRuleByID 查询规则详情
func (ctr *TriggerRuleController) GetTriggerRuleByID(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.triggerRuleService.GetTriggerRuleByID(ctx, c.Param("id"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageTriggerRule 规则分页
func (ctr *TriggerRuleController) PageTriggerRule(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageTriggerRuleDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.triggerRuleService.PageTriggerRule(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// RecentTriggers 最近结束的触发（仅内存，最新在前）。
//
// 与 PageTriggerRuleRecord 的分工：这里是"正在发生什么"的观测窗口（最近 100 条，
// 重启即清空）；持久化历史与条件/动作快照走分页接口。
func (ctr *TriggerRuleController) RecentTriggers(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.RecentTriggerDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(ctr.triggerRuleService.RecentTriggers(ctx, req.RuleID, req.Limit)))
}

// PageTriggerRuleRecord 触发历史分页
func (ctr *TriggerRuleController) PageTriggerRuleRecord(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageTriggerRuleRecordDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.triggerRuleService.PageTriggerRuleRecord(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
