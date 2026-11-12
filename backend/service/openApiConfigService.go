// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"strings"

	"gorm.io/gorm"

	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
	"iot-gateway/utils/canonicaljson"
)

const (
	openApiConfigVersion = "1.11"
	cfgSyncVersion       = 1
)

// OpenApiConfigService 开放接口的配置同步服务。
//
// 只维护网关配置，不触达现场设备；读接口返回的 Payload 就是管理平台应保存的
// 期望态，密钥字段已脱敏。写接口按自然键幂等 upsert，逐条部分成功。
type OpenApiConfigService struct {
	sqliteDB                *gorm.DB
	thingModelService       *ThingModelService
	pushChannelService      *PushChannelService
	pushChannelFormService  *PushChannelFormService
	alarmWebhookService     *AlarmWebhookService
	alarmWebhookFormService *AlarmWebhookFormService
	systemConfigService     *SystemConfigService
	protocolService         *ProtocolService
	gatewayService          *GatewayService
	deviceService           *DeviceService
	deviceAddressService    *DeviceAddressService
	collectorGroupService   *CollectorGroupService
}

// NewOpenApiConfigService 创建开放接口配置同步服务。
func NewOpenApiConfigService(
	sqliteDB *gorm.DB,
	thingModelService *ThingModelService,
	pushChannelService *PushChannelService,
	pushChannelFormService *PushChannelFormService,
	alarmWebhookService *AlarmWebhookService,
	alarmWebhookFormService *AlarmWebhookFormService,
	systemConfigService *SystemConfigService,
	protocolService *ProtocolService,
	gatewayService *GatewayService,
	deviceService *DeviceService,
	deviceAddressService *DeviceAddressService,
	collectorGroupService *CollectorGroupService,
) *OpenApiConfigService {
	return &OpenApiConfigService{
		sqliteDB:                sqliteDB,
		thingModelService:       thingModelService,
		pushChannelService:      pushChannelService,
		pushChannelFormService:  pushChannelFormService,
		alarmWebhookService:     alarmWebhookService,
		alarmWebhookFormService: alarmWebhookFormService,
		systemConfigService:     systemConfigService,
		protocolService:         protocolService,
		gatewayService:          gatewayService,
		deviceService:           deviceService,
		deviceAddressService:    deviceAddressService,
		collectorGroupService:   collectorGroupService,
	}
}

// Capabilities 返回配置同步能力与网关版本，供管理平台协商。
func (s *OpenApiConfigService) Capabilities(ctx context.Context) (*vo.OpenApiConfigCapabilitiesVO, error) {
	gatewayVersion := ""
	if gw, err := s.gatewayService.GetGateway(ctx); err == nil && gw != nil {
		gatewayVersion = gw.FirmwareVersion
	}
	return &vo.OpenApiConfigCapabilitiesVO{
		CfgSyncVersion: cfgSyncVersion,
		OpenApiVersion: openApiConfigVersion,
		GatewayVersion: gatewayVersion,
		HashVersion:    canonicaljson.HashVersion,
		HashAlgorithm:  "sha256-canonical-json-v1",
		Types: map[string]vo.OpenApiConfigTypeCapabilityVO{
			"runtime":       {Read: true, Update: true, Reset: true, MaxBatch: 50},
			"push_channel":  {Read: true, Upsert: true, Delete: true, MaxBatch: 100},
			"alarm_webhook": {Read: true, Upsert: true, Delete: true, MaxBatch: 100},
			"thing_model":   {Read: true, Upsert: true, Delete: true, MaxBatch: 100, ReplaceProperties: true},
			"device":        {Read: true, Upsert: true, Delete: true, MaxBatch: 20, ReplacePoints: true},
		},
	}, nil
}

