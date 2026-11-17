// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package controller

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/model/dto"
	"iot-gateway/response"
	"iot-gateway/service"
)

// maxBackupUploadBytes 是上传备份包的上限。备份包含整库与插件清单，正常
// 远小于此值；上限只用于挡住误传大文件把磁盘写满。
const maxBackupUploadBytes = 4 << 30 // 4 GiB

// BackupController 备份与恢复控制器。
//
// 恢复接口只做“校验 + 暂存 + 提示重启”，不做热恢复；它只挂在管理端路由上，
// 不注册到 /openApi 与 /agentApi（后者免鉴权，绝不能暴露恢复入口）。
type BackupController struct {
	backupService *service.BackupService
}

// NewBackupController 创建备份控制器
func NewBackupController(backupService *service.BackupService) *BackupController {
	return &BackupController{backupService: backupService}
}

// List 列出备份包。
func (ctr *BackupController) List(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.backupService.List()
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// Status 返回调度状态与待恢复任务。
func (ctr *BackupController) Status(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.backupService.Status()
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// Create 立即创建备份。
func (ctr *BackupController) Create(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.CreateBackupDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.backupService.Create(&req, operatorOf(c))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// Download 下载一个备份包。
func (ctr *BackupController) Download(c *gin.Context) {
	ctx := c.Request.Context()
	name := strings.TrimSpace(c.Query("name"))
	if name == "" {
		c.JSON(200, response.Fail("PARAM_ERROR", "缺少 name"))
		return
	}
	path, err := ctr.backupService.Download(name, operatorOf(c))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.FileAttachment(path, name)
}

// Delete 删除一个备份包。
func (ctr *BackupController) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	name := strings.TrimSpace(c.Query("name"))
	if name == "" {
		c.JSON(200, response.Fail("PARAM_ERROR", "缺少 name"))
		return
	}
	if err := ctr.backupService.Delete(name, operatorOf(c)); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// Stage 暂存已有备份包，等下次启动恢复。
func (ctr *BackupController) Stage(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.StageBackupDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.backupService.Stage(&req, operatorOf(c))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// Upload 上传一个备份包并暂存，等下次启动恢复。
func (ctr *BackupController) Upload(c *gin.Context) {
	ctx := c.Request.Context()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBackupUploadBytes)
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(200, response.Fail("PARAM_ERROR", "缺少上传文件 file: "+err.Error()))
		return
	}
	f, err := file.Open()
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	defer f.Close()

	identity := strings.TrimSpace(c.PostForm("identity"))
	data, err := ctr.backupService.Upload(file.Filename, f, identity, operatorOf(c))
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// CancelPending 取消待恢复任务。
func (ctr *BackupController) CancelPending(c *gin.Context) {
	ctx := c.Request.Context()
	if err := ctr.backupService.CancelPending(operatorOf(c)); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// operatorOf 返回当前操作者；/agentApi 下没有 JWT，落成 api。
func operatorOf(c *gin.Context) string {
	if name := strings.TrimSpace(c.GetString("username")); name != "" {
		return name
	}
	return "api"
}
