// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/push"
	"iot-gateway/utils/canonicaljson"
)

// ==================== 数据推送通道 ====================

// BatchUpsertPushChannels 推送通道批量 upsert（自然键 type/name）。
func (s *OpenApiConfigService) BatchUpsertPushChannels(ctx context.Context, req *dto.OpenApiPushChannelBatchUpsertDTO) (*vo.OpenApiConfigBatchUpsertVO, error) {
	if req == nil || len(req.Items) == 0 {
		return nil, openApiConfigParamErr("items 不能为空")
	}
	result := &vo.OpenApiConfigBatchUpsertVO{
		Total: len(req.Items),
		Items: make([]vo.OpenApiConfigUpsertItemVO, 0, len(req.Items)),
	}
	seen := make(map[string]bool, len(req.Items))
	for i := range req.Items {
		item := &req.Items[i]
		chType := normalizeChannelType(item.Type, item.Name)
		key := chType + "/" + strings.TrimSpace(item.Name)
		if seen[key] {
			result.Items = append(result.Items, failedOpenApiConfigItem(key, "批次内推送通道重复"))
			result.Failed++
			continue
		}
		seen[key] = true

		res := s.upsertPushChannel(ctx, chType, item)
		result.Items = append(result.Items, res)
		switch res.Action {
		case vo.OpenApiConfigActionCreated:
			result.Success++
			result.Created++
		case vo.OpenApiConfigActionUpdated:
			result.Success++
			result.Updated++
		default:
			result.Failed++
		}
	}
	return result, nil
}

func (s *OpenApiConfigService) upsertPushChannel(ctx context.Context, chType string, item *dto.OpenApiPushChannelUpsertDTO) vo.OpenApiConfigUpsertItemVO {
	name := strings.TrimSpace(item.Name)
	key := chType + "/" + name
	itemVO := vo.OpenApiConfigUpsertItemVO{TargetKey: key}

	if name == "" {
		return failOpenApiConfigItem(itemVO, openApiConfigParamErr("通道名称不能为空"))
	}
	meta, ok := push.RegisteredTypeMeta(chType)
	if !ok {
		return failOpenApiConfigItem(itemVO, openApiConfigParamErr("未知的通道类型: "+chType))
	}

	var existing po.PushChannel
	err := s.sqliteDB.WithContext(ctx).Where("type = ? AND name = ?", chType, name).First(&existing).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if len(item.Config) == 0 {
			return failOpenApiConfigItem(itemVO, openApiConfigParamErr("新建推送通道必须提供 config"))
		}
		incoming, err := canonicaljson.Decode(item.Config)
		if err != nil {
			return failOpenApiConfigItem(itemVO, openApiConfigParamErr("config 解析失败: "+err.Error()))
		}
		incoming = canonicaljson.KeepMasked(nil, incoming)
		cfg, err := json.Marshal(incoming)
		if err != nil {
			return failOpenApiConfigItem(itemVO, err)
		}
		if err := validateChannelConfig(meta, cfg); err != nil {
			return failOpenApiConfigItem(itemVO, err)
		}
		desc := ""
		if item.Description != nil {
			desc = *item.Description
		}
		status := 1
		if item.Status != nil {
			status = *item.Status
		}
		now := nowString()
		row := po.PushChannel{
			Type:        chType,
			Name:        name,
			Description: desc,
			ConfigJSON:  string(cfg),
			Status:      status,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.sqliteDB.WithContext(ctx).Create(&row).Error; err != nil {
			return failOpenApiConfigItem(itemVO, err)
		}
		hash, err := pushChannelConfigHash(row)
		if err != nil {
			return failOpenApiConfigItem(itemVO, err)
		}
		itemVO.Action = vo.OpenApiConfigActionCreated
		itemVO.ContentHash = hash
		return itemVO

	case err != nil:
		return failOpenApiConfigItem(itemVO, err)
	}

	updates := make(map[string]any)
	if item.Description != nil {
		updates["description"] = *item.Description
		existing.Description = *item.Description
	}
	if item.Status != nil {
		updates["status"] = *item.Status
		existing.Status = *item.Status
	}
	if len(item.Config) > 0 {
		existingCfg, err := parseJSONValueOrEmptyObject(existing.ConfigJSON)
		if err != nil {
			return failOpenApiConfigItem(itemVO, err)
		}
		incoming, err := canonicaljson.Decode(item.Config)
		if err != nil {
			return failOpenApiConfigItem(itemVO, openApiConfigParamErr("config 解析失败: "+err.Error()))
		}
		merged, err := json.Marshal(canonicaljson.KeepMasked(existingCfg, incoming))
		if err != nil {
			return failOpenApiConfigItem(itemVO, err)
		}
		if err := validateChannelConfig(meta, merged); err != nil {
			return failOpenApiConfigItem(itemVO, err)
		}
		updates["config_json"] = string(merged)
		existing.ConfigJSON = string(merged)
	}
	if len(updates) > 0 {
		updates["updated_at"] = nowString()
		if err := s.sqliteDB.WithContext(ctx).
			Model(&po.PushChannel{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
			return failOpenApiConfigItem(itemVO, err)
		}
	}

	hash, err := pushChannelConfigHash(existing)
	if err != nil {
		return failOpenApiConfigItem(itemVO, err)
	}
	itemVO.Action = vo.OpenApiConfigActionUpdated
	itemVO.ContentHash = hash
	return itemVO
}

// BatchDeletePushChannels 推送通道批量删除（自然键 type/name）。
func (s *OpenApiConfigService) BatchDeletePushChannels(ctx context.Context, req *dto.OpenApiPushChannelBatchDeleteDTO) (*vo.OpenApiConfigBatchDeleteVO, error) {
	if req == nil || len(req.Items) == 0 {
		return nil, openApiConfigParamErr("items 不能为空")
	}
	result := &vo.OpenApiConfigBatchDeleteVO{
		Requested: len(req.Items),
		Items:     make([]vo.OpenApiConfigDeleteItemVO, 0, len(req.Items)),
	}
	for _, item := range req.Items {
		chType := normalizeChannelType(item.Type, item.Name)
		name := strings.TrimSpace(item.Name)
		key := chType + "/" + name
		var row po.PushChannel
		err := s.sqliteDB.WithContext(ctx).Where("type = ? AND name = ?", chType, name).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			result.Items = append(result.Items, failedOpenApiConfigDelete(key, enums.PushChannelNotExistsEnum.GetCode(), "推送通道不存在"))
			result.Failed++
			continue
		}
		if err != nil {
			result.Items = append(result.Items, failedOpenApiConfigDelete(key, enums.SystemErrorEnum.GetCode(), err.Error()))
			result.Failed++
			continue
		}
		if err := s.pushChannelService.DeletePushChannel(ctx, row.ID); err != nil {
			code, message := openApiItemErrInfo(ctx, err)
			result.Items = append(result.Items, failedOpenApiConfigDelete(key, code, message))
			result.Failed++
			continue
		}
		result.Items = append(result.Items, vo.OpenApiConfigDeleteItemVO{TargetKey: key, Action: vo.OpenApiConfigActionDeleted})
		result.Deleted++
	}
	return result, nil
}

