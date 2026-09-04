// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/driver"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
)

// ProtocolService 协议服务
type ProtocolService struct {
	sqliteDB *gorm.DB
}

// NewProtocolService 创建协议服务
func NewProtocolService(sqliteDB *gorm.DB) *ProtocolService {
	return &ProtocolService{sqliteDB: sqliteDB}
}

// CreateProtocol 创建协议
func (s *ProtocolService) CreateProtocol(ctx context.Context, req *dto.CreateProtocolDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 检查名称是否已存在
		var existing po.IotProtocol
		if err := tx.Where("name = ?", req.Name).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if existing.ID != "" {
			return appError.NewAppErrorCtx(enums.ProtocolExistsEnum.GetCode(), enums.ProtocolExistsEnum.GetMessage(), enums.ProtocolExistsEnum.GetMsgKey())
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		sort := req.Sort
		if sort == 0 {
			sort = 1
		}
		formJson := "{}"
		if req.FormJSON != nil {
			formJson = string(req.FormJSON)
		}
		protocol := &po.IotProtocol{
			Name:        req.Name,
			Description: req.Description,
			FormJSON:    formJson,
			Sort:        sort,
			Status:      1,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		return tx.Create(protocol).Error
	})
}

// UpdateProtocol 更新协议
func (s *ProtocolService) UpdateProtocol(ctx context.Context, req *dto.UpdateProtocolDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 检查协议是否存在
		var existing po.IotProtocol
		id := req.ID
		if err := tx.Where("id = ?", id).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(enums.ProtocolNotExistsEnum.GetCode(), enums.ProtocolNotExistsEnum.GetMessage(), enums.ProtocolNotExistsEnum.GetMsgKey())
			}
			return err
		}

		// 检查名称是否被占用（修改名称时）
		if req.Name != "" && req.Name != existing.Name {
			var dup po.IotProtocol
			if err := tx.Where("name = ? AND id != ?", req.Name, id).First(&dup).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			if dup.ID != "" {
				return appError.NewAppErrorCtx(enums.ProtocolExistsEnum.GetCode(), enums.ProtocolExistsEnum.GetMessage(), enums.ProtocolExistsEnum.GetMsgKey())
			}
		}

		updates := make(map[string]any)
		if req.Name != "" {
			updates["name"] = req.Name
		}
		updates["description"] = req.Description
		if req.Sort != nil {
			updates["sort"] = *req.Sort
		}
		if req.Status != nil {
			updates["status"] = *req.Status
		}
		if len(updates) == 0 {
			return nil
		}
		updates["updated_at"] = time.Now().Format("2006-01-02 15:04:05")

		return tx.Model(&po.IotProtocol{}).Where("id = ?", id).Updates(updates).Error
	})
}

// GetProtocolById 根据ID查询协议
func (s *ProtocolService) GetProtocolById(ctx context.Context, id string) (*vo.ProtocolVO, error) {
	var protocol po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&protocol).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(enums.ProtocolNotExistsEnum.GetCode(), enums.ProtocolNotExistsEnum.GetMessage(), enums.ProtocolNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}

	return &vo.ProtocolVO{
		ID:          protocol.ID,
		Name:        protocol.Name,
		Description: protocol.Description,
		FormJSON:    json.RawMessage(protocol.FormJSON),
		Sort:        protocol.Sort,
		Status:      protocol.Status,
		CreatedAt:   protocol.CreatedAt,
		UpdatedAt:   protocol.UpdatedAt,
	}, nil
}

// GetProtocolByName 根据协议名称查询协议
func (s *ProtocolService) GetProtocolByName(ctx context.Context, name string) (*vo.ProtocolVO, error) {
	var protocol po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Where("name = ?", name).First(&protocol).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(enums.ProtocolNotExistsEnum.GetCode(), enums.ProtocolNotExistsEnum.GetMessage(), enums.ProtocolNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}

	return &vo.ProtocolVO{
		ID:          protocol.ID,
		Name:        protocol.Name,
		Description: protocol.Description,
		FormJSON:    json.RawMessage(protocol.FormJSON),
		Sort:        protocol.Sort,
		Status:      protocol.Status,
		CreatedAt:   protocol.CreatedAt,
		UpdatedAt:   protocol.UpdatedAt,
	}, nil
}

