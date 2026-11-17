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
	"gorm.io/gorm/clause"

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

// syncModelDevices 以 desired 为准同步 device.model_id（事务内调用）。
//
// 新增设备必须存在且模型可绑定；任何被新增或解绑的设备只要还有一个点位绑着
// 属性，就整单拒绝（41013）—— 换绑会让那些属性归属到别的模型，语义悬空。
func syncModelDevices(tx *gorm.DB, modelID string, desiredIDs []string) error {
	desired, err := normalizeThingModelBindingIDs(desiredIDs)
	if err != nil {
		return err
	}
	desiredSet := make(map[string]struct{}, len(desired))
	for _, id := range desired {
		desiredSet[id] = struct{}{}
	}

	var currentIDs []string
	if err := tx.Model(&po.Device{}).Where("model_id = ?", modelID).Pluck("id", &currentIDs).Error; err != nil {
		return err
	}
	currentSet := make(map[string]struct{}, len(currentIDs))
	for _, id := range currentIDs {
		currentSet[id] = struct{}{}
	}

	var added, removed []string
	for _, id := range desired {
		if _, ok := currentSet[id]; !ok {
			added = append(added, id)
		}
	}
	for _, id := range currentIDs {
		if _, ok := desiredSet[id]; !ok {
			removed = append(removed, id)
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}

	if len(added) > 0 {
		// 与单独改设备的规则一致：模型必须存在且启用。调用方已先落 status，
		// 所以「同一次保存里启用模型 + 加设备」能通过。
		if err := ensureThingModelBindable(tx, modelID); err != nil {
			return err
		}
		var found []string
		if err := tx.Model(&po.Device{}).Where("id IN ?", added).Pluck("id", &found).Error; err != nil {
			return err
		}
		if len(found) != len(added) {
			return thingModelErr(enums.DeviceNotExistsEnum)
		}
	}

	changed := make([]string, 0, len(added)+len(removed))
	changed = append(changed, added...)
	changed = append(changed, removed...)
	var lockedIDs []string
	if err := tx.Model(&po.DeviceAddress{}).
		Distinct("device_id").
		Where("device_id IN ? AND property_id <> ''", changed).
		Pluck("device_id", &lockedIDs).Error; err != nil {
		return err
	}
	if len(lockedIDs) > 0 {
		return thingModelErr(enums.ThingModelBindingLockedEnum)
	}

	now := nowString()
	if len(added) > 0 {
		if err := tx.Model(&po.Device{}).Where("id IN ?", added).
			Updates(map[string]any{"model_id": modelID, "updated_at": now}).Error; err != nil {
			return err
		}
	}
	if len(removed) > 0 {
		if err := tx.Model(&po.Device{}).Where("id IN ?", removed).
			Updates(map[string]any{"model_id": "", "updated_at": now}).Error; err != nil {
			return err
		}
	}
	return nil
}

// bindAddressesToProperty 把一批点位绑定到属性（事务内调用）。
//
// 先复用地址侧的唯一绑定校验（validatePropertyBindingDB），再改 property_id；
// 已绑其他属性的点位直接拒绝，绝不静默抢绑。
func bindAddressesToProperty(ctx context.Context, tx *gorm.DB, propertyID string, addressIDs []string) error {
	ids, err := normalizeThingModelBindingIDs(addressIDs)
	if err != nil {
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

	deviceIDs := make([]string, 0, len(addrs))
	seenDevice := make(map[string]struct{}, len(addrs))
	for _, a := range addrs {
		if _, ok := seenDevice[a.DeviceID]; ok {
			continue
		}
		seenDevice[a.DeviceID] = struct{}{}
		deviceIDs = append(deviceIDs, a.DeviceID)
	}
	var devices []po.Device
	if err := tx.WithContext(ctx).Where("id IN ?", deviceIDs).Find(&devices).Error; err != nil {
		return err
	}
	if len(devices) != len(deviceIDs) {
		return thingModelErr(enums.DeviceNotExistsEnum)
	}
	deviceByID := make(map[string]po.Device, len(devices))
	for _, d := range devices {
		deviceByID[d.ID] = d
	}

	for _, a := range addrs {
		if a.PropertyID != "" && a.PropertyID != propertyID {
			return thingModelErr(enums.ThingModelPointBoundElsewhereEnum)
		}
	}

	// 同一设备对同一属性只能绑一个点位。027 迁移里的部分唯一索引
	// idx_device_address_device_property 是硬约束；这里先给出可读业务错误，
	// 免得 SQLite 的 UNIQUE constraint 文案直接冒到界面上。
	batchDevice := make(map[string]struct{}, len(addrs))
	for _, a := range addrs {
		if _, ok := batchDevice[a.DeviceID]; ok {
			return thingModelErr(enums.ThingModelDevicePointDupEnum)
		}
		batchDevice[a.DeviceID] = struct{}{}
	}
	// 更新场景：批内只包含新增点位，同设备上“保留不动”的旧绑定也要算冲突。
	var existing []po.DeviceAddress
	if err := tx.WithContext(ctx).
		Where("property_id = ? AND device_id IN ?", propertyID, deviceIDs).
		Find(&existing).Error; err != nil {
		return err
	}
	inBatch := make(map[string]struct{}, len(addrs))
	for _, a := range addrs {
		inBatch[a.ID] = struct{}{}
	}
	for _, e := range existing {
		if _, ok := inBatch[e.ID]; !ok {
			return thingModelErr(enums.ThingModelDevicePointDupEnum)
		}
	}

	for _, a := range addrs {
		device := deviceByID[a.DeviceID]
		protocolName, err := protocolNameByID(tx.WithContext(ctx), device.ProtocolID)
		if err != nil {
			return err
		}
		if err := validatePropertyBindingDB(ctx, tx.WithContext(ctx), device, propertyID,
			protocolName, a.DataType, a.RwPermission, false); err != nil {
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
		if err := tx.Create(model).Error; err != nil {
			return err
		}
		if len(req.DeviceIDs) > 0 {
			return syncModelDevices(tx, model.ID, req.DeviceIDs)
		}
		return nil
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
		// 先落模型行（含 status），再同步设备：这样同一次保存里「启用模型 + 加设备」
		// 能看到新状态，「停用 + 加设备」会被 ensureThingModelBindable 拦下。
		if err := tx.Model(&po.ThingModel{}).Where("id = ?", req.ID).Updates(updates).Error; err != nil {
			return err
		}
		if req.DeviceIDs != nil {
			return syncModelDevices(tx, req.ID, *req.DeviceIDs)
		}
		return nil
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
	if err := s.fillPropertyPointCounts(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// fillPropertyPointCounts 一次性补齐属性的点位绑定数。
// 分页后一页最多 100 个属性，按 property_id IN (...) 分组计数，避免逐行查询。
func (s *ThingModelService) fillPropertyPointCounts(ctx context.Context, rows []vo.ThingModelPropertyVO) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	type countRow struct {
		PropertyID string `gorm:"column:property_id"`
		Count      int64  `gorm:"column:count"`
	}
	var counts []countRow
	if err := s.sqliteDB.WithContext(ctx).Model(&po.DeviceAddress{}).
		Select("property_id, COUNT(*) AS count").
		Where("property_id IN ?", ids).
		Group("property_id").
		Scan(&counts).Error; err != nil {
		return err
	}
	byID := make(map[string]int64, len(counts))
	for _, c := range counts {
		byID[c.PropertyID] = c.Count
	}
	for i := range rows {
		rows[i].PointCount = byID[rows[i].ID]
	}
	return nil
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
	if err := s.fillPropertyPointCounts(ctx, records); err != nil {
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

// thingModelDeviceOptionLimit 设备候选单次返回上限。
// 与绑定上限分开：这是“下拉候选”的显示上限，不是一次能绑多少个。
const thingModelDeviceOptionLimit = 200

// ListThingModelDeviceOptions 物模型表单的设备候选（远程搜索）。
//
// modelId 可选：传了就先校验模型存在，编辑一个已被删除的模型时能尽早失败；
// 同时把绑定到该模型的设备排在前面，保证编辑态打开时已选设备在首批结果里。
// keyword 模糊匹配设备名称与编码。返回全部“可绑定 + 已绑定其他模型 + 已锁”
// 的设备，是为了让前端把不可选的原因说明白，而不是让人疑惑“设备去哪了”。
func (s *ThingModelService) ListThingModelDeviceOptions(ctx context.Context, modelID, keyword string) (*vo.ThingModelDeviceOptionPageVO, error) {
	modelID = strings.TrimSpace(modelID)
	keyword = strings.TrimSpace(keyword)
	if modelID != "" {
		var model po.ThingModel
		if err := s.sqliteDB.WithContext(ctx).Where("id = ?", modelID).First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, thingModelErr(enums.ThingModelNotExistsEnum)
			}
			return nil, err
		}
	}

	db := s.sqliteDB.WithContext(ctx).Model(&po.Device{})
	if keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where("name LIKE ? OR code LIKE ?", like, like)
	}
	// 绑定当前模型的设备排在最前：编辑态打开弹窗时，已选设备必须在首批结果里，
	// 否则标签只剩 id（远程搜索不会把整张设备表都发给前端）。
	//
	// 排序必须合成一条 Expression：GORM 的 OrderBy.MergeClause 在“表达式 + 列”
	// 混用时会把前一条 Expression 丢掉，分两次 Order 会静默退化成只按名称排。
	if modelID != "" {
		db = db.Order(clause.OrderBy{Expression: clause.Expr{
			SQL:  "CASE WHEN model_id = ? THEN 0 ELSE 1 END, name ASC, code ASC",
			Vars: []interface{}{modelID},
		}})
	} else {
		db = db.Order("name ASC, code ASC")
	}
	var devices []po.Device
	if err := db.Limit(thingModelDeviceOptionLimit + 1).Find(&devices).Error; err != nil {
		return nil, err
	}
	truncated := len(devices) > thingModelDeviceOptionLimit
	if truncated {
		devices = devices[:thingModelDeviceOptionLimit]
	}

	modelIDs := make([]string, 0)
	seenModel := make(map[string]struct{})
	for _, d := range devices {
		if d.ModelID == "" {
			continue
		}
		if _, ok := seenModel[d.ModelID]; ok {
			continue
		}
		seenModel[d.ModelID] = struct{}{}
		modelIDs = append(modelIDs, d.ModelID)
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

	var lockedIDs []string
	if err := s.sqliteDB.WithContext(ctx).Model(&po.DeviceAddress{}).
		Where("property_id <> ''").Distinct("device_id").
		Pluck("device_id", &lockedIDs).Error; err != nil {
		return nil, err
	}
	locked := make(map[string]struct{}, len(lockedIDs))
	for _, id := range lockedIDs {
		locked[id] = struct{}{}
	}

	out := make([]vo.ThingModelDeviceOptionVO, 0, len(devices))
	for _, d := range devices {
		_, isLocked := locked[d.ID]
		out = append(out, vo.ThingModelDeviceOptionVO{
			ID:        d.ID,
			Code:      d.Code,
			Name:      d.Name,
			ModelID:   d.ModelID,
			ModelName: modelNames[d.ModelID],
			Locked:    isLocked,
			Status:    d.Status,
		})
	}
	return &vo.ThingModelDeviceOptionPageVO{Records: out, Truncated: truncated}, nil
}

// ListThingModelDevices 列出绑定到某模型的设备（列表页「设备数」抽屉用）。
//
// 不分页：这里回答的是“这个模型被哪些设备用着”，数量通常有限；真到几百台
// 时表格自身可滚动，比再加一套分页更直接。
func (s *ThingModelService) ListThingModelDevices(ctx context.Context, modelID string) ([]vo.ThingModelBoundDeviceVO, error) {
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

	var devices []po.Device
	if err := s.sqliteDB.WithContext(ctx).
		Where("model_id = ?", modelID).
		Order("name ASC, code ASC").
		Find(&devices).Error; err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return []vo.ThingModelBoundDeviceVO{}, nil
	}

	protocolIDs := make([]string, 0)
	seenProtocol := make(map[string]struct{}, len(devices))
	for _, d := range devices {
		if d.ProtocolID == "" {
			continue
		}
		if _, ok := seenProtocol[d.ProtocolID]; ok {
			continue
		}
		seenProtocol[d.ProtocolID] = struct{}{}
		protocolIDs = append(protocolIDs, d.ProtocolID)
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

	out := make([]vo.ThingModelBoundDeviceVO, 0, len(devices))
	for _, d := range devices {
		out = append(out, vo.ThingModelBoundDeviceVO{
			ID:           d.ID,
			Code:         d.Code,
			Name:         d.Name,
			ProtocolID:   d.ProtocolID,
			ProtocolName: protocolNames[d.ProtocolID],
			Status:       d.Status,
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

// ListThingModelPointOptions 属性表单的点位候选：只返回已绑定该模型的设备下的点位。
//
// 用 join 一次取完，不先查 device_id 再拼大 IN —— 设备多时后者会撞 SQLite
// 参数上限。不要求模型启用：停用的模型也必须能打开表单解绑。
func (s *ThingModelService) ListThingModelPointOptions(ctx context.Context, modelID string) ([]vo.ThingModelPointOptionVO, error) {
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

	var rows []pointOptionRow
	if err := s.sqliteDB.WithContext(ctx).
		Table("device_address AS a").
		Select("a.id, a.device_id, a.property_id, a.name, a.label, a.common_data_type, a.data_type, "+
			"a.rw_permission, a.status, d.code AS device_code, d.name AS device_name, d.protocol_id").
		Joins("JOIN device AS d ON d.id = a.device_id").
		Where("d.model_id = ?", modelID).
		Order("d.name ASC, a.id ASC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
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

	var props []po.ThingModelProperty
	if err := s.sqliteDB.WithContext(ctx).Where("model_id = ?", modelID).Find(&props).Error; err != nil {
		return nil, err
	}
	propByID := make(map[string]po.ThingModelProperty, len(props))
	for _, p := range props {
		propByID[p.ID] = p
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

		// 点位同步：先按表单解绑被移除的点位，再做 kind/access 的 41018 判定。
		// 顺序很重要 —— 这样「改类型 + 同时清空全部绑定」可以一次保存完成，
		// 而只要还留着任何绑定，改类型仍然被拒绝（事务回滚后绑定原样恢复）。
		var added []string
		if req.AddressIDs != nil {
			desired, err := normalizeThingModelBindingIDs(*req.AddressIDs)
			if err != nil {
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
		// 先落属性行，新增点位才能按新的 kind/access/status 校验；
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
