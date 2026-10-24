// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

// EncodedMessage 编码器输出的一条待发送消息。
//
// 消息类通道（MQTT / HTTP / 未来的 Kafka）只需要字节；主题、方法、鉴权等
// 由通道配置决定。Sparkplug B 这类带会话状态的协议不套用本接口，单独实现通道。
type EncodedMessage struct {
	Payload []byte
}

// Encoder 把一批采集记录编码为一条或多条消息。
//
// native-json 恒为一条；mapping-json 的 emit=record 会返回多条。
type Encoder interface {
	Encode(batch PushBatch) ([]EncodedMessage, error)
}

// 载荷格式名。
const (
	FormatNativeJSON  = "native-json"
	FormatMappingJSON = "mapping-json"
	// FormatScript 用 goja 脚本生成载荷，是声明式映射表达不了时的逃生通道。
	FormatScript = "script"
)

// 载荷格式使用的编辑器。
const (
	EditorNone    = "none"
	EditorMapping = "mapping"
	EditorScript  = "script"
)

// FormatMeta 描述一种可选的载荷格式，供管理端渲染格式下拉框与编辑器。
type FormatMeta struct {
	Name            string `json:"name"`
	Label           string `json:"label"`
	RequiresMapping bool   `json:"requiresMapping"`
	// Editor 决定管理端用哪种编辑器编辑 mapping：none / mapping / script。
	// RequiresMapping 保留给旧版前端；新前端优先看 Editor。
	Editor string `json:"editor,omitempty"`
}

// SupportedFormats 返回消息类通道可选的载荷格式。
func SupportedFormats() []FormatMeta {
	return []FormatMeta{
		{Name: FormatNativeJSON, Label: "Native JSON", Editor: EditorNone},
		{Name: FormatMappingJSON, Label: "Mapping JSON", RequiresMapping: true, Editor: EditorMapping},
		{Name: FormatScript, Label: "Script", RequiresMapping: true, Editor: EditorScript},
	}
}
