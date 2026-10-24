// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package codec 按名称构建消息类推送通道的载荷编码器。
package codec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"iot-gateway/push"
	"iot-gateway/push/codec/mappingjson"
	"iot-gateway/push/codec/nativejson"
	"iot-gateway/push/codec/scriptpayload"
)

// New 根据 format 与 mapping 构建编码器。
//
// format 为空时使用 native-json，保证历史配置行为不变。
func New(format string, mapping json.RawMessage) (push.Encoder, error) {
	name := strings.ToLower(strings.TrimSpace(format))
	if name == "" {
		name = push.FormatNativeJSON
	}

	switch name {
	case push.FormatNativeJSON:
		if !blankJSON(mapping) {
			return nil, fmt.Errorf("native-json 不接受 mapping 配置")
		}
		return nativejson.New(), nil
	case push.FormatMappingJSON:
		return mappingjson.New(mapping)
	case push.FormatScript:
		return scriptpayload.New(mapping)
	default:
		return nil, fmt.Errorf("不支持的载荷格式 %q", format)
	}
}

// Validate 只校验配置，不保留编码器。
func Validate(format string, mapping json.RawMessage) error {
	_, err := New(format, mapping)
	return err
}

func blankJSON(raw json.RawMessage) bool {
	b := bytes.TrimSpace(raw)
	return len(b) == 0 || bytes.Equal(b, []byte("null"))
}
