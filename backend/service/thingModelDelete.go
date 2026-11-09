// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
)

// BatchDeleteOpenApiThingModels 开放接口：按模型标识（code）批量删除物模型。
//
// 删除模型会连带删除其属性。删除保护与单条删除完全一致：已被设备绑定、或其属性
// 已被点位绑定的模型返回 41005 并保持原样。逐条独立、互不影响，顶层响应始终是
// 成功信封 —— 批量部分成功由 Results 表达。
//
// 已不存在的模型返回 41002（不是静默成功）：调用方应能区分「删掉了」与
// 「本就不存在」，是否把后者视为成功由调用方决定。
func (s *ThingModelService) BatchDeleteOpenApiThingModels(ctx context.Context, req *dto.OpenApiThingModelBatchDeleteDTO) (*vo.OpenApiThingModelBatchDeleteVO, error) {
	if req == nil || len(req.Codes) == 0 {
		return nil, thingModelErr(enums.ParamValidEnum)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := &vo.OpenApiThingModelBatchDeleteVO{
		Requested: len(req.Codes),
		Results:   make([]vo.OpenApiThingModelDeleteItemVO, 0, len(req.Codes)),
	}
	for _, raw := range req.Codes {
		code := strings.TrimSpace(raw)
		item := vo.OpenApiThingModelDeleteItemVO{
			Code:   code,
			Action: vo.ThingModelDeleteActionDeleted,
		}
		if err := s.deleteOpenApiThingModelByCode(ctx, code); err != nil {
			item.Action = vo.ThingModelDeleteActionFailed
			item.ErrorCode, item.ErrorMessage = openApiItemErrInfo(ctx, err)
			result.Failed++
		} else {
			result.Deleted++
		}
		result.Results = append(result.Results, item)
	}
	return result, nil
}

// deleteOpenApiThingModelByCode 按自然键解析出模型再复用单条删除。
// 复用而不是重写删除 SQL：绑定保护与「属性随模型一起删」只有一份实现。
func (s *ThingModelService) deleteOpenApiThingModelByCode(ctx context.Context, code string) error {
	if code == "" {
		return openApiThingModelParamErr("模型标识不能为空")
	}
	var model po.ThingModel
	if err := s.sqliteDB.WithContext(ctx).Where("code = ?", code).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return thingModelErr(enums.ThingModelNotExistsEnum)
		}
		return err
	}
	return s.DeleteThingModel(ctx, model.ID)
}

// BatchDeleteOpenApiThingModelProperties 开放接口：按（模型标识, 属性标识符）
// 批量删除物模型属性。
//
// 删除保护与单条删除一致：已被点位绑定的属性返回 41006 并保持原样。
// 模型不存在返回 41002，属性不存在返回 41004；逐条独立、互不影响。
func (s *ThingModelService) BatchDeleteOpenApiThingModelProperties(ctx context.Context, req *dto.OpenApiThingModelPropertyBatchDeleteDTO) (*vo.OpenApiThingModelPropertyBatchDeleteVO, error) {
	if req == nil || len(req.Items) == 0 {
		return nil, thingModelErr(enums.ParamValidEnum)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := &vo.OpenApiThingModelPropertyBatchDeleteVO{
		Requested: len(req.Items),
		Results:   make([]vo.OpenApiThingModelPropertyDeleteItemVO, 0, len(req.Items)),
	}
	for i := range req.Items {
		reqItem := &req.Items[i]
		modelCode := strings.TrimSpace(reqItem.ModelCode)
		identifier := strings.TrimSpace(reqItem.Identifier)
		item := vo.OpenApiThingModelPropertyDeleteItemVO{
			ModelCode:  modelCode,
			Identifier: identifier,
			Action:     vo.ThingModelDeleteActionDeleted,
		}
		if err := s.deleteOpenApiThingModelProperty(ctx, modelCode, identifier); err != nil {
			item.Action = vo.ThingModelDeleteActionFailed
			item.ErrorCode, item.ErrorMessage = openApiItemErrInfo(ctx, err)
			result.Failed++
		} else {
			result.Deleted++
		}
		result.Results = append(result.Results, item)
	}
	return result, nil
}

// deleteOpenApiThingModelProperty 按自然键解析出属性再复用单条删除。
func (s *ThingModelService) deleteOpenApiThingModelProperty(ctx context.Context, modelCode, identifier string) error {
	if modelCode == "" {
		return openApiThingModelParamErr("模型标识不能为空")
	}
	if identifier == "" {
		return openApiThingModelParamErr("属性标识符不能为空")
	}

	var model po.ThingModel
	if err := s.sqliteDB.WithContext(ctx).Where("code = ?", modelCode).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return thingModelErr(enums.ThingModelNotExistsEnum)
		}
		return err
	}

	var prop po.ThingModelProperty
	if err := s.sqliteDB.WithContext(ctx).
		Where("model_id = ? AND identifier = ?", model.ID, identifier).
		First(&prop).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return thingModelErr(enums.ThingModelPropertyNotExistsEnum)
		}
		return err
	}
	return s.DeleteThingModelProperty(ctx, prop.ID)
}
