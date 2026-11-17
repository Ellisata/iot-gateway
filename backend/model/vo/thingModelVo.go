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

// ThingModelDeviceOptionVO 物模型绑定设备下拉选项。
//
// Locked 表示该设备已有点位绑定属性：此时改绑/解绑设备会被服务层以 41013
// 拒绝，前端据此置灰并说明原因。
type ThingModelDeviceOptionVO struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	ModelID   string `json:"modelId"`
	ModelName string `json:"modelName"`
	Locked    bool   `json:"locked"`
	Status    int    `json:"status"`
}

// ThingModelBoundDeviceVO 物模型列表「设备数」抽屉里的已绑定设备。
//
// 与设备候选分开：这里只回答“谁绑了我”，不需要可选性判断，
// 但需要协议名，方便运维认出设备类型。
type ThingModelBoundDeviceVO struct {
	ID           string `json:"id"`
	Code         string `json:"code"`
	Name         string `json:"name"`
	ProtocolID   string `json:"protocolId"`
	ProtocolName string `json:"protocolName"`
	Status       int    `json:"status"`
}

// ThingModelDeviceOptionPageVO 设备候选的远程搜索结果。
//
// Keyword 搜索有上限（见 service.thingModelDeviceOptionLimit）；命中更多时
// Truncated 为 true，前端据此提示“继续输入更精确的关键字”。不分页是刻意的：
// 下拉候选只服务于“找到并勾选”，翻页会把选择动作切成两段。
type ThingModelDeviceOptionPageVO struct {
	Records   []ThingModelDeviceOptionVO `json:"records"`
	Truncated bool                       `json:"truncated"`
}

// ThingModelPointOptionVO 属性绑定点位下拉选项。
//
// Kind 由设备协议名 + 点位内部数据类型经 driver 类型注册表推导；协议未加载
// 或类型无法识别时为空串（前端据此禁用，因为无法自证与属性兼容）。
// PropertyID 非空表示该点位已绑定属性；不等于当前属性时不能被抢绑。
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
