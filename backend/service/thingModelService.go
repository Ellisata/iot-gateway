// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/constants"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
)

// 物模型标识与属性标识符的格式约束。
// 用正则而不是 binding 的 alphanum：两者都允许首字符为数字，而下游映射把
// identifier 当字段名用，数字开头在多数模板语言里不是合法标识符。
var (
	thingModelCodePattern       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
	thingModelIdentifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
)

// 属性可用的数据类型类别与读写模式。binding 已覆盖 JSON 入口，
// 这里再判一次是为了服务层被直接调用时也守住同一套取值。
var (
	thingModelKinds = map[string]struct{}{
		"bool": {}, "int": {}, "uint": {}, "float": {}, "string": {}, "time": {},
	}
	thingModelAccessModes = map[string]struct{}{
		"R": {}, "W": {}, "RW": {},
	}
)

// ThingModelService 设备物模型服务。
//
// 物模型是协议无关的语义模板，不参与采集/推送执行；本服务只维护模板本身，
// 设备绑定与点位绑定在各自的 service 里校验。
type ThingModelService struct {
	sqliteDB *gorm.DB
}

// NewThingModelService 创建物模型服务
func NewThingModelService(sqliteDB *gorm.DB) *ThingModelService {
	return &ThingModelService{sqliteDB: sqliteDB}
}

func thingModelErr(e *enums.BusinessEnum) error {
	return appError.NewAppErrorCtx(e.GetCode(), e.GetMessage(), e.GetMsgKey())
}

func nowString() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// ==================== 物模型 ====================

// CreateThingModel 创建物模型
func (s *ThingModelService) CreateThingModel(ctx context.Context, req *dto.CreateThingModelDTO) error {
	code := strings.TrimSpace(req.Code)
	name := strings.TrimSpace(req.Name)
	if !thingModelCodePattern.MatchString(code) {
		return thingModelErr(enums.ThingModelCodeInvalidEnum)
	}
	if name == "" {
		return thingModelErr(enums.ParamValidEnum)
	}
	if !constants.IsValidThingModelCategory(req.Category) {
		return thingModelErr(enums.ThingModelCategoryInvalidEnum)
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = "1.0.0"
	}

	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&po.ThingModel{}).
			Where("code = ? OR name = ?", code, name).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return thingModelErr(enums.ThingModelExistsEnum)
		}

		now := nowString()
		return tx.Create(&po.ThingModel{
			Code:        code,
			Name:        name,
			Category:    req.Category,
			Version:     version,
			Description: req.Description,
			Status:      1,
			CreatedAt:   now,
			UpdatedAt:   now,
		}).Error
	})
}

// UpdateThingModel 更新物模型。
//
// Code 创建后不可修改：它是导入导出与外部引用的稳定键。
func (s *ThingModelService) UpdateThingModel(ctx context.Context, req *dto.UpdateThingModelDTO) error {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return thingModelErr(enums.ParamValidEnum)
	}
	if req.Code != "" && !thingModelCodePattern.MatchString(strings.TrimSpace(req.Code)) {
		return thingModelErr(enums.ThingModelCodeInvalidEnum)
	}
	if !constants.IsValidThingModelCategory(req.Category) {
		return thingModelErr(enums.ThingModelCategoryInvalidEnum)
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = "1.0.0"
	}

	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.ThingModel
		if err := tx.Where("id = ?", req.ID).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return thingModelErr(enums.ThingModelNotExistsEnum)
			}
			return err
		}
		if req.Code != "" && strings.TrimSpace(req.Code) != existing.Code {
			return thingModelErr(enums.ThingModelCodeImmutableEnum)
		}

		var dup int64
		if err := tx.Model(&po.ThingModel{}).
			Where("name = ? AND id != ?", name, req.ID).
			Count(&dup).Error; err != nil {
			return err
		}
		if dup > 0 {
			return thingModelErr(enums.ThingModelExistsEnum)
		}

		updates := map[string]any{
			"name":        name,
			"category":    req.Category,
			"version":     version,
			"description": req.Description,
			"updated_at":  nowString(),
		}
		if req.Status != nil {
			updates["status"] = *req.Status
		}
		return tx.Model(&po.ThingModel{}).Where("id = ?", req.ID).Updates(updates).Error
	})
}

