// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// ==================== 物模型 DTO ====================

// CreateThingModelDTO 创建物模型请求。
//
// Category 的空串表示“未分类”；其余取值必须是 constants.ThingModelCategoryCodes
// 中的固定 code。这里的 binding 覆盖 JSON 入口，Excel/脚本等旁路仍需服务层校验。
type CreateThingModelDTO struct {
	Code        string `json:"code" binding:"required,min=1,max=64"`
	Name        string `json:"name" binding:"required,min=1,max=100"`
	Category    string `json:"category" binding:"omitempty,oneof=sensor meter actuator robot plc other"`
	Version     string `json:"version" binding:"omitempty,max=32"`
	Description string `json:"description" binding:"omitempty,max=500"`
}

// UpdateThingModelDTO 更新物模型请求。
//
// Code 可以改名，省略则保持原值，传了就必须全表唯一。它仍是导入导出与外部
// 引用的稳定键：改名后按旧标识配置的外部引用需同步更新。
//
// Category 不在这里做 oneof：开放接口可能已下发平台自定义分类，本地管理端
// 必须能保留它（例如只改名称）。服务层会放行“与原值相同”的分类，但新增或
// 改挂仍只接受内置分类，避免本地随意制造新 code。
type UpdateThingModelDTO struct {
	ID          string `json:"id" binding:"required"`
	Code        string `json:"code" binding:"omitempty,max=64"`
	Name        string `json:"name" binding:"required,min=1,max=100"`
	Category    string `json:"category" binding:"omitempty,max=32"`
	Version     string `json:"version" binding:"omitempty,max=32"`
	Description string `json:"description" binding:"omitempty,max=500"`
	Status      *int   `json:"status" binding:"omitempty,oneof=0 1"`
}

// CopyThingModelDTO 复制物模型请求。
//
// 只按 id 复制：副本的字段全部来自库里的原行，不接受调用方覆盖。
type CopyThingModelDTO struct {
	ID string `json:"id" binding:"required"`
}

// PageThingModelDTO 物模型分页查询。
// Keyword 同时模糊匹配 code 与 name，避免为两个字段各做一个搜索框。
type PageThingModelDTO struct {
	Page     int    `form:"page" binding:"required,min=1"`
	Size     int    `form:"size" binding:"required,min=1,max=100"`
	Keyword  string `form:"keyword"`
	Category string `form:"category"`
}

// ==================== 物模型属性 DTO ====================

// PageThingModelPropertyDTO 物模型属性分页查询。
//
// 属性总是挂在某个模型下，modelId 必填；要全量取请用 listThingModelProperties，
// 要跨模型取（设备点位绑定下拉）请用 listAllThingModelProperties。
type PageThingModelPropertyDTO struct {
	ModelID string `form:"modelId" binding:"required"`
	Page    int    `form:"page" binding:"required,min=1"`
	Size    int    `form:"size" binding:"required,min=1,max=100"`
	// Keyword 同时模糊匹配属性标识符与名称；空串 = 不过滤。
	Keyword string `form:"keyword"`
}

// PageThingModelPointDeviceDTO 属性绑定点位时的设备候选分页。
//
// 属性绑定点位是两级选择（先设备、后点位），设备候选只返回「至少有一个点位」
// 的设备：没有点位的设备在这里选了也没有可绑对象。
type PageThingModelPointDeviceDTO struct {
	Page    int    `form:"page" binding:"required,min=1"`
	Size    int    `form:"size" binding:"required,min=1,max=100"`
	Keyword string `form:"keyword"` // 同时模糊匹配设备名称与编码；空串 = 全部
}

// PageThingModelPointOptionDTO 属性绑定点位的点位候选分页（按设备）。
type PageThingModelPointOptionDTO struct {
	DeviceID string `form:"deviceId" binding:"required"`
	Page     int    `form:"page" binding:"required,min=1"`
	Size     int    `form:"size" binding:"required,min=1,max=100"`
	Keyword  string `form:"keyword"` // 同时模糊匹配地址名与说明；空串 = 全部
}

// CreateThingModelPropertyDTO 创建物模型属性请求。
//
// kind 只作为语义说明（可空 = 未指定），传了只取 driver.KindXxx 六类；accessMode
// 与 device_address.rw_permission 同枚举。identifier 在模型内唯一，创建后可改
// （改名后下游按旧标识符配置的引用需同步更新）。
type CreateThingModelPropertyDTO struct {
	ModelID     string `json:"modelId" binding:"required"`
	Identifier  string `json:"identifier" binding:"required,min=1,max=64"`
	Name        string `json:"name" binding:"required,min=1,max=100"`
	Kind        string `json:"kind" binding:"omitempty,oneof=bool int uint float string time"`
	AccessMode  string `json:"accessMode" binding:"required,oneof=R W RW"`
	Unit        string `json:"unit" binding:"omitempty,max=32"`
	Description string `json:"description" binding:"omitempty,max=500"`
	SortOrder   int    `json:"sortOrder" binding:"omitempty,min=0,max=99999"`
	// AddressIDs 创建时一并绑定的设备点位（写 device_address.property_id）。
	// 属性与点位是 1 对 1，最多一项；可来自任意设备，不要求设备与模型存在绑定关系。
	AddressIDs []string `json:"addressIds" binding:"omitempty,max=1"`
}

// UpdateThingModelPropertyDTO 更新物模型属性请求。
//
// Identifier 可省略（省略 = 不改）；传了必须符合格式且在模型内唯一。属性不允许在模型间移动。
type UpdateThingModelPropertyDTO struct {
	ID          string `json:"id" binding:"required"`
	Identifier  string `json:"identifier" binding:"omitempty,max=64"`
	Name        string `json:"name" binding:"required,min=1,max=100"`
	Kind        string `json:"kind" binding:"omitempty,oneof=bool int uint float string time"`
	AccessMode  string `json:"accessMode" binding:"required,oneof=R W RW"`
	Unit        string `json:"unit" binding:"omitempty,max=32"`
	Description string `json:"description" binding:"omitempty,max=500"`
	SortOrder   int    `json:"sortOrder" binding:"omitempty,min=0,max=99999"`
	Status      *int   `json:"status" binding:"omitempty,oneof=0 1"`
	// AddressIDs 点位绑定的三态同步：省略 = 不改；空数组 = 解绑；
	// 非空 = 以该列表为准（属性与点位 1 对 1，最多一项）。
	// 解绑只清 property_id，不删除点位行。
	AddressIDs *[]string `json:"addressIds" binding:"omitempty,max=1"`
}
