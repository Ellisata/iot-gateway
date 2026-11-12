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
// Code 创建后不可修改：它是导入导出与外部引用的稳定键。请求里可以省略，
// 传了就必须与原值一致；不一致会被服务层拒绝。
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
// 属性总是挂在某个模型下，modelId 必填；要全量取请用 listThingModelProperties
// （设备点位绑定下拉仍走那个接口，分页参数只在这里生效）。
type PageThingModelPropertyDTO struct {
	ModelID string `form:"modelId" binding:"required"`
	Page    int    `form:"page" binding:"required,min=1"`
	Size    int    `form:"size" binding:"required,min=1,max=100"`
}

// CreateThingModelPropertyDTO 创建物模型属性请求。
//
// kind 只取 driver.KindXxx 六类；accessMode 与 device_address.rw_permission
// 同枚举。identifier 在模型内唯一，创建后不可修改。
type CreateThingModelPropertyDTO struct {
	ModelID     string `json:"modelId" binding:"required"`
	Identifier  string `json:"identifier" binding:"required,min=1,max=64"`
	Name        string `json:"name" binding:"required,min=1,max=100"`
	Kind        string `json:"kind" binding:"required,oneof=bool int uint float string time"`
	AccessMode  string `json:"accessMode" binding:"required,oneof=R W RW"`
	Unit        string `json:"unit" binding:"omitempty,max=32"`
	Description string `json:"description" binding:"omitempty,max=500"`
	SortOrder   int    `json:"sortOrder" binding:"omitempty,min=0,max=99999"`
}

// UpdateThingModelPropertyDTO 更新物模型属性请求。
//
// Identifier 可省略；传了必须与原值一致（创建后不可改）。属性不允许在模型间移动。
type UpdateThingModelPropertyDTO struct {
	ID          string `json:"id" binding:"required"`
	Identifier  string `json:"identifier" binding:"omitempty,max=64"`
	Name        string `json:"name" binding:"required,min=1,max=100"`
	Kind        string `json:"kind" binding:"required,oneof=bool int uint float string time"`
	AccessMode  string `json:"accessMode" binding:"required,oneof=R W RW"`
	Unit        string `json:"unit" binding:"omitempty,max=32"`
	Description string `json:"description" binding:"omitempty,max=500"`
	SortOrder   int    `json:"sortOrder" binding:"omitempty,min=0,max=99999"`
	Status      *int   `json:"status" binding:"omitempty,oneof=0 1"`
}
