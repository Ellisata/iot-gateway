// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package controller

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/pluginhost"
	"iot-gateway/response"
	"iot-gateway/service"
)

const (
	// pluginUploadField 上传插件的 multipart 字段名。
	// 复数形式是刻意的：一次可携带多个文件（同名部件重复出现）。
	pluginUploadField = "files"

	// maxPluginImportBodySize 单次请求体的总大小上限。
	//
	// multipart 解析会把超出的部分落到系统临时目录，不设上限等于开了一条
	// 「用一次上传把磁盘写满」的路径。512MiB 足够容纳两整套发行版插件
	// （单套 8 个 × 约 8MiB），又远小于把磁盘撑爆的量级。
	maxPluginImportBodySize = 512 << 20

	// pluginFormMemory 解析时驻留内存的上限，超出部分落临时文件。
	// 取值与 gin 的默认 MaxMultipartMemory 一致。
	pluginFormMemory = 32 << 20
)

// PluginController 协议插件安装控制器
type PluginController struct {
	pluginService *service.PluginService
}

// NewPluginController 创建插件安装控制器
func NewPluginController(pluginService *service.PluginService) *PluginController {
	return &PluginController{pluginService: pluginService}
}

// PluginInfo 查询网关侧的插件环境（平台 / 目录 / 命名规则）
func (ctr *PluginController) PluginInfo(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.pluginService.Info(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ImportPlugin 批量上传协议插件
func (ctr *PluginController) ImportPlugin(c *gin.Context) {
	ctx := c.Request.Context()

	// 先卡住整个请求体再解析：超限会在读取时报错，而不是先落满磁盘再发现。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPluginImportBodySize)

	files, err := readPluginUploads(c)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}

	result, err := ctr.pluginService.ImportPlugins(ctx, files)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(result))
}

// readPluginUploads 把 multipart 中的插件部件读成待安装列表。
//
// 逐部件的失败（超限）**不**中断整批：它作为 RejectReason 交给服务层，
// 与校验失败走同一条「记一条失败明细、继续处理下一个」的路径，
// 这样用户一次就能看到全部问题，而不是修一个报一个。
func readPluginUploads(c *gin.Context) ([]service.PluginUpload, error) {
	if err := c.Request.ParseMultipartForm(pluginFormMemory); err != nil {
		return nil, appError.NewAppError(
			enums.ParamValidEnum.GetCode(),
			fmt.Sprintf("解析上传内容失败（可能超出请求体上限 %d MiB）：%v",
				int64(maxPluginImportBodySize)>>20, err))
	}
	// 解析出来的部件可能已落在系统临时目录，必须清理，
	// 否则每次上传都会在那里留下一份插件副本。
	defer func() { _ = c.Request.MultipartForm.RemoveAll() }()

	headers := c.Request.MultipartForm.File[pluginUploadField]
	if len(headers) == 0 {
		return nil, appError.NewAppErrorCtx(
			enums.PluginNoFileEnum.GetCode(),
			enums.PluginNoFileEnum.GetMessage(),
			enums.PluginNoFileEnum.GetMsgKey())
	}
	if len(headers) > service.MaxPluginBatch {
		return nil, appError.NewAppErrorCtx(
			enums.PluginTooManyEnum.GetCode(),
			fmt.Sprintf("%s（最多 %d 个，本次 %d 个）",
				enums.PluginTooManyEnum.GetMessage(), service.MaxPluginBatch, len(headers)),
			enums.PluginTooManyEnum.GetMsgKey())
	}

	uploads := make([]service.PluginUpload, 0, len(headers))
	for _, h := range headers {
		uploads = append(uploads, readOnePluginUpload(h))
	}
	return uploads, nil
}

// readOnePluginUpload 读取单个部件。
//
// 文件名一律取 basename：浏览器可能带上完整路径（旧 IE 行为、或被构造的请求），
// 服务层的路径穿越校验是最后一道防线，这里先用 basename 归一，
// 让「奇怪的文件名」从一开始就不成立。
func readOnePluginUpload(h *multipart.FileHeader) service.PluginUpload {
	name := filepath.Base(h.Filename)

	// 超限文件不读内容：结论从部件头就能得出，没必要先把 256MiB+ 拖进内存。
	if h.Size > pluginhost.MaxPluginFileSize {
		return service.PluginUpload{
			Name: name,
			RejectReason: fmt.Sprintf("文件大小 %d MiB 超过单文件上限 %d MiB",
				h.Size>>20, int64(pluginhost.MaxPluginFileSize)>>20),
		}
	}

	src, err := h.Open()
	if err != nil {
		return service.PluginUpload{Name: name, RejectReason: "读取上传文件失败：" + err.Error()}
	}
	defer src.Close()

	// 读上限 +1 字节：即便部件头谎报了大小，也不会读进超过上限的内容。
	data, err := io.ReadAll(io.LimitReader(src, int64(pluginhost.MaxPluginFileSize)+1))
	if err != nil {
		return service.PluginUpload{Name: name, RejectReason: "读取上传文件失败：" + err.Error()}
	}
	return service.PluginUpload{Name: name, Data: data}
}