// ==================== 告警 Webhook ====================

// BatchUpsertAlarmWebhooks 告警 Webhook 批量 upsert（自然键 name）。
func (s *OpenApiConfigService) BatchUpsertAlarmWebhooks(ctx context.Context, req *dto.OpenApiAlarmWebhookBatchUpsertDTO) (*vo.OpenApiConfigBatchUpsertVO, error) {
	if req == nil || len(req.Items) == 0 {
		return nil, openApiConfigParamErr("items 不能为空")
	}
	result := &vo.OpenApiConfigBatchUpsertVO{
		Total: len(req.Items),
		Items: make([]vo.OpenApiConfigUpsertItemVO, 0, len(req.Items)),
	}
	seen := make(map[string]bool, len(req.Items))
	for i := range req.Items {
		item := &req.Items[i]
		name := strings.TrimSpace(item.Name)
		if seen[name] {
			result.Items = append(result.Items, failedOpenApiConfigItem(name, "批次内告警 Webhook 名称重复"))
			result.Failed++
			continue
		}
		seen[name] = true

		res := s.upsertAlarmWebhook(ctx, item)
		result.Items = append(result.Items, res)
		switch res.Action {
		case vo.OpenApiConfigActionCreated:
			result.Success++
			result.Created++
		case vo.OpenApiConfigActionUpdated:
			result.Success++
			result.Updated++
		default:
			result.Failed++
		}
	}
	// 批量写入后统一热刷新一次，避免 N 条配置触发 N 次重建。
	s.alarmWebhookService.refresh()
	return result, nil
}

