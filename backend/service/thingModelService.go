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
// 物模型是协议无关的语义模板；本服务只维护模板本身，点位绑定在设备地址服务里
// 校验。属性直接绑定设备点位，设备不绑定模型。属性的 access_mode 不在这里执行，
// 但它是读写能力的硬上界：采集侧（collector）与写入侧（ControlService）按它收紧点位权限。
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

// thingModelBindingLimit 单次请求允许绑定的设备/点位数上限。
// DTO 上的 max=200 已经挡了 HTTP 入口，这里再判一次是为了服务层被直接调用
// （开放接口、测试）时也守住同一口径。
const thingModelBindingLimit = 200

// normalizeThingModelBindingIDs 去空白、去空串、保序去重，并检查上限。
// 空项不报错而是丢弃：前端多选清空后可能残留空串，把它当成错误只会让人困惑。
func normalizeThingModelBindingIDs(ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) > thingModelBindingLimit {
		return nil, thingModelErr(enums.ThingModelBindingLimitEnum)
	}
	return out, nil
}

// ensureSinglePointBinding 属性与设备点位是 1 对 1：一个属性最多绑定一个点位。
//
// 服务层兜底在 DTO 的 max=1 之后：开放接口与测试会绕过 HTTP binding 直接调服务。
func ensureSinglePointBinding(ids []string) error {
	if len(ids) > 1 {
		return thingModelErr(enums.ThingModelPropertySinglePointEnum)
	}
	return nil
}

// bindAddressesToProperty 把一个点位绑定到属性（事务内调用）。
//
// 属性与点位是 1 对 1，入参超过一个点位直接拒绝。先复用地址侧的绑定校验
// （validatePropertyBindingDB），再改 property_id；已绑其他属性的点位直接
// 拒绝，绝不静默抢绑。
func bindAddressesToProperty(ctx context.Context, tx *gorm.DB, propertyID string, addressIDs []string) error {
	ids, err := normalizeThingModelBindingIDs(addressIDs)
	if err != nil {
		return err
	}
	if err := ensureSinglePointBinding(ids); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	var addrs []po.DeviceAddress
	if err := tx.WithContext(ctx).Where("id IN ?", ids).Find(&addrs).Error; err != nil {
		return err
	}
	if len(addrs) != len(ids) {
		return thingModelErr(enums.DeviceAddressNotExistsEnum)
	}

	for _, a := range addrs {
		if a.PropertyID != "" && a.PropertyID != propertyID {
			return thingModelErr(enums.ThingModelPointBoundElsewhereEnum)
		}
		if err := validatePropertyBindingDB(ctx, tx.WithContext(ctx), propertyID,
			a.RwPermission, false); err != nil {
			return err
		}
	}

	now := nowString()
	for _, a := range addrs {
		res := tx.WithContext(ctx).Model(&po.DeviceAddress{}).
			Where("id = ? AND (property_id = '' OR property_id = ?)", a.ID, propertyID).
			Updates(map[string]any{"property_id": propertyID, "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 读与写之间被别的请求绑走了：与显式抢绑同一套错误，不静默成功。
			return thingModelErr(enums.ThingModelPointBoundElsewhereEnum)
		}
	}
	return nil
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
		model := &po.ThingModel{
			Code:        code,
			Name:        name,
			Category:    category,
			Version:     version,
			Description: req.Description,
			Status:      1,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		return tx.Create(model).Error
	})
}