// DeleteThingModel 删除物模型。
//
// 有设备绑定、或仍有指向其属性的点位时拒绝删除：静默解除绑定会让设备
// 在界面上突然变成“未绑定”，而采集本身完全正常，问题很难被发现。
// 没有外键，属性行在同一事务里显式删除。
func (s *ThingModelService) DeleteThingModel(ctx context.Context, id string) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.ThingModel
		if err := tx.Where("id = ?", id).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return thingModelErr(enums.ThingModelNotExistsEnum)
			}
			return err
		}

		var devices int64
		if err := tx.Model(&po.Device{}).Where("model_id = ?", id).Count(&devices).Error; err != nil {
			return err
		}
		var boundAddresses int64
		if err := tx.Model(&po.DeviceAddress{}).
			Where("property_id IN (?)",
				tx.Model(&po.ThingModelProperty{}).Select("id").Where("model_id = ?", id)).
			Count(&boundAddresses).Error; err != nil {
			return err
		}
		if devices > 0 || boundAddresses > 0 {
			return thingModelErr(enums.ThingModelInUseEnum)
		}

		if err := tx.Where("model_id = ?", id).Delete(&po.ThingModelProperty{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&po.ThingModel{}).Error
	})
}

// GetThingModelByID 根据 ID 查询物模型（含派生计数）
func (s *ThingModelService) GetThingModelByID(ctx context.Context, id string) (*vo.ThingModelVO, error) {
	var model po.ThingModel
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, thingModelErr(enums.ThingModelNotExistsEnum)
		}
		return nil, err
	}

	propertyCount, deviceCount, err := s.countsForModel(ctx, id)
	if err != nil {
		return nil, err
	}
	return toThingModelVO(model, propertyCount, deviceCount), nil
}

