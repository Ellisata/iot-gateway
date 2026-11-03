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

// PushRecentMessagesVO 某个通道最近推送的报文（新→旧）。
//
// Active 表示该通道当前是否在推送引擎里。未启用或正在热加载时用它区分，
// 比返回「通道不存在」更准确；前端据此显示「通道当前未运行」。
type PushRecentMessagesVO struct {
	Active   bool                  `json:"active"`
	Messages []PushRecentMessageVO `json:"messages"`
}

// PushRecentMessageVO 一条最近推送的报文。
//
// Payload 是预览文本（最多 2 KiB），不是完整载荷；Size 是原始字节数。
// StatusCode 仅 HTTP/Webhook 类通道有意义，MQTT 恒为 0。
type PushRecentMessageVO struct {
	Seq        uint64 `json:"seq"`
	Time       string `json:"time"`
	Target     string `json:"target"`
	DeviceCode string `json:"deviceCode"`
	GroupCode  string `json:"groupCode"`
	Records    int    `json:"records"`
	StatusCode int    `json:"statusCode"`
	DurationMs int64  `json:"durationMs"`
	Size       int    `json:"size"`
	Payload    string `json:"payload"`
	Truncated  bool   `json:"truncated"`
	Error      string `json:"error"`
	FromDrain  bool   `json:"fromDrain"`
}

// PushChannelTestSendVO 测试发送结果。
type PushChannelTestSendVO struct {
	Success    bool   `json:"success"`
	StatusCode int    `json:"statusCode,omitempty"`
	Messages   int    `json:"messages"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}
