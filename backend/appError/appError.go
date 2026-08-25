package appError

import (
	"context"

	"iot-gateway/i18n"
)

// AppError 业务错误
type AppError struct {
	Code    string // 错误码
	Message string // 默认消息
	MsgKey  string // i18n 翻译 key
}

func (e *AppError) Error() string {
	return e.Message
}

// NewAppError 创建业务错误
func NewAppError(code, msg string) *AppError {
	return &AppError{
		Code:    code,
		Message: msg,
	}
}

// NewAppErrorCtx 创建支持 i18n 的业务错误
func NewAppErrorCtx(code, msg, msgKey string) *AppError {
	return &AppError{
		Code:    code,
		Message: msg,
		MsgKey:  msgKey,
	}
}

// GetMessageCtx 获取上下文中语言的错误消息
func (e *AppError) GetMessageCtx(ctx context.Context) string {
	if e.MsgKey != "" {
		if translated := i18n.TCtx(ctx, e.MsgKey); translated != e.MsgKey {
			return translated
		}
	}
	return e.Message
}
