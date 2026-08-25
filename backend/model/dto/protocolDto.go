package dto

import "encoding/json"

// ==================== 协议 DTO ====================

// CreateProtocolDTO 创建协议请求
type CreateProtocolDTO struct {
	Name        string          `json:"name" binding:"required,min=1,max=100"`
	Description string          `json:"description"`
	FormJSON    json.RawMessage `json:"formJson"`
	Sort        int             `json:"sort"`
}

// UpdateProtocolDTO 更新协议请求
type UpdateProtocolDTO struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	FormJSON    json.RawMessage `json:"formJson"`
	Sort        *int            `json:"sort"`
	Status      *int            `json:"status"`
}

// UpdateProtocolFormByNameDTO 根据协议名称更新表单 JSON
type UpdateProtocolFormByNameDTO struct {
	Name     string          `json:"name" binding:"required,min=1,max=100"`
	FormJSON json.RawMessage `json:"formJson" binding:"required"`
}

// UpdateProtocolFormByIdDTO 根据协议ID更新表单 JSON
type UpdateProtocolFormByIdDTO struct {
	ID       string          `json:"id" binding:"required"`
	FormJSON json.RawMessage `json:"formJson" binding:"required"`
}

// PageProtocolDTO 协议分页查询
type PageProtocolDTO struct {
	Page int    `form:"page" binding:"required,min=1"`
	Size int    `form:"size" binding:"required,min=1,max=100"`
	Name string `form:"name"` // 协议名称（模糊查询）
}
