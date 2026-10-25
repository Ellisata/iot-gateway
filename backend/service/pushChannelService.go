// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/appError"
	"iot-gateway/collector"
	"iot-gateway/enums"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/push"
	"iot-gateway/push/codec"
	"iot-gateway/response"
)

// PushChannelService 数据推送通道服务
type PushChannelService struct {
	sqliteDB     *gorm.DB
	alarmService *AlarmService
}

// NewPushChannelService 创建数据推送通道服务
func NewPushChannelService(sqliteDB *gorm.DB, alarmService *AlarmService) *PushChannelService {
	return &PushChannelService{sqliteDB: sqliteDB, alarmService: alarmService}
}

// CreatePushChannel 创建数据推送通道
func (s *PushChannelService) CreatePushChannel(ctx context.Context, req *dto.CreatePushChannelDTO) error {
	channelType, meta, err := resolveChannelType(req.Type, req.Name)
	if err != nil {
		return err
	}
	if err := validateChannelConfig(meta, req.ConfigJSON); err != nil {
		return err
	}

	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 同一类型下实例名唯一；不同类型可以同名。
		var existing po.PushChannel
		if err := tx.Where("type = ? AND name = ?", channelType, req.Name).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if existing.ID != "" {
			return appError.NewAppErrorCtx(enums.PushChannelExistsEnum.GetCode(), enums.PushChannelExistsEnum.GetMessage(), enums.PushChannelExistsEnum.GetMsgKey())
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		channel := &po.PushChannel{
			Type:        channelType,
			Name:        req.Name,
			Description: req.Description,
			ConfigJSON:  string(req.ConfigJSON),
			Status:      1,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		return tx.Create(channel).Error
	})
}

// UpdatePushChannel 更新数据推送通道
func (s *PushChannelService) UpdatePushChannel(ctx context.Context, req *dto.UpdatePushChannelDTO) error {
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 检查通道是否存在
		var existing po.PushChannel
		id := req.ID
		if err := tx.Where("id = ?", id).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(enums.PushChannelNotExistsEnum.GetCode(), enums.PushChannelNotExistsEnum.GetMessage(), enums.PushChannelNotExistsEnum.GetMsgKey())
			}
			return err
		}

		existingType := existing.Type
		if existingType == "" {
			existingType = existing.Name
		}
		if req.Type != "" && strings.ToLower(strings.TrimSpace(req.Type)) != existingType {
			return appError.NewAppError(enums.ParamValidEnum.GetCode(), "通道类型不可修改")
		}
		if len(req.ConfigJSON) > 0 {
			meta, ok := push.RegisteredTypeMeta(existingType)
			if !ok {
				return appError.NewAppError(enums.ParamValidEnum.GetCode(), "未知的通道类型: "+existingType)
			}
			if err := validateChannelConfig(meta, req.ConfigJSON); err != nil {
				return err
			}
		}

		// 检查同一类型下的实例名是否被占用（修改名称时）
		if req.Name != "" && req.Name != existing.Name {
			var dup po.PushChannel
			if err := tx.Where("type = ? AND name = ? AND id != ?", existingType, req.Name, id).First(&dup).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			if dup.ID != "" {
				return appError.NewAppErrorCtx(enums.PushChannelExistsEnum.GetCode(), enums.PushChannelExistsEnum.GetMessage(), enums.PushChannelExistsEnum.GetMsgKey())
			}
		}

		updates := make(map[string]any)
		if req.Name != "" {
			updates["name"] = req.Name
		}
		if req.Description != "" {
			updates["description"] = req.Description
		}
		if len(req.ConfigJSON) > 0 {
			updates["config_json"] = string(req.ConfigJSON)
		}
		if req.Status != nil {
			updates["status"] = *req.Status
		}
		if len(updates) == 0 {
			return nil
		}
		updates["updated_at"] = time.Now().Format("2006-01-02 15:04:05")

		return tx.Model(&po.PushChannel{}).Where("id = ?", id).Updates(updates).Error
	})
	if err != nil {
		return err
	}

	// 通道停用（status=0）：清除其活跃断联报警，历史保留。
	// 事务外执行（尽力而为，失败不影响通道更新结果）。
	if req.Status != nil && *req.Status == 0 && s.alarmService != nil {
		if err := s.alarmService.ClearChannelAlarm(ctx, req.ID); err != nil {
			logger.Warn("push channel: clear alarm for disabled channel %s failed: %v", req.ID, err)
		}
	}
	return nil
}

// GetPushChannelById 根据ID查询数据推送通道
func (s *PushChannelService) GetPushChannelById(ctx context.Context, id string) (*vo.PushChannelVO, error) {
	var channel po.PushChannel
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&channel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(enums.PushChannelNotExistsEnum.GetCode(), enums.PushChannelNotExistsEnum.GetMessage(), enums.PushChannelNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}

	return &vo.PushChannelVO{
		ID:          channel.ID,
		Type:        channel.Type,
		Name:        channel.Name,
		Description: channel.Description,
		ConfigJSON:  json.RawMessage(channel.ConfigJSON),
		Status:      channel.Status,
		CreatedAt:   channel.CreatedAt,
		UpdatedAt:   channel.UpdatedAt,
	}, nil
}

