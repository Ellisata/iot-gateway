// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// 物模型批量 upsert 的逐条动作。失败也是逐条数据，不影响顶层响应信封。
const (
	ThingModelUpsertActionCreated = "created"
	ThingModelUpsertActionUpdated = "updated"
	ThingModelUpsertActionFailed  = "failed"
)

// OpenApiThingModelBatchUpsertVO 开放接口物模型与属性批量 upsert 响应。
//
// Total/Success/Failed 以**模型**为单位；PropertiesCreated/PropertiesUpdated
// 只统计提交成功模型里实际落库的属性。逐条结果在 Models 中按请求顺序排列。
type OpenApiThingModelBatchUpsertVO struct {
	Total             int                             `json:"total"`
	Success           int                             `json:"success"`
	Failed            int                             `json:"failed"`
	Created           int                             `json:"created"`
	Updated           int                             `json:"updated"`
	PropertiesCreated int                             `json:"propertiesCreated"`
	PropertiesUpdated int                             `json:"propertiesUpdated"`
	Models            []OpenApiThingModelUpsertItemVO `json:"models"`
}

// OpenApiThingModelUpsertItemVO 单个模型的 upsert 结果。
//
// Action 为 created / updated / failed。失败时 ErrorCode/ErrorMessage 给出原因，
// Properties 至多包含一条失败属性的明细；模型级失败（如标识非法、名称冲突）时为空数组。
type OpenApiThingModelUpsertItemVO struct {
	Code         string                                  `json:"code"`
	Name         string                                  `json:"name"`
	Action       string                                  `json:"action"`
	ID           string                                  `json:"id,omitempty"`
	ErrorCode    string                                  `json:"errorCode,omitempty"`
	ErrorMessage string                                  `json:"errorMessage,omitempty"`
	Properties   []OpenApiThingModelPropertyUpsertItemVO `json:"properties"`
}

// OpenApiThingModelPropertyUpsertItemVO 单个属性的 upsert 结果。
type OpenApiThingModelPropertyUpsertItemVO struct {
	Identifier   string `json:"identifier"`
	Action       string `json:"action"`
	ID           string `json:"id,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}
