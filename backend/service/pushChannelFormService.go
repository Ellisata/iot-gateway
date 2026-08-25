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

// PushChannelFormService 数据推送通道表单服务
type PushChannelFormService struct {
	sqliteDB *gorm.DB
}

// NewPushChannelFormService 创建数据推送通道表单服务
func NewPushChannelFormService(sqliteDB *gorm.DB) *PushChannelFormService {
	return &PushChannelFormService{sqliteDB: sqliteDB}
}

// CreatePushChannelForm 创建数据推送通道表单，若名称已存在则更新 FormJSON
func (s *PushChannelFormService) CreatePushChannelForm(ctx context.Context, req *dto.CreatePushChannelFormDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().Format("2006-01-02 15:04:05")

		// 检查名称是否已存在
		var existing po.PushChannelForm
		if err := tx.Where("name = ?", req.Name).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}

		// 名称已存在，更新 FormJSON
		if existing.ID != "" {
			updates := map[string]any{
				"form_json":  string(req.FormJSON),
				"updated_at": now,
			}
			return tx.Model(&po.PushChannelForm{}).Where("id = ?", existing.ID).Updates(updates).Error
		}

		form := &po.PushChannelForm{
			Name:      req.Name,
			FormJSON:  string(req.FormJSON),
			CreatedAt: now,
			UpdatedAt: now,
		}

		return tx.Create(form).Error
	})
}

// UpdatePushChannelForm 更新数据推送通道表单
func (s *PushChannelFormService) UpdatePushChannelForm(ctx context.Context, req *dto.UpdatePushChannelFormDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 检查表单是否存在
		var existing po.PushChannelForm
		id := req.ID
		if err := tx.Where("id = ?", id).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(enums.PushChannelFormNotExistsEnum.GetCode(), enums.PushChannelFormNotExistsEnum.GetMessage(), enums.PushChannelFormNotExistsEnum.GetMsgKey())
			}
			return err
		}

		// 检查名称是否被占用（修改名称时）
		if req.Name != "" && req.Name != existing.Name {
			var dup po.PushChannelForm
			if err := tx.Where("name = ? AND id != ?", req.Name, id).First(&dup).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			if dup.ID != "" {
				return appError.NewAppErrorCtx(enums.PushChannelFormExistsEnum.GetCode(), enums.PushChannelFormExistsEnum.GetMessage(), enums.PushChannelFormExistsEnum.GetMsgKey())
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

		return tx.Model(&po.PushChannelForm{}).Where("id = ?", id).Updates(updates).Error
	})
}

// GetPushChannelFormByName 根据名称查询数据推送通道表单
func (s *PushChannelFormService) GetPushChannelFormByName(ctx context.Context, name string) (*vo.PushChannelFormVO, error) {
	var form po.PushChannelForm
	if err := s.sqliteDB.WithContext(ctx).Where("name = ?", name).First(&form).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(enums.PushChannelFormNotExistsEnum.GetCode(), enums.PushChannelFormNotExistsEnum.GetMessage(), enums.PushChannelFormNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}

	return &vo.PushChannelFormVO{
		ID:        form.ID,
		Name:      form.Name,
		FormJSON:  json.RawMessage(form.FormJSON),
		CreatedAt: form.CreatedAt,
		UpdatedAt: form.UpdatedAt,
	}, nil
}
