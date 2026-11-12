// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/constants"
	"iot-gateway/enums"
	"iot-gateway/excelutil"
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

// 属性可用的读写模式。binding 已覆盖 JSON 入口，这里再判一次是为了服务层
// 被直接调用时也守住同一套取值。数据类型类别是跨领域的 driver.KindXxx，
// 与订阅设备/点位共用 deviceKindCodes（见 deviceKind.go）。
var (
	thingModelAccessModeCodes = []string{"R", "W", "RW"}
	thingModelAccessModes     = stringSet(thingModelAccessModeCodes)

	// 模板下拉展示名。与前端 zh-CN 的 thingModel.access.* 文案保持一致；
	// 模板由后端生成，拿不到前端 i18n，**改一处须同步另一处**。
	thingModelAccessModeDisplayNames = map[string]string{
		"R": "只读", "W": "只写", "RW": "读写",
	}
)

// ThingModelService 设备物模型服务。
//
// 物模型是协议无关的语义模板；本服务只维护模板本身，设备绑定与点位绑定在
// 各自的 service 里校验。属性的 access_mode 不在这里执行，但它是读写能力的
// 硬上界：采集侧（collector）与写入侧（ControlService）按它收紧点位权限。
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
	category := strings.TrimSpace(req.Category)
	if !thingModelCodePattern.MatchString(code) {
		return thingModelErr(enums.ThingModelCodeInvalidEnum)
	}
	if name == "" {
		return thingModelErr(enums.ParamValidEnum)
	}
	if !constants.IsValidThingModelCategory(category) {
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
			Category:    category,
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
	category := strings.TrimSpace(req.Category)
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
		// 开放接口下发的自定义分类允许原样保留；新增或改挂仍只接受内置分类。
		if category != existing.Category && !constants.IsValidThingModelCategory(category) {
			return thingModelErr(enums.ThingModelCategoryInvalidEnum)
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
			"category":    category,
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
	if category := strings.TrimSpace(req.Category); category != "" {
		// 筛选要能命中平台下发的自定义分类，因此只校验格式，不套内置枚举。
		if !constants.IsValidThingModelCategoryCode(category) {
			return nil, thingModelErr(enums.ThingModelCategoryInvalidEnum)
		}
		db = db.Where("category = ?", category)
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

// thingModelImportHeaders 物模型导入模板表头（顺序即列序）。
var thingModelImportHeaders = []string{"模型标识", "名称", "分类", "版本", "描述"}

// ImportThingModels 从 Excel(.xlsx) 批量导入物模型。
//
// 模型标识与名称都要求唯一（与单条创建同口径），重复行跳过并记录原因；
// 分类列接受模板下拉项「传感器（sensor）」、中文名「传感器」或 code「sensor」
// （code 忽略大小写），留空表示未分类。其余行部分导入并返回汇总。
func (s *ThingModelService) ImportThingModels(ctx context.Context, data []byte) (*vo.ExcelImportResultVO, error) {
	var rows []po.ThingModel
	if err := s.sqliteDB.WithContext(ctx).Select("code", "name").Find(&rows).Error; err != nil {
		return nil, err
	}
	existingCodes := make(map[string]bool, len(rows))
	existingNames := make(map[string]bool, len(rows))
	for _, r := range rows {
		existingCodes[r.Code] = true
		existingNames[r.Name] = true
	}

	seenCodes := make(map[string]bool)
	seenNames := make(map[string]bool)
	now := nowString()
	return excelutil.Import(ctx, s.sqliteDB, data, excelutil.Options[po.ThingModel]{
		Headers: thingModelImportHeaders,
		Cols:    len(thingModelImportHeaders),
		DupMsg:  enums.ThingModelExistsEnum.GetMessage(),
		Label: func(row []string) string {
			return importRowLabel(excelutil.Cell(row, 0), excelutil.Cell(row, 1))
		},
		Parse: func(row []string) (*po.ThingModel, string, string) {
			code := excelutil.Cell(row, 0)
			name := excelutil.Cell(row, 1)
			category, categoryOK := constants.ParseThingModelCategory(excelutil.Cell(row, 2))
			version := excelutil.Cell(row, 3)
			desc := excelutil.Cell(row, 4)

			if !categoryOK {
				return nil, code, "分类须为下拉选项（如「传感器（sensor）」）或留空"
			}
			if reason := validateThingModelImportRow(code, name, category, version, existingNames, seenNames); reason != "" {
				return nil, code, reason
			}
			if existingCodes[code] || seenCodes[code] {
				return nil, code, enums.ThingModelExistsEnum.GetMessage()
			}
			if version == "" {
				version = "1.0.0"
			}
			seenCodes[code] = true
			seenNames[name] = true
			return &po.ThingModel{
				Code:        code,
				Name:        name,
				Category:    category,
				Version:     version,
				Description: desc,
				Status:      1,
				CreatedAt:   now,
				UpdatedAt:   now,
			}, code, ""
		},
	})
}

// validateThingModelImportRow 校验单行物模型导入数据（纯函数）。
// 返回 "" 表示通过；名称重复在这里一并拦截（标识重复由调用方按去重表判断）。
func validateThingModelImportRow(code, name, category, version string, existingNames, seenNames map[string]bool) string {
	if code == "" {
		return "模型标识为空"
	}
	if !thingModelCodePattern.MatchString(code) {
		return "模型标识不合法（字母开头，仅字母数字下划线短横线，最多64位）"
	}
	if name == "" {
		return "名称为空"
	}
	if utf8.RuneCountInString(name) > 100 {
		return "名称不能超过100个字符"
	}
	if existingNames[name] || seenNames[name] {
		return enums.ThingModelExistsEnum.GetMessage()
	}
	if !constants.IsValidThingModelCategory(category) {
		return "分类须为 sensor/meter/actuator/robot/plc/other 或留空"
	}
	if utf8.RuneCountInString(version) > 32 {
		return "版本号不能超过32个字符"
	}
	return ""
}

// GenerateThingModelImportTemplate 生成物模型导入模板（.xlsx）。
// 分类列带下拉选择，取值与 constants.ThingModelCategoryCodes 保持一致。
func (s *ThingModelService) GenerateThingModelImportTemplate() (*bytes.Buffer, error) {
	return excelutil.BuildTemplateWithLists(
		thingModelImportHeaders,
		[]string{"th_sensor", "温湿度传感器", "传感器（sensor）", "1.0.0", "示例数据，可删除或覆盖"},
		[]float64{20, 24, 12, 10, 40},
		[]excelutil.ListValidation{{
			Col:     2,
			Options: constants.ThingModelCategoryTemplateOptions(),
			Prompt:  "请选择分类；留空表示未分类",
		}},
	)
}

// importRowLabel 导入失败明细里展示的标识：优先名称，名称缺失时退回标识。
func importRowLabel(code, name string) string {
	if name != "" {
		return name
	}
	return code
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

// thingModelPropertyImportHeaders 物模型属性导入模板表头（顺序即列序）。
var thingModelPropertyImportHeaders = []string{"属性标识", "名称", "数据类型", "读写模式", "单位", "排序", "描述"}

// ImportThingModelProperties 从 Excel(.xlsx) 批量导入指定模型下的属性。
//
// 属性标识符在模型内唯一（与单条创建同口径），重复行跳过并记录原因；
// 数据类型/读写模式接受模板下拉项「浮点（float）」「只读（R）」、中文名或 code
// （code 忽略大小写）。排序留空时按模型内最大值 +10 顺延；其余行部分导入。
func (s *ThingModelService) ImportThingModelProperties(ctx context.Context, modelID string, data []byte) (*vo.ExcelImportResultVO, error) {
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

	var identifiers []string
	if err := s.sqliteDB.WithContext(ctx).Model(&po.ThingModelProperty{}).
		Where("model_id = ?", modelID).Pluck("identifier", &identifiers).Error; err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(identifiers))
	for _, id := range identifiers {
		existing[id] = true
	}

	var maxSortOrder int
	if err := s.sqliteDB.WithContext(ctx).Model(&po.ThingModelProperty{}).
		Where("model_id = ?", modelID).
		Select("COALESCE(MAX(sort_order), 0)").
		Scan(&maxSortOrder).Error; err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	autoSort := maxSortOrder + 10
	now := nowString()
	return excelutil.Import(ctx, s.sqliteDB, data, excelutil.Options[po.ThingModelProperty]{
		Headers: thingModelPropertyImportHeaders,
		Cols:    len(thingModelPropertyImportHeaders),
		DupMsg:  enums.ThingModelPropertyExistsEnum.GetMessage(),
		Label: func(row []string) string {
			return importRowLabel(excelutil.Cell(row, 0), excelutil.Cell(row, 1))
		},
		Parse: func(row []string) (*po.ThingModelProperty, string, string) {
			identifier := excelutil.Cell(row, 0)
			name := excelutil.Cell(row, 1)
			kindRaw := excelutil.Cell(row, 2)
			accessRaw := excelutil.Cell(row, 3)
			unit := excelutil.Cell(row, 4)
			sortRaw := excelutil.Cell(row, 5)
			desc := excelutil.Cell(row, 6)

			kind, accessMode, reason := validateThingModelPropertyImportRow(
				identifier, name, kindRaw, accessRaw, unit, desc, existing, seen,
			)
			if reason != "" {
				return nil, identifier, reason
			}
			sortOrder := autoSort
			if sortRaw == "" {
				autoSort += 10
			} else {
				parsed, err := strconv.Atoi(sortRaw)
				if err != nil || parsed < 0 {
					return nil, identifier, "排序须为非负整数"
				}
				sortOrder = parsed
			}
			seen[identifier] = true
			return &po.ThingModelProperty{
				ModelID:     modelID,
				Identifier:  identifier,
				Name:        name,
				Kind:        kind,
				AccessMode:  accessMode,
				Unit:        unit,
				Description: desc,
				SortOrder:   sortOrder,
				Status:      1,
				CreatedAt:   now,
				UpdatedAt:   now,
			}, identifier, ""
		},
	})
}

// validateThingModelPropertyImportRow 校验单行属性导入数据（纯函数），
// 并把数据类型/读写模式归一回内部 code。返回的 reason 为 "" 表示通过；
// 标识符重复也在这里一并拦截。
func validateThingModelPropertyImportRow(identifier, name, kindRaw, accessRaw, unit, desc string, existing, seen map[string]bool) (kind, accessMode, reason string) {
	if identifier == "" {
		return "", "", "属性标识为空"
	}
	if !thingModelIdentifierPattern.MatchString(identifier) {
		return "", "", "属性标识符不合法（字母开头，仅字母数字下划线，最多64位）"
	}
	if existing[identifier] || seen[identifier] {
		return "", "", enums.ThingModelPropertyExistsEnum.GetMessage()
	}
	if name == "" {
		return "", "", "名称为空"
	}
	if utf8.RuneCountInString(name) > 100 {
		return "", "", "名称不能超过100个字符"
	}
	kind, kindOK := constants.ParseEnumValue(kindRaw, deviceKindCodes, deviceKindDisplayNames)
	if !kindOK {
		return "", "", "数据类型须为下拉选项（如「浮点（float）」）"
	}
	accessMode, accessOK := constants.ParseEnumValue(accessRaw, thingModelAccessModeCodes, thingModelAccessModeDisplayNames)
	if !accessOK {
		return "", "", "读写模式须为下拉选项（如「只读（R）」）"
	}
	if utf8.RuneCountInString(unit) > 32 {
		return "", "", "单位不能超过32个字符"
	}
	if utf8.RuneCountInString(desc) > 500 {
		return "", "", "描述不能超过500个字符"
	}
	return kind, accessMode, ""
}

// GenerateThingModelPropertyImportTemplate 生成物模型属性导入模板（.xlsx）。
// 数据类型/读写模式两列带下拉选择，形如「浮点（float）」「只读（R）」。
func (s *ThingModelService) GenerateThingModelPropertyImportTemplate() (*bytes.Buffer, error) {
	return excelutil.BuildTemplateWithLists(
		thingModelPropertyImportHeaders,
		[]string{"temperature", "温度", "浮点（float）", "只读（R）", "℃", "10", "示例数据，可删除或覆盖"},
		[]float64{18, 20, 12, 12, 10, 8, 36},
		[]excelutil.ListValidation{
			{
				Col:     2,
				Options: constants.BuildEnumOptions(deviceKindCodes, deviceKindDisplayNames),
				Prompt:  "请选择数据类型",
			},
			{
				Col:     3,
				Options: constants.BuildEnumOptions(thingModelAccessModeCodes, thingModelAccessModeDisplayNames),
				Prompt:  "请选择读写模式",
			},
		},
	)
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
	_, ok := deviceKinds[kind]
	return ok
}

func isThingModelAccessMode(mode string) bool {
	_, ok := thingModelAccessModes[mode]
	return ok
}
