// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/collector"
	"iot-gateway/enums"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
	"iot-gateway/subscribe"
	"iot-gateway/subscribe/mapping"
)

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
	row := &po.MqttSubscription{
		Name:        req.Name,
		Description: req.Description,
		Broker:      req.Broker,
		Port:        req.Port,
		ClientID:    req.ClientID,
		Username:    req.Username,
		Password:    req.Password,
		Topic:       req.Topic,
		QoS:         req.QoS,
		MappingJSON: string(req.MappingJSON),
		SampleJSON:  string(req.SampleJSON),
		Sort:        req.Sort,
		Status:      status,
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

	// mappingJson 为空表示沿用原规则（列表页只改启停时不必回传整份规则）。
	mappingJSON := string(req.MappingJSON)
	if mappingJSON == "" {
		mappingJSON = existing.MappingJSON
	}
	candidate := po.MqttSubscription{
		Name:        req.Name,
		Broker:      req.Broker,
		Port:        req.Port,
		ClientID:    req.ClientID,
		Username:    req.Username,
		Password:    req.Password,
		Topic:       req.Topic,
		QoS:         req.QoS,
		MappingJSON: mappingJSON,
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

		updates := map[string]any{
			"name":         req.Name,
			"description":  req.Description,
			"broker":       req.Broker,
			"port":         req.Port,
			"client_id":    req.ClientID,
			"username":     req.Username,
			"password":     req.Password,
			"topic":        req.Topic,
			"qos":          req.QoS,
			"mapping_json": mappingJSON,
			"sample_json":  string(req.SampleJSON),
			"sort":         req.Sort,
			"updated_at":   time.Now().Format("2006-01-02 15:04:05"),
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
// 该表是身份索引，不含采样值；按设备 ID + 点位 ID 自然键稳定排序，
// 避免 last_seen_at 每分钟刷新导致列表行来回跳动。
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
	if req.DeviceAddressName != "" {
		db = db.Where("device_address_name LIKE ?", "%"+req.DeviceAddressName+"%")
	}
	if req.Protocol != "" {
		db = db.Where("protocol LIKE ?", "%"+req.Protocol+"%")
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
			DeviceAddressName:  r.DeviceAddressName,
			DeviceAddressLabel: r.DeviceAddressLabel,
			DataType:           r.DataType,
			Kind:               r.Kind,
			Protocol:           r.Protocol,
			FirstSeenAt:        r.FirstSeenAt,
			LastSeenAt:         r.LastSeenAt,
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

// PreviewMqttSubscription 用尚未保存的映射规则 + 样例报文试跑转换。
//
// 转换失败不是 AppError：配置写错是编辑过程中的正常反馈，由前端直接展示。
func (s *MqttSubscriptionService) PreviewMqttSubscription(req *dto.PreviewMqttSubscriptionDTO) *vo.MqttSubscriptionPreviewVO {
	result := &vo.MqttSubscriptionPreviewVO{Records: []collector.CollectedRecord{}}

	rule, err := mapping.Parse(req.MappingJSON)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	res, err := rule.Convert(req.SampleJSON)
	if err != nil {
		result.Error = err.Error()
		result.Skipped = res.Skipped
		return result
	}
	result.Records = res.Records
	result.Count = len(res.Records)
	result.Skipped = res.Skipped
	if res.FirstError != nil {
		result.Error = res.FirstError.Error()
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
			LastMessage: st.LastMessage,
			Error:       st.Error,
		}
	}
	return out
}

func toMqttSubscriptionVO(row *po.MqttSubscription) *vo.MqttSubscriptionVO {
	return &vo.MqttSubscriptionVO{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Broker:      row.Broker,
		Port:        row.Port,
		ClientID:    row.ClientID,
		Username:    row.Username,
		Password:    row.Password,
		Topic:       row.Topic,
		QoS:         row.QoS,
		MappingJSON: json.RawMessage(row.MappingJSON),
		SampleJSON:  json.RawMessage(row.SampleJSON),
		Sort:        row.Sort,
		Status:      row.Status,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}
