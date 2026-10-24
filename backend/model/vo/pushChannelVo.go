// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

import "encoding/json"

// PushChannelVO 数据推送通道响应
type PushChannelVO struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	ConfigJSON  json.RawMessage `json:"configJson"`
	Status      int             `json:"status"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
}

// PushChannelPreviewVO 出站载荷预览结果。
// 转换失败作为 Error 字段返回，不作为业务错误。
type PushChannelPreviewVO struct {
	Payload  string   `json:"payload,omitempty"`
	Payloads []string `json:"payloads,omitempty"`
	Count    int      `json:"count"`
	Error    string   `json:"error,omitempty"`
}

// PushChannelTestSendVO 测试发送结果。
type PushChannelTestSendVO struct {
	Success    bool   `json:"success"`
	StatusCode int    `json:"statusCode,omitempty"`
	Messages   int    `json:"messages"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}
