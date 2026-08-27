package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/docs"
	"iot-gateway/model/dto"
	"iot-gateway/response"
	"iot-gateway/service"
)

// OpenApiSecretController 开放接口密钥控制器
type OpenApiSecretController struct {
	openApiSecretService *service.OpenApiSecretService
}

// NewOpenApiSecretController 创建开放接口密钥控制器
func NewOpenApiSecretController(openApiSecretService *service.OpenApiSecretService) *OpenApiSecretController {
	return &OpenApiSecretController{openApiSecretService: openApiSecretService}
}

// CreateOpenApiSecret 创建密钥
func (ctr *OpenApiSecretController) CreateOpenApiSecret(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateOpenApiSecretDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.openApiSecretService.CreateOpenApiSecret(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// UpdateOpenApiSecretName 编辑密钥名称
func (ctr *OpenApiSecretController) UpdateOpenApiSecretName(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.UpdateOpenApiSecretNameDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	if err := ctr.openApiSecretService.UpdateOpenApiSecretName(ctx, &req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// ListOpenApiSecret 查询密钥列表（无需分页）
func (ctr *OpenApiSecretController) ListOpenApiSecret(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.openApiSecretService.ListOpenApiSecret(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// GetOpenApiDoc 获取开放接口说明文档（服务端渲染 markdown 为 HTML）
func (ctr *OpenApiSecretController) GetOpenApiDoc(c *gin.Context) {
	c.JSON(200, response.Success(docs.RenderOpenApiDocHTML()))
}

// DeleteOpenApiSecret 删除密钥
func (ctr *OpenApiSecretController) DeleteOpenApiSecret(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if err := ctr.openApiSecretService.DeleteOpenApiSecret(ctx, id); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}