// UpdateThingModel 更新物模型。
//
// Code 允许改名（与属性标识符同一口径），省略则保持原值。它仍是导入导出与
// 外部引用的稳定键：改名后按旧标识配置的外部引用需同步更新；开放接口按 code
// 定位模型，拿旧 code 再 upsert 会落成新模型。
func (s *ThingModelService) UpdateThingModel(ctx context.Context, req *dto.UpdateThingModelDTO) error {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return thingModelErr(enums.ParamValidEnum)
	}
	code := strings.TrimSpace(req.Code)
	if code != "" && !thingModelCodePattern.MatchString(code) {
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
		// 改名仍须全表唯一：code 列有唯一索引，先显式查重，避免把约束冲突
		// 直接漏成 500。
		if code != "" && code != existing.Code {
			var dupCode int64
			if err := tx.Model(&po.ThingModel{}).
				Where("code = ? AND id != ?", code, req.ID).
				Count(&dupCode).Error; err != nil {
				return err
			}
			if dupCode > 0 {
				return thingModelErr(enums.ThingModelExistsEnum)
			}
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
		// 省略 code = 不改名（与属性的 identifier 三态同一口径）。
		if code != "" {
			updates["code"] = code
		}
		if req.Status != nil {
			updates["status"] = *req.Status
		}
		return tx.Model(&po.ThingModel{}).Where("id = ?", req.ID).Updates(updates).Error
	})
}

