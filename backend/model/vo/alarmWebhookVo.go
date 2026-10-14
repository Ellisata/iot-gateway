// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

import "encoding/json"

// AlarmWebhookVO 报警 Webhook 响应。
//
// URL 含凭据（钉钉 access_token / 企微 key / 飞书 hook uuid），照 push_channel
// 返回 MQTT 口令的既有做法原样回显以便编辑；Secret（加签密钥）则**永不回显**，
// 只以 HasSecret 告知是否已配置。
type AlarmWebhookVO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	TypeName    string `json:"typeName"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Secret      string `json:"secret"`    // 恒为空串
	HasSecret   bool   `json:"hasSecret"` // 是否已配置密钥
	MsgType     string `json:"msgType"`
	AtAll       bool   `json:"atAll"`
	AtList      string `json:"atList"` // 逗号分隔，便于界面回显编辑
	Status      int    `json:"status"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// AlarmWebhookFormVO 报警 Webhook 表单响应
type AlarmWebhookFormVO struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	FormJSON  json.RawMessage `json:"formJson"`
	CreatedAt string          `json:"createdAt"`
	UpdatedAt string          `json:"updatedAt"`
}
