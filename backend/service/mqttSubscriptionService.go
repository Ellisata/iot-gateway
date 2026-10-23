// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/collector"
	"iot-gateway/enums"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/parser"
	"iot-gateway/response"
	"iot-gateway/subscribe"
)

// normalizeParserType 空类型按 builtin（历史配置兼容）。
func normalizeParserType(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return parser.TypeBuiltin
	}
	return t
}

// MqttSubscriptionService MQTT 订阅管理服务
type MqttSubscriptionService struct {
	sqliteDB *gorm.DB
	manager  *subscribe.Manager
}

// NewMqttSubscriptionService 创建 MQTT 订阅管理服务
func NewMqttSubscriptionService(sqliteDB *gorm.DB, manager *subscribe.Manager) *MqttSubscriptionService {
	return &MqttSubscriptionService{sqliteDB: sqliteDB, manager: manager}
}

// CreateMqttSubscription 新增 MQTT 订阅
func (s *MqttSubscriptionService) CreateMqttSubscription(ctx context.Context, req *dto.CreateMqttSubscriptionDTO) error {
	status := 1
	if req.Status != nil {
		status = *req.Status
	}
	parserType := normalizeParserType(req.ParserType)
	mappingJSON := string(req.MappingJSON)
	parserConfig := string(req.ParserConfig)
	if parserType == parser.TypeBuiltin {
		if strings.TrimSpace(mappingJSON) == "" {
			return appError.NewAppError(enums.ParamValidEnum.GetCode(), "映射规则不能为空")
		}
		parserConfig = ""
	} else {
		if strings.TrimSpace(parserConfig) == "" {
			return appError.NewAppError(enums.ParamValidEnum.GetCode(), "解析配置不能为空")
		}
		if strings.TrimSpace(mappingJSON) == "" {
			mappingJSON = "{}"
		}
	}
	row := &po.MqttSubscription{
		Name:         req.Name,
		Description:  req.Description,
		Broker:       req.Broker,
		Port:         req.Port,
		ClientID:     req.ClientID,
		Username:     req.Username,
		Password:     req.Password,
		Topic:        req.Topic,
		QoS:          req.QoS,
		ParserType:   parserType,
		ParserConfig: parserConfig,
		MappingJSON:  mappingJSON,
		SampleJSON:   string(req.SampleJSON),
		SampleTopic:  req.SampleTopic,
		Sort:         req.Sort,
		Status:       status,
	}
	if err := subscribe.ValidateConfig(row); err != nil {
		return appError.NewAppError(enums.ParamValidEnum.GetCode(), err.Error())
	}

	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.MqttSubscription
		if err := tx.Where("name = ?", req.Name).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if existing.ID != "" {
			return appError.NewAppErrorCtx(enums.MqttSubscriptionExistsEnum.GetCode(),
				enums.MqttSubscriptionExistsEnum.GetMessage(), enums.MqttSubscriptionExistsEnum.GetMsgKey())
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		row.CreatedAt = now
		row.UpdatedAt = now
		return tx.Create(row).Error
	})
	if err != nil {
		return err
	}
	s.reload()
	return nil
}

// reload 让管理器立即应用配置变更。失败只记日志：管理器的 10s 配置监视器
// 会在下一轮自行补救，不应因为一次热加载失败让保存操作报错。
func (s *MqttSubscriptionService) reload() {
	if s.manager == nil {
		return
	}
	if err := s.manager.Refresh(); err != nil {
		logger.Warn("mqtt subscription: hot reload failed: %v", err)
	}
}

