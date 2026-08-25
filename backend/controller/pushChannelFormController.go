package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/model/dto"
	"iot-gateway/response"
	"iot-gateway/service"
)

// PushChannelFormController 数据推送通道表单控制器
type PushChannelFormController struct {
	pushChannelFormService *service.PushChannelFormService
}

// NewPushChannelFormController 创建数据推送通道表单控制器
func NewPushChannelFormController(pushChannelFormService *service.PushChannelFormService) *PushChannelFormController {
	return &PushChannelFormController{pushChannelFormService: pushChannelFormService}
}

// CreatePushChannelForm 创建数据推送通道表单
func (ctr *PushChannelFormController) CreatePushChannelForm(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreatePushChannelFormDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.pushChannelFormService.CreatePushChannelForm(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdatePushChannelForm 更新数据推送通道表单
func (ctr *PushChannelFormController) UpdatePushChannelForm(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdatePushChannelFormDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.pushChannelFormService.UpdatePushChannelForm(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetPushChannelFormByName 根据名称查询数据推送通道表单
func (ctr *PushChannelFormController) GetPushChannelFormByName(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.GetPushChannelFormByNameDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.pushChannelFormService.GetPushChannelFormByName(ctx, req.Name)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
