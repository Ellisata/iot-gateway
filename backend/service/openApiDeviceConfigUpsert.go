// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"bytes"
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
	"iot-gateway/utils/canonicaljson"
)

const (
	openApiDeviceUpsertMaxPoints = 500
	openApiDeviceUpsertMaxGroups = 100
)

// BatchUpsertDeviceConfigs 设备配置批量 upsert（设备 + 采集组 + 点位）。
//
// 每台设备一个事务：设备失败只回滚该设备，其余设备继续。自然键：
// 设备 Code（非空优先）或 Name；点位/采集组在设备内按 Name。
func (s *OpenApiConfigService) BatchUpsertDeviceConfigs(ctx context.Context, req *dto.OpenApiDeviceConfigBatchUpsertDTO) (*vo.OpenApiConfigBatchUpsertVO, error) {
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
		key := deviceConfigKey(item.Code, item.Name)
		if seen[key] {
			result.Items = append(result.Items, failedOpenApiConfigItem(key, "批次内设备自然键重复"))
			result.Failed++
			continue
		}
		seen[key] = true

		res := s.upsertDeviceConfig(ctx, item)
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

// BatchDeleteDeviceConfigs 设备配置批量删除（逐台独立，保留既有引用保护）。
func (s *OpenApiConfigService) BatchDeleteDeviceConfigs(ctx context.Context, req *dto.OpenApiDeviceConfigBatchDeleteDTO) (*vo.OpenApiConfigBatchDeleteVO, error) {
	if req == nil || len(req.Items) == 0 {
		return nil, openApiConfigParamErr("items 不能为空")
	}
	result := &vo.OpenApiConfigBatchDeleteVO{
		Requested: len(req.Items),
		Items:     make([]vo.OpenApiConfigDeleteItemVO, 0, len(req.Items)),
	}
	for _, item := range req.Items {
		key := deviceConfigKey(item.Code, item.Name)
		device, err := s.findDeviceByNaturalKey(ctx, item.Code, item.Name)
		if err != nil {
			code, message := openApiItemErrInfo(ctx, err)
			result.Items = append(result.Items, failedOpenApiConfigDelete(key, code, message))
			result.Failed++
			continue
		}
		if err := s.deviceService.DeleteDevice(ctx, device.ID); err != nil {
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

func (s *OpenApiConfigService) upsertDeviceConfig(ctx context.Context, item *dto.OpenApiDeviceConfigUpsertDTO) vo.OpenApiConfigUpsertItemVO {
	key := deviceConfigKey(item.Code, item.Name)
	itemVO := vo.OpenApiConfigUpsertItemVO{TargetKey: key}

	name := strings.TrimSpace(item.Name)
	code := strings.TrimSpace(item.Code)
	protocolName := strings.TrimSpace(item.ProtocolName)
	modelCode := strings.TrimSpace(item.ModelCode)
	mode := strings.ToLower(strings.TrimSpace(item.Mode))
	if mode == "" {
		mode = "merge"
	}
	if mode != "merge" && mode != "replace" {
		return failOpenApiConfigItem(itemVO, openApiConfigParamErr("mode 只能是 merge 或 replace"))
	}
	if name == "" {
		return failOpenApiConfigItem(itemVO, openApiConfigParamErr("设备名称不能为空"))
	}
	if code != "" && !validateEntityCode(code) {
		return failOpenApiConfigItem(itemVO, deviceCodeInvalidErr())
	}
	if protocolName == "" {
		return failOpenApiConfigItem(itemVO, openApiConfigParamErr("protocolName 不能为空"))
	}
	if len(item.Groups) > openApiDeviceUpsertMaxGroups {
		return failOpenApiConfigItem(itemVO, appError.NewAppErrorCtx(enums.CfgBatchLimitEnum.GetCode(),
			"单台设备采集组数量不能超过100", enums.CfgBatchLimitEnum.GetMsgKey()))
	}
	if len(item.Points) > openApiDeviceUpsertMaxPoints {
		return failOpenApiConfigItem(itemVO, appError.NewAppErrorCtx(enums.CfgBatchLimitEnum.GetCode(),
			"单台设备点位数量不能超过500", enums.CfgBatchLimitEnum.GetMsgKey()))
	}

	protocolJSON, err := normalizeJSONObject(item.ProtocolJSON)
	if err != nil {
		return failOpenApiConfigItem(itemVO, openApiConfigParamErr("protocolJson 解析失败: "+err.Error()))
	}

	var created bool
	var deviceID string
	var deletedAddressIDs []string
	err = s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var protocol po.IotProtocol
		if err := tx.Where("name = ?", protocolName).First(&protocol).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return cfgProtocolNotExistsErr()
			}
			return err
		}

		var model po.ThingModel
		modelID := ""
		if modelCode != "" {
			if err := tx.Where("code = ?", modelCode).First(&model).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return appError.NewAppErrorCtx(enums.CfgThingModelNotExistsEnum.GetCode(),
						enums.CfgThingModelNotExistsEnum.GetMessage(), enums.CfgThingModelNotExistsEnum.GetMsgKey())
				}
				return err
			}
			if model.Status != 1 {
				return appError.NewAppErrorCtx(enums.CfgThingModelNotExistsEnum.GetCode(),
					enums.CfgThingModelNotExistsEnum.GetMessage(), enums.CfgThingModelNotExistsEnum.GetMsgKey())
			}
			modelID = model.ID
		}

		device, exists, err := findDeviceForUpsert(tx, code, name)
		if err != nil {
			return err
		}
		// 密钥保留：管理平台读到的 protocolJson 已脱敏，"***" 表示沿用网关现值。
		// 新建时没有现值，把 "***" 落成空串，绝不把占位符写进库。
		if len(item.ProtocolJSON) > 0 {
			incoming, decodeErr := canonicaljson.Decode([]byte(protocolJSON))
			if decodeErr != nil {
				return decodeErr
			}
			var merged any
			if exists {
				existingCfg, parseErr := parseJSONValueOrEmptyObject(device.ProtocolJSON)
				if parseErr != nil {
					return parseErr
				}
				merged = canonicaljson.KeepMasked(existingCfg, incoming)
			} else {
				merged = canonicaljson.KeepMasked(nil, incoming)
			}
			mergedRaw, marshalErr := json.Marshal(merged)
			if marshalErr != nil {
				return marshalErr
			}
			protocolJSON = string(mergedRaw)
		}
		payloadPointNames := pointNameSet(item.Points)

		if !exists {
			if err := ensureDeviceNameFree(tx, name, ""); err != nil {
				return err
			}
			if code != "" {
				if err := ensureDeviceCodeFree(tx, code, ""); err != nil {
					return err
				}
			}
			status := 1
			if item.Status != nil {
				status = *item.Status
			}
			desc := ""
			if item.Description != nil {
				desc = *item.Description
			}
			now := nowString()
			device = po.Device{
				Code:         code,
				Name:         name,
				ProtocolID:   protocol.ID,
				ProtocolJSON: protocolJSON,
				ModelID:      modelID,
				Description:  desc,
				Status:       status,
				ScanIdentity: deriveScanIdentityFromJSON(protocolName, protocolJSON),
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			if err := tx.Create(&device).Error; err != nil {
				return err
			}
			created = true
		} else {
			if code != "" && device.Code != "" && code != device.Code {
				return appError.NewAppError(enums.ParamValidEnum.GetCode(), "设备编码不可修改")
			}
			if code != "" && device.Code == "" {
				if err := ensureDeviceCodeFree(tx, code, device.ID); err != nil {
					return err
				}
			}
			if name != device.Name {
				if err := ensureDeviceNameFree(tx, name, device.ID); err != nil {
					return err
				}
			}

			updates := make(map[string]any)
			if code != "" && device.Code == "" {
				updates["code"] = code
				device.Code = code
			}
			if name != device.Name {
				updates["name"] = name
				device.Name = name
			}
			if item.Description != nil {
				updates["description"] = *item.Description
				device.Description = *item.Description
			}
			if item.Status != nil {
				updates["status"] = *item.Status
				device.Status = *item.Status
			}

			protocolChanged := device.ProtocolID != protocol.ID
			if protocolChanged {
				if err := ensureAllPointsSubmitted(tx, device.ID, payloadPointNames); err != nil {
					return err
				}
				updates["protocol_id"] = protocol.ID
				device.ProtocolID = protocol.ID
			}
			if len(item.ProtocolJSON) > 0 && !sameJSON(device.ProtocolJSON, protocolJSON) {
				updates["protocol_json"] = protocolJSON
				device.ProtocolJSON = protocolJSON
			}
			if protocolChanged || len(updates) > 0 {
				updates["scan_identity"] = deriveScanIdentityFromJSON(protocolName, device.ProtocolJSON)
				device.ScanIdentity = deriveScanIdentityFromJSON(protocolName, device.ProtocolJSON)
			}

			if device.ModelID != modelID {
				if err := ensureModelChangePointsSubmitted(tx, device.ID, payloadPointNames); err != nil {
					return err
				}
				updates["model_id"] = modelID
				device.ModelID = modelID
			}

			if len(updates) > 0 {
				updates["updated_at"] = nowString()
				if err := tx.Model(&po.Device{}).Where("id = ?", device.ID).Updates(updates).Error; err != nil {
					return err
				}
			}
		}

		deviceID = device.ID
		groups, err := s.upsertDeviceGroups(tx, &device, item.Groups)
		if err != nil {
			return err
		}
		if err := s.upsertDevicePoints(ctx, tx, &device, protocol.Name, &model, item.Points, groups); err != nil {
			return err
		}

		if mode == "replace" {
			deletedAddressIDs, err = s.replaceDeviceStructure(tx, device.ID, payloadPointNames, item.Groups)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return failOpenApiConfigItem(itemVO, err)
	}
	if len(deletedAddressIDs) > 0 {
		s.deviceAddressService.clearPointAlarms(ctx, deletedAddressIDs)
	}

	hash, err := s.deviceConfigHashByID(ctx, deviceID)
	if err != nil {
		return failOpenApiConfigItem(itemVO, err)
	}
	if created {
		itemVO.Action = vo.OpenApiConfigActionCreated
	} else {
		itemVO.Action = vo.OpenApiConfigActionUpdated
	}
	itemVO.ContentHash = hash
	return itemVO
}

// upsertDeviceGroups 在设备事务内 upsert 采集组，返回 name → 组（含未出现在 payload 的现有组）。
func (s *OpenApiConfigService) upsertDeviceGroups(tx *gorm.DB, device *po.Device, groups []dto.OpenApiCollectorGroupUpsertDTO) (map[string]po.CollectorGroup, error) {
	out := make(map[string]po.CollectorGroup)
	var existing []po.CollectorGroup
	if err := tx.Where("device_id = ?", device.ID).Find(&existing).Error; err != nil {
		return nil, err
	}
	for _, g := range existing {
		out[g.Name] = g
	}

	for i := range groups {
		item := &groups[i]
		name := strings.TrimSpace(item.Name)
		if !validateCollectorGroupName(name) {
			return nil, collectorGroupNameInvalidErr()
		}
		code := strings.TrimSpace(item.Code)
		if code != "" && (!validateEntityCode(code) || isReservedEntityCode(code)) {
			return nil, collectorGroupCodeInvalidErr()
		}

		if g, ok := out[name]; ok {
			updates := make(map[string]any)
			if code != "" {
				if g.Code != "" && g.Code != code {
					return nil, collectorGroupCodeImmutableErr()
				}
				if g.Code == "" {
					var dup po.CollectorGroup
					if err := tx.Where("device_id = ? AND code = ? AND id != ?", device.ID, code, g.ID).
						First(&dup).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
						return nil, err
					}
					if dup.ID != "" {
						return nil, collectorGroupCodeExistsErr()
					}
					updates["code"] = code
					g.Code = code
				}
			}
			if item.IntervalMs != nil {
				if *item.IntervalMs < collectorGroupMinIntervalMs || *item.IntervalMs > collectorGroupMaxIntervalMs {
					return nil, collectorGroupIntervalInvalidErr()
				}
				updates["interval_ms"] = *item.IntervalMs
				g.IntervalMs = *item.IntervalMs
			}
			if item.Description != nil {
				updates["description"] = *item.Description
				g.Description = *item.Description
			}
			if item.Status != nil {
				updates["status"] = *item.Status
				g.Status = *item.Status
			}
			if len(updates) > 0 {
				updates["updated_at"] = nowString()
				if err := tx.Model(&po.CollectorGroup{}).Where("id = ?", g.ID).Updates(updates).Error; err != nil {
					return nil, err
				}
			}
			out[name] = g
			continue
		}

		if code == "" {
			return nil, appError.NewAppErrorCtx(enums.CfgDeviceConfigInvalidEnum.GetCode(),
				"新建采集组必须提供 code", enums.CfgDeviceConfigInvalidEnum.GetMsgKey())
		}
		var dup po.CollectorGroup
		if err := tx.Where("device_id = ? AND code = ?", device.ID, code).First(&dup).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if dup.ID != "" {
			return nil, collectorGroupCodeExistsErr()
		}
		interval := 1000
		if item.IntervalMs != nil {
			interval = *item.IntervalMs
		}
		if interval < collectorGroupMinIntervalMs || interval > collectorGroupMaxIntervalMs {
			return nil, collectorGroupIntervalInvalidErr()
		}
		status := 1
		if item.Status != nil {
			status = *item.Status
		}
		desc := ""
		if item.Description != nil {
			desc = *item.Description
		}
		now := nowString()
		g := po.CollectorGroup{
			DeviceID:    device.ID,
			Code:        code,
			Name:        name,
			IntervalMs:  interval,
			Description: desc,
			Status:      status,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Create(&g).Error; err != nil {
			return nil, err
		}
		out[name] = g
	}
	return out, nil
}

// upsertDevicePoints 在设备事务内 upsert 点位。
func (s *OpenApiConfigService) upsertDevicePoints(ctx context.Context, tx *gorm.DB, device *po.Device, protocolName string, model *po.ThingModel, points []dto.OpenApiDevicePointUpsertDTO, groups map[string]po.CollectorGroup) error {
	for i := range points {
		item := &points[i]
		name := strings.TrimSpace(item.Name)
		label := strings.TrimSpace(item.Label)
		common := strings.TrimSpace(item.CommonDataType)
		rw := strings.ToUpper(strings.TrimSpace(item.RwPermission))
		if name == "" || label == "" || common == "" {
			return appError.NewAppErrorCtx(enums.CfgDeviceConfigInvalidEnum.GetCode(),
				"点位 name/label/commonDataType 不能为空", enums.CfgDeviceConfigInvalidEnum.GetMsgKey())
		}
		if rw != "R" && rw != "W" && rw != "RW" {
			return appError.NewAppErrorCtx(enums.CfgDeviceConfigInvalidEnum.GetCode(),
				"点位读写权限只能是 R/W/RW", enums.CfgDeviceConfigInvalidEnum.GetMsgKey())
		}
		internal, err := s.deviceAddressService.resolveDataType(protocolName, common)
		if err != nil {
			return err
		}
		kind := kindOfAddressType(protocolName, internal)

		groupID := ""
		if item.GroupName != nil {
			groupName := strings.TrimSpace(*item.GroupName)
			if groupName != "" {
				g, ok := groups[groupName]
				if !ok {
					return appError.NewAppErrorCtx(enums.CfgCollectorGroupNotExistsEnum.GetCode(),
						enums.CfgCollectorGroupNotExistsEnum.GetMessage(), enums.CfgCollectorGroupNotExistsEnum.GetMsgKey())
				}
				groupID = g.ID
			}
		}

		propertyID := ""
		if item.PropertyIdentifier != nil {
			identifier := strings.TrimSpace(*item.PropertyIdentifier)
			if identifier != "" {
				if model == nil || model.ID == "" {
					return appError.NewAppErrorCtx(enums.CfgDeviceConfigInvalidEnum.GetCode(),
						"点位绑定属性前必须先绑定物模型", enums.CfgDeviceConfigInvalidEnum.GetMsgKey())
				}
				var property po.ThingModelProperty
				if err := tx.Where("model_id = ? AND identifier = ?", model.ID, identifier).First(&property).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return appError.NewAppErrorCtx(enums.ThingModelPropertyNotExistsEnum.GetCode(),
							enums.ThingModelPropertyNotExistsEnum.GetMessage(), enums.ThingModelPropertyNotExistsEnum.GetMsgKey())
					}
					return err
				}
				propertyID = property.ID
			}
		}

		enabled := false
		op := ""
		values := []string(nil)
		if item.Threshold != nil {
			enabled = item.Threshold.Enabled
			op = strings.TrimSpace(item.Threshold.Op)
			values = item.Threshold.Values
		}
		if err := validateAddressThreshold(enabled, op, values, kind); err != nil {
			return err
		}
		transforms, err := parseAddressTransforms(item.Transform)
		if err != nil {
			return err
		}
		if err := validateAddressTransforms(transforms, kind); err != nil {
			return err
		}

		scanFrequency := 1000
		if item.ScanFrequency != nil && *item.ScanFrequency > 0 {
			scanFrequency = *item.ScanFrequency
		}
		status := 1
		if item.Status != nil {
			status = *item.Status
		}
		desc := ""
		if item.Description != nil {
			desc = *item.Description
		}

		var existing po.DeviceAddress
		err = tx.Where("device_id = ? AND name = ?", device.ID, name).First(&existing).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err := validatePropertyBindingDB(ctx, tx, *device, propertyID, protocolName, internal, rw, false); err != nil {
				return err
			}
			now := nowString()
			row := po.DeviceAddress{
				DeviceID:         device.ID,
				Name:             name,
				Label:            label,
				CommonDataType:   common,
				DataType:         internal,
				RwPermission:     rw,
				PropertyID:       propertyID,
				GroupID:          groupID,
				ThresholdEnabled: boolToInt(enabled),
				ThresholdOp:      op,
				ThresholdValue:   marshalThresholdValues(values),
				TransformJSON:    marshalAddressTransforms(transforms),
				ScanFrequency:    scanFrequency,
				Description:      desc,
				Status:           status,
				CreatedAt:        now,
				UpdatedAt:        now,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if err := validatePropertyBindingDB(ctx, tx, *device, propertyID, protocolName, internal, rw, existing.PropertyID == propertyID); err != nil {
				return err
			}
			if err := tx.Model(&po.DeviceAddress{}).Where("id = ?", existing.ID).Updates(map[string]any{
				"label":             label,
				"common_data_type":  common,
				"data_type":         internal,
				"rw_permission":     rw,
				"property_id":       propertyID,
				"group_id":          groupID,
				"threshold_enabled": boolToInt(enabled),
				"threshold_op":      op,
				"threshold_value":   marshalThresholdValues(values),
				"transform_json":    marshalAddressTransforms(transforms),
				"scan_frequency":    scanFrequency,
				"description":       desc,
				"status":            status,
				"updated_at":        nowString(),
			}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// replaceDeviceStructure 删除本次未出现的点位与采集组。
func (s *OpenApiConfigService) replaceDeviceStructure(tx *gorm.DB, deviceID string, keepPoints map[string]bool, groups []dto.OpenApiCollectorGroupUpsertDTO) ([]string, error) {
	var addrs []po.DeviceAddress
	if err := tx.Where("device_id = ?", deviceID).Find(&addrs).Error; err != nil {
		return nil, err
	}
	deleteIDs := make([]string, 0)
	for _, a := range addrs {
		if !keepPoints[a.Name] {
			deleteIDs = append(deleteIDs, a.ID)
		}
	}
	if len(deleteIDs) > 0 {
		if err := ensureAddressesNotReferencedByTriggerRules(tx, deleteIDs); err != nil {
			return nil, err
		}
		if err := tx.Where("device_id = ? AND id IN ?", deviceID, deleteIDs).
			Delete(&po.DeviceAddress{}).Error; err != nil {
			return nil, err
		}
	}

	keepGroups := make(map[string]bool, len(groups))
	for _, g := range groups {
		keepGroups[strings.TrimSpace(g.Name)] = true
	}
	var existingGroups []po.CollectorGroup
	if err := tx.Where("device_id = ?", deviceID).Find(&existingGroups).Error; err != nil {
		return nil, err
	}
	for _, g := range existingGroups {
		if keepGroups[g.Name] {
			continue
		}
		var refs int64
		if err := tx.Model(&po.DeviceAddress{}).
			Where("device_id = ? AND group_id = ?", deviceID, g.ID).
			Count(&refs).Error; err != nil {
			return nil, err
		}
		if refs > 0 {
			continue
		}
		if err := ensureCollectorGroupNotReferencedByPushChannels(tx, g.ID); err != nil {
			return nil, err
		}
		if err := tx.Where("id = ?", g.ID).Delete(&po.CollectorGroup{}).Error; err != nil {
			return nil, err
		}
	}
	return deleteIDs, nil
}

func (s *OpenApiConfigService) deviceConfigHashByID(ctx context.Context, deviceID string) (string, error) {
	var d po.Device
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", deviceID).First(&d).Error; err != nil {
		return "", err
	}
	protocolName := ""
	if d.ProtocolID != "" {
		var p po.IotProtocol
		if err := s.sqliteDB.WithContext(ctx).Where("id = ?", d.ProtocolID).First(&p).Error; err == nil {
			protocolName = p.Name
		}
	}
	modelCode := ""
	if d.ModelID != "" {
		var m po.ThingModel
		if err := s.sqliteDB.WithContext(ctx).Where("id = ?", d.ModelID).First(&m).Error; err == nil {
			modelCode = m.Code
		}
	}
	var groups []po.CollectorGroup
	if err := s.sqliteDB.WithContext(ctx).Where("device_id = ?", d.ID).Order("name ASC").Find(&groups).Error; err != nil {
		return "", err
	}
	var addrs []po.DeviceAddress
	if err := s.sqliteDB.WithContext(ctx).Where("device_id = ?", d.ID).Order("name ASC").Find(&addrs).Error; err != nil {
		return "", err
	}
	propIDs := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a.PropertyID != "" {
			propIDs = append(propIDs, a.PropertyID)
		}
	}
	propIdentifiers := make(map[string]string, len(propIDs))
	if len(propIDs) > 0 {
		var props []po.ThingModelProperty
		if err := s.sqliteDB.WithContext(ctx).Where("id IN ?", propIDs).Find(&props).Error; err != nil {
			return "", err
		}
		for _, p := range props {
			propIdentifiers[p.ID] = p.Identifier
		}
	}
	points := make([]devicePointSemantic, 0, len(addrs))
	for _, a := range addrs {
		points = append(points, devicePointSemantic{
			Name:               a.Name,
			Label:              a.Label,
			CommonDataType:     a.CommonDataType,
			RwPermission:       a.RwPermission,
			ScanFrequency:      a.ScanFrequency,
			Description:        a.Description,
			Status:             a.Status,
			PropertyIdentifier: propIdentifiers[a.PropertyID],
			GroupName:          groupNameByID(groups, a.GroupID),
			ThresholdEnabled:   a.ThresholdEnabled == 1,
			ThresholdOp:        a.ThresholdOp,
			ThresholdValues:    parseStringArrayOrEmpty(a.ThresholdValue),
			Transforms:         parseJSONArrayOrEmpty(a.TransformJSON),
		})
	}
	return deviceConfigHash(d, protocolName, modelCode, groups, points)
}

// ==================== 查询/校验辅助 ====================

func deviceConfigKey(code, name string) string {
	c := strings.TrimSpace(code)
	if c != "" {
		return c
	}
	return strings.TrimSpace(name)
}

func findDeviceForUpsert(tx *gorm.DB, code, name string) (po.Device, bool, error) {
	var d po.Device
	if code != "" {
		err := tx.Where("code = ?", code).First(&d).Error
		if err == nil {
			return d, true, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return po.Device{}, false, err
		}
		// 编码未命中时再看同名历史设备：仅允许接管 code 为空的旧行。
		err = tx.Where("name = ?", name).First(&d).Error
		switch {
		case err == nil:
			if d.Code != "" {
				return po.Device{}, false, appError.NewAppErrorCtx(enums.DeviceExistsEnum.GetCode(),
					"同名设备已有编码，不能按名称接管", enums.DeviceExistsEnum.GetMsgKey())
			}
			return d, true, nil
		case errors.Is(err, gorm.ErrRecordNotFound):
			return po.Device{}, false, nil
		default:
			return po.Device{}, false, err
		}
	}

	err := tx.Where("name = ?", name).First(&d).Error
	switch {
	case err == nil:
		if d.Code != "" {
			return po.Device{}, false, appError.NewAppErrorCtx(enums.CfgDeviceConfigInvalidEnum.GetCode(),
				"该设备已有编码，请使用 code 作为自然键", enums.CfgDeviceConfigInvalidEnum.GetMsgKey())
		}
		return d, true, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return po.Device{}, false, nil
	default:
		return po.Device{}, false, err
	}
}

func (s *OpenApiConfigService) findDeviceByNaturalKey(ctx context.Context, code, name string) (po.Device, error) {
	var d po.Device
	db := s.sqliteDB.WithContext(ctx)
	if c := strings.TrimSpace(code); c != "" {
		if err := db.Where("code = ?", c).First(&d).Error; err == nil {
			return d, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return po.Device{}, err
		}
	}
	if n := strings.TrimSpace(name); n != "" {
		if err := db.Where("name = ?", n).First(&d).Error; err == nil {
			return d, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return po.Device{}, err
		}
	}
	return po.Device{}, appError.NewAppErrorCtx(enums.DeviceNotExistsEnum.GetCode(),
		enums.DeviceNotExistsEnum.GetMessage(), enums.DeviceNotExistsEnum.GetMsgKey())
}

func ensureDeviceNameFree(tx *gorm.DB, name, exceptID string) error {
	var d po.Device
	q := tx.Where("name = ?", name)
	if exceptID != "" {
		q = q.Where("id != ?", exceptID)
	}
	if err := q.First(&d).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	return appError.NewAppErrorCtx(enums.DeviceExistsEnum.GetCode(),
		enums.DeviceExistsEnum.GetMessage(), enums.DeviceExistsEnum.GetMsgKey())
}

func ensureDeviceCodeFree(tx *gorm.DB, code, exceptID string) error {
	var d po.Device
	q := tx.Where("code = ?", code)
	if exceptID != "" {
		q = q.Where("id != ?", exceptID)
	}
	if err := q.First(&d).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	return deviceCodeExistsErr()
}

func ensureAllPointsSubmitted(tx *gorm.DB, deviceID string, keep map[string]bool) error {
	var addrs []po.DeviceAddress
	if err := tx.Select("name").Where("device_id = ?", deviceID).Find(&addrs).Error; err != nil {
		return err
	}
	for _, a := range addrs {
		if !keep[a.Name] {
			return appError.NewAppErrorCtx(enums.CfgProtocolChangePointsMissingEnum.GetCode(),
				enums.CfgProtocolChangePointsMissingEnum.GetMessage(), enums.CfgProtocolChangePointsMissingEnum.GetMsgKey())
		}
	}
	return nil
}

func ensureModelChangePointsSubmitted(tx *gorm.DB, deviceID string, keep map[string]bool) error {
	var addrs []po.DeviceAddress
	if err := tx.Select("name").Where("device_id = ? AND property_id <> ''", deviceID).Find(&addrs).Error; err != nil {
		return err
	}
	for _, a := range addrs {
		if !keep[a.Name] {
			return appError.NewAppErrorCtx(enums.CfgThingModelPointsMissingEnum.GetCode(),
				enums.CfgThingModelPointsMissingEnum.GetMessage(), enums.CfgThingModelPointsMissingEnum.GetMsgKey())
		}
	}
	return nil
}

func pointNameSet(points []dto.OpenApiDevicePointUpsertDTO) map[string]bool {
	out := make(map[string]bool, len(points))
	for _, p := range points {
		if name := strings.TrimSpace(p.Name); name != "" {
			out[name] = true
		}
	}
	return out
}

func normalizeJSONObject(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "{}", nil
	}
	var v any
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return "", err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return "", errors.New("必须是 JSON 对象")
	}
	compact, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(compact), nil
}

func cfgProtocolNotExistsErr() error {
	return appError.NewAppErrorCtx(enums.CfgProtocolNotExistsEnum.GetCode(),
		enums.CfgProtocolNotExistsEnum.GetMessage(), enums.CfgProtocolNotExistsEnum.GetMsgKey())
}
