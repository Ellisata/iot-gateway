// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/constants"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
)

// openApiThingModelUpsertMaxProperties 单个模型单次提交的属性上限。
// 与设备导入等批量接口的量级保持一致，防止一次请求把前端/数据库拖住。
const openApiThingModelUpsertMaxProperties = 200

// BatchUpsertOpenApiThingModels 开放接口：物模型与属性批量 upsert。
//
// 自然键是 thing_model.code；属性在其模型内以 identifier 为自然键。语义为
// 「没有则创建，有则更新」，**永不删除**请求中未出现的模型或属性 —— 删除会
// 让已有点位绑定静默失效，不是外部同步接口应有的副作用。
//
// 每个模型一个独立事务：某模型失败只回滚该模型，其余模型继续，逐条结果在
// Models 中按请求顺序返回。模型级失败不会让顶层响应变成错误信封。
func (s *ThingModelService) BatchUpsertOpenApiThingModels(ctx context.Context, req *dto.OpenApiThingModelBatchUpsertDTO) (*vo.OpenApiThingModelBatchUpsertVO, error) {
	if req == nil || len(req.Models) == 0 {
		return nil, thingModelErr(enums.ParamValidEnum)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := &vo.OpenApiThingModelBatchUpsertVO{
		Total:  len(req.Models),
		Models: make([]vo.OpenApiThingModelUpsertItemVO, 0, len(req.Models)),
	}
	seenCodes := make(map[string]bool, len(req.Models))

	// 顺序处理：SQLite 只有一条写连接，并发事务只会互相等待；
	// 顺序还能让「批次内重复 code」的结果稳定（首次出现者生效）。
	for i := range req.Models {
		reqItem := &req.Models[i]
		code := strings.TrimSpace(reqItem.Code)
		name := strings.TrimSpace(reqItem.Name)

		if code != "" && seenCodes[code] {
			result.Models = append(result.Models, failedThingModelItem(
				code, name,
				enums.ThingModelExistsEnum.GetCode(),
				"批次内模型标识重复",
			))
			result.Failed++
			continue
		}
		if code != "" {
			seenCodes[code] = true
		}

		item := s.upsertOpenApiThingModel(ctx, reqItem, code, name)
		result.Models = append(result.Models, item)

		switch item.Action {
		case vo.ThingModelUpsertActionCreated:
			result.Success++
			result.Created++
		case vo.ThingModelUpsertActionUpdated:
			result.Success++
			result.Updated++
		default:
			result.Failed++
		}
		for _, p := range item.Properties {
			switch p.Action {
			case vo.ThingModelUpsertActionCreated:
				result.PropertiesCreated++
			case vo.ThingModelUpsertActionUpdated:
				result.PropertiesUpdated++
			}
		}
	}
	return result, nil
}

// upsertOpenApiThingModel 处理单个模型（含其全部属性）的 upsert，失败时把
// 错误转成逐条结果而不是 Go error —— 批量接口的部分成功靠这个边界成立。
func (s *ThingModelService) upsertOpenApiThingModel(
	ctx context.Context,
	req *dto.OpenApiThingModelUpsertDTO,
	code, name string,
) vo.OpenApiThingModelUpsertItemVO {
	item := vo.OpenApiThingModelUpsertItemVO{
		Code:       code,
		Name:       name,
		Properties: make([]vo.OpenApiThingModelPropertyUpsertItemVO, 0, len(req.Properties)),
	}

	if err := validateOpenApiThingModelItem(req, code, name); err != nil {
		return failThingModelItem(item, err, ctx)
	}
	if len(req.Properties) > openApiThingModelUpsertMaxProperties {
		return failThingModelItem(item,
			openApiThingModelParamErr("单个模型的属性数量不能超过200"), ctx)
	}

	var (
		model      po.ThingModel
		action     string
		properties []vo.OpenApiThingModelPropertyUpsertItemVO
		// failedIdentifier 记录导致整模型回滚的属性标识，失败明细里要能指出来。
		failedIdentifier string
	)

	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		model, action, err = upsertOpenApiThingModelRow(tx, req, code, name)
		if err != nil {
			return err
		}
		properties, failedIdentifier, err = upsertOpenApiThingModelProperties(tx, model.ID, req.Properties)
		if err != nil {
			return err
		}
		if req.ReplaceProperties != nil && *req.ReplaceProperties {
			return deleteOpenApiThingModelPropertiesNotIn(tx, model.ID, req.Properties)
		}
		return nil
	})
	if err != nil {
		item = failThingModelItem(item, err, ctx)
		if failedIdentifier != "" {
			item.Properties = []vo.OpenApiThingModelPropertyUpsertItemVO{{
				Identifier:   failedIdentifier,
				Action:       vo.ThingModelUpsertActionFailed,
				ErrorCode:    item.ErrorCode,
				ErrorMessage: item.ErrorMessage,
			}}
		}
		return item
	}

	item.Action = action
	item.ID = model.ID
	item.Properties = properties
	if hash, err := s.openApiThingModelContentHash(ctx, model.ID); err == nil {
		item.ContentHash = hash
	}
	return item
}

