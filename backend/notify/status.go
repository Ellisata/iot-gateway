// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

// WebhookStatusVO 单个 webhook 的运行健康状况。
type WebhookStatusVO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	QueueDepth      uint64 `json:"queueDepth"`      // 待发送事件数
	SentCount       uint64 `json:"sentCount"`       // 成功投递次数
	FailedCount     uint64 `json:"failedCount"`     // 重试耗尽/永久失败的次数
	DroppedCount    uint64 `json:"droppedCount"`    // 队列满或停机排空超时被丢弃的事件数
	LastSentTime    string `json:"lastSentTime"`    // 最近一次投递尝试时刻
	LastSuccessTime string `json:"lastSuccessTime"` // 最近一次投递成功时刻
	LastErr         string `json:"error"`
}

// DispatcherStatus 报警通知器整体运行状态。
type DispatcherStatus struct {
	Running        bool              `json:"running"`
	IngressDepth   int               `json:"ingressDepth"`   // 入口队列积压
	IngressDropped uint64            `json:"ingressDropped"` // 入口队列满导致丢弃的事件数
	Webhooks       []WebhookStatusVO `json:"webhooks"`
}
