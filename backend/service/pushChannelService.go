// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/push"
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
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 检查名称是否已存在
		var existing po.PushChannel
		if err := tx.Where("name = ?", req.Name).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if existing.ID != "" {
			return appError.NewAppErrorCtx(enums.PushChannelExistsEnum.GetCode(), enums.PushChannelExistsEnum.GetMessage(), enums.PushChannelExistsEnum.GetMsgKey())
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		channel := &po.PushChannel{
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

		// 检查名称是否被占用（修改名称时）
		if req.Name != "" && req.Name != existing.Name {
			var dup po.PushChannel
			if err := tx.Where("name = ? AND id != ?", req.Name, id).First(&dup).Error; err != nil {
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
	if err := push.TestChannelConnectivity(s.sqliteDB, req.Name, string(req.ConfigJSON)); err != nil {
		logger.Error("%s", err.Error())
		return appError.NewAppError(enums.PushChannelConnectFailEnum.GetCode(), "连接失败")
	}
	return nil
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
