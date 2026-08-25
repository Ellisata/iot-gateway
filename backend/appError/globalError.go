package appError

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/go-playground/validator/v10"

	"iot-gateway/logger"
	"iot-gateway/response"
)

// HandleError 处理错误，返回中文消息
func HandleError(err error) response.Response[interface{}] {
	return handleError(context.Background(), err)
}

// HandleErrorCtx 处理错误，返回翻译后的消息
func HandleErrorCtx(ctx context.Context, err error) response.Response[interface{}] {
	return handleError(ctx, err)
}

func handleError(ctx context.Context, err error) response.Response[interface{}] {
	logger.Error("%s", err.Error())

	// AppError
	var appErr *AppError
	if errors.As(err, &appErr) {
		msg := appErr.GetMessageCtx(ctx)
		return response.Fail(appErr.Code, msg)
	}

	// 参数校验错误
	var validErrs validator.ValidationErrors
	if errors.As(err, &validErrs) {
		field := validErrs[0].Field()
		tag := validErrs[0].Tag()
		msg := "参数校验失败: " + field + " " + tag
		return response.Fail("PARAM_ERROR", msg)
	}

	// JSON 语法错误
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return response.Fail("PARAM_ERROR", "请求体 JSON 格式错误")
	}

	// JSON 类型不匹配
	var unmarshalTypeErr *json.UnmarshalTypeError
	if errors.As(err, &unmarshalTypeErr) {
		msg := "参数类型错误: " + unmarshalTypeErr.Field + " 期望 " + unmarshalTypeErr.Type.String()
		return response.Fail("PARAM_ERROR", msg)
	}

	// Content-Type 错误（ShouldBindJSON 要求 application/json）
	if strings.Contains(err.Error(), "Content-Type") {
		return response.Fail("PARAM_ERROR", "请求头 Content-Type 必须为 application/json")
	}

	// 请求体为空
	if errors.Is(err, io.EOF) {
		return response.Fail("PARAM_ERROR", "请求体不能为空")
	}

	// 未知错误
	return response.Fail("-1", "system error")
}
