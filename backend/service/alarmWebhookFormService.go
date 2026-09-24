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
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
)

// AlarmWebhookFormService 报警 Webhook 表单服务（表单结构由数据驱动，后端不感知字段）。
type AlarmWebhookFormService struct {
	sqliteDB *gorm.DB
}

// NewAlarmWebhookFormService 创建报警 Webhook 表单服务
func NewAlarmWebhookFormService(sqliteDB *gorm.DB) *AlarmWebhookFormService {
	return &AlarmWebhookFormService{sqliteDB: sqliteDB}
}

// CreateAlarmWebhookForm 创建报警 Webhook 表单，名称已存在时更新其 FormJSON
func (s *AlarmWebhookFormService) CreateAlarmWebhookForm(ctx context.Context, req *dto.CreateAlarmWebhookFormDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Format("2006-01-02 15:04:05")

		var existing po.AlarmWebhookForm
		if err := tx.Where("name = ?", req.Name).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}

		if existing.ID != "" {
			return tx.Model(&po.AlarmWebhookForm{}).Where("id = ?", existing.ID).
				Updates(map[string]any{
					"form_json":  string(req.FormJSON),
					"updated_at": now,
				}).Error
		}

		form := &po.AlarmWebhookForm{
			Name:      req.Name,
			FormJSON:  string(req.FormJSON),
			CreatedAt: now,
			UpdatedAt: now,
		}
		return tx.Create(form).Error
	})
}

// UpdateAlarmWebhookForm 更新报警 Webhook 表单
func (s *AlarmWebhookFormService) UpdateAlarmWebhookForm(ctx context.Context, req *dto.UpdateAlarmWebhookFormDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.AlarmWebhookForm
		if err := tx.Where("id = ?", req.ID).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(enums.AlarmWebhookFormNotExistsEnum.GetCode(),
					enums.AlarmWebhookFormNotExistsEnum.GetMessage(), enums.AlarmWebhookFormNotExistsEnum.GetMsgKey())
			}
			return err
		}

		if req.Name != "" && req.Name != existing.Name {
			var dup po.AlarmWebhookForm
			if err := tx.Where("name = ? AND id != ?", req.Name, req.ID).First(&dup).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			if dup.ID != "" {
				return appError.NewAppErrorCtx(enums.AlarmWebhookFormExistsEnum.GetCode(),
					enums.AlarmWebhookFormExistsEnum.GetMessage(), enums.AlarmWebhookFormExistsEnum.GetMsgKey())
			}
		}

		updates := make(map[string]any)
		if req.Name != "" {
			updates["name"] = req.Name
		}
		if len(req.FormJSON) > 0 {
			updates["form_json"] = string(req.FormJSON)
		}
		if len(updates) == 0 {
			return nil
		}
		updates["updated_at"] = time.Now().Format("2006-01-02 15:04:05")

		return tx.Model(&po.AlarmWebhookForm{}).Where("id = ?", req.ID).Updates(updates).Error
	})
}

// GetAlarmWebhookFormByName 根据名称查询报警 Webhook 表单（名称即 webhook 类型名）
func (s *AlarmWebhookFormService) GetAlarmWebhookFormByName(ctx context.Context, name string) (*vo.AlarmWebhookFormVO, error) {
	var form po.AlarmWebhookForm
	if err := s.sqliteDB.WithContext(ctx).Where("name = ?", name).First(&form).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(enums.AlarmWebhookFormNotExistsEnum.GetCode(),
				enums.AlarmWebhookFormNotExistsEnum.GetMessage(), enums.AlarmWebhookFormNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}

	return &vo.AlarmWebhookFormVO{
		ID:        form.ID,
		Name:      form.Name,
		FormJSON:  json.RawMessage(form.FormJSON),
		CreatedAt: form.CreatedAt,
		UpdatedAt: form.UpdatedAt,
	}, nil
}
