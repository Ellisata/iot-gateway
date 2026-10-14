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
	"iot-gateway/pluginhost"
	"iot-gateway/response"
)

// ProtocolFormSource 提供插件为某协议声明的表单（*pluginhost.Manager 实现）。
//
// 抽成接口而非直接依赖 *pluginhost.Manager，理由与 pluginhost.Reloader 相同：
// 让本服务的测试能注入假实现，不必在 service 包里拉起真实的插件子进程。
type ProtocolFormSource interface {
	// PluginProtocolForm 返回插件为协议 name 声明的表单；
	// 无插件提供时返回 pluginhost.ErrProtocolNotProvisioned，插件未声明表单时返回 (nil, nil)。
	PluginProtocolForm(name string) ([]byte, error)
}

// ProtocolService 协议服务
//
// 协议**只能由插件供给**（driver 注册表里已无任何内置协议，见 CLAUDE.md），
// 因此这里没有「新建协议」：手工建的行没有任何驱动能与之对应，
// 设备选了它只会在采集时被静默跳过。同理也没有「删除协议」——
// 删掉的行会在插件下次加载时被 provision 重新插回，按钮看着成功实则无效；
// 取而代之的是 ResetProtocolForm（把表单恢复成插件当前声明的那一份）。
type ProtocolService struct {
	sqliteDB *gorm.DB
	forms    ProtocolFormSource
}

// NewProtocolService 创建协议服务
func NewProtocolService(sqliteDB *gorm.DB, forms ProtocolFormSource) *ProtocolService {
	return &ProtocolService{sqliteDB: sqliteDB, forms: forms}
}

// UpdateProtocol 更新协议（可改的只有描述与排序）。
//
// 协议名不可改：它是 iot_protocol.name 与 driver 注册名的唯一纽带，
// 而 device.protocol_id 正是经由协议名反查驱动的。改名会让引用它的设备静默停采，
// 且插件下次加载时 provision 按名字找不到这一行、会再插一条正确的，
// 改名后的旧行则沦为孤儿。故此处直接拒绝而非静默忽略 —— 静默忽略会让
// 调用方以为改成功了。
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

		if req.Name != "" && req.Name != existing.Name {
			return appError.NewAppErrorCtx(enums.ProtocolNameImmutableEnum.GetCode(), enums.ProtocolNameImmutableEnum.GetMessage(), enums.ProtocolNameImmutableEnum.GetMsgKey())
		}

		updates := map[string]any{
			"description": req.Description,
			"updated_at":  time.Now().Format("2006-01-02 15:04:05"),
		}
		if req.Sort != nil {
			updates["sort"] = *req.Sort
		}

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

	scope := driver.ScopeOf(protocolName)
	common, extended := driver.AvailableTypes(scope)
	all := append(append([]string(nil), common...), extended...)

	// 逐类型解析 Kind，供前端的报警条件编辑器按类别给出比较符下拉
	// （"Boolean" 与 "Float" 光看名字分不出类别）。与 deviceAddressService
	// 校验条件时走的是同一套查表：通用名 → 内部名 → Kind。
	reg := driver.GetTypeRegistry().ForProtocol(scope)
	kindByType := make(map[string]string, len(all))
	for _, name := range all {
		if internal, ok := driver.LookupScopeType(scope, name); ok {
			if kind := reg.KindOf(internal); kind != "" {
				kindByType[name] = kind
			}
		}
	}

	return &vo.DataTypeOptionVO{
		Protocol:      protocolName,
		CommonTypes:   common,
		ExtendedTypes: extended,
		AllTypes:      all,
		KindByType:    kindByType,
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

// ResetProtocolForm 把协议的表单恢复成插件当前声明的那一份。
//
// 取代原先的「删除协议」。删除在插件供给模型下是无意义的：iot_protocol 行由
// provision 在插件每次加载时按名字补回，删掉的行重启即复活，按钮看着成功实则无效。
// 运维真正需要的是「我把表单改坏了」或「插件更新了表单，我要用插件那份」，
// 这正是本方法。它也不碰 description —— 描述是运维数据，provision 同样只在为空时才补。
func (s *ProtocolService) ResetProtocolForm(ctx context.Context, id string) error {
	var existing po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return appError.NewAppErrorCtx(enums.ProtocolNotExistsEnum.GetCode(), enums.ProtocolNotExistsEnum.GetMessage(), enums.ProtocolNotExistsEnum.GetMsgKey())
		}
		return err
	}

	form, err := s.forms.PluginProtocolForm(existing.Name)
	if err != nil {
		if errors.Is(err, pluginhost.ErrProtocolNotProvisioned) {
			return appError.NewAppErrorCtx(enums.ProtocolPluginAbsentEnum.GetCode(), enums.ProtocolPluginAbsentEnum.GetMessage(), enums.ProtocolPluginAbsentEnum.GetMsgKey())
		}
		return err
	}
	if len(form) == 0 {
		return appError.NewAppErrorCtx(enums.ProtocolNoDefaultFormEnum.GetCode(), enums.ProtocolNoDefaultFormEnum.GetMessage(), enums.ProtocolNoDefaultFormEnum.GetMsgKey())
	}

	return s.sqliteDB.WithContext(ctx).Model(&po.IotProtocol{}).Where("id = ?", existing.ID).Updates(map[string]any{
		"form_json":  string(form),
		"updated_at": time.Now().Format("2006-01-02 15:04:05"),
	}).Error
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
