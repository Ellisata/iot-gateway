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