// DeleteThingModel 删除物模型。
//
// 仍有指向其属性的点位时拒绝删除：静默解除绑定会让点位在界面上突然
// 变成“未绑定”，而采集本身完全正常，问题很难被发现。
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

		var boundAddresses int64
		if err := tx.Model(&po.DeviceAddress{}).
			Where("property_id IN (?)",
				tx.Model(&po.ThingModelProperty{}).Select("id").Where("model_id = ?", id)).
			Count(&boundAddresses).Error; err != nil {
			return err
		}
		if boundAddresses > 0 {
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

	propertyCount, err := s.countPropertiesForModel(ctx, id)
	if err != nil {
		return nil, err
	}
	return toThingModelVO(model, propertyCount), nil
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

	records := make([]vo.ThingModelVO, len(models))
	for i, m := range models {
		records[i] = *toThingModelVO(m, propertyCounts[m.ID])
	}
	return &response.PageVo[vo.ThingModelVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}

func (s *ThingModelService) countPropertiesForModel(ctx context.Context, id string) (int64, error) {
	var propertyCount int64
	if err := s.sqliteDB.WithContext(ctx).Model(&po.ThingModelProperty{}).
		Where("model_id = ?", id).Count(&propertyCount).Error; err != nil {
		return 0, err
	}
	return propertyCount, nil
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

func toThingModelVO(m po.ThingModel, propertyCount int64) *vo.ThingModelVO {
	return &vo.ThingModelVO{
		ID:            m.ID,
		Code:          m.Code,
		Name:          m.Name,
		Category:      m.Category,
		Version:       m.Version,
		Description:   m.Description,
		Status:        m.Status,
		PropertyCount: propertyCount,
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
	if err := s.fillPropertyPointBindings(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListAllThingModelProperties 跨模型列出全部属性，供设备点位表单的属性下拉。
//
// 点位直接绑定属性，与设备属于哪个模型无关；下拉里必须带上模型标识与名称，
// 否则不同模型里的同名 identifier 无法区分。
func (s *ThingModelService) ListAllThingModelProperties(ctx context.Context) ([]vo.ThingModelPropertyOptionVO, error) {
	var props []po.ThingModelProperty
	if err := s.sqliteDB.WithContext(ctx).
		Order("sort_order ASC, identifier ASC").
		Find(&props).Error; err != nil {
		return nil, err
	}
	if len(props) == 0 {
		return []vo.ThingModelPropertyOptionVO{}, nil
	}

	modelIDs := make([]string, 0)
	seenModel := make(map[string]struct{}, len(props))
	for _, p := range props {
		if _, ok := seenModel[p.ModelID]; ok {
			continue
		}
		seenModel[p.ModelID] = struct{}{}
		modelIDs = append(modelIDs, p.ModelID)
	}
	var models []po.ThingModel
	if err := s.sqliteDB.WithContext(ctx).Where("id IN ?", modelIDs).Find(&models).Error; err != nil {
		return nil, err
	}
	modelByID := make(map[string]po.ThingModel, len(models))
	for _, m := range models {
		modelByID[m.ID] = m
	}

	out := make([]vo.ThingModelPropertyOptionVO, 0, len(props))
	for _, p := range props {
		m := modelByID[p.ModelID]
		out = append(out, vo.ThingModelPropertyOptionVO{
			ID:         p.ID,
			ModelID:    p.ModelID,
			ModelCode:  m.Code,
			ModelName:  m.Name,
			Identifier: p.Identifier,
			Name:       p.Name,
			Kind:       p.Kind,
			AccessMode: p.AccessMode,
			Unit:       p.Unit,
			Status:     p.Status,
		})
	}
	return out, nil
}

// fillPropertyPointBindings 一次性补齐属性的绑定点位：数量 + 首条点位的
// 设备名/点位名（列表页列「点位数量」已改为「绑定点位」，展示
// 设备名称.点位名称）。
//
// 分页后一页最多 100 个属性，按 property_id IN (...) 联表一次取回，避免逐行
// 查询。1 对 1 下每个属性最多一条；存量多绑属性取与
// ListThingModelPropertyPoints（抽屉/编辑弹窗的数据源）相同排序下的第一条，
// 保证列表页展示的就是保存后保留的那条。LEFT JOIN 保证设备行缺失时仍计入数量。
func (s *ThingModelService) fillPropertyPointBindings(ctx context.Context, rows []vo.ThingModelPropertyVO) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	type bindingRow struct {
		PropertyID string `gorm:"column:property_id"`
		PointName  string `gorm:"column:point_name"`
		DeviceName string `gorm:"column:device_name"`
	}
	var bindings []bindingRow
	if err := s.sqliteDB.WithContext(ctx).
		Table("device_address AS a").
		Select("a.property_id, a.name AS point_name, d.name AS device_name").
		Joins("LEFT JOIN device AS d ON d.id = a.device_id").
		Where("a.property_id IN ?", ids).
		Order("a.property_id ASC, d.name ASC, a.id ASC").
		Scan(&bindings).Error; err != nil {
		return err
	}
	counts := make(map[string]int64, len(bindings))
	// bindings 已按属性分组且组内有固定顺序，每个属性第一次出现的那条即展示项。
	first := make(map[string]bindingRow, len(bindings))
	for _, b := range bindings {
		counts[b.PropertyID]++
		if _, ok := first[b.PropertyID]; !ok {
			first[b.PropertyID] = b
		}
	}
	for i := range rows {
		rows[i].PointCount = counts[rows[i].ID]
		if b, ok := first[rows[i].ID]; ok {
			rows[i].PointDeviceName = b.DeviceName
			rows[i].PointName = b.PointName
		}
	}
	return nil
}

// PageThingModelProperties 分页列出某模型下的属性（排序与全量接口一致）。
//
// keyword 同时模糊匹配 identifier 与 name，与物模型列表的 keyword 同口径。
// 顺带返回模型内的最大 sort_order：前端分页后看不到全量列表，新增属性要接着
// 末尾排就得知道全量最大值，而不是当前页的最大值 —— 所以最大值**不随 keyword
// 变化**，始终按模型全量计算。
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
	if kw := strings.TrimSpace(req.Keyword); kw != "" {
		like := "%" + kw + "%"
		db = db.Where("identifier LIKE ? OR name LIKE ?", like, like)
	}
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
	if err := s.fillPropertyPointBindings(ctx, records); err != nil {
		return nil, err
	}
	return &vo.ThingModelPropertyPageVO{
		Page:         req.Page,
		Size:         req.Size,
		Total:        total,
		Records:      records,
		MaxSortOrder: maxSortOrder,
	}, nil
}

// ListThingModelPropertyPoints 列出绑定到某属性的设备点位（属性列表「点位数」抽屉）。
//
// 只回答“这个属性被哪些设备的哪些点位用着”；不做兼容性判断——能绑上就说明
// 创建/编辑时已经校验过，这里再算一遍只会让停用后的历史绑定看起来像错误。
func (s *ThingModelService) ListThingModelPropertyPoints(ctx context.Context, propertyID string) ([]vo.ThingModelPropertyPointVO, error) {
	propertyID = strings.TrimSpace(propertyID)
	if propertyID == "" {
		return nil, thingModelErr(enums.ParamValidEnum)
	}
	var property po.ThingModelProperty
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", propertyID).First(&property).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, thingModelErr(enums.ThingModelPropertyNotExistsEnum)
		}
		return nil, err
	}

	var rows []pointOptionRow
	if err := s.sqliteDB.WithContext(ctx).
		Table("device_address AS a").
		Select("a.id, a.device_id, a.property_id, a.name, a.label, a.common_data_type, a.data_type, "+
			"a.rw_permission, a.status, d.code AS device_code, d.name AS device_name, d.protocol_id").
		Joins("JOIN device AS d ON d.id = a.device_id").
		Where("a.property_id = ?", propertyID).
		Order("d.name ASC, a.id ASC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]vo.ThingModelPropertyPointVO, 0, len(rows))
	for _, r := range rows {
		out = append(out, vo.ThingModelPropertyPointVO{
			AddressID:      r.ID,
			DeviceID:       r.DeviceID,
			DeviceCode:     r.DeviceCode,
			DeviceName:     r.DeviceName,
			Name:           r.Name,
			Label:          r.Label,
			CommonDataType: r.CommonDataType,
			DataType:       r.DataType,
			RwPermission:   r.RwPermission,
			Status:         r.Status,
		})
	}
	return out, nil
}

// pointOptionRow 是点位候选的联表查询行：点位自身字段 + 所属设备的展示字段。
type pointOptionRow struct {
	ID             string `gorm:"column:id"`
	DeviceID       string `gorm:"column:device_id"`
	PropertyID     string `gorm:"column:property_id"`
	Name           string `gorm:"column:name"`
	Label          string `gorm:"column:label"`
	CommonDataType string `gorm:"column:common_data_type"`
	DataType       string `gorm:"column:data_type"`
	RwPermission   string `gorm:"column:rw_permission"`
	Status         int    `gorm:"column:status"`
	DeviceCode     string `gorm:"column:device_code"`
	DeviceName     string `gorm:"column:device_name"`
	ProtocolID     string `gorm:"column:protocol_id"`
}

// pointOptionSelect 是点位候选联表的固定列集，全量/分页两条路径共用。
const pointOptionSelect = "a.id, a.device_id, a.property_id, a.name, a.label, a.common_data_type, a.data_type, " +
	"a.rw_permission, a.status, d.code AS device_code, d.name AS device_name, d.protocol_id"

// ListThingModelPointOptions 属性表单的点位候选：返回全部设备的点位。
//
// 属性与设备之间没有绑定关系，点位可来自任意设备，因此这里不做设备过滤。
// 用 join 一次取完，不先查 device_id 再拼大 IN —— 设备多时后者会撞 SQLite
// 参数上限。不要求模型启用：停用的模型也必须能打开表单解绑。
//
// 属性表单已改为「设备 → 点位」两级分页选择，此接口保留给仍按全量拉取
// 的调用方（如 agentApi 镜像）；新页面请用 PageThingModelPointOptions。
func (s *ThingModelService) ListThingModelPointOptions(ctx context.Context) ([]vo.ThingModelPointOptionVO, error) {
	var rows []pointOptionRow
	if err := s.sqliteDB.WithContext(ctx).
		Table("device_address AS a").
		Select(pointOptionSelect).
		Joins("JOIN device AS d ON d.id = a.device_id").
		Order("d.name ASC, a.id ASC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	return s.pointOptionsToVOs(ctx, rows)
}

// PageThingModelPointOptions 属性表单的点位候选分页（按设备）。
//
// keyword 同时模糊匹配地址名与说明；空串不过滤。排序按 (名称, id)：
// 同名的点位在批量导入后很常见，只按名称排序会让翻页重复或漏行。
func (s *ThingModelService) PageThingModelPointOptions(
	ctx context.Context, req *dto.PageThingModelPointOptionDTO,
) (*response.PageVo[vo.ThingModelPointOptionVO], error) {
	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		return nil, thingModelErr(enums.ParamValidEnum)
	}

	db := s.sqliteDB.WithContext(ctx).
		Table("device_address AS a").
		Where("a.device_id = ?", deviceID)
	if keyword := strings.TrimSpace(req.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		db = db.Where("a.name LIKE ? OR a.label LIKE ?", pattern, pattern)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var rows []pointOptionRow
	offset := (req.Page - 1) * req.Size
	if err := db.Select(pointOptionSelect).
		Joins("JOIN device AS d ON d.id = a.device_id").
		Order("a.name ASC, a.id ASC").
		Offset(offset).Limit(req.Size).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	records, err := s.pointOptionsToVOs(ctx, rows)
	if err != nil {
		return nil, err
	}
	return &response.PageVo[vo.ThingModelPointOptionVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}

// PageThingModelPointDevices 属性绑定点位时的设备候选分页。
//
// 只返回名下至少有一个点位的设备：没有点位的设备在这里选了也没有可绑对象。
// PointCount 一并返回，前端可以直接展示规模而不用为每台设备再查一轮。
func (s *ThingModelService) PageThingModelPointDevices(
	ctx context.Context, req *dto.PageThingModelPointDeviceDTO,
) (*response.PageVo[vo.ThingModelPointDeviceVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.Device{}).
		Where("EXISTS (SELECT 1 FROM device_address AS a WHERE a.device_id = device.id)")
	if keyword := strings.TrimSpace(req.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		db = db.Where("(name LIKE ? OR code LIKE ?)", pattern, pattern)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var rows []struct {
		ID         string `gorm:"column:id"`
		Code       string `gorm:"column:code"`
		Name       string `gorm:"column:name"`
		Status     int    `gorm:"column:status"`
		PointCount int64  `gorm:"column:point_count"`
	}
	pointsDB := s.sqliteDB.WithContext(ctx).
		Table("device AS d").
		Select("d.id, d.code, d.name, d.status, COUNT(a.id) AS point_count").
		Joins("JOIN device_address AS a ON a.device_id = d.id")
	if keyword := strings.TrimSpace(req.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		pointsDB = pointsDB.Where("(d.name LIKE ? OR d.code LIKE ?)", pattern, pattern)
	}
	offset := (req.Page - 1) * req.Size
	if err := pointsDB.
		Group("d.id, d.code, d.name, d.status").
		Order("d.name ASC, d.id ASC").
		Offset(offset).Limit(req.Size).
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	records := make([]vo.ThingModelPointDeviceVO, len(rows))
	for i, r := range rows {
		records[i] = vo.ThingModelPointDeviceVO{
			ID:         r.ID,
			Code:       r.Code,
			Name:       r.Name,
			Status:     r.Status,
			PointCount: r.PointCount,
		}
	}
	return &response.PageVo[vo.ThingModelPointDeviceVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}

// pointOptionsToVOs 把点位候选联表行补全成 VO（协议类型、已绑定属性与模型名）。
//
// 全量选项与分页选项共用这一份映射：两处各写一份时，将来加字段只会加进
// 其中一条路径，「分页里看不到已绑定属性」这类问题就会悄悄出现。
func (s *ThingModelService) pointOptionsToVOs(
	ctx context.Context, rows []pointOptionRow,
) ([]vo.ThingModelPointOptionVO, error) {
	if len(rows) == 0 {
		return []vo.ThingModelPointOptionVO{}, nil
	}

	protocolIDs := make([]string, 0)
	seenProtocol := make(map[string]struct{})
	for _, r := range rows {
		if r.ProtocolID == "" {
			continue
		}
		if _, ok := seenProtocol[r.ProtocolID]; ok {
			continue
		}
		seenProtocol[r.ProtocolID] = struct{}{}
		protocolIDs = append(protocolIDs, r.ProtocolID)
	}
	protocolNames := make(map[string]string, len(protocolIDs))
	if len(protocolIDs) > 0 {
		var protocols []po.IotProtocol
		if err := s.sqliteDB.WithContext(ctx).Select("id", "name").
			Where("id IN ?", protocolIDs).Find(&protocols).Error; err != nil {
			return nil, err
		}
		for _, p := range protocols {
			protocolNames[p.ID] = p.Name
		}
	}

	propertyIDs := make([]string, 0, len(rows))
	seenProperty := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		if r.PropertyID == "" {
			continue
		}
		if _, ok := seenProperty[r.PropertyID]; ok {
			continue
		}
		seenProperty[r.PropertyID] = struct{}{}
		propertyIDs = append(propertyIDs, r.PropertyID)
	}
	propByID := make(map[string]po.ThingModelProperty, len(propertyIDs))
	if len(propertyIDs) > 0 {
		var props []po.ThingModelProperty
		if err := s.sqliteDB.WithContext(ctx).Where("id IN ?", propertyIDs).Find(&props).Error; err != nil {
			return nil, err
		}
		for _, p := range props {
			propByID[p.ID] = p
		}
	}

	// 属性标识只在模型内唯一，前端展示「已绑定属性」时要带模型名才不会指向不明。
	modelIDs := make([]string, 0)
	seenModel := make(map[string]struct{}, len(propertyIDs))
	for _, propertyID := range propertyIDs {
		modelID := propByID[propertyID].ModelID
		if modelID == "" {
			continue
		}
		if _, ok := seenModel[modelID]; ok {
			continue
		}
		seenModel[modelID] = struct{}{}
		modelIDs = append(modelIDs, modelID)
	}
	modelNames := make(map[string]string, len(modelIDs))
	if len(modelIDs) > 0 {
		var models []po.ThingModel
		if err := s.sqliteDB.WithContext(ctx).Select("id", "name").
			Where("id IN ?", modelIDs).Find(&models).Error; err != nil {
			return nil, err
		}
		for _, m := range models {
			modelNames[m.ID] = m.Name
		}
	}

	out := make([]vo.ThingModelPointOptionVO, 0, len(rows))
	for _, r := range rows {
		prop := propByID[r.PropertyID]
		out = append(out, vo.ThingModelPointOptionVO{
			AddressID:          r.ID,
			DeviceID:           r.DeviceID,
			DeviceCode:         r.DeviceCode,
			DeviceName:         r.DeviceName,
			Name:               r.Name,
			Label:              r.Label,
			CommonDataType:     r.CommonDataType,
			DataType:           r.DataType,
			Kind:               kindOfAddressType(protocolNames[r.ProtocolID], r.DataType),
			RwPermission:       r.RwPermission,
			PropertyID:         r.PropertyID,
			PropertyIdentifier: prop.Identifier,
			PropertyName:       prop.Name,
			ModelID:            prop.ModelID,
			ModelName:          modelNames[prop.ModelID],
			Status:             r.Status,
		})
	}
	return out, nil
}

// CreateThingModelProperty 创建物模型属性
func (s *ThingModelService) CreateThingModelProperty(ctx context.Context, req *dto.CreateThingModelPropertyDTO) error {
	identifier := strings.TrimSpace(req.Identifier)
	name := strings.TrimSpace(req.Name)
	if !thingModelIdentifierPattern.MatchString(identifier) {
		return thingModelErr(enums.ThingModelIdentifierInvalidEnum)
	}
	if name == "" || (req.Kind != "" && !isThingModelKind(req.Kind)) || !isThingModelAccessMode(req.AccessMode) {
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
		prop := &po.ThingModelProperty{
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
		}
		if err := tx.Create(prop).Error; err != nil {
			return err
		}
		if len(req.AddressIDs) > 0 {
			return bindAddressesToProperty(ctx, tx, prop.ID, req.AddressIDs)
		}
		return nil
	})
}

// UpdateThingModelProperty 更新物模型属性。
//
// Identifier 可修改（模型内唯一）：点位绑定按 property_id 关联，改名不会断开；
// 但映射、脚本与推送里按 identifier 引用它的地方需要同步更新。采集侧靠配置
// 校验和感知改名并重建任务，记录里的 propertyCode 才会换成新值。
func (s *ThingModelService) UpdateThingModelProperty(ctx context.Context, req *dto.UpdateThingModelPropertyDTO) error {
	name := strings.TrimSpace(req.Name)
	if name == "" || (req.Kind != "" && !isThingModelKind(req.Kind)) || !isThingModelAccessMode(req.AccessMode) {
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
		// 改名按模型内唯一校验：撞上同模型的其它属性直接拒绝，不能等到
		// 唯一索引报错才给一个看不懂的「保存失败」。
		renameTo := ""
		if identifier != "" && identifier != existing.Identifier {
			var count int64
			if err := tx.Model(&po.ThingModelProperty{}).
				Where("model_id = ? AND identifier = ? AND id <> ?", existing.ModelID, identifier, existing.ID).
				Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return thingModelErr(enums.ThingModelPropertyExistsEnum)
			}
			renameTo = identifier
		}

		// 点位同步：先按表单解绑被移除的点位，再做 access 的 41018 判定。
		// 顺序很重要 —— 这样「改读写模式 + 同时清空全部绑定」可以一次保存完成，
		// 而只要还留着任何绑定，改读写模式仍然被拒绝（事务回滚后绑定原样恢复）。
		// kind 只是说明，不参与这项判定。
		var added []string
		if req.AddressIDs != nil {
			desired, err := normalizeThingModelBindingIDs(*req.AddressIDs)
			if err != nil {
				return err
			}
			if err := ensureSinglePointBinding(desired); err != nil {
				return err
			}
			desiredSet := make(map[string]struct{}, len(desired))
			for _, id := range desired {
				desiredSet[id] = struct{}{}
			}
			var currentIDs []string
			if err := tx.Model(&po.DeviceAddress{}).Where("property_id = ?", req.ID).
				Pluck("id", &currentIDs).Error; err != nil {
				return err
			}
			currentSet := make(map[string]struct{}, len(currentIDs))
			for _, id := range currentIDs {
				currentSet[id] = struct{}{}
			}
			var removed []string
			for _, id := range currentIDs {
				if _, ok := desiredSet[id]; !ok {
					removed = append(removed, id)
				}
			}
			for _, id := range desired {
				if _, ok := currentSet[id]; !ok {
					added = append(added, id)
				}
			}
			if len(removed) > 0 {
				if err := tx.Model(&po.DeviceAddress{}).Where("id IN ?", removed).
					Updates(map[string]any{"property_id": "", "updated_at": nowString()}).Error; err != nil {
					return err
				}
			}
		}

		// 已被点位绑定后，access_mode 是绑定的兼容性前提：改了会让已有绑定
		// 悄悄失效（采集本身照常，只是语义不对）。要求先解绑再改。
		// kind 只是语义说明，改它不影响任何绑定，随时可改。
		if req.AccessMode != existing.AccessMode {
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
		if renameTo != "" {
			updates["identifier"] = renameTo
		}
		if req.Status != nil {
			updates["status"] = *req.Status
		}
		// 先落属性行，新增点位才能按新的 access/status 校验；
		// 保留下来的旧绑定不重复校验（模型/属性事后停用不应阻止改名保存）。
		if err := tx.Model(&po.ThingModelProperty{}).Where("id = ?", req.ID).Updates(updates).Error; err != nil {
			return err
		}
		if len(added) > 0 {
			return bindAddressesToProperty(ctx, tx, req.ID, added)
		}
		return nil
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
// （code 忽略大小写）。数据类型留空表示未指定（它只是语义说明）。排序留空时按
// 模型内最大值 +10 顺延；其余行部分导入。
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
	// 数据类型只是语义说明：留空 = 未指定，填了才要求是下拉项之一。
	kind = ""
	if kindRaw != "" {
		var kindOK bool
		kind, kindOK = constants.ParseEnumValue(kindRaw, deviceKindCodes, deviceKindDisplayNames)
		if !kindOK {
			return "", "", "数据类型须为下拉选项（如「浮点（float）」），留空表示未指定"
		}
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
				Prompt:  "可选，仅作说明；留空表示未指定",
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