// PageThingModels 物模型分页读（含属性与 contentHash）。
func (s *OpenApiConfigService) PageThingModels(ctx context.Context, req *dto.OpenApiConfigPageDTO) (*response.PageVo[vo.OpenApiConfigItemVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.ThingModel{})
	if kw := strings.TrimSpace(req.Keyword); kw != "" {
		like := "%" + kw + "%"
		db = db.Where("code LIKE ? OR name LIKE ?", like, like)
	}
	if req.Status != nil {
		db = db.Where("status = ?", *req.Status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	var models []po.ThingModel
	if err := db.Offset((req.Page - 1) * req.Size).Limit(req.Size).
		Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, err
	}

	propsByModel, err := s.loadThingModelProperties(ctx, modelIDs(models))
	if err != nil {
		return nil, err
	}

	records := make([]vo.OpenApiConfigItemVO, 0, len(models))
	for _, m := range models {
		props := propsByModel[m.ID]
		payload := map[string]any{
			"code":        m.Code,
			"name":        m.Name,
			"category":    m.Category,
			"version":     m.Version,
			"description": m.Description,
			"status":      m.Status,
			"properties":  thingModelPropertyPayloads(props),
		}
		hash, err := thingModelConfigHash(m, props)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		records = append(records, vo.OpenApiConfigItemVO{
			TargetKey:   m.Code,
			Payload:     raw,
			ContentHash: hash,
		})
	}
	return &response.PageVo[vo.OpenApiConfigItemVO]{Page: req.Page, Size: req.Size, Total: total, Records: records}, nil
}

// PageDeviceConfigs 设备配置分页读（设备 + 采集组 + 点位 + contentHash）。
func (s *OpenApiConfigService) PageDeviceConfigs(ctx context.Context, req *dto.OpenApiDeviceConfigPageDTO) (*response.PageVo[vo.OpenApiConfigItemVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.Device{})
	if kw := strings.TrimSpace(req.Keyword); kw != "" {
		like := "%" + kw + "%"
		db = db.Where("name LIKE ? OR code LIKE ? OR scan_identity LIKE ?", like, like, like)
	}
	if req.Status != nil {
		db = db.Where("status = ?", *req.Status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	var devices []po.Device
	if err := db.Offset((req.Page - 1) * req.Size).Limit(req.Size).
		Order("created_at DESC").Find(&devices).Error; err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return &response.PageVo[vo.OpenApiConfigItemVO]{Page: req.Page, Size: req.Size, Total: total, Records: []vo.OpenApiConfigItemVO{}}, nil
	}

	ids := deviceIDs(devices)
	groupsByDevice, err := s.loadCollectorGroups(ctx, ids)
	if err != nil {
		return nil, err
	}
	addrsByDevice, err := s.loadDeviceAddresses(ctx, ids)
	if err != nil {
		return nil, err
	}
	protocolNames, err := s.loadProtocolNames(ctx, devices)
	if err != nil {
		return nil, err
	}
	modelCodes, err := s.loadThingModelCodes(ctx, devices)
	if err != nil {
		return nil, err
	}
	propertyIdentifiers, err := s.loadPropertyIdentifiers(ctx, addrsByDevice)
	if err != nil {
		return nil, err
	}

	records := make([]vo.OpenApiConfigItemVO, 0, len(devices))
	for _, d := range devices {
		groups := groupsByDevice[d.ID]
		addrs := addrsByDevice[d.ID]
		points := make([]devicePointSemantic, 0, len(addrs))
		pointPayloads := make([]map[string]any, 0, len(addrs))
		for _, a := range addrs {
			point := devicePointSemantic{
				Name:               a.Name,
				Label:              a.Label,
				CommonDataType:     a.CommonDataType,
				RwPermission:       a.RwPermission,
				ScanFrequency:      a.ScanFrequency,
				Description:        a.Description,
				Status:             a.Status,
				PropertyIdentifier: propertyIdentifiers[a.PropertyID],
				GroupName:          groupNameByID(groups, a.GroupID),
				ThresholdEnabled:   a.ThresholdEnabled == 1,
				ThresholdOp:        a.ThresholdOp,
				ThresholdValues:    parseStringArrayOrEmpty(a.ThresholdValue),
				Transforms:         parseJSONArrayOrEmpty(a.TransformJSON),
			}
			points = append(points, point)
			pointPayloads = append(pointPayloads, map[string]any{
				"name":               point.Name,
				"label":              point.Label,
				"commonDataType":     point.CommonDataType,
				"rwPermission":       point.RwPermission,
				"scanFrequency":      point.ScanFrequency,
				"description":        point.Description,
				"status":             point.Status,
				"propertyIdentifier": point.PropertyIdentifier,
				"groupName":          point.GroupName,
				"threshold": map[string]any{
					"enabled": point.ThresholdEnabled,
					"op":      point.ThresholdOp,
					"values":  nonNilStrings(point.ThresholdValues),
				},
				"transform": point.Transforms,
			})
		}

		protocolJSON, err := parseJSONValueOrEmptyObject(d.ProtocolJSON)
		if err != nil {
			return nil, err
		}
		payload := map[string]any{
			"code":         d.Code,
			"name":         d.Name,
			"protocolName": protocolNames[d.ProtocolID],
			"protocolJson": canonicaljson.MaskSecrets(protocolJSON),
			"modelCode":    modelCodes[d.ModelID],
			"description":  d.Description,
			"status":       d.Status,
			"groups":       collectorGroupPayloads(groups),
			"points":       pointPayloads,
		}
		hash, err := deviceConfigHash(d, protocolNames[d.ProtocolID], modelCodes[d.ModelID], groups, points)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		key := strings.TrimSpace(d.Code)
		if key == "" {
			key = d.Name
		}
		records = append(records, vo.OpenApiConfigItemVO{
			TargetKey:   key,
			Payload:     raw,
			ContentHash: hash,
		})
	}
	return &response.PageVo[vo.OpenApiConfigItemVO]{Page: req.Page, Size: req.Size, Total: total, Records: records}, nil
}

// PagePushChannels 推送通道分页读（config 脱敏 + contentHash）。
func (s *OpenApiConfigService) PagePushChannels(ctx context.Context, req *dto.OpenApiPushChannelPageDTO) (*response.PageVo[vo.OpenApiConfigItemVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.PushChannel{})
	if name := strings.TrimSpace(req.Name); name != "" {
		db = db.Where("name LIKE ?", "%"+name+"%")
	}
	if t := strings.ToLower(strings.TrimSpace(req.Type)); t != "" {
		db = db.Where("type = ?", t)
	}
	if req.Status != nil {
		db = db.Where("status = ?", *req.Status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	var channels []po.PushChannel
	if err := db.Offset((req.Page - 1) * req.Size).Limit(req.Size).
		Order("created_at DESC").Find(&channels).Error; err != nil {
		return nil, err
	}

	records := make([]vo.OpenApiConfigItemVO, 0, len(channels))
	for _, ch := range channels {
		chType := strings.ToLower(strings.TrimSpace(ch.Type))
		if chType == "" {
			chType = strings.ToLower(strings.TrimSpace(ch.Name))
		}
		c := ch
		c.Type = chType

		cfg, err := parseJSONValueOrEmptyObject(c.ConfigJSON)
		if err != nil {
			return nil, err
		}
		payload := map[string]any{
			"type":        c.Type,
			"name":        c.Name,
			"description": c.Description,
			"config":      canonicaljson.MaskSecrets(cfg),
			"status":      c.Status,
		}
		hash, err := pushChannelConfigHash(c)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		records = append(records, vo.OpenApiConfigItemVO{
			TargetKey:   c.Type + "/" + c.Name,
			Payload:     raw,
			ContentHash: hash,
		})
	}
	return &response.PageVo[vo.OpenApiConfigItemVO]{Page: req.Page, Size: req.Size, Total: total, Records: records}, nil
}

// ListPushChannelForms 全部推送通道动态表单。
func (s *OpenApiConfigService) ListPushChannelForms(ctx context.Context) ([]vo.OpenApiFormVO, error) {
	var rows []po.PushChannelForm
	if err := s.sqliteDB.WithContext(ctx).Order("name ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]vo.OpenApiFormVO, 0, len(rows))
	for _, r := range rows {
		out = append(out, vo.OpenApiFormVO{Name: r.Name, FormJSON: json.RawMessage(r.FormJSON)})
	}
	return out, nil
}

// ListPushChannelTypes 推送通道类型元数据。
func (s *OpenApiConfigService) ListPushChannelTypes() []vo.OpenApiPushChannelTypeVO {
	return s.pushChannelService.ListPushChannelTypes()
}

// PageAlarmWebhooks 告警 Webhook 分页读（secret 脱敏 + contentHash）。
func (s *OpenApiConfigService) PageAlarmWebhooks(ctx context.Context, req *dto.OpenApiAlarmWebhookPageDTO) (*response.PageVo[vo.OpenApiConfigItemVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.AlarmWebhook{})
	if name := strings.TrimSpace(req.Name); name != "" {
		db = db.Where("name LIKE ?", "%"+name+"%")
	}
	if t := strings.TrimSpace(req.Type); t != "" {
		db = db.Where("type = ?", t)
	}
	if req.Status != nil {
		db = db.Where("status = ?", *req.Status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []po.AlarmWebhook
	if err := db.Offset((req.Page - 1) * req.Size).Limit(req.Size).
		Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	records := make([]vo.OpenApiConfigItemVO, 0, len(rows))
	for _, w := range rows {
		secret := ""
		if w.Secret != "" {
			secret = canonicaljson.Mask
		}
		payload := map[string]any{
			"name":        w.Name,
			"type":        w.Type,
			"description": w.Description,
			"url":         w.URL,
			"secret":      secret,
			"msgType":     w.MsgType,
			"atAll":       w.AtAll == 1,
			"atList":      decodeAtList(w.AtList),
			"status":      w.Status,
		}
		hash, err := alarmWebhookConfigHash(w)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		records = append(records, vo.OpenApiConfigItemVO{
			TargetKey:   w.Name,
			Payload:     raw,
			ContentHash: hash,
		})
	}
	return &response.PageVo[vo.OpenApiConfigItemVO]{Page: req.Page, Size: req.Size, Total: total, Records: records}, nil
}

// ListAlarmWebhookForms 全部告警 Webhook 动态表单。
func (s *OpenApiConfigService) ListAlarmWebhookForms(ctx context.Context) ([]vo.OpenApiFormVO, error) {
	var rows []po.AlarmWebhookForm
	if err := s.sqliteDB.WithContext(ctx).Order("name ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]vo.OpenApiFormVO, 0, len(rows))
	for _, r := range rows {
		out = append(out, vo.OpenApiFormVO{Name: r.Name, FormJSON: json.RawMessage(r.FormJSON)})
	}
	return out, nil
}

// ListAlarmWebhookTypes 告警 Webhook 类型元数据。
func (s *OpenApiConfigService) ListAlarmWebhookTypes() []vo.OpenApiAlarmWebhookTypeVO {
	return s.alarmWebhookService.ListAlarmWebhookTypes()
}

// ListSystemConfig 运行开关全量读（拍平 + contentHash）。
func (s *OpenApiConfigService) ListSystemConfig(ctx context.Context) ([]vo.OpenApiRuntimeConfigVO, error) {
	cfg, err := s.systemConfigService.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]vo.OpenApiRuntimeConfigVO, 0)
	for _, group := range cfg.Groups {
		for _, f := range group.Fields {
			options := make([]vo.OpenApiRuntimeOptionVO, 0, len(f.Options))
			for _, o := range f.Options {
				options = append(options, vo.OpenApiRuntimeOptionVO{Value: o.Value, LabelKey: o.LabelKey})
			}
			hash, err := runtimeConfigHash(f.Key, f.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, vo.OpenApiRuntimeConfigVO{
				TargetKey:      f.Key,
				Key:            f.Key,
				Type:           f.Type,
				Value:          f.Value,
				DefaultValue:   f.DefaultValue,
				Options:        options,
				Source:         f.Source,
				Hot:            f.Hot,
				PendingRestart: f.PendingRestart,
				LabelKey:       f.LabelKey,
				DescKey:        f.DescKey,
				ContentHash:    hash,
			})
		}
	}
	return out, nil
}

// ListProtocols 协议与点位数据类型（供设备模板编辑）。
func (s *OpenApiConfigService) ListProtocols(ctx context.Context) ([]vo.OpenApiProtocolVO, error) {
	var rows []po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Order("sort ASC, name ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]vo.OpenApiProtocolVO, 0, len(rows))
	for _, p := range rows {
		item := vo.OpenApiProtocolVO{
			Name:        p.Name,
			Description: p.Description,
			FormJSON:    json.RawMessage(p.FormJSON),
		}
		if dt, err := s.protocolService.GetProtocolDataTypes(ctx, p.Name, ""); err == nil && dt != nil {
			item.CommonTypes = dt.CommonTypes
			item.ExtendedTypes = dt.ExtendedTypes
			item.KindByType = dt.KindByType
		}
		out = append(out, item)
	}
	return out, nil
}

// ==================== 批量读辅助 ====================

func modelIDs(models []po.ThingModel) []string {
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return ids
}

func deviceIDs(devices []po.Device) []string {
	ids := make([]string, 0, len(devices))
	for _, d := range devices {
		ids = append(ids, d.ID)
	}
	return ids
}

func (s *OpenApiConfigService) loadThingModelProperties(ctx context.Context, ids []string) (map[string][]po.ThingModelProperty, error) {
	out := make(map[string][]po.ThingModelProperty, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []po.ThingModelProperty
	if err := s.sqliteDB.WithContext(ctx).
		Where("model_id IN ?", ids).
		Order("model_id ASC, sort_order ASC, identifier ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ModelID] = append(out[r.ModelID], r)
	}
	return out, nil
}

func (s *OpenApiConfigService) loadCollectorGroups(ctx context.Context, deviceIDs []string) (map[string][]po.CollectorGroup, error) {
	out := make(map[string][]po.CollectorGroup, len(deviceIDs))
	if len(deviceIDs) == 0 {
		return out, nil
	}
	var rows []po.CollectorGroup
	if err := s.sqliteDB.WithContext(ctx).
		Where("device_id IN ?", deviceIDs).
		Order("device_id ASC, name ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.DeviceID] = append(out[r.DeviceID], r)
	}
	return out, nil
}

func (s *OpenApiConfigService) loadDeviceAddresses(ctx context.Context, deviceIDs []string) (map[string][]po.DeviceAddress, error) {
	out := make(map[string][]po.DeviceAddress, len(deviceIDs))
	if len(deviceIDs) == 0 {
		return out, nil
	}
	var rows []po.DeviceAddress
	if err := s.sqliteDB.WithContext(ctx).
		Where("device_id IN ?", deviceIDs).
		Order("device_id ASC, name ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.DeviceID] = append(out[r.DeviceID], r)
	}
	return out, nil
}

func (s *OpenApiConfigService) loadProtocolNames(ctx context.Context, devices []po.Device) (map[string]string, error) {
	ids := make([]string, 0, len(devices))
	for _, d := range devices {
		if d.ProtocolID != "" {
			ids = append(ids, d.ProtocolID)
		}
	}
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

func (s *OpenApiConfigService) loadThingModelCodes(ctx context.Context, devices []po.Device) (map[string]string, error) {
	ids := make([]string, 0, len(devices))
	for _, d := range devices {
		if d.ModelID != "" {
			ids = append(ids, d.ModelID)
		}
	}
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []po.ThingModel
	if err := s.sqliteDB.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r.Code
	}
	return out, nil
}

func (s *OpenApiConfigService) loadPropertyIdentifiers(ctx context.Context, addrsByDevice map[string][]po.DeviceAddress) (map[string]string, error) {
	ids := make([]string, 0)
	for _, addrs := range addrsByDevice {
		for _, a := range addrs {
			if a.PropertyID != "" {
				ids = append(ids, a.PropertyID)
			}
		}
	}
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []po.ThingModelProperty
	if err := s.sqliteDB.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r.Identifier
	}
	return out, nil
}

func thingModelPropertyPayloads(props []po.ThingModelProperty) []map[string]any {
	out := make([]map[string]any, 0, len(props))
	for _, p := range props {
		out = append(out, map[string]any{
			"identifier":  p.Identifier,
			"name":        p.Name,
			"kind":        p.Kind,
			"accessMode":  p.AccessMode,
			"unit":        p.Unit,
			"description": p.Description,
			"sortOrder":   p.SortOrder,
			"status":      p.Status,
		})
	}
	return out
}

func collectorGroupPayloads(groups []po.CollectorGroup) []map[string]any {
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		out = append(out, map[string]any{
			"code":        g.Code,
			"name":        g.Name,
			"intervalMs":  g.IntervalMs,
			"description": g.Description,
			"status":      g.Status,
		})
	}
	return out
}

func groupNameByID(groups []po.CollectorGroup, id string) string {
	if id == "" {
		return ""
	}
	for _, g := range groups {
		if g.ID == id {
			return g.Name
		}
	}
	return ""
}