func (s *OpenApiConfigService) upsertAlarmWebhook(ctx context.Context, item *dto.OpenApiAlarmWebhookUpsertDTO) vo.OpenApiConfigUpsertItemVO {
	name := strings.TrimSpace(item.Name)
	itemVO := vo.OpenApiConfigUpsertItemVO{TargetKey: name}
	if name == "" {
		return failOpenApiConfigItem(itemVO, openApiConfigParamErr("Webhook 名称不能为空"))
	}

	var existing po.AlarmWebhook
	err := s.sqliteDB.WithContext(ctx).Where("name = ?", name).First(&existing).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		cfg := po.AlarmWebhook{
			Name:   name,
			Type:   strings.TrimSpace(item.Type),
			Secret: openApiSecretForCreate(item.Secret),
			Status: 1,
		}
		if item.Description != nil {
			cfg.Description = *item.Description
		}
		if item.URL != nil {
			cfg.URL = strings.TrimSpace(*item.URL)
		}
		if item.MsgType != nil {
			cfg.MsgType = strings.TrimSpace(*item.MsgType)
		}
		if item.AtAll != nil {
			cfg.AtAll = boolToInt(*item.AtAll)
		}
		if item.AtList != nil {
			cfg.AtList = encodeAtList(*item.AtList)
		}
		if item.Status != nil {
			cfg.Status = *item.Status
		}
		if err := validateWebhookConfig(&cfg); err != nil {
			return failOpenApiConfigItem(itemVO, err)
		}
		now := nowString()
		cfg.CreatedAt = now
		cfg.UpdatedAt = now
		if err := s.sqliteDB.WithContext(ctx).Create(&cfg).Error; err != nil {
			return failOpenApiConfigItem(itemVO, err)
		}
		hash, err := alarmWebhookConfigHash(cfg)
		if err != nil {
			return failOpenApiConfigItem(itemVO, err)
		}
		itemVO.Action = vo.OpenApiConfigActionCreated
		itemVO.ContentHash = hash
		return itemVO

	case err != nil:
		return failOpenApiConfigItem(itemVO, err)
	}

	merged := existing
	if item.Type != "" {
		merged.Type = strings.TrimSpace(item.Type)
	}
	if item.Description != nil {
		merged.Description = *item.Description
	}
	if item.URL != nil {
		merged.URL = strings.TrimSpace(*item.URL)
	}
	if item.Secret != nil && *item.Secret != canonicaljson.Mask {
		merged.Secret = *item.Secret
	}
	if item.MsgType != nil {
		merged.MsgType = strings.TrimSpace(*item.MsgType)
	}
	if item.AtAll != nil {
		merged.AtAll = boolToInt(*item.AtAll)
	}
	if item.AtList != nil {
		merged.AtList = encodeAtList(*item.AtList)
	}
	if item.Status != nil {
		merged.Status = *item.Status
	}
	if err := validateWebhookConfig(&merged); err != nil {
		return failOpenApiConfigItem(itemVO, err)
	}
	if err := s.sqliteDB.WithContext(ctx).Model(&po.AlarmWebhook{}).Where("id = ?", existing.ID).Updates(map[string]any{
		"type":        merged.Type,
		"description": merged.Description,
		"url":         merged.URL,
		"secret":      merged.Secret,
		"msg_type":    merged.MsgType,
		"at_all":      merged.AtAll,
		"at_list":     merged.AtList,
		"status":      merged.Status,
		"updated_at":  nowString(),
	}).Error; err != nil {
		return failOpenApiConfigItem(itemVO, err)
	}

	hash, err := alarmWebhookConfigHash(merged)
	if err != nil {
		return failOpenApiConfigItem(itemVO, err)
	}
	itemVO.Action = vo.OpenApiConfigActionUpdated
	itemVO.ContentHash = hash
	return itemVO
}

// BatchDeleteAlarmWebhooks 告警 Webhook 批量删除（自然键 name）。
func (s *OpenApiConfigService) BatchDeleteAlarmWebhooks(ctx context.Context, req *dto.OpenApiAlarmWebhookBatchDeleteDTO) (*vo.OpenApiConfigBatchDeleteVO, error) {
	if req == nil || len(req.Names) == 0 {
		return nil, openApiConfigParamErr("names 不能为空")
	}
	result := &vo.OpenApiConfigBatchDeleteVO{
		Requested: len(req.Names),
		Items:     make([]vo.OpenApiConfigDeleteItemVO, 0, len(req.Names)),
	}
	for _, raw := range req.Names {
		name := strings.TrimSpace(raw)
		var row po.AlarmWebhook
		err := s.sqliteDB.WithContext(ctx).Where("name = ?", name).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			result.Items = append(result.Items, failedOpenApiConfigDelete(name, enums.AlarmWebhookNotExistsEnum.GetCode(), "告警 Webhook 不存在"))
			result.Failed++
			continue
		}
		if err != nil {
			result.Items = append(result.Items, failedOpenApiConfigDelete(name, enums.SystemErrorEnum.GetCode(), err.Error()))
			result.Failed++
			continue
		}
		if err := s.alarmWebhookService.DeleteAlarmWebhook(ctx, row.ID); err != nil {
			code, message := openApiItemErrInfo(ctx, err)
			result.Items = append(result.Items, failedOpenApiConfigDelete(name, code, message))
			result.Failed++
			continue
		}
		result.Items = append(result.Items, vo.OpenApiConfigDeleteItemVO{TargetKey: name, Action: vo.OpenApiConfigActionDeleted})
		result.Deleted++
	}
	return result, nil
}

