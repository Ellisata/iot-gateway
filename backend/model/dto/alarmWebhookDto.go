// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// ==================== 报警 Webhook 通知 DTO ====================

// CreateAlarmWebhookDTO 创建报警 Webhook 请求。
//
// type 的取值集合由 notify 包的注册表决定，故这里只做长度校验，
// 取值合法性在 service 层用 notify.ValidateConfig 判定（支持集只维护一处）。
type CreateAlarmWebhookDTO struct {
	Name        string `json:"name" binding:"required,min=1,max=100"`
	Type        string `json:"type" binding:"required,min=1,max=50"`
	Description string `json:"description" binding:"max=255"`
	// http_url 而非 url：后者会放过 javascript:/file: 等非 HTTP scheme
	URL     string `json:"url" binding:"required,http_url,max=500"`
	Secret  string `json:"secret" binding:"max=500"`
	MsgType string `json:"msgType" binding:"max=20"`
	AtAll   bool   `json:"atAll"`
	// AtList 逗号分隔的 @ 列表（钉钉为手机号、飞书为 open_id）。
	// 界面用的是普通文本框，故按字符串收发，入库前统一规整为 JSON 数组。
	AtList string `json:"atList" binding:"max=500"`
}

// UpdateAlarmWebhookDTO 更新报警 Webhook 请求（字段全部可选，零值不覆盖）
type UpdateAlarmWebhookDTO struct {
	ID          string `json:"id" binding:"required"`
	Name        string `json:"name" binding:"omitempty,min=1,max=100"`
	Type        string `json:"type" binding:"omitempty,max=50"`
	Description string `json:"description" binding:"max=255"`
	URL         string `json:"url" binding:"omitempty,http_url,max=500"`
	// Secret 用指针区分「不修改」(nil) 与「清空」("")：
	// 界面回显时不会有明文密钥，若用值类型则无法把「没填」与「清空」分开。
	Secret  *string `json:"secret" binding:"omitempty,max=500"`
	MsgType string  `json:"msgType" binding:"max=20"`
	AtAll   *bool   `json:"atAll"`
	AtList  *string `json:"atList" binding:"omitempty,max=500"`
	Status  *int    `json:"status"`
}

// PageAlarmWebhookDTO 报警 Webhook 分页查询
type PageAlarmWebhookDTO struct {
	Page   int    `form:"page" binding:"required,min=1"`
	Size   int    `form:"size" binding:"required,min=1,max=100"`
	Name   string `form:"name"`   // 名称（模糊查询）
	Type   string `form:"type"`   // dingtalk | wecom | feishu | custom
	Status *int   `form:"status"` // 1 启用 0 停用
}

// TestSendAlarmWebhookDTO 测试发送报警 Webhook 请求。
//
// ID 非空时以库中配置为基准，请求里的字段覆盖之（便于「改完先测再存」）；
// ID 为空则完全使用请求字段（便于「还没保存先测」）。
type TestSendAlarmWebhookDTO struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	URL     string `json:"url" binding:"omitempty,http_url,max=500"`
	Secret  string `json:"secret" binding:"max=500"`
	MsgType string `json:"msgType" binding:"max=20"`
	AtAll   *bool  `json:"atAll"`
	AtList  string `json:"atList" binding:"max=500"`
	// AlarmType/TargetType/TargetName 用于预览两种文案，缺省为断联报警 + 测试设备
	AlarmType  string `json:"alarmType"`  // offline | recover
	TargetType string `json:"targetType"` // device | channel
	TargetName string `json:"targetName"`
}

// ==================== 报警 Webhook 表单 DTO ====================

// CreateAlarmWebhookFormDTO 创建报警 Webhook 表单请求
type CreateAlarmWebhookFormDTO struct {
	Name     string          `json:"name" binding:"required,min=1,max=100"`
	FormJSON json.RawMessage `json:"formJson" binding:"required"`
}

// UpdateAlarmWebhookFormDTO 更新报警 Webhook 表单请求
type UpdateAlarmWebhookFormDTO struct {
	ID       string          `json:"id" binding:"required"`
	Name     string          `json:"name"`
	FormJSON json.RawMessage `json:"formJson"`
}

// GetAlarmWebhookFormByNameDTO 根据名称查询报警 Webhook 表单请求
type GetAlarmWebhookFormByNameDTO struct {
	Name string `form:"name" binding:"required,min=1,max=100"`
}