// UpdateMqttSubscription 编辑 MQTT 订阅
func (s *MqttSubscriptionService) UpdateMqttSubscription(ctx context.Context, req *dto.UpdateMqttSubscriptionDTO) error {
	var existing po.MqttSubscription
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", req.ID).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return appError.NewAppErrorCtx(enums.MqttSubscriptionNotExistsEnum.GetCode(),
				enums.MqttSubscriptionNotExistsEnum.GetMessage(), enums.MqttSubscriptionNotExistsEnum.GetMsgKey())
		}
		return err
	}

	// 解析配置为空表示沿用原值（列表页只改启停时不必回传整份规则）。
	parserType := normalizeParserType(req.ParserType)
	if strings.TrimSpace(req.ParserType) == "" {
		parserType = normalizeParserType(existing.ParserType)
	}
	mappingJSON := string(req.MappingJSON)
	parserConfig := string(req.ParserConfig)
	if parserType == parser.TypeBuiltin {
		if strings.TrimSpace(mappingJSON) == "" {
			mappingJSON = existing.MappingJSON
		}
		// 从脚本切回内置映射时清掉旧脚本，避免库里留下无意义的配置。
		parserConfig = ""
	} else {
		if strings.TrimSpace(parserConfig) == "" {
			parserConfig = existing.ParserConfig
		}
		if strings.TrimSpace(parserConfig) == "" {
			return appError.NewAppError(enums.ParamValidEnum.GetCode(), "解析配置不能为空")
		}
		if strings.TrimSpace(mappingJSON) == "" {
			mappingJSON = existing.MappingJSON
			if strings.TrimSpace(mappingJSON) == "" {
				mappingJSON = "{}"
			}
		}
	}
	candidate := po.MqttSubscription{
		Name:         req.Name,
		Broker:       req.Broker,
		Port:         req.Port,
		ClientID:     req.ClientID,
		Username:     req.Username,
		Password:     req.Password,
		Topic:        req.Topic,
		QoS:          req.QoS,
		ParserType:   parserType,
		ParserConfig: parserConfig,
		MappingJSON:  mappingJSON,
	}
	if err := subscribe.ValidateConfig(&candidate); err != nil {
		return appError.NewAppError(enums.ParamValidEnum.GetCode(), err.Error())
	}

	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if req.Name != existing.Name {
			var dup po.MqttSubscription
			if err := tx.Where("name = ? AND id != ?", req.Name, req.ID).First(&dup).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			if dup.ID != "" {
				return appError.NewAppErrorCtx(enums.MqttSubscriptionExistsEnum.GetCode(),
					enums.MqttSubscriptionExistsEnum.GetMessage(), enums.MqttSubscriptionExistsEnum.GetMsgKey())
			}
		}

		// 样例只服务于预览：本次没传就沿用原值，避免列表页只改启停时清空样例。
		sampleJSON := string(req.SampleJSON)
		if strings.TrimSpace(sampleJSON) == "" {
			sampleJSON = existing.SampleJSON
		}
		sampleTopic := req.SampleTopic
		if strings.TrimSpace(sampleTopic) == "" {
			sampleTopic = existing.SampleTopic
		}

		updates := map[string]any{
			"name":          req.Name,
			"description":   req.Description,
			"broker":        req.Broker,
			"port":          req.Port,
			"client_id":     req.ClientID,
			"username":      req.Username,
			"password":      req.Password,
			"topic":         req.Topic,
			"qos":           req.QoS,
			"parser_type":   parserType,
			"parser_config": parserConfig,
			"mapping_json":  mappingJSON,
			"sample_json":   sampleJSON,
			"sample_topic":  sampleTopic,
			"sort":          req.Sort,
			"updated_at":    time.Now().Format("2006-01-02 15:04:05"),
		}
		if req.Status != nil {
			updates["status"] = *req.Status
		}
		return tx.Model(&po.MqttSubscription{}).Where("id = ?", req.ID).Updates(updates).Error
	})
	if err != nil {
		return err
	}
	s.reload()
	return nil
}

