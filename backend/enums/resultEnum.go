package enums

import (
	"context"
	"iot-gateway/i18n"
)

// BusinessEnum 业务枚举
type BusinessEnum struct {
	code    string
	message string
	msgKey  string
}

func NewBusinessEnum(code, message, msgKey string) *BusinessEnum {
	return &BusinessEnum{
		code:    code,
		message: message,
		msgKey:  msgKey,
	}
}

func (e *BusinessEnum) GetCode() string {
	return e.code
}

func (e *BusinessEnum) GetMessage() string {
	return e.message
}

func (e *BusinessEnum) GetMsgKey() string {
	return e.msgKey
}

func (e *BusinessEnum) GetMessageCtx(ctx context.Context) string {
	if e == nil || e.msgKey == "" {
		return e.message
	}
	return i18n.TCtx(ctx, e.msgKey)
}

// 业务错误码枚举
var (
	// 通用错误码
	SuccessEnum      = NewBusinessEnum("0", "成功", "")
	SystemErrorEnum  = NewBusinessEnum("-1", "系统错误", "err.system_error")
	ParamValidEnum   = NewBusinessEnum("10001", "参数校验错误", "err.param_valid")
	UnauthorizedEnum = NewBusinessEnum("10002", "未授权", "err.unauthorized")
	ForbiddenEnum    = NewBusinessEnum("10003", "禁止访问", "err.forbidden")
	NotFoundEnum     = NewBusinessEnum("10004", "资源不存在", "err.not_found")

	// 用户相关错误码
	UserExistsEnum    = NewBusinessEnum("20001", "用户已存在", "err.user_exists")
	UserNotExistsEnum = NewBusinessEnum("20002", "用户不存在", "err.user_not_exists")
	PasswordErrEnum   = NewBusinessEnum("20003", "密码错误", "err.password_error")
	TokenExpiredErr   = NewBusinessEnum("20004", "令牌已过期", "err.token_expired")
	TokenInvalidErr   = NewBusinessEnum("20005", "无效的令牌", "err.token_invalid")
	TokenEmptyErr     = NewBusinessEnum("20006", "令牌为空", "err.token_empty")

	// 设备相关错误码
	DeviceExistsEnum    = NewBusinessEnum("40001", "设备名称已存在", "err.device_exists")
	DeviceNotExistsEnum = NewBusinessEnum("40002", "设备不存在", "err.device_not_found")
	DevicePingFailEnum  = NewBusinessEnum("40003", "设备连接测试失败", "err.device_ping_fail")

	// 采集相关错误码
	CollectionStartFailEnum = NewBusinessEnum("30001", "开始采集失败", "collection.start.failed")
	CollectionStopFailEnum  = NewBusinessEnum("30002", "停止采集失败", "collection.stop.failed")

	// 协议相关错误码
	ProtocolExistsEnum    = NewBusinessEnum("50001", "协议名称已存在", "err.protocol_exists")
	ProtocolNotExistsEnum = NewBusinessEnum("50002", "协议不存在", "err.protocol_not_found")

	// 设备地址相关错误码
	DeviceAddressExistsEnum      = NewBusinessEnum("40011", "设备地址名称已存在", "err.device_address_exists")
	DeviceAddressNotExistsEnum   = NewBusinessEnum("40012", "设备地址不存在", "err.device_address_not_found")
	DeviceAddressDataTypeErrEnum = NewBusinessEnum("40013", "协议不支持该数据类型", "err.device_address_data_type")

	// 数据推送通道相关错误码
	PushChannelExistsEnum      = NewBusinessEnum("60001", "数据推送通道名称已存在", "err.push_channel_exists")
	PushChannelNotExistsEnum   = NewBusinessEnum("60002", "数据推送通道不存在", "err.push_channel_not_found")
	PushChannelConnectFailEnum = NewBusinessEnum("60003", "数据推送通道连通性测试失败", "err.push_channel_connect_fail")

	// 数据推送通道表单相关错误码
	PushChannelFormExistsEnum    = NewBusinessEnum("60011", "数据推送通道表单名称已存在", "err.push_channel_form_exists")
	PushChannelFormNotExistsEnum = NewBusinessEnum("60012", "数据推送通道表单不存在", "err.push_channel_form_not_found")
)
