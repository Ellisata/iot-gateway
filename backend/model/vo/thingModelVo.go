// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// ThingModelVO 物模型响应。
//
// PropertyCount 是列表页需要的派生计数，不落库：模型下的属性数。
type ThingModelVO struct {
	ID            string `json:"id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Version       string `json:"version"`
	Description   string `json:"description"`
	Status        int    `json:"status"`
	PropertyCount int64  `json:"propertyCount"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

// ThingModelOptionVO 物模型下拉选项。
type ThingModelOptionVO struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Status int    `json:"status"`
}

// ThingModelPropertyOptionVO 跨模型的属性下拉选项（设备点位绑定属性用）。
//
// 点位可直接绑定任意模型下的属性，因此下拉必须带上模型标识与名称，
// 否则不同模型里同名的 identifier 在选项里无法区分。
type ThingModelPropertyOptionVO struct {
	ID         string `json:"id"`
	ModelID    string `json:"modelId"`
	ModelCode  string `json:"modelCode"`
	ModelName  string `json:"modelName"`
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	AccessMode string `json:"accessMode"`
	Unit       string `json:"unit"`
	Status     int    `json:"status"`
}

// ThingModelPointOptionVO 属性绑定点位下拉选项。
//
// Kind 由设备协议名 + 点位内部数据类型经 driver 类型注册表推导；协议未加载
// 或类型无法识别时为空串（前端据此禁用，因为无法自证与属性兼容）。
// PropertyID 非空表示该点位已绑定属性；不等于当前属性时不能被抢绑。
// 属性标识只在物模型内唯一，因此带上 ModelName：候选列表里不同模型可以有
// 同名属性，只显示属性名会让「已绑定属性」指向不明。
type ThingModelPointOptionVO struct {
	AddressID          string `json:"addressId"`
	DeviceID           string `json:"deviceId"`
	DeviceCode         string `json:"deviceCode"`
	DeviceName         string `json:"deviceName"`
	Name               string `json:"name"`
	Label              string `json:"label"`
	CommonDataType     string `json:"commonDataType"`
	DataType           string `json:"dataType"`
	Kind               string `json:"kind"`
	RwPermission       string `json:"rwPermission"`
	PropertyID         string `json:"propertyId"`
	PropertyIdentifier string `json:"propertyIdentifier"`
	PropertyName       string `json:"propertyName"`
	ModelID            string `json:"modelId"`
	ModelName          string `json:"modelName"`
	Status             int    `json:"status"`
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
	// PointCount 绑定到该属性的设备点位数（device_address.property_id 计数）。
	PointCount int64  `json:"pointCount"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// ThingModelPropertyPointVO 绑定到某属性的设备点位（属性抽屉「点位数」用）。
type ThingModelPropertyPointVO struct {
	AddressID      string `json:"addressId"`
	DeviceID       string `json:"deviceId"`
	DeviceCode     string `json:"deviceCode"`
	DeviceName     string `json:"deviceName"`
	Name           string `json:"name"`
	Label          string `json:"label"`
	CommonDataType string `json:"commonDataType"`
	DataType       string `json:"dataType"`
	RwPermission   string `json:"rwPermission"`
	Status         int    `json:"status"`
}