// GetProtocolDataTypes 返回协议支持的可配置数据类型（通用 + 协议专属扩展），
// 供配置界面类型下拉使用。protocolName 与 protocolID 二选一（传 ID 时内部查协议名）；
// 协议不存在返回 ProtocolNotExists；协议无对应驱动 scope（不可配置点位）时返回空列表。
func (s *ProtocolService) GetProtocolDataTypes(ctx context.Context, protocolName, protocolID string) (*vo.DataTypeOptionVO, error) {
	if protocolName == "" && protocolID != "" {
		protocol, err := s.GetProtocolById(ctx, protocolID)
		if err != nil {
			return nil, err
		}
		protocolName = protocol.Name
	}
	if protocolName == "" {
		return nil, appError.NewAppErrorCtx(enums.ParamValidEnum.GetCode(),
			"协议名或协议ID不能为空", enums.ParamValidEnum.GetMsgKey())
	}

	common, extended := driver.AvailableTypes(driver.ScopeOf(protocolName))
	return &vo.DataTypeOptionVO{
		Protocol:      protocolName,
		CommonTypes:   common,
		ExtendedTypes: extended,
		AllTypes:      append(append([]string(nil), common...), extended...),
	}, nil
}

// UpdateProtocolFormByName 根据协议名称更新表单 JSON
func (s *ProtocolService) UpdateProtocolFormByName(ctx context.Context, req *dto.UpdateProtocolFormByNameDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.IotProtocol
		if err := tx.Where("name = ?", req.Name).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(enums.ProtocolNotExistsEnum.GetCode(), enums.ProtocolNotExistsEnum.GetMessage(), enums.ProtocolNotExistsEnum.GetMsgKey())
			}
			return err
		}

		return tx.Model(&po.IotProtocol{}).Where("id = ?", existing.ID).Updates(map[string]any{
			"form_json":  req.FormJSON,
			"updated_at": time.Now().Format("2006-01-02 15:04:05"),
		}).Error
	})
}

// UpdateProtocolFormById 根据协议ID更新表单 JSON
func (s *ProtocolService) UpdateProtocolFormById(ctx context.Context, req *dto.UpdateProtocolFormByIdDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.IotProtocol
		if err := tx.Where("id = ?", req.ID).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(enums.ProtocolNotExistsEnum.GetCode(), enums.ProtocolNotExistsEnum.GetMessage(), enums.ProtocolNotExistsEnum.GetMsgKey())
			}
			return err
		}

		return tx.Model(&po.IotProtocol{}).Where("id = ?", req.ID).Updates(map[string]any{
			"form_json":  req.FormJSON,
			"updated_at": time.Now().Format("2006-01-02 15:04:05"),
		}).Error
	})
}

// DeleteProtocol 删除协议
func (s *ProtocolService) DeleteProtocol(ctx context.Context, id string) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).Delete(&po.IotProtocol{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return appError.NewAppErrorCtx(enums.ProtocolNotExistsEnum.GetCode(), enums.ProtocolNotExistsEnum.GetMessage(), enums.ProtocolNotExistsEnum.GetMsgKey())
		}
		return nil
	})
}

// PageProtocol 协议分页查询（支持名称模糊检索）
func (s *ProtocolService) PageProtocol(ctx context.Context, req *dto.PageProtocolDTO) (*response.PageVo[vo.ProtocolVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.IotProtocol{})

	if req.Name != "" {
		db = db.Where("name LIKE ?", "%"+req.Name+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var protocols []po.IotProtocol
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).Order("sort ASC, created_at DESC").Find(&protocols).Error; err != nil {
		return nil, err
	}

	records := make([]vo.ProtocolVO, len(protocols))
	for i, p := range protocols {
		records[i] = vo.ProtocolVO{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description,
			FormJSON:    json.RawMessage(p.FormJSON),
			Sort:        p.Sort,
			Status:      p.Status,
			CreatedAt:   p.CreatedAt,
			UpdatedAt:   p.UpdatedAt,
		}
	}

	return &response.PageVo[vo.ProtocolVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}
