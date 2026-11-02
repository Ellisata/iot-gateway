// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

// PreviewResult 载荷预览结果。
//
// 配置/编码失败作为 Error 字段返回，不抛业务错误：预览是「试跑」，
// 失败原因应当原样展示给配置者。
type PreviewResult struct {
	Payloads []string `json:"payloads"`
	Error    string   `json:"error,omitempty"`
}

// Previewer 可选接口：消息类通道用它实现「不发送、只看载荷」。
// 不放进 Channel 接口，避免破坏 TDengine/InfluxDB 与测试桩。
type Previewer interface {
	Preview(batch PushBatch) PreviewResult
}

// TestSendResult 测试发送结果。
type TestSendResult struct {
	Success    bool   `json:"success"`
	StatusCode int    `json:"statusCode,omitempty"`
	Messages   int    `json:"messages"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

// TestSender 可选接口：通道可以实现「真的发一条样例数据」。
type TestSender interface {
	TestSend(batch PushBatch) TestSendResult
}

// GroupSelector 可选接口：通道按采集组过滤是否接收某一批数据。
//
// 不放进 Channel 接口：时序库、OPC UA 等通道没有「订阅某几个组」的需求，
// 强制实现只会多一个永远返回 true 的方法。未实现本接口 = 接收全部组。
//
// 语义由实现方约定，但必须满足：空订阅列表 = 接收全部（含未分组记录）；
// 非空列表 = 只接收匹配组，未分组记录（groupID == ""）不接收。
type GroupSelector interface {
	AcceptsGroup(deviceID, groupID string) bool
}

// AcceptsGroupID 是 GroupSelector 的通用判据，供各消息类通道复用。
//
// 空列表（或只含空串）视为未配置订阅 = 接收全部；一旦有有效组 ID，
// 未分组记录（groupID == ""）就不再接收。
func AcceptsGroupID(ids []string, groupID string) bool {
	hasFilter := false
	for _, id := range ids {
		if id == "" {
			continue
		}
		hasFilter = true
		if id == groupID {
			return true
		}
	}
	return !hasFilter
}

// PreviewWithEncoder 用给定编码器试编码，返回每条消息的载荷文本。
func PreviewWithEncoder(enc Encoder, batch PushBatch) PreviewResult {
	if enc == nil {
		return PreviewResult{Error: "通道未配置编码器"}
	}
	msgs, err := enc.Encode(batch)
	if err != nil {
		return PreviewResult{Error: err.Error()}
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, string(m.Payload))
	}
	return PreviewResult{Payloads: out}
}