// ==================== 运行开关 ====================

// BatchUpdateSystemConfig 运行开关批量修改。
func (s *OpenApiConfigService) BatchUpdateSystemConfig(ctx context.Context, req *dto.OpenApiSystemConfigBatchUpdateDTO, updatedBy string) (*vo.OpenApiSystemConfigBatchUpdateVO, error) {
	if req == nil || len(req.Items) == 0 {
		return nil, openApiConfigParamErr("items 不能为空")
	}
	result := &vo.OpenApiSystemConfigBatchUpdateVO{
		Total: len(req.Items),
		Items: make([]vo.OpenApiSystemConfigUpdateItemVO, 0, len(req.Items)),
	}
	for i := range req.Items {
		item := &req.Items[i]
		res, err := s.systemConfigService.Update(ctx, &dto.UpdateSystemConfigDTO{
			Key:   item.Key,
			Value: item.Value,
		}, updatedBy)
		if err != nil {
			code, message := openApiItemErrInfo(ctx, err)
			result.Failed++
			result.Items = append(result.Items, vo.OpenApiSystemConfigUpdateItemVO{
				Key: item.Key, ErrorCode: code, ErrorMessage: message,
			})
			continue
		}
		result.Success++
		if res.RequiresRestart {
			result.RequiresRestart = append(result.RequiresRestart, res.Key)
		}
		result.Items = append(result.Items, vo.OpenApiSystemConfigUpdateItemVO{
			Key:             res.Key,
			Value:           res.Value,
			RequiresRestart: res.RequiresRestart,
		})
	}
	return result, nil
}

// BatchResetSystemConfig 运行开关批量恢复默认。
func (s *OpenApiConfigService) BatchResetSystemConfig(ctx context.Context, req *dto.OpenApiSystemConfigBatchResetDTO, updatedBy string) (*vo.OpenApiSystemConfigBatchUpdateVO, error) {
	if req == nil || len(req.Keys) == 0 {
		return nil, openApiConfigParamErr("keys 不能为空")
	}
	result := &vo.OpenApiSystemConfigBatchUpdateVO{
		Total: len(req.Keys),
		Items: make([]vo.OpenApiSystemConfigUpdateItemVO, 0, len(req.Keys)),
	}
	for _, key := range req.Keys {
		res, err := s.systemConfigService.Reset(ctx, &dto.ResetSystemConfigDTO{Key: key}, updatedBy)
		if err != nil {
			code, message := openApiItemErrInfo(ctx, err)
			result.Failed++
			result.Items = append(result.Items, vo.OpenApiSystemConfigUpdateItemVO{
				Key: key, ErrorCode: code, ErrorMessage: message,
			})
			continue
		}
		result.Success++
		if res.RequiresRestart {
			result.RequiresRestart = append(result.RequiresRestart, res.Key)
		}
		result.Items = append(result.Items, vo.OpenApiSystemConfigUpdateItemVO{
			Key:             res.Key,
			Value:           res.Value,
			RequiresRestart: res.RequiresRestart,
		})
	}
	return result, nil
}

// ==================== 小工具 ====================

func normalizeChannelType(rawType, fallbackName string) string {
	t := strings.ToLower(strings.TrimSpace(rawType))
	if t == "" {
		t = strings.ToLower(strings.TrimSpace(fallbackName))
	}
	return t
}

func openApiSecretForCreate(secret *string) string {
	if secret == nil || *secret == canonicaljson.Mask {
		return ""
	}
	return *secret
}

func failOpenApiConfigItem(item vo.OpenApiConfigUpsertItemVO, err error) vo.OpenApiConfigUpsertItemVO {
	code, message := openApiItemErrInfo(context.Background(), err)
	item.Action = vo.OpenApiConfigActionFailed
	item.ContentHash = ""
	item.ErrorCode = code
	item.ErrorMessage = message
	return item
}

func failedOpenApiConfigItem(key, message string) vo.OpenApiConfigUpsertItemVO {
	return vo.OpenApiConfigUpsertItemVO{
		TargetKey:    key,
		Action:       vo.OpenApiConfigActionFailed,
		ErrorCode:    enums.ParamValidEnum.GetCode(),
		ErrorMessage: message,
	}
}

func failedOpenApiConfigDelete(key, code, message string) vo.OpenApiConfigDeleteItemVO {
	return vo.OpenApiConfigDeleteItemVO{
		TargetKey:    key,
		Action:       vo.OpenApiConfigActionFailed,
		ErrorCode:    code,
		ErrorMessage: message,
	}
}

func openApiConfigParamErr(message string) error {
	return appError.NewAppError(enums.ParamValidEnum.GetCode(), message)
}
