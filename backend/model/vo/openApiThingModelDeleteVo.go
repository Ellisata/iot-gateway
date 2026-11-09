// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// 物模型批量删除的逐条动作。
const (
	ThingModelDeleteActionDeleted = "deleted"
	ThingModelDeleteActionFailed  = "failed"
)

// OpenApiThingModelBatchDeleteVO 开放接口物模型批量删除响应。
//
// Requested/Deleted/Failed 以**模型**为单位。删除模型会连带删除其属性；
// 已被设备绑定、或其属性已被点位绑定的模型会失败（41005），其余模型继续。
type OpenApiThingModelBatchDeleteVO struct {
	Requested int                             `json:"requested"`
	Deleted   int                             `json:"deleted"`
	Failed    int                             `json:"failed"`
	Results   []OpenApiThingModelDeleteItemVO `json:"results"`
}

// OpenApiThingModelDeleteItemVO 单个模型的删除结果。
type OpenApiThingModelDeleteItemVO struct {
	Code         string `json:"code"`
	Action       string `json:"action"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// OpenApiThingModelPropertyBatchDeleteVO 开放接口物模型属性批量删除响应。
type OpenApiThingModelPropertyBatchDeleteVO struct {
	Requested int                                     `json:"requested"`
	Deleted   int                                     `json:"deleted"`
	Failed    int                                     `json:"failed"`
	Results   []OpenApiThingModelPropertyDeleteItemVO `json:"results"`
}

// OpenApiThingModelPropertyDeleteItemVO 单条属性的删除结果。
type OpenApiThingModelPropertyDeleteItemVO struct {
	ModelCode    string `json:"modelCode"`
	Identifier   string `json:"identifier"`
	Action       string `json:"action"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}