// deleteOpenApiThingModelPropertiesNotIn 删除模型下未出现在本次 payload 的属性。
//
// 只由 replaceProperties=true 触发。被点位绑定的属性拒绝删除（41006），
// 整个模型回滚，避免留下「点位绑着已删除属性」的悬空绑定。
func deleteOpenApiThingModelPropertiesNotIn(tx *gorm.DB, modelID string, props []dto.OpenApiThingModelPropertyUpsertDTO) error {
	keep := make(map[string]bool, len(props))
	for _, p := range props {
		if id := strings.TrimSpace(p.Identifier); id != "" {
			keep[id] = true
		}
	}
	var existing []po.ThingModelProperty
	if err := tx.Where("model_id = ?", modelID).Find(&existing).Error; err != nil {
		return err
	}
	for _, p := range existing {
		if keep[p.Identifier] {
			continue
		}
		var bound int64
		if err := tx.Model(&po.DeviceAddress{}).Where("property_id = ?", p.ID).Count(&bound).Error; err != nil {
			return err
		}
		if bound > 0 {
			return thingModelErr(enums.ThingModelPropertyInUseEnum)
		}
		if err := tx.Where("id = ?", p.ID).Delete(&po.ThingModelProperty{}).Error; err != nil {
			return err
		}
	}
	return nil
}

// openApiThingModelContentHash 回读模型与属性并计算 contentHash。
func (s *ThingModelService) openApiThingModelContentHash(ctx context.Context, modelID string) (string, error) {
	var model po.ThingModel
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", modelID).First(&model).Error; err != nil {
		return "", err
	}
	var props []po.ThingModelProperty
	if err := s.sqliteDB.WithContext(ctx).Where("model_id = ?", modelID).
		Order("sort_order ASC, identifier ASC").Find(&props).Error; err != nil {
		return "", err
	}
	return thingModelConfigHash(model, props)
}

