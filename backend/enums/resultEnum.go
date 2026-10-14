// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

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
	// ProtocolNameImmutableEnum 协议名是与插件驱动的唯一纽带
	// （iot_protocol.name == driver.Register 名），改名会让引用它的设备静默停采。
	ProtocolNameImmutableEnum = NewBusinessEnum("50003", "协议名称不可修改", "err.protocol_name_immutable")
	// 以下两条是「恢复插件默认表单」的两种失败原因，分开是为了让用户知道
	// 该去找什么：前者说明插件文件不在（或被移除过），后者说明插件本身没带表单。
	ProtocolPluginAbsentEnum  = NewBusinessEnum("50004", "该协议当前没有已加载的插件提供", "err.protocol_plugin_absent")
	ProtocolNoDefaultFormEnum = NewBusinessEnum("50005", "该插件未为该协议声明默认表单", "err.protocol_no_default_form")

	// 设备地址相关错误码
	DeviceAddressExistsEnum      = NewBusinessEnum("40011", "设备地址名称已存在", "err.device_address_exists")
	DeviceAddressNotExistsEnum   = NewBusinessEnum("40012", "设备地址不存在", "err.device_address_not_found")
	DeviceAddressDataTypeErrEnum = NewBusinessEnum("40013", "协议不支持该数据类型", "err.device_address_data_type")

	// 局域网设备扫描发现相关错误码。
	//
	// 归在设备域而不是协议域：「该协议不支持扫描」虽然是协议能力问题，但用户
	// 视角是"我想发现设备"，且同域的 4000x 已覆盖设备的增删查测。放 5xxxx
	// 会让人以为这是协议配置的问题，从而去协议页找原因。
	DeviceScanDisabledEnum     = NewBusinessEnum("40021", "局域网扫描功能未启用", "err.device_scan_disabled")
	DeviceScanBusyEnum         = NewBusinessEnum("40022", "已有扫描任务在运行，请等待它结束或先取消", "err.device_scan_busy")
	DeviceScanJobNotFoundEnum  = NewBusinessEnum("40023", "扫描任务不存在或已过期", "err.device_scan_job_not_found")
	DeviceScanUnsupportedEnum  = NewBusinessEnum("40024", "该协议不支持局域网扫描", "err.device_scan_unsupported")
	DeviceScanNoTargetsEnum    = NewBusinessEnum("40025", "没有可扫描的目标", "err.device_scan_no_targets")
	DeviceScanParamInvalidEnum = NewBusinessEnum("40026", "扫描参数不合法", "err.device_scan_param_invalid")

	// 点位范围扫描相关错误码。
	//
	// 只新增这一个，其余全部复用上面设备扫描那一组：点扫与设备扫描是同一个页面、
	// 同一个总开关（deviceScan.enabled）上的两个动作，用户视角里"扫描没成功"是
	// 一回事 —— 为它再分一套码会让前端的错误处理分裂成两份。
	//
	// 唯一必须分开的是"该协议不支持点位扫描"：它与 40024（不支持局域网扫描）
	// 的下一步动作完全不同（要去看该协议的 PointDiscovery 声明，而不是换插件），
	// 混用会让用户去错误的地方找原因。
	DevicePointScanUnsupportedEnum = NewBusinessEnum("40027", "该协议不支持点位扫描", "err.device_point_scan_unsupported")

	// 数据推送通道相关错误码
	PushChannelExistsEnum      = NewBusinessEnum("60001", "数据推送通道名称已存在", "err.push_channel_exists")
	PushChannelNotExistsEnum   = NewBusinessEnum("60002", "数据推送通道不存在", "err.push_channel_not_found")
	PushChannelConnectFailEnum = NewBusinessEnum("60003", "数据推送通道连通性测试失败", "err.push_channel_connect_fail")

	// 数据推送通道表单相关错误码
	PushChannelFormExistsEnum    = NewBusinessEnum("60011", "数据推送通道表单名称已存在", "err.push_channel_form_exists")
	PushChannelFormNotExistsEnum = NewBusinessEnum("60012", "数据推送通道表单不存在", "err.push_channel_form_not_found")

	// 开放接口密钥相关错误码
	OpenApiSecretExistsEnum    = NewBusinessEnum("70001", "开放接口密钥名称已存在", "err.open_api_secret_exists")
	OpenApiSecretNotExistsEnum = NewBusinessEnum("70002", "开放接口密钥不存在", "err.open_api_secret_not_found")
	OpenApiSecretKeyEmptyEnum  = NewBusinessEnum("70003", "开放接口密钥为空", "err.open_api_secret_key_empty")
	OpenApiSecretInvalidEnum   = NewBusinessEnum("70004", "开放接口密钥无效", "err.open_api_secret_invalid")
	// 默认密钥（名 default）是启动播种的、随局域网广播发布的内置凭据，
	// 删除会让已发现本网关的管理平台集体失效，改名则让删除守卫失去判据。
	OpenApiSecretBuiltinEnum = NewBusinessEnum("70005", "默认密钥不允许删除或重命名", "err.open_api_secret_builtin")

	// 网关自身身份相关错误码
	GatewayNotExistsEnum = NewBusinessEnum("80001", "网关身份不存在", "err.gateway_not_exists")

	// 协议插件上传相关错误码
	PluginDisabledEnum    = NewBusinessEnum("90001", "插件功能未启用，上传的插件不会被加载", "err.plugin_disabled")
	PluginNoFileEnum      = NewBusinessEnum("90002", "未接收到上传的插件文件", "err.plugin_no_file")
	PluginTooManyEnum     = NewBusinessEnum("90003", "单次上传的插件文件数量超限", "err.plugin_too_many")
	PluginDuplicateEnum   = NewBusinessEnum("90004", "同一批次中存在重名的插件文件", "err.plugin_duplicate")
	PluginDirInvalidEnum  = NewBusinessEnum("90005", "插件目录不可用", "err.plugin_dir_invalid")
	PluginInstallFailEnum = NewBusinessEnum("90006", "插件安装失败", "err.plugin_install_fail")

	// 报警通知 Webhook 相关错误码（钉钉 / 企微 / 飞书 / 自定义）
	AlarmWebhookExistsEnum        = NewBusinessEnum("95001", "报警通知名称已存在", "err.alarm_webhook_exists")
	AlarmWebhookNotExistsEnum     = NewBusinessEnum("95002", "报警通知不存在", "err.alarm_webhook_not_found")
	AlarmWebhookConfigInvalidEnum = NewBusinessEnum("95003", "报警通知配置不合法", "err.alarm_webhook_config_invalid")
	AlarmWebhookTestSendFailEnum  = NewBusinessEnum("95004", "报警通知测试发送失败", "err.alarm_webhook_test_send_fail")
	AlarmWebhookFormExistsEnum    = NewBusinessEnum("95005", "报警通知表单名称已存在", "err.alarm_webhook_form_exists")
	AlarmWebhookFormNotExistsEnum = NewBusinessEnum("95006", "报警通知表单不存在", "err.alarm_webhook_form_not_found")
)
