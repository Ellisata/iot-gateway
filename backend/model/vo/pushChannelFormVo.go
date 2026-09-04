// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

import "encoding/json"

// PushChannelFormVO 数据推送通道表单响应
type PushChannelFormVO struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	FormJSON  json.RawMessage `json:"formJson"`
	CreatedAt string          `json:"createdAt"`
	UpdatedAt string          `json:"updatedAt"`
}