// upsertOpenApiThingModelRow 在事务内按 code 创建或更新模型行。
// code 是匹配键，更新时绝不改写；可选字段只在请求显式给出时覆盖。
func upsertOpenApiThingModelRow(tx *gorm.DB, req *dto.OpenApiThingModelUpsertDTO, code, name string) (po.ThingModel, string, error) {
	var existing po.ThingModel
	err := tx.Where("code = ?", code).First(&existing).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		var dup int64
		if err := tx.Model(&po.ThingModel{}).Where("name = ?", name).Count(&dup).Error; err != nil {
			return po.ThingModel{}, "", err
		}
		if dup > 0 {
			return po.ThingModel{}, "", thingModelErr(enums.ThingModelExistsEnum)
		}

		category := ""
		if req.Category != nil {
			// 平台可以下发自定义分类，统一按小写 code 落库，避免同一分类出现大小写副本。
			category = strings.ToLower(strings.TrimSpace(*req.Category))
		}
		version := "1.0.0"
		if req.Version != nil {
			if v := strings.TrimSpace(*req.Version); v != "" {
				version = v
			}
		}
		description := ""
		if req.Description != nil {
			description = *req.Description
		}
		status := 1
		if req.Status != nil {
			status = *req.Status
		}

		now := nowString()
		model := po.ThingModel{
			Code:        code,
			Name:        name,
			Category:    category,
			Version:     version,
			Description: description,
			Status:      status,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Create(&model).Error; err != nil {
			return po.ThingModel{}, "", err
		}
		return model, vo.ThingModelUpsertActionCreated, nil

	case err != nil:
		return po.ThingModel{}, "", err
	}

	var dup int64
	if err := tx.Model(&po.ThingModel{}).
		Where("name = ? AND id != ?", name, existing.ID).
		Count(&dup).Error; err != nil {
		return po.ThingModel{}, "", err
	}
	if dup > 0 {
		return po.ThingModel{}, "", thingModelErr(enums.ThingModelExistsEnum)
	}

	updates := map[string]any{
		"name":       name,
		"updated_at": nowString(),
	}
	if req.Category != nil {
		updates["category"] = strings.ToLower(strings.TrimSpace(*req.Category))
	}
	if req.Version != nil {
		version := strings.TrimSpace(*req.Version)
		if version == "" {
			version = "1.0.0"
		}
		updates["version"] = version
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if err := tx.Model(&po.ThingModel{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
		return po.ThingModel{}, "", err
	}
	existing.Name = name
	return existing, vo.ThingModelUpsertActionUpdated, nil
}

// upsertOpenApiThingModelProperties 在事务内 upsert 模型的全部属性。
//
// 返回的 failedIdentifier 在出错时指出是哪条属性；调用方据此在结果里给出指向。
// 批次内重复 identifier 视为整模型失败（与「属性标识符在模型内唯一」同口径），
// 而不是静默让后一条覆盖前一条。
func upsertOpenApiThingModelProperties(
	tx *gorm.DB,
	modelID string,
	props []dto.OpenApiThingModelPropertyUpsertDTO,
) ([]vo.OpenApiThingModelPropertyUpsertItemVO, string, error) {
	results := make([]vo.OpenApiThingModelPropertyUpsertItemVO, 0, len(props))
	if len(props) == 0 {
		return results, "", nil
	}

	var maxSortOrder int
	if err := tx.Model(&po.ThingModelProperty{}).
		Where("model_id = ?", modelID).
		Select("COALESCE(MAX(sort_order), 0)").
		Scan(&maxSortOrder).Error; err != nil {
		return nil, "", err
	}
	autoSort := maxSortOrder + 10
	seen := make(map[string]bool, len(props))
	now := nowString()

	for i := range props {
		p := &props[i]
		identifier := strings.TrimSpace(p.Identifier)
		name := strings.TrimSpace(p.Name)

		if err := validateOpenApiThingModelPropertyItem(p, identifier, name); err != nil {
			return nil, identifier, err
		}
		if seen[identifier] {
			return nil, identifier, appError.NewAppError(
				enums.ThingModelPropertyExistsEnum.GetCode(), "批次内属性标识符重复")
		}
		seen[identifier] = true

		var existing po.ThingModelProperty
		err := tx.Where("model_id = ? AND identifier = ?", modelID, identifier).
			First(&existing).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			if p.Kind == nil || p.AccessMode == nil {
				return nil, identifier, openApiThingModelParamErr("新建属性必须提供 kind 与 accessMode")
			}
			sortOrder := autoSort
			if p.SortOrder != nil {
				sortOrder = *p.SortOrder
				if sortOrder >= autoSort {
					autoSort = sortOrder + 10
				}
			} else {
				autoSort += 10
			}
			unit := ""
			if p.Unit != nil {
				unit = strings.TrimSpace(*p.Unit)
			}
			description := ""
			if p.Description != nil {
				description = *p.Description
			}
			status := 1
			if p.Status != nil {
				status = *p.Status
			}

			prop := po.ThingModelProperty{
				ModelID:     modelID,
				Identifier:  identifier,
				Name:        name,
				Kind:        *p.Kind,
				AccessMode:  *p.AccessMode,
				Unit:        unit,
				Description: description,
				SortOrder:   sortOrder,
				Status:      status,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if err := tx.Create(&prop).Error; err != nil {
				return nil, identifier, err
			}
			results = append(results, vo.OpenApiThingModelPropertyUpsertItemVO{
				Identifier: identifier,
				Action:     vo.ThingModelUpsertActionCreated,
				ID:         prop.ID,
			})

		case err != nil:
			return nil, identifier, err

		default:
			kind := existing.Kind
			if p.Kind != nil {
				kind = *p.Kind
			}
			accessMode := existing.AccessMode
			if p.AccessMode != nil {
				accessMode = *p.AccessMode
			}
			// 已被点位绑定时 kind/access_mode 是绑定的兼容性前提：改了会让已有
			// 绑定悄悄失效。与 UpdateThingModelProperty 同一条规则。
			if kind != existing.Kind || accessMode != existing.AccessMode {
				var bound int64
				if err := tx.Model(&po.DeviceAddress{}).
					Where("property_id = ?", existing.ID).Count(&bound).Error; err != nil {
					return nil, identifier, err
				}
				if bound > 0 {
					return nil, identifier, thingModelErr(enums.ThingModelPropertyBoundImmutableEnum)
				}
			}

			updates := map[string]any{
				"name":       name,
				"updated_at": now,
			}
			if p.Kind != nil {
				updates["kind"] = *p.Kind
			}
			if p.AccessMode != nil {
				updates["access_mode"] = *p.AccessMode
			}
			if p.Unit != nil {
				updates["unit"] = strings.TrimSpace(*p.Unit)
			}
			if p.Description != nil {
				updates["description"] = *p.Description
			}
			if p.SortOrder != nil {
				updates["sort_order"] = *p.SortOrder
			}
			if p.Status != nil {
				updates["status"] = *p.Status
			}
			if err := tx.Model(&po.ThingModelProperty{}).
				Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
				return nil, identifier, err
			}
			results = append(results, vo.OpenApiThingModelPropertyUpsertItemVO{
				Identifier: identifier,
				Action:     vo.ThingModelUpsertActionUpdated,
				ID:         existing.ID,
			})
		}
	}
	return results, "", nil
}

// validateOpenApiThingModelItem 校验模型条目的必填项与取值范围。
func validateOpenApiThingModelItem(req *dto.OpenApiThingModelUpsertDTO, code, name string) error {
	if !thingModelCodePattern.MatchString(code) {
		return thingModelErr(enums.ThingModelCodeInvalidEnum)
	}
	if name == "" {
		return openApiThingModelParamErr("模型名称不能为空")
	}
	if utf8.RuneCountInString(name) > 100 {
		return openApiThingModelParamErr("模型名称不能超过100个字符")
	}
	if req.Category != nil && !constants.IsValidOpenApiThingModelCategory(*req.Category) {
		return thingModelErr(enums.ThingModelCategoryInvalidEnum)
	}
	if req.Version != nil && utf8.RuneCountInString(strings.TrimSpace(*req.Version)) > 32 {
		return openApiThingModelParamErr("版本号不能超过32个字符")
	}
	if req.Description != nil && utf8.RuneCountInString(*req.Description) > 500 {
		return openApiThingModelParamErr("描述不能超过500个字符")
	}
	if req.Status != nil && *req.Status != 0 && *req.Status != 1 {
		return openApiThingModelParamErr("状态只能是 0（停用）或 1（启用）")
	}
	return nil
}

// validateOpenApiThingModelPropertyItem 校验属性条目的格式与可选字段范围。
// kind/accessMode 的「新建必填」在事务内按属性是否存在判断，这里只在给出时校验取值。
func validateOpenApiThingModelPropertyItem(p *dto.OpenApiThingModelPropertyUpsertDTO, identifier, name string) error {
	if !thingModelIdentifierPattern.MatchString(identifier) {
		return thingModelErr(enums.ThingModelIdentifierInvalidEnum)
	}
	if name == "" {
		return openApiThingModelParamErr("属性名称不能为空")
	}
	if utf8.RuneCountInString(name) > 100 {
		return openApiThingModelParamErr("属性名称不能超过100个字符")
	}
	if p.Kind != nil && !isThingModelKind(*p.Kind) {
		return openApiThingModelParamErr("属性数据类型不合法（bool/int/uint/float/string/time）")
	}
	if p.AccessMode != nil && !isThingModelAccessMode(*p.AccessMode) {
		return openApiThingModelParamErr("属性读写模式不合法（R/W/RW）")
	}
	if p.Unit != nil && utf8.RuneCountInString(strings.TrimSpace(*p.Unit)) > 32 {
		return openApiThingModelParamErr("单位不能超过32个字符")
	}
	if p.Description != nil && utf8.RuneCountInString(*p.Description) > 500 {
		return openApiThingModelParamErr("描述不能超过500个字符")
	}
	if p.SortOrder != nil && (*p.SortOrder < 0 || *p.SortOrder > 99999) {
		return openApiThingModelParamErr("排序须在 0~99999 之间")
	}
	if p.Status != nil && *p.Status != 0 && *p.Status != 1 {
		return openApiThingModelParamErr("状态只能是 0（停用）或 1（启用）")
	}
	return nil
}

// failThingModelItem 把错误写到逐条结果上；顶层响应仍是成功信封。
func failThingModelItem(item vo.OpenApiThingModelUpsertItemVO, err error, ctx context.Context) vo.OpenApiThingModelUpsertItemVO {
	code, message := openApiItemErrInfo(ctx, err)
	item.Action = vo.ThingModelUpsertActionFailed
	item.ID = ""
	item.ErrorCode = code
	item.ErrorMessage = message
	return item
}

func failedThingModelItem(code, name, errorCode, errorMessage string) vo.OpenApiThingModelUpsertItemVO {
	return vo.OpenApiThingModelUpsertItemVO{
		Code:         code,
		Name:         name,
		Action:       vo.ThingModelUpsertActionFailed,
		ErrorCode:    errorCode,
		ErrorMessage: errorMessage,
		Properties:   []vo.OpenApiThingModelPropertyUpsertItemVO{},
	}
}

func openApiThingModelParamErr(message string) error {
	return appError.NewAppError(enums.ParamValidEnum.GetCode(), message)
}

// openApiItemErrInfo 把业务错误转成逐条结果的 (code, message)；
// 非业务错误（数据库错误等）统一回落为系统错误码。
func openApiItemErrInfo(ctx context.Context, err error) (string, string) {
	var appErr *appError.AppError
	if errors.As(err, &appErr) {
		return appErr.Code, appErr.GetMessageCtx(ctx)
	}
	return enums.SystemErrorEnum.GetCode(), enums.SystemErrorEnum.GetMessage()
}
