// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// UpdateSystemConfigDTO 修改一个运行期配置项。
//
// value 用 json.RawMessage 而不是 any：类型由 key 决定（当前只有布尔），
// 先原样收下再交给 runtimecfg 严格解析。用 any 会让 gin 的 binding 把数字
// 与字符串都收进来，错误推迟到更后面才暴露，信息也更少。
type UpdateSystemConfigDTO struct {
	Key   string          `json:"key" binding:"required"`
	Value json.RawMessage `json:"value" binding:"required"`
}

// ResetSystemConfigDTO 删除一个配置项的覆盖值，恢复 default.yaml 的默认值。
type ResetSystemConfigDTO struct {
	Key string `json:"key" binding:"required"`
}