// PageThingModel 物模型分页查询（keyword 同时模糊匹配 code 与 name）
func (s *ThingModelService) PageThingModel(ctx context.Context, req *dto.PageThingModelDTO) (*response.PageVo[vo.ThingModelVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.ThingModel{})

	if kw := strings.TrimSpace(req.Keyword); kw != "" {
		like := "%" + kw + "%"
		db = db.Where("code LIKE ? OR name LIKE ?", like, like)
	}
	if req.Category != "" {
		if !constants.IsValidThingModelCategory(req.Category) {
			return nil, thingModelErr(enums.ThingModelCategoryInvalidEnum)
		}
		db = db.Where("category = ?", req.Category)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var models []po.ThingModel
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).
		Order("created_at DESC, id DESC").Find(&models).Error; err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	propertyCounts, err := s.countPropertiesByModel(ctx, ids)
	if err != nil {
		return nil, err
	}
	deviceCounts, err := s.countDevicesByModel(ctx, ids)
	if err != nil {
		return nil, err
	}

	records := make([]vo.ThingModelVO, len(models))
	for i, m := range models {
		records[i] = *toThingModelVO(m, propertyCounts[m.ID], deviceCounts[m.ID])
	}
	return &response.PageVo[vo.ThingModelVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}

func (s *ThingModelService) countsForModel(ctx context.Context, id string) (int64, int64, error) {
	var propertyCount, deviceCount int64
	if err := s.sqliteDB.WithContext(ctx).Model(&po.ThingModelProperty{}).
		Where("model_id = ?", id).Count(&propertyCount).Error; err != nil {
		return 0, 0, err
	}
	if err := s.sqliteDB.WithContext(ctx).Model(&po.Device{}).
		Where("model_id = ?", id).Count(&deviceCount).Error; err != nil {
		return 0, 0, err
	}
	return propertyCount, deviceCount, nil
}

type modelCountRow struct {
	ModelID string `gorm:"column:model_id"`
	N       int64  `gorm:"column:n"`
}

func (s *ThingModelService) countPropertiesByModel(ctx context.Context, ids []string) (map[string]int64, error) {
	out := make(map[string]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []modelCountRow
	if err := s.sqliteDB.WithContext(ctx).Model(&po.ThingModelProperty{}).
		Select("model_id, COUNT(*) AS n").
		Where("model_id IN ?", ids).
		Group("model_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ModelID] = r.N
	}
	return out, nil
}

func (s *ThingModelService) countDevicesByModel(ctx context.Context, ids []string) (map[string]int64, error) {
	out := make(map[string]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []modelCountRow
	if err := s.sqliteDB.WithContext(ctx).Model(&po.Device{}).
		Select("model_id, COUNT(*) AS n").
		Where("model_id IN ?", ids).
		Group("model_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ModelID] = r.N
	}
	return out, nil
}

func toThingModelVO(m po.ThingModel, propertyCount, deviceCount int64) *vo.ThingModelVO {
	return &vo.ThingModelVO{
		ID:            m.ID,
		Code:          m.Code,
		Name:          m.Name,
		Category:      m.Category,
		Version:       m.Version,
		Description:   m.Description,
		Status:        m.Status,
		PropertyCount: propertyCount,
		DeviceCount:   deviceCount,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

// ListThingModels 返回全部物模型，供设备绑定下拉使用。
// 停用的模型也返回（带 status），由前端置灰：服务端仍需拒绝新的绑定。
func (s *ThingModelService) ListThingModels(ctx context.Context) ([]vo.ThingModelOptionVO, error) {
	var rows []po.ThingModel
	if err := s.sqliteDB.WithContext(ctx).
		Order("name ASC, code ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]vo.ThingModelOptionVO, len(rows))
	for i, m := range rows {
		out[i] = vo.ThingModelOptionVO{ID: m.ID, Code: m.Code, Name: m.Name, Status: m.Status}
	}
	return out, nil
}

// ==================== 物模型属性 ====================

// ListThingModelProperties 列出某模型下的全部属性（按 sort_order 升序）
func (s *ThingModelService) ListThingModelProperties(ctx context.Context, modelID string) ([]vo.ThingModelPropertyVO, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil, thingModelErr(enums.ParamValidEnum)
	}
	var model po.ThingModel
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", modelID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, thingModelErr(enums.ThingModelNotExistsEnum)
		}
		return nil, err
	}

	var rows []po.ThingModelProperty
	if err := s.sqliteDB.WithContext(ctx).
		Where("model_id = ?", modelID).
		Order("sort_order ASC, created_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]vo.ThingModelPropertyVO, len(rows))
	for i, p := range rows {
		out[i] = toThingModelPropertyVO(p)
	}
	return out, nil
}

// PageThingModelProperties 分页列出某模型下的属性（排序与全量接口一致）。
//
// 顺带返回模型内的最大 sort_order：前端分页后看不到全量列表，新增属性要接着
// 末尾排就得知道全量最大值，而不是当前页的最大值。
func (s *ThingModelService) PageThingModelProperties(ctx context.Context, req *dto.PageThingModelPropertyDTO) (*vo.ThingModelPropertyPageVO, error) {
	modelID := strings.TrimSpace(req.ModelID)
	if modelID == "" {
		return nil, thingModelErr(enums.ParamValidEnum)
	}
	var model po.ThingModel
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", modelID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, thingModelErr(enums.ThingModelNotExistsEnum)
		}
		return nil, err
	}

	db := s.sqliteDB.WithContext(ctx).Model(&po.ThingModelProperty{}).Where("model_id = ?", modelID)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var maxSortOrder int
	if err := s.sqliteDB.WithContext(ctx).Model(&po.ThingModelProperty{}).
		Where("model_id = ?", modelID).
		Select("COALESCE(MAX(sort_order), 0)").
		Scan(&maxSortOrder).Error; err != nil {
		return nil, err
	}

	var rows []po.ThingModelProperty
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).
		Order("sort_order ASC, created_at ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}

	records := make([]vo.ThingModelPropertyVO, len(rows))
	for i, p := range rows {
		records[i] = toThingModelPropertyVO(p)
	}
	return &vo.ThingModelPropertyPageVO{
		Page:         req.Page,
		Size:         req.Size,
		Total:        total,
		Records:      records,
		MaxSortOrder: maxSortOrder,
	}, nil
}

// CreateThingModelProperty 创建物模型属性
func (s *ThingModelService) CreateThingModelProperty(ctx context.Context, req *dto.CreateThingModelPropertyDTO) error {
	identifier := strings.TrimSpace(req.Identifier)
	name := strings.TrimSpace(req.Name)
	if !thingModelIdentifierPattern.MatchString(identifier) {
		return thingModelErr(enums.ThingModelIdentifierInvalidEnum)
	}
	if name == "" || !isThingModelKind(req.Kind) || !isThingModelAccessMode(req.AccessMode) {
		return thingModelErr(enums.ParamValidEnum)
	}

	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model po.ThingModel
		if err := tx.Where("id = ?", req.ModelID).First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return thingModelErr(enums.ThingModelNotExistsEnum)
			}
			return err
		}

		var count int64
		if err := tx.Model(&po.ThingModelProperty{}).
			Where("model_id = ? AND identifier = ?", req.ModelID, identifier).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return thingModelErr(enums.ThingModelPropertyExistsEnum)
		}

		now := nowString()
		return tx.Create(&po.ThingModelProperty{
			ModelID:     req.ModelID,
			Identifier:  identifier,
			Name:        name,
			Kind:        req.Kind,
			AccessMode:  req.AccessMode,
			Unit:        strings.TrimSpace(req.Unit),
			Description: req.Description,
			SortOrder:   req.SortOrder,
			Status:      1,
			CreatedAt:   now,
			UpdatedAt:   now,
		}).Error
	})
}

