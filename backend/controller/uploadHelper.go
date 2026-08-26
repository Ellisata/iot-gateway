package controller

import (
	"io"
	"strings"

	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/enums"
)

// readXlsxUpload 读取 multipart 上传的 .xlsx 文件字节（字段名 fileField）。
// 未上传 / 非 .xlsx 格式时返回业务错误。
func readXlsxUpload(c *gin.Context, fileField string) ([]byte, error) {
	fileHeader, err := c.FormFile(fileField)
	if err != nil {
		return nil, appError.NewAppError(enums.ParamValidEnum.GetCode(), "未接收到上传文件（字段名 "+fileField+"）")
	}
	if !strings.HasSuffix(strings.ToLower(fileHeader.Filename), ".xlsx") {
		return nil, appError.NewAppError(enums.ParamValidEnum.GetCode(), "仅支持 .xlsx 格式的 Excel 文件")
	}
	src, err := fileHeader.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return io.ReadAll(src)
}
