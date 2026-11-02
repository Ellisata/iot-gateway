// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// ThingModelVO 物模型响应。
//
// PropertyCount / DeviceCount 是列表页需要的派生计数，不落库：前者是模型下
// 属性数，后者是绑定到该模型的设备数（device.model_id）。
type ThingModelVO struct {
	ID            string `json:"id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Version       string `json:"version"`
	Description   string `json:"description"`
	Status        int    `json:"status"`
	PropertyCount int64  `json:"propertyCount"`
	DeviceCount   int64  `json:"deviceCount"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

// ThingModelOptionVO 物模型下拉选项（设备绑定用）。
type ThingModelOptionVO struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Status int    `json:"status"`
}

// ThingModelPropertyPageVO 物模型属性分页响应。
//
// 没有复用 response.PageVo 是因为多带一个 MaxSortOrder：属性列表分页后前端
// 拿不到全量列表，新增属性时无法再按「当前最大值 + 10」取默认排序（取到的只是
// 当前页的最大值，会插到中间），故由服务端直接给出该模型内的最大排序值。
type ThingModelPropertyPageVO struct {
	Page         int                    `json:"page"`
	Size         int                    `json:"size"`
	Total        int64                  `json:"total"`
	Records      []ThingModelPropertyVO `json:"records"`
	MaxSortOrder int                    `json:"maxSortOrder"`
}

// ThingModelPropertyVO 物模型属性响应。
type ThingModelPropertyVO struct {
	ID          string `json:"id"`
	ModelID     string `json:"modelId"`
	Identifier  string `json:"identifier"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	AccessMode  string `json:"accessMode"`
	Unit        string `json:"unit"`
	Description string `json:"description"`
	SortOrder   int    `json:"sortOrder"`
	Status      int    `json:"status"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}