// UpdateThingModelProperty 更新物模型属性。
//
// Identifier 创建后不可修改：绑定校验、映射与外部引用都靠它定位属性。
func (s *ThingModelService) UpdateThingModelProperty(ctx context.Context, req *dto.UpdateThingModelPropertyDTO) error {
	name := strings.TrimSpace(req.Name)
	if name == "" || !isThingModelKind(req.Kind) || !isThingModelAccessMode(req.AccessMode) {
		return thingModelErr(enums.ParamValidEnum)
	}
	identifier := strings.TrimSpace(req.Identifier)
	if identifier != "" && !thingModelIdentifierPattern.MatchString(identifier) {
		return thingModelErr(enums.ThingModelIdentifierInvalidEnum)
	}

	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.ThingModelProperty
		if err := tx.Where("id = ?", req.ID).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return thingModelErr(enums.ThingModelPropertyNotExistsEnum)
			}
			return err
		}
		if identifier != "" && identifier != existing.Identifier {
			return thingModelErr(enums.ThingModelIdentifierImmutableEnum)
		}
		// 已被点位绑定后，kind/access 是绑定的兼容性前提：改了会让已有绑定
		// 悄悄失效（采集本身照常，只是语义不对）。要求先解绑再改。
		if req.Kind != existing.Kind || req.AccessMode != existing.AccessMode {
			var bound int64
			if err := tx.Model(&po.DeviceAddress{}).Where("property_id = ?", req.ID).
				Count(&bound).Error; err != nil {
				return err
			}
			if bound > 0 {
				return thingModelErr(enums.ThingModelPropertyBoundImmutableEnum)
			}
		}

		updates := map[string]any{
			"name":        name,
			"kind":        req.Kind,
			"access_mode": req.AccessMode,
			"unit":        strings.TrimSpace(req.Unit),
			"description": req.Description,
			"sort_order":  req.SortOrder,
			"updated_at":  nowString(),
		}
		if req.Status != nil {
			updates["status"] = *req.Status
		}
		return tx.Model(&po.ThingModelProperty{}).Where("id = ?", req.ID).Updates(updates).Error
	})
}

// DeleteThingModelProperty 删除物模型属性。
//
// 已被点位绑定时拒绝：删除会让 device_address.property_id 指向不存在的行，
// 界面上该点位会显示成“未绑定”，但采集仍然在跑，属于静默改语义。
func (s *ThingModelService) DeleteThingModelProperty(ctx context.Context, id string) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.ThingModelProperty
		if err := tx.Where("id = ?", id).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return thingModelErr(enums.ThingModelPropertyNotExistsEnum)
			}
			return err
		}

		var bound int64
		if err := tx.Model(&po.DeviceAddress{}).
			Where("property_id = ?", id).Count(&bound).Error; err != nil {
			return err
		}
		if bound > 0 {
			return thingModelErr(enums.ThingModelPropertyInUseEnum)
		}

		return tx.Where("id = ?", id).Delete(&po.ThingModelProperty{}).Error
	})
}

func toThingModelPropertyVO(p po.ThingModelProperty) vo.ThingModelPropertyVO {
	return vo.ThingModelPropertyVO{
		ID:          p.ID,
		ModelID:     p.ModelID,
		Identifier:  p.Identifier,
		Name:        p.Name,
		Kind:        p.Kind,
		AccessMode:  p.AccessMode,
		Unit:        p.Unit,
		Description: p.Description,
		SortOrder:   p.SortOrder,
		Status:      p.Status,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

func isThingModelKind(kind string) bool {
	_, ok := thingModelKinds[kind]
	return ok
}

func isThingModelAccessMode(mode string) bool {
	_, ok := thingModelAccessModes[mode]
	return ok
}