// DeleteMqttSubscription 删除 MQTT 订阅
func (s *MqttSubscriptionService) DeleteMqttSubscription(ctx context.Context, id string) error {
	result := s.sqliteDB.WithContext(ctx).Where("id = ?", id).Delete(&po.MqttSubscription{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return appError.NewAppErrorCtx(enums.MqttSubscriptionNotExistsEnum.GetCode(),
			enums.MqttSubscriptionNotExistsEnum.GetMessage(), enums.MqttSubscriptionNotExistsEnum.GetMsgKey())
	}
	s.reload()
	return nil
}

// GetMqttSubscriptionById 根据 ID 查询 MQTT 订阅
func (s *MqttSubscriptionService) GetMqttSubscriptionById(ctx context.Context, id string) (*vo.MqttSubscriptionVO, error) {
	var row po.MqttSubscription
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(enums.MqttSubscriptionNotExistsEnum.GetCode(),
				enums.MqttSubscriptionNotExistsEnum.GetMessage(), enums.MqttSubscriptionNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}
	return toMqttSubscriptionVO(&row), nil
}

// PageMqttSubscription MQTT 订阅分页查询（名称模糊检索）
func (s *MqttSubscriptionService) PageMqttSubscription(ctx context.Context, req *dto.PageMqttSubscriptionDTO) (*response.PageVo[vo.MqttSubscriptionVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.MqttSubscription{})
	if req.Name != "" {
		db = db.Where("name LIKE ?", "%"+req.Name+"%")
	}
	if req.Status != nil {
		db = db.Where("status = ?", *req.Status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var rows []po.MqttSubscription
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).Order("sort ASC, created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	records := make([]vo.MqttSubscriptionVO, len(rows))
	for i := range rows {
		records[i] = *toMqttSubscriptionVO(&rows[i])
	}
	return &response.PageVo[vo.MqttSubscriptionVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}

// PageMqttSubscriptionDevicePoint 分页查询订阅报文里出现过的设备/点位信息。
//
// 该表是身份索引，不含采样值；按设备 ID + 点位 ID 自然键稳定排序。
func (s *MqttSubscriptionService) PageMqttSubscriptionDevicePoint(ctx context.Context, req *dto.PageMqttSubscriptionDevicePointDTO) (*response.PageVo[vo.MqttSubscriptionDevicePointVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.MqttSubscriptionDevicePoint{})
	if req.DeviceID != "" {
		db = db.Where("device_id LIKE ?", "%"+req.DeviceID+"%")
	}
	if req.DeviceName != "" {
		db = db.Where("device_name LIKE ?", "%"+req.DeviceName+"%")
	}
	if req.DeviceAddressID != "" {
		db = db.Where("device_address_id LIKE ?", "%"+req.DeviceAddressID+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var rows []po.MqttSubscriptionDevicePoint
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).
		Order("device_id ASC, device_address_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}

	records := make([]vo.MqttSubscriptionDevicePointVO, len(rows))
	for i := range rows {
		r := &rows[i]
		records[i] = vo.MqttSubscriptionDevicePointVO{
			DeviceID:           r.DeviceID,
			DeviceAddressID:    r.DeviceAddressID,
			DeviceName:         r.DeviceName,
			DeviceAddressLabel: r.DeviceAddressLabel,
			Kind:               r.Kind,
			Source:             r.Source,
			CreatedAt:          r.CreatedAt,
			UpdatedAt:          r.UpdatedAt,
		}
	}
	return &response.PageVo[vo.MqttSubscriptionDevicePointVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}

// PreviewMqttSubscription 用尚未保存的解析配置 + 样例报文试跑转换。
//
// 与运行期走同一条 parser.New → Parse 路径；转换失败不是 AppError：
// 配置写错是编辑过程中的正常反馈，由前端直接展示。
func (s *MqttSubscriptionService) PreviewMqttSubscription(req *dto.PreviewMqttSubscriptionDTO) *vo.MqttSubscriptionPreviewVO {
	result := &vo.MqttSubscriptionPreviewVO{Records: []collector.CollectedRecord{}}

	parserType := normalizeParserType(req.ParserType)
	cfg := req.ParserConfig
	if parserType == parser.TypeBuiltin {
		cfg = req.MappingJSON
	}
	if strings.TrimSpace(string(cfg)) == "" {
		result.Error = "解析配置为空"
		return result
	}

	p, err := parser.New(parser.Config{Type: parserType, Config: cfg})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := p.Parse(ctx, []parser.Message{{
		Topic:      req.SampleTopic,
		Payload:    req.SampleJSON,
		ReceivedAt: time.Now(),
	}})
	if err != nil {
		result.Error = err.Error()
		result.Skipped = res.Skipped
		return result
	}
	result.Records = res.Records
	result.Count = len(res.Records)
	result.Skipped = res.Skipped
	if len(res.Errors) > 0 {
		result.Error = res.Errors[0].Reason
	}
	return result
}

// TestMqttSubscription 测试一组尚未保存的订阅参数：校验配置并连接 broker。
func (s *MqttSubscriptionService) TestMqttSubscription(req *dto.TestMqttSubscriptionDTO) error {
	if err := subscribe.TestConnectivity(req.Broker, req.Port, req.ClientID, req.Username, req.Password, req.Topic); err != nil {
		logger.Error("mqtt subscription: connectivity test failed: %v", err)
		return appError.NewAppError(enums.MqttSubscriptionConnectFailEnum.GetCode(), fmt.Sprintf("连接失败: %v", err))
	}
	return nil
}

// MqttSubscriptionStatus 返回所有活跃订阅的运行状态。
func (s *MqttSubscriptionService) MqttSubscriptionStatus() []vo.MqttSubscriptionStatusVO {
	if s.manager == nil {
		return []vo.MqttSubscriptionStatusVO{}
	}
	statuses := s.manager.Status()
	out := make([]vo.MqttSubscriptionStatusVO, len(statuses))
	for i, st := range statuses {
		out[i] = vo.MqttSubscriptionStatusVO{
			ID:          st.ID,
			Name:        st.Name,
			Broker:      st.Broker,
			Topic:       st.Topic,
			Running:     st.Running,
			Connected:   st.Connected,
			Messages:    st.Messages,
			Records:     st.Records,
			Errors:      st.Errors,
			Skipped:     st.Skipped,
			Dropped:     st.Dropped,
			LastMessage: st.LastMessage,
			Error:       st.Error,
		}
	}
	return out
}

func toMqttSubscriptionVO(row *po.MqttSubscription) *vo.MqttSubscriptionVO {
	parserType := normalizeParserType(row.ParserType)
	var parserConfig json.RawMessage
	if parserType != parser.TypeBuiltin {
		parserConfig = safeRawJSON(row.ParserConfig)
	}
	return &vo.MqttSubscriptionVO{
		ID:           row.ID,
		Name:         row.Name,
		Description:  row.Description,
		Broker:       row.Broker,
		Port:         row.Port,
		ClientID:     row.ClientID,
		Username:     row.Username,
		Password:     row.Password,
		Topic:        row.Topic,
		QoS:          row.QoS,
		ParserType:   parserType,
		ParserConfig: parserConfig,
		MappingJSON:  safeRawJSON(row.MappingJSON),
		SampleJSON:   safeRawJSON(row.SampleJSON),
		SampleTopic:  row.SampleTopic,
		Sort:         row.Sort,
		Status:       row.Status,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
}

// safeRawJSON 把库里的文本转成合法 RawMessage。
//
// 空串/损坏的 JSON 直接返回 nil（序列化为 null），否则 encoding/json 会在
// 整个响应序列化时失败并返回空 body —— 一条坏配置不该让详情/列表接口失效。
func safeRawJSON(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s == "" || !json.Valid([]byte(s)) {
		return nil
	}
	return json.RawMessage(s)
}
