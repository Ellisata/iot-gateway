// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/notify"
	"iot-gateway/response"
)

// AlarmWebhookService 报警 Webhook 通知配置服务。
//
// 配置写入后立即触发通知器热刷新，使增删改无需重启即生效。
type AlarmWebhookService struct {
	sqliteDB   *gorm.DB
	dispatcher *notify.Dispatcher
}

// NewAlarmWebhookService 创建报警 Webhook 服务
func NewAlarmWebhookService(sqliteDB *gorm.DB, dispatcher *notify.Dispatcher) *AlarmWebhookService {
	return &AlarmWebhookService{sqliteDB: sqliteDB, dispatcher: dispatcher}
}

// CreateAlarmWebhook 创建报警 Webhook
func (s *AlarmWebhookService) CreateAlarmWebhook(ctx context.Context, req *dto.CreateAlarmWebhookDTO) error {
	cfg := &po.AlarmWebhook{
		Name:        req.Name,
		Type:        req.Type,
		Description: req.Description,
		URL:         req.URL,
		Secret:      req.Secret,
		MsgType:     req.MsgType,
		AtAll:       boolToInt(req.AtAll),
		AtList:      normalizeAtList(req.AtList),
	}
	if err := validateWebhookConfig(cfg); err != nil {
		return err
	}

	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.AlarmWebhook
		if err := tx.Where("name = ?", req.Name).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if existing.ID != "" {
			return appError.NewAppErrorCtx(enums.AlarmWebhookExistsEnum.GetCode(),
				enums.AlarmWebhookExistsEnum.GetMessage(), enums.AlarmWebhookExistsEnum.GetMsgKey())
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		cfg.Status = 1
		cfg.CreatedAt = now
		cfg.UpdatedAt = now
		return tx.Create(cfg).Error
	})
	if err != nil {
		return err
	}

	s.refresh()
	return nil
}

// UpdateAlarmWebhook 更新报警 Webhook（字段可选，零值不覆盖）
func (s *AlarmWebhookService) UpdateAlarmWebhook(ctx context.Context, req *dto.UpdateAlarmWebhookDTO) error {
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.AlarmWebhook
		if err := tx.Where("id = ?", req.ID).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(enums.AlarmWebhookNotExistsEnum.GetCode(),
					enums.AlarmWebhookNotExistsEnum.GetMessage(), enums.AlarmWebhookNotExistsEnum.GetMsgKey())
			}
			return err
		}

		if req.Name != "" && req.Name != existing.Name {
			var dup po.AlarmWebhook
			if err := tx.Where("name = ? AND id != ?", req.Name, req.ID).First(&dup).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			if dup.ID != "" {
				return appError.NewAppErrorCtx(enums.AlarmWebhookExistsEnum.GetCode(),
					enums.AlarmWebhookExistsEnum.GetMessage(), enums.AlarmWebhookExistsEnum.GetMsgKey())
			}
		}

		// merged 为「应用本次改动后的完整配置」，同时用于配置校验与构造更新字段，
		// 避免出现只改一半导致校验漏判（例如把类型改成 wecom 却没同时清掉 @ 配置）。
		merged := existing
		updates := make(map[string]any)
		if req.Name != "" {
			merged.Name = req.Name
			updates["name"] = req.Name
		}
		if req.Type != "" {
			merged.Type = req.Type
			updates["type"] = req.Type
		}
		if req.Description != "" {
			merged.Description = req.Description
			updates["description"] = req.Description
		}
		if req.URL != "" {
			merged.URL = req.URL
			updates["url"] = req.URL
		}
		if req.Secret != nil {
			merged.Secret = *req.Secret
			updates["secret"] = *req.Secret
		}
		if req.MsgType != "" {
			merged.MsgType = req.MsgType
			updates["msg_type"] = req.MsgType
		}
		if req.AtAll != nil {
			merged.AtAll = boolToInt(*req.AtAll)
			updates["at_all"] = merged.AtAll
		}
		if req.AtList != nil {
			merged.AtList = normalizeAtList(*req.AtList)
			updates["at_list"] = merged.AtList
		}
		if req.Status != nil {
			merged.Status = *req.Status
			updates["status"] = *req.Status
		}

		if len(updates) == 0 {
			return nil
		}
		if err := validateWebhookConfig(&merged); err != nil {
			return err
		}

		updates["updated_at"] = time.Now().Format("2006-01-02 15:04:05")
		return tx.Model(&po.AlarmWebhook{}).Where("id = ?", req.ID).Updates(updates).Error
	})
	if err != nil {
		return err
	}

	s.refresh()
	return nil
}