// TestPushChannel 测试推送通道连通性（不落库）。
// 直接以通道类型名 + 配置 JSON 构建临时通道实例并建立连接探测，失败返回具体错误原因。
// 与设备 TestDeviceConnection 同样采用原始 JSON 串传递，不同通道类型的 configJson 字段各不相同。
func (s *PushChannelService) TestPushChannel(ctx context.Context, req *dto.TestPushChannelDTO) error {
	channelType := strings.ToLower(strings.TrimSpace(req.Type))
	if channelType == "" {
		// 旧客户端把类型放在 name 里
		channelType = strings.ToLower(strings.TrimSpace(req.Name))
	}
	if channelType == "" {
		return appError.NewAppError(enums.ParamValidEnum.GetCode(), "缺少通道类型")
	}
	if err := push.TestChannelConnectivity(s.sqliteDB, channelType, string(req.ConfigJSON)); err != nil {
		logger.Error("%s", err.Error())
		return appError.NewAppError(enums.PushChannelConnectFailEnum.GetCode(), "连接失败: "+err.Error())
	}
	return nil
}

// ListPushChannelTypes 返回已注册的通道类型元数据。
func (s *PushChannelService) ListPushChannelTypes() []push.ChannelTypeMeta {
	return push.SupportedChannelTypeMetas()
}

// resolveChannelType 归一化并校验通道类型；空类型回落为实例名（历史语义）。
func resolveChannelType(rawType, fallbackName string) (string, push.ChannelTypeMeta, error) {
	t := strings.ToLower(strings.TrimSpace(rawType))
	if t == "" {
		t = strings.ToLower(strings.TrimSpace(fallbackName))
	}
	meta, ok := push.RegisteredTypeMeta(t)
	if !ok {
		return "", push.ChannelTypeMeta{}, appError.NewAppError(enums.ParamValidEnum.GetCode(), "未知的通道类型: "+t)
	}
	return meta.Type, meta, nil
}

// validateChannelConfig 只在新格式字段出现时校验；旧配置完全跳过，保持兼容。
func validateChannelConfig(meta push.ChannelTypeMeta, cfg json.RawMessage) error {
	if len(bytes.TrimSpace(cfg)) == 0 {
		return nil
	}
	var probe struct {
		Format  string          `json:"format"`
		Mapping json.RawMessage `json:"mapping"`
	}
	if err := json.Unmarshal(cfg, &probe); err != nil {
		return appError.NewAppError(enums.ParamValidEnum.GetCode(), "配置 JSON 解析失败: "+err.Error())
	}
	if strings.TrimSpace(probe.Format) == "" && len(bytes.TrimSpace(probe.Mapping)) == 0 {
		return nil
	}
	if !meta.MessageBased {
		return appError.NewAppError(enums.ParamValidEnum.GetCode(), "该通道类型不支持 format/mapping 配置")
	}
	if err := codec.Validate(probe.Format, probe.Mapping); err != nil {
		return appError.NewAppError(enums.ParamValidEnum.GetCode(), err.Error())
	}
	return nil
}

// PreviewPushChannel 用样例记录试跑出站编码，不发送、不落库。
func (s *PushChannelService) PreviewPushChannel(ctx context.Context, req *dto.PreviewPushChannelDTO) *vo.PushChannelPreviewVO {
	ch, err := push.NewTemporaryChannel(s.sqliteDB, req.Type, req.Type, string(req.ConfigJSON))
	if err != nil {
		return &vo.PushChannelPreviewVO{Error: err.Error()}
	}
	previewer, ok := ch.(push.Previewer)
	if !ok {
		return &vo.PushChannelPreviewVO{Error: "该通道类型不支持载荷预览"}
	}
	res := previewer.Preview(s.samplePushBatch(ctx))
	return previewResultVO(res)
}

// PreviewPushChannelMapping 只试编码出站映射，不依赖通道类型与连接配置。
func (s *PushChannelService) PreviewPushChannelMapping(ctx context.Context, req *dto.PreviewPushChannelMappingDTO) *vo.PushChannelPreviewVO {
	raw := bytes.TrimSpace(req.Mapping)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return &vo.PushChannelPreviewVO{Error: "mapping 不能为空"}
	}
	format := strings.TrimSpace(req.Format)
	if format == "" {
		format = push.FormatMappingJSON
	}
	enc, err := codec.New(format, req.Mapping)
	if err != nil {
		return &vo.PushChannelPreviewVO{Error: err.Error()}
	}
	return previewResultVO(push.PreviewWithEncoder(enc, s.samplePushBatch(ctx)))
}

func previewResultVO(res push.PreviewResult) *vo.PushChannelPreviewVO {
	out := &vo.PushChannelPreviewVO{
		Payloads: res.Payloads,
		Count:    len(res.Payloads),
		Error:    res.Error,
	}
	if len(res.Payloads) == 1 {
		out.Payload = res.Payloads[0]
	}
	return out
}

