package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/model/dto"
	"iot-gateway/response"
	"iot-gateway/service"
)

// LogFileController 日志查看控制器
type LogFileController struct {
	logFileService *service.LogFileService
}

// NewLogFileController 创建日志查看控制器
func NewLogFileController(logFileService *service.LogFileService) *LogFileController {
	return &LogFileController{logFileService: logFileService}
}

// ListFiles 日志文件分页列表
func (ctr *LogFileController) ListFiles(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.ListLogFileDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.logFileService.ListFiles(req.Page, req.Size)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ReadFile 读取日志文件末尾 N 行
func (ctr *LogFileController) ReadFile(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.ReadLogDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.logFileService.ReadFile(req.FileName, req.Tail)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
