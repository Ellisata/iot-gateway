package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/model/dto"
	"iot-gateway/response"
	"iot-gateway/service"
)

// ProtocolController 协议控制器
type ProtocolController struct {
	protocolService *service.ProtocolService
}

// NewProtocolController 创建协议控制器
func NewProtocolController(protocolService *service.ProtocolService) *ProtocolController {
	return &ProtocolController{protocolService: protocolService}
}

// CreateProtocol 创建协议
func (ctr *ProtocolController) CreateProtocol(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateProtocolDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.protocolService.CreateProtocol(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateProtocol 更新协议
func (ctr *ProtocolController) UpdateProtocol(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateProtocolDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.protocolService.UpdateProtocol(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetProtocolById 根据ID查询协议
func (ctr *ProtocolController) GetProtocolById(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	data, err := ctr.protocolService.GetProtocolById(ctx, id)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// GetProtocolByName 根据协议名称查询协议
func (ctr *ProtocolController) GetProtocolByName(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Query("name")
	data, err := ctr.protocolService.GetProtocolByName(ctx, name)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// GetProtocolDataTypes 根据协议名/协议ID查询该协议可配置的数据类型列表
// （通用类型 + 协议专属扩展类型），供设备点位配置界面类型下拉使用。
func (ctr *ProtocolController) GetProtocolDataTypes(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.protocolService.GetProtocolDataTypes(ctx, c.Query("protocolName"), c.Query("protocolId"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// UpdateProtocolFormByName 根据协议名称更新表单 JSON
func (ctr *ProtocolController) UpdateProtocolFormByName(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateProtocolFormByNameDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.protocolService.UpdateProtocolFormByName(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateProtocolFormById 根据协议ID更新表单 JSON
func (ctr *ProtocolController) UpdateProtocolFormById(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateProtocolFormByIdDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.protocolService.UpdateProtocolFormById(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// DeleteProtocol 删除协议
func (ctr *ProtocolController) DeleteProtocol(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if err := ctr.protocolService.DeleteProtocol(ctx, id); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// PageProtocol 协议分页查询
func (ctr *ProtocolController) PageProtocol(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageProtocolDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.protocolService.PageProtocol(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
