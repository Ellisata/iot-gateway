// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package utils

import (
	"bytes"
	"encoding/json"
)

// PrettyPrint JSON 格式化输出
func PrettyPrint(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// ToJSON 将对象转换为 JSON 字符串
func ToJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// FromJSON 将 JSON 字符串解析为对象
func FromJSON(data string, v interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader([]byte(data)))
	decoder.UseNumber()
	return decoder.Decode(v)
}
