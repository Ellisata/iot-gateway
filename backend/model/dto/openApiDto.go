// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// OpenApiDeviceNamesDTO 开放接口批量查询设备名称请求（JSON Body 传设备ID数组）
type OpenApiDeviceNamesDTO struct {
	IDs []string `json:"ids" binding:"required,min=1"`
}

// OpenApiAddressLabelsDTO 开放接口批量查询点位标签请求（JSON Body 传设备ID数组 + 点位ID数组，
// 设备ID作归属校验：不属于这些设备的点位视为不存在）
type OpenApiAddressLabelsDTO struct {
	DeviceIDs  []string `json:"deviceIds" binding:"required,min=1"`
	AddressIDs []string `json:"addressIds" binding:"required,min=1"`
}

// OpenApiSyncDevicePointsDTO 开放接口「设备 + 点位」游标分批同步请求。
//
// 用游标而非 page/size：同步是长任务，翻页途中若有人增删设备/点位，
// OFFSET 会整体位移，导致漏读或重读某些行且不报任何错。设备 id 是 UUIDv7
// （长度固定的 32 位 hex，字典序即时间序），故 after = 上一页最后一条设备 id 即可稳定续读。
//
// After 为空表示从头开始；Size 沿用其它分页接口的 1~100。
type OpenApiSyncDevicePointsDTO struct {
	After string `form:"after"` // 游标：上一页响应里的 nextAfter；首次不传
	Size  int    `form:"size" binding:"required,min=1,max=100"`
}

// OpenApiSyncSubscriptionDevicePointsDTO 开放接口「订阅设备 + 点位」游标分批同步请求。
//
// 与 OpenApiSyncDevicePointsDTO 同形：订阅设备/点位表也是按 (device_id,
// device_address_id) 稳定排序的，游标只到设备粒度 —— 一页拿一批设备及其全部点位，
// 翻页途中新增/删除点位不会让未读区间整体位移。
//
// After 为空表示从头开始；Size 沿用 1~100。
type OpenApiSyncSubscriptionDevicePointsDTO struct {
	After string `form:"after"` // 游标：上一页响应里的 nextAfter；首次不传
	Size  int    `form:"size" binding:"required,min=1,max=100"`
}

// OpenApiThingModelBatchUpsertDTO 开放接口物模型与属性批量 upsert 请求。
//
// 只约束顶层批次大小（1~100）。**不要给嵌套字段加 dive 校验**：那会让一条坏数据
// 把整批打成顶层 PARAM_ERROR，破坏「跨模型部分成功」的契约 —— 逐条校验在服务层做。
type OpenApiThingModelBatchUpsertDTO struct {
	Models []OpenApiThingModelUpsertDTO `json:"models" binding:"required,min=1,max=100"`
}

// OpenApiThingModelUpsertDTO 单个物模型的 upsert 条目。
//
// Code 是自然键：不存在则创建，存在则更新；创建后不可修改。
// 可选字段用指针区分「省略」与「显式传空」：省略表示更新时保持原值、创建时取默认值；
// 显式传空串则清空（Category 空串即「未分类」）。
type OpenApiThingModelUpsertDTO struct {
	Code        string                               `json:"code"`
	Name        string                               `json:"name"`
	Category    *string                              `json:"category"`
	Version     *string                              `json:"version"`
	Description *string                              `json:"description"`
	Status      *int                                 `json:"status"`
	Properties  []OpenApiThingModelPropertyUpsertDTO `json:"properties"`
	// ReplaceProperties 为 true 时，删除请求中未出现的属性（受点位绑定保护）。
	// 默认 false，保持 4.13 既有「只增不删」契约。
	ReplaceProperties *bool `json:"replaceProperties"`
}

// OpenApiThingModelPropertyUpsertDTO 单个物模型属性的 upsert 条目。
//
// Identifier 在模型内是自然键：不存在则创建，存在则更新；创建后不可修改。
// Kind / AccessMode 在新建时必填；更新时省略表示保持原值（被点位绑定后改变会拒绝）。
type OpenApiThingModelPropertyUpsertDTO struct {
	Identifier  string  `json:"identifier"`
	Name        string  `json:"name"`
	Kind        *string `json:"kind"`
	AccessMode  *string `json:"accessMode"`
	Unit        *string `json:"unit"`
	Description *string `json:"description"`
	SortOrder   *int    `json:"sortOrder"`
	Status      *int    `json:"status"`
}

// OpenApiThingModelBatchDeleteDTO 开放接口物模型批量删除请求。
//
// 按模型标识（code）删除；模型下的属性随模型一起删除。已被设备绑定、或其属性
// 已被点位绑定的模型会被拒绝（41005），逐条结果互不影响。
type OpenApiThingModelBatchDeleteDTO struct {
	Codes []string `json:"codes" binding:"required,min=1,max=200"`
}

// OpenApiThingModelPropertyBatchDeleteDTO 开放接口物模型属性批量删除请求。
//
// 属性按（模型标识 + 属性标识符）定位：这两个字段共同构成属性对外可见的自然键。
// 已被点位绑定的属性会被拒绝（41006），逐条结果互不影响。
type OpenApiThingModelPropertyBatchDeleteDTO struct {
	Items []OpenApiThingModelPropertyDeleteItemDTO `json:"items" binding:"required,min=1,max=200"`
}

// OpenApiThingModelPropertyDeleteItemDTO 单条属性删除定位。
type OpenApiThingModelPropertyDeleteItemDTO struct {
	ModelCode  string `json:"modelCode"`
	Identifier string `json:"identifier"`
}
