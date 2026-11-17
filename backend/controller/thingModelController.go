// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/response"
	"iot-gateway/service"
)

// ThingModelController 设备物模型控制器
type ThingModelController struct {
	thingModelService *service.ThingModelService
}

// NewThingModelController 创建物模型控制器
func NewThingModelController(thingModelService *service.ThingModelService) *ThingModelController {
	return &ThingModelController{thingModelService: thingModelService}
}

// CreateThingModel 创建物模型
func (ctr *ThingModelController) CreateThingModel(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateThingModelDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.thingModelService.CreateThingModel(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateThingModel 更新物模型
func (ctr *ThingModelController) UpdateThingModel(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateThingModelDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.thingModelService.UpdateThingModel(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// DeleteThingModel 删除物模型
func (ctr *ThingModelController) DeleteThingModel(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctr.thingModelService.DeleteThingModel(ctx, c.Param("id")); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// GetThingModelByID 根据 ID 查询物模型
func (ctr *ThingModelController) GetThingModelByID(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.thingModelService.GetThingModelByID(ctx, c.Param("id"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageThingModel 物模型分页查询
func (ctr *ThingModelController) PageThingModel(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageThingModelDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.thingModelService.PageThingModel(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListThingModels 物模型下拉选项（设备绑定用）
func (ctr *ThingModelController) ListThingModels(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.thingModelService.ListThingModels(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListThingModelProperties 列出某模型下的全部属性
func (ctr *ThingModelController) ListThingModelProperties(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.thingModelService.ListThingModelProperties(ctx, c.Query("modelId"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListThingModelDeviceOptions 物模型表单的设备候选（keyword 远程搜索）
func (ctr *ThingModelController) ListThingModelDeviceOptions(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.thingModelService.ListThingModelDeviceOptions(ctx, c.Query("modelId"), c.Query("keyword"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListThingModelDevices 列出绑定到某模型的设备（列表页「设备数」抽屉）
func (ctr *ThingModelController) ListThingModelDevices(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.thingModelService.ListThingModelDevices(ctx, c.Query("modelId"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListThingModelPointOptions 属性表单的点位候选（限该模型下的设备）
func (ctr *ThingModelController) ListThingModelPointOptions(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.thingModelService.ListThingModelPointOptions(ctx, c.Query("modelId"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListThingModelPropertyPoints 列出绑定到某属性的设备点位
func (ctr *ThingModelController) ListThingModelPropertyPoints(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.thingModelService.ListThingModelPropertyPoints(ctx, c.Query("propertyId"))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageThingModelProperties 物模型属性分页查询
func (ctr *ThingModelController) PageThingModelProperties(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageThingModelPropertyDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.thingModelService.PageThingModelProperties(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// CreateThingModelProperty 创建物模型属性
func (ctr *ThingModelController) CreateThingModelProperty(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateThingModelPropertyDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.thingModelService.CreateThingModelProperty(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateThingModelProperty 更新物模型属性
func (ctr *ThingModelController) UpdateThingModelProperty(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateThingModelPropertyDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.thingModelService.UpdateThingModelProperty(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// DeleteThingModelProperty 删除物模型属性
func (ctr *ThingModelController) DeleteThingModelProperty(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctr.thingModelService.DeleteThingModelProperty(ctx, c.Param("id")); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// ImportThingModel 批量导入物模型（Excel .xlsx，上传字段名 file）
func (ctr *ThingModelController) ImportThingModel(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := readXlsxUpload(c, "file")
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	result, err := ctr.thingModelService.ImportThingModels(ctx, data)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(result))
}

// ImportThingModelTemplate 下载物模型导入模板（.xlsx）
func (ctr *ThingModelController) ImportThingModelTemplate(c *gin.Context) {
	ctx := c.Request.Context()
	buf, err := ctr.thingModelService.GenerateThingModelImportTemplate()
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", `attachment; filename="thing-model-import-template.xlsx"`)
	c.Data(200, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf.Bytes())
}

// ImportThingModelProperty 批量导入物模型属性（Excel .xlsx，上传字段名 file，modelId 指定所属模型）
func (ctr *ThingModelController) ImportThingModelProperty(c *gin.Context) {
	ctx := c.Request.Context()
	modelID := c.PostForm("modelId")
	if modelID == "" {
		c.JSON(200, appError.HandleErrorCtx(ctx, appError.NewAppError(enums.ParamValidEnum.GetCode(), "缺少参数 modelId")))
		return
	}
	data, err := readXlsxUpload(c, "file")
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	result, err := ctr.thingModelService.ImportThingModelProperties(ctx, modelID, data)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(result))
}

// ImportThingModelPropertyTemplate 下载物模型属性导入模板（.xlsx）
func (ctr *ThingModelController) ImportThingModelPropertyTemplate(c *gin.Context) {
	ctx := c.Request.Context()
	buf, err := ctr.thingModelService.GenerateThingModelPropertyImportTemplate()
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", `attachment; filename="thing-model-property-import-template.xlsx"`)
	c.Data(200, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf.Bytes())
}