// TestSendPushChannel 用样例记录真的向下游发送一次，不落库、不影响运行中的通道。
func (s *PushChannelService) TestSendPushChannel(ctx context.Context, req *dto.TestSendPushChannelDTO) *vo.PushChannelTestSendVO {
	ch, err := push.NewTemporaryChannel(s.sqliteDB, req.Type, req.Type, string(req.ConfigJSON))
	if err != nil {
		return &vo.PushChannelTestSendVO{Error: err.Error()}
	}
	sender, ok := ch.(push.TestSender)
	if !ok {
		return &vo.PushChannelTestSendVO{Error: "该通道类型不支持测试发送"}
	}
	res := sender.TestSend(s.samplePushBatch(ctx))
	return &vo.PushChannelTestSendVO{
		Success:    res.Success,
		StatusCode: res.StatusCode,
		Messages:   res.Messages,
		DurationMs: res.DurationMs,
		Error:      res.Error,
	}
}

// samplePushBatch 用第一台启用设备/点位构造一条样例记录；没有配置时用内置样例。
func (s *PushChannelService) samplePushBatch(ctx context.Context) push.PushBatch {
	now := time.Now().Format("2006-01-02 15:04:05.000")

	var dev po.Device
	if err := s.sqliteDB.WithContext(ctx).Where("status = 1").Order("created_at ASC").First(&dev).Error; err != nil {
		return builtinSample(now)
	}

	var addr po.DeviceAddress
	if err := s.sqliteDB.WithContext(ctx).
		Where("device_id = ? AND status = 1", dev.ID).
		Order("created_at ASC").
		First(&addr).Error; err != nil {
		addr = po.DeviceAddress{ID: "sample-point", Name: "sample", Label: "样例点位"}
	}

	protocol := ""
	var proto po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", dev.ProtocolID).First(&proto).Error; err == nil {
		protocol = proto.Name
	}
	if protocol == "" {
		protocol = "Demo"
	}

	dataType := addr.DataType
	if dataType == "" {
		dataType = "float32"
	}
	rec := collector.CollectedRecord{
		DeviceID:           dev.ID,
		DeviceName:         dev.Name,
		DeviceAddressID:    addr.ID,
		DeviceAddressName:  addr.Name,
		DeviceAddressLabel: addr.Label,
		Value:              "23.5",
		DataType:           dataType,
		Kind:               driver.KindFloat,
		Protocol:           protocol,
		Quality:            driver.QualityGood,
		CollectedAt:        now,
	}
	return push.PushBatch{DeviceID: dev.ID, CollectedAt: now, Records: []collector.CollectedRecord{rec}}
}

func builtinSample(now string) push.PushBatch {
	rec := collector.CollectedRecord{
		DeviceID:           "demo-device",
		DeviceName:         "样例设备",
		DeviceAddressID:    "demo-point",
		DeviceAddressName:  "temperature",
		DeviceAddressLabel: "温度",
		Value:              "23.5",
		DataType:           "float32",
		Kind:               driver.KindFloat,
		Protocol:           "Demo",
		Quality:            driver.QualityGood,
		CollectedAt:        now,
	}
	return push.PushBatch{DeviceID: rec.DeviceID, CollectedAt: now, Records: []collector.CollectedRecord{rec}}
}

// DeletePushChannel 删除数据推送通道
func (s *PushChannelService) DeletePushChannel(ctx context.Context, id string) error {
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).Delete(&po.PushChannel{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return appError.NewAppErrorCtx(enums.PushChannelNotExistsEnum.GetCode(), enums.PushChannelNotExistsEnum.GetMessage(), enums.PushChannelNotExistsEnum.GetMsgKey())
		}
		return nil
	})
	if err != nil {
		return err
	}

	// 删除后清除其活跃断联报警，历史保留（尽力而为）。
	if s.alarmService != nil {
		if err := s.alarmService.ClearChannelAlarm(ctx, id); err != nil {
			logger.Warn("push channel: clear alarm for deleted channel %s failed: %v", id, err)
		}
	}
	return nil
}

// PagePushChannel 数据推送通道分页查询（支持名称模糊检索）
func (s *PushChannelService) PagePushChannel(ctx context.Context, req *dto.PagePushChannelDTO) (*response.PageVo[vo.PushChannelVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.PushChannel{})

	if req.Name != "" {
		db = db.Where("name LIKE ?", "%"+req.Name+"%")
	}
	if req.Type != "" {
		db = db.Where("type = ?", strings.ToLower(strings.TrimSpace(req.Type)))
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var channels []po.PushChannel
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).Order("created_at DESC").Find(&channels).Error; err != nil {
		return nil, err
	}

	records := make([]vo.PushChannelVO, len(channels))
	for i, ch := range channels {
		records[i] = vo.PushChannelVO{
			ID:          ch.ID,
			Type:        ch.Type,
			Name:        ch.Name,
			Description: ch.Description,
			ConfigJSON:  json.RawMessage(ch.ConfigJSON),
			Status:      ch.Status,
			CreatedAt:   ch.CreatedAt,
			UpdatedAt:   ch.UpdatedAt,
		}
	}

	return &response.PageVo[vo.PushChannelVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}