// DeleteAlarmWebhook 删除报警 Webhook
func (s *AlarmWebhookService) DeleteAlarmWebhook(ctx context.Context, id string) error {
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).Delete(&po.AlarmWebhook{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return appError.NewAppErrorCtx(enums.AlarmWebhookNotExistsEnum.GetCode(),
				enums.AlarmWebhookNotExistsEnum.GetMessage(), enums.AlarmWebhookNotExistsEnum.GetMsgKey())
		}
		return nil
	})
	if err != nil {
		return err
	}

	s.refresh()
	return nil
}

// GetAlarmWebhookById 根据 ID 查询报警 Webhook
func (s *AlarmWebhookService) GetAlarmWebhookById(ctx context.Context, id string) (*vo.AlarmWebhookVO, error) {
	var cfg po.AlarmWebhook
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(enums.AlarmWebhookNotExistsEnum.GetCode(),
				enums.AlarmWebhookNotExistsEnum.GetMessage(), enums.AlarmWebhookNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}
	res := toAlarmWebhookVO(cfg)
	return &res, nil
}

// PageAlarmWebhook 报警 Webhook 分页查询
func (s *AlarmWebhookService) PageAlarmWebhook(ctx context.Context, req *dto.PageAlarmWebhookDTO) (*response.PageVo[vo.AlarmWebhookVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.AlarmWebhook{})

	if req.Name != "" {
		db = db.Where("name LIKE ?", "%"+req.Name+"%")
	}
	if req.Type != "" {
		db = db.Where("type = ?", req.Type)
	}
	if req.Status != nil {
		db = db.Where("status = ?", *req.Status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var rows []po.AlarmWebhook
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	records := make([]vo.AlarmWebhookVO, len(rows))
	for i, cfg := range rows {
		records[i] = toAlarmWebhookVO(cfg)
	}

	return &response.PageVo[vo.AlarmWebhookVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}

// TestSendAlarmWebhook 按给定配置同步发送一条测试报警，不重试。
// 失败时把平台原始错误带出去，便于操作者定位（加签错、关键字不匹配等）。
func (s *AlarmWebhookService) TestSendAlarmWebhook(ctx context.Context, req *dto.TestSendAlarmWebhookDTO) error {
	cfg := po.AlarmWebhook{
		Type:    req.Type,
		URL:     req.URL,
		Secret:  req.Secret,
		MsgType: req.MsgType,
		AtList:  normalizeAtList(req.AtList),
	}
	if req.AtAll != nil {
		cfg.AtAll = boolToInt(*req.AtAll)
	}

	// 指定 ID 时以库中配置为基准，请求里填了的字段覆盖之
	//（支持「改完配置先测一下再保存」）。密钥不回显，故请求未带密钥时沿用库中的。
	if req.ID != "" {
		var existing po.AlarmWebhook
		if err := s.sqliteDB.WithContext(ctx).Where("id = ?", req.ID).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(enums.AlarmWebhookNotExistsEnum.GetCode(),
					enums.AlarmWebhookNotExistsEnum.GetMessage(), enums.AlarmWebhookNotExistsEnum.GetMsgKey())
			}
			return err
		}
		if cfg.Type != "" {
			existing.Type = cfg.Type
		}
		if cfg.URL != "" {
			existing.URL = cfg.URL
		}
		if req.Secret != "" {
			existing.Secret = req.Secret
		}
		if cfg.MsgType != "" {
			existing.MsgType = cfg.MsgType
		}
		if req.AtAll != nil {
			existing.AtAll = cfg.AtAll
		}
		if req.AtList != "" {
			existing.AtList = cfg.AtList
		}
		cfg = existing
	}

	if err := validateWebhookConfig(&cfg); err != nil {
		return err
	}

	ev := notify.BuildTestEvent(notify.TestEventOptions{
		AlarmType:  req.AlarmType,
		TargetType: req.TargetType,
		TargetName: req.TargetName,
	})
	if err := notify.SendOnce(&cfg, ev); err != nil {
		logger.Error("alarm webhook test send failed: %v", err)
		return appError.NewAppError(enums.AlarmWebhookTestSendFailEnum.GetCode(),
			enums.AlarmWebhookTestSendFailEnum.GetMessage()+": "+err.Error())
	}
	return nil
}

// StatusAlarmWebhook 通知器运行健康状况（各 webhook 队列积压与投递计数）
func (s *AlarmWebhookService) StatusAlarmWebhook() notify.DispatcherStatus {
	return s.dispatcher.GetStatus()
}

// RefreshAlarmWebhook 手动触发通知器热刷新
func (s *AlarmWebhookService) RefreshAlarmWebhook() error {
	return s.dispatcher.Refresh()
}

// ListAlarmWebhookTypes 支持的 Webhook 类型及其元信息（供界面渲染配置表单）
func (s *AlarmWebhookService) ListAlarmWebhookTypes() []notify.TypeMeta {
	return notify.SupportedTypeMetas()
}

// refresh 配置变更后尽力触发热刷新：失败不影响写入结果（10s 巡检兜底）。
func (s *AlarmWebhookService) refresh() {
	if s.dispatcher == nil {
		return
	}
	if err := s.dispatcher.Refresh(); err != nil {
		logger.Warn("alarm webhook: refresh dispatcher failed: %v", err)
	}
}

// validateWebhookConfig 校验配置合法性（类型、URL、报式格式、@ 组合）。
func validateWebhookConfig(cfg *po.AlarmWebhook) error {
	if err := notify.ValidateConfig(cfg); err != nil {
		// 用 NewAppError 而非 NewAppErrorCtx：后者会用 i18n 文案替换掉具体原因，
		// 而这里的具体原因（类型不支持 / 企微不能 @ 等）正是操作者需要的。
		return appError.NewAppError(enums.AlarmWebhookConfigInvalidEnum.GetCode(),
			enums.AlarmWebhookConfigInvalidEnum.GetMessage()+": "+err.Error())
	}
	return nil
}

// toAlarmWebhookVO PO -> VO。密钥永不回显，仅以 HasSecret 告知是否已配置。
func toAlarmWebhookVO(cfg po.AlarmWebhook) vo.AlarmWebhookVO {
	return vo.AlarmWebhookVO{
		ID:          cfg.ID,
		Name:        cfg.Name,
		Type:        cfg.Type,
		TypeName:    webhookTypeName(cfg.Type),
		Description: cfg.Description,
		URL:         cfg.URL,
		Secret:      "",
		HasSecret:   cfg.Secret != "",
		MsgType:     cfg.MsgType,
		AtAll:       cfg.AtAll == 1,
		AtList:      formatAtList(cfg.AtList),
		Status:      cfg.Status,
		CreatedAt:   cfg.CreatedAt,
		UpdatedAt:   cfg.UpdatedAt,
	}
}

// webhookTypeName Webhook 类型中文说明。
func webhookTypeName(t string) string {
	switch t {
	case notify.TypeDingTalk:
		return "钉钉群机器人"
	case notify.TypeWeCom:
		return "企业微信群机器人"
	case notify.TypeFeishu:
		return "飞书群机器人"
	case notify.TypeCustom:
		return "通用自定义 Webhook"
	}
	return t
}

// normalizeAtList 把界面传来的逗号分隔 @ 列表规整为 JSON 数组文本后入库。
// 界面用的是普通文本框，统一在服务层转成 notify 侧期望的规范形式。
func normalizeAtList(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// 中英文逗号/分号/空白都当分隔符，避免用户抄名单时的格式差异导致整串被当成一个人
	parts := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', ' ', '\t', '\n':
			return true
		}
		return false
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return ""
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}

// formatAtList 把库中的 JSON 数组文本还原为逗号分隔字符串（供界面回显编辑）。
func formatAtList(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var list []string
	if err := json.Unmarshal([]byte(s), &list); err != nil {
		return s
	}
	return strings.Join(list, ",")
}

// boolToInt 布尔转数据库整型标志位。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
