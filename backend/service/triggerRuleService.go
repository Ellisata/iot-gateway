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
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
	"iot-gateway/triggerrule"
)

// TriggerRuleReloader 规则引擎的热加载入口（生产实现为 *triggerrule.Engine）。
type TriggerRuleReloader interface {
	Refresh() error
}

// TriggerRuleLiveReader 规则引擎的实时观测入口（生产实现为 *triggerrule.Engine）。
//
// 只读内存环形缓冲：不落库、不阻塞采集，进程重启即清空。持久化历史仍走
// trigger_rule_record / PageTriggerRuleRecord。
type TriggerRuleLiveReader interface {
	RecentTriggers(ruleID string, limit int) []triggerrule.RecentTrigger
}

// TriggerRuleService 事件触发采集规则服务。
type TriggerRuleService struct {
	sqliteDB *gorm.DB
	reloader TriggerRuleReloader
	live     TriggerRuleLiveReader
}

// NewTriggerRuleService 创建规则服务。
func NewTriggerRuleService(sqliteDB *gorm.DB, reloader TriggerRuleReloader,
	live TriggerRuleLiveReader) *TriggerRuleService {
	return &TriggerRuleService{sqliteDB: sqliteDB, reloader: reloader, live: live}
}

const (
	triggerRuleMaxNameRunes = 64
	triggerRuleMaxAction    = 200
	triggerRuleMaxCooldown  = 86400000
	triggerRuleMinTimeout   = 100
	triggerRuleMaxTimeout   = 60000
	triggerRuleDefaultTout  = 5000
)

func triggerRuleNotExistsErr() error {
	return appError.NewAppErrorCtx(enums.TriggerRuleNotExistsEnum.GetCode(),
		enums.TriggerRuleNotExistsEnum.GetMessage(), enums.TriggerRuleNotExistsEnum.GetMsgKey())
}

func triggerRuleExistsErr() error {
	return appError.NewAppErrorCtx(enums.TriggerRuleExistsEnum.GetCode(),
		enums.TriggerRuleExistsEnum.GetMessage(), enums.TriggerRuleExistsEnum.GetMsgKey())
}

func triggerRuleNameInvalidErr() error {
	return appError.NewAppErrorCtx(enums.TriggerRuleNameInvalidEnum.GetCode(),
		enums.TriggerRuleNameInvalidEnum.GetMessage(), enums.TriggerRuleNameInvalidEnum.GetMsgKey())
}

func triggerRuleConditionInvalidErr(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return appError.NewAppErrorCtx(enums.TriggerRuleConditionInvalidEnum.GetCode(), msg,
		enums.TriggerRuleConditionInvalidEnum.GetMsgKey())
}

func triggerRuleAddressNotFoundErr() error {
	return appError.NewAppErrorCtx(enums.TriggerRuleAddressNotFoundEnum.GetCode(),
		enums.TriggerRuleAddressNotFoundEnum.GetMessage(), enums.TriggerRuleAddressNotFoundEnum.GetMsgKey())
}

func triggerRuleDeviceNotExistsErr() error {
	return appError.NewAppErrorCtx(enums.TriggerRuleDeviceNotExistsEnum.GetCode(),
		enums.TriggerRuleDeviceNotExistsEnum.GetMessage(), enums.TriggerRuleDeviceNotExistsEnum.GetMsgKey())
}

func triggerRuleActionDeviceMismatchErr() error {
	return appError.NewAppErrorCtx(enums.TriggerRuleActionDeviceMismatchEnum.GetCode(),
		enums.TriggerRuleActionDeviceMismatchEnum.GetMessage(), enums.TriggerRuleActionDeviceMismatchEnum.GetMsgKey())
}

func triggerRuleActionNotReadableErr() error {
	return appError.NewAppErrorCtx(enums.TriggerRuleActionAddressNotReadableEnum.GetCode(),
		enums.TriggerRuleActionAddressNotReadableEnum.GetMessage(), enums.TriggerRuleActionAddressNotReadableEnum.GetMsgKey())
}

func triggerRuleChannelNotExistsErr() error {
	return appError.NewAppErrorCtx(enums.TriggerRuleChannelNotExistsEnum.GetCode(),
		enums.TriggerRuleChannelNotExistsEnum.GetMessage(), enums.TriggerRuleChannelNotExistsEnum.GetMsgKey())
}

func triggerRuleCodeInvalidErr() error {
	return appError.NewAppErrorCtx(enums.TriggerRuleCodeInvalidEnum.GetCode(),
		enums.TriggerRuleCodeInvalidEnum.GetMessage(), enums.TriggerRuleCodeInvalidEnum.GetMsgKey())
}

func triggerRuleCodeExistsErr() error {
	return appError.NewAppErrorCtx(enums.TriggerRuleCodeExistsEnum.GetCode(),
		enums.TriggerRuleCodeExistsEnum.GetMessage(), enums.TriggerRuleCodeExistsEnum.GetMsgKey())
}

func triggerRuleCodeImmutableErr() error {
	return appError.NewAppErrorCtx(enums.TriggerRuleCodeImmutableEnum.GetCode(),
		enums.TriggerRuleCodeImmutableEnum.GetMessage(), enums.TriggerRuleCodeImmutableEnum.GetMsgKey())
}

// normalizedTriggerRule 校验通过后的规范值。
type normalizedTriggerRule struct {
	code        string
	name        string
	description string
	status      int

	actionDeviceID string
	actionIDsJSON  string

	targetChannelID string
	conditionsJSON  string

	cooldownMs int
	timeoutMs  int
}

// CreateTriggerRule 创建规则。
func (s *TriggerRuleService) CreateTriggerRule(ctx context.Context, req *dto.CreateTriggerRuleDTO) error {
	db := s.sqliteDB.WithContext(ctx)
	n, err := s.validateConfig(ctx, db, &req.TriggerRuleConfigDTO, "", 1, "")
	if err != nil {
		return err
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	rule := &po.TriggerRule{
		Code:             n.code,
		Name:             n.name,
		Description:      n.description,
		Status:           n.status,
		ActionDeviceID:   n.actionDeviceID,
		ActionAddressIDs: n.actionIDsJSON,
		TargetChannelID:  n.targetChannelID,
		ConditionsJSON:   n.conditionsJSON,
		CooldownMs:       n.cooldownMs,
		TimeoutMs:        n.timeoutMs,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := db.Create(rule).Error; err != nil {
		return err
	}
	// GORM 的 default:1 会把显式 0 当成"未设置"。创建停用规则时补一次状态，
	// 否则用户点"停用创建"却得到一条启用规则。
	if n.status == 0 {
		if err := db.Model(&po.TriggerRule{}).Where("id = ?", rule.ID).Update("status", 0).Error; err != nil {
			return err
		}
	}
	s.refresh()
	return nil
}

// UpdateTriggerRule 更新规则（整份覆盖；status 省略时保留原状态）。
func (s *TriggerRuleService) UpdateTriggerRule(ctx context.Context, req *dto.UpdateTriggerRuleDTO) error {
	db := s.sqliteDB.WithContext(ctx)

	var existing po.TriggerRule
	if err := db.Where("id = ?", req.ID).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return triggerRuleNotExistsErr()
		}
		return err
	}

	n, err := s.validateConfig(ctx, db, &req.TriggerRuleConfigDTO, req.ID, existing.Status, existing.Code)
	if err != nil {
		return err
	}

	updates := map[string]interface{}{
		"code":               n.code,
		"name":               n.name,
		"description":        n.description,
		"status":             n.status,
		"action_device_id":   n.actionDeviceID,
		"action_address_ids": n.actionIDsJSON,
		"target_channel_id":  n.targetChannelID,
		"conditions_json":    n.conditionsJSON,
		"cooldown_ms":        n.cooldownMs,
		"timeout_ms":         n.timeoutMs,
		"updated_at":         time.Now().Format("2006-01-02 15:04:05"),
	}
	if err := db.Model(&po.TriggerRule{}).Where("id = ?", req.ID).Updates(updates).Error; err != nil {
		return err
	}
	s.refresh()
	return nil
}

// DeleteTriggerRule 删除规则；历史记录保留。
func (s *TriggerRuleService) DeleteTriggerRule(ctx context.Context, id string) error {
	db := s.sqliteDB.WithContext(ctx)
	var existing po.TriggerRule
	if err := db.Where("id = ?", id).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return triggerRuleNotExistsErr()
		}
		return err
	}
	if err := db.Where("id = ?", id).Delete(&po.TriggerRule{}).Error; err != nil {
		return err
	}
	s.refresh()
	return nil
}

func (s *TriggerRuleService) refresh() {
	if s.reloader == nil {
		return
	}
	if err := s.reloader.Refresh(); err != nil {
		// 保存已经成功；热加载失败由 10s watcher 兜底，不能把保存误报成失败。
		logger.Warn("triggerRule: refresh rule engine failed: %v", err)
	}
}

// validateConfig 校验并规范化创建/更新配置。
//
// excludeID：更新时排除自身做名称/编码唯一检查；create 传空串。
// defaultStatus：status 省略时使用（更新时取原状态）。
// existingCode：更新时传库中编码；空串 = 新建（或历史空编码补录）。
// 编码创建后不可修改；更新时留空表示保持原编码。
func (s *TriggerRuleService) validateConfig(ctx context.Context, db *gorm.DB,
	cfg *dto.TriggerRuleConfigDTO, excludeID string, defaultStatus int,
	existingCode string) (*normalizedTriggerRule, error) {

	code := strings.TrimSpace(cfg.Code)
	switch {
	case existingCode == "":
		if code == "" || !validateEntityCode(code) {
			return nil, triggerRuleCodeInvalidErr()
		}
	case code == "":
		code = existingCode
	case code != existingCode:
		return nil, triggerRuleCodeImmutableErr()
	}
	if code != existingCode {
		var codeDup po.TriggerRule
		q := db.Where("code = ?", code)
		if excludeID != "" {
			q = q.Where("id != ?", excludeID)
		}
		if err := q.First(&codeDup).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if codeDup.ID != "" {
			return nil, triggerRuleCodeExistsErr()
		}
	}

	name := strings.TrimSpace(cfg.Name)
	if !validateTriggerRuleName(name) {
		return nil, triggerRuleNameInvalidErr()
	}
	var dup po.TriggerRule
	q := db.Where("name = ?", name)
	if excludeID != "" {
		q = q.Where("id != ?", excludeID)
	}
	if err := q.First(&dup).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if dup.ID != "" {
		return nil, triggerRuleExistsErr()
	}

	var device po.Device
	if err := db.Where("id = ?", cfg.ActionDeviceID).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, triggerRuleDeviceNotExistsErr()
		}
		return nil, err
	}
	if device.Status != 1 {
		return nil, triggerRuleDeviceNotExistsErr()
	}

	actionIDs, err := normalizeIDList(cfg.ActionAddressIDs, triggerRuleMaxAction)
	if err != nil {
		return nil, err
	}
	actionAddrs, err := s.loadAddresses(ctx, db, actionIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range actionIDs {
		a, ok := actionAddrs[id]
		if !ok || a.Status != 1 {
			return nil, triggerRuleAddressNotFoundErr()
		}
		if a.DeviceID != cfg.ActionDeviceID {
			return nil, triggerRuleActionDeviceMismatchErr()
		}
		if !strings.Contains(strings.ToUpper(a.RwPermission), "R") {
			return nil, triggerRuleActionNotReadableErr()
		}
	}

	var channel po.PushChannel
	if err := db.Where("id = ?", cfg.TargetChannelID).First(&channel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, triggerRuleChannelNotExistsErr()
		}
		return nil, err
	}
	if channel.Status != 1 {
		return nil, triggerRuleChannelNotExistsErr()
	}

	conds, err := triggerrule.ParseConditions(cfg.ConditionsJSON)
	if err != nil {
		return nil, triggerRuleConditionInvalidErr("%s", err.Error())
	}
	if err := s.validateConditions(ctx, db, conds); err != nil {
		return nil, err
	}

	cooldown := cfg.CooldownMs
	if cooldown < 0 || cooldown > triggerRuleMaxCooldown {
		return nil, triggerRuleConditionInvalidErr("冷却时间必须在 0-%d 毫秒之间", triggerRuleMaxCooldown)
	}
	timeout := cfg.TimeoutMs
	if timeout == 0 {
		timeout = triggerRuleDefaultTout
	}
	if timeout < triggerRuleMinTimeout || timeout > triggerRuleMaxTimeout {
		return nil, triggerRuleConditionInvalidErr("超时时间必须在 %d-%d 毫秒之间",
			triggerRuleMinTimeout, triggerRuleMaxTimeout)
	}

	status := defaultStatus
	if cfg.Status != nil {
		status = *cfg.Status
	}

	actionJSON, err := json.Marshal(actionIDs)
	if err != nil {
		return nil, err
	}
	condJSON, err := json.Marshal(conds)
	if err != nil {
		return nil, err
	}

	return &normalizedTriggerRule{
		code:            code,
		name:            name,
		description:     cfg.Description,
		status:          status,
		actionDeviceID:  cfg.ActionDeviceID,
		actionIDsJSON:   string(actionJSON),
		targetChannelID: cfg.TargetChannelID,
		conditionsJSON:  string(condJSON),
		cooldownMs:      cooldown,
		timeoutMs:       timeout,
	}, nil
}

func validateTriggerRuleName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > triggerRuleMaxNameRunes {
		return false
	}
	for _, r := range name {
		if r == 0 || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func normalizeIDList(ids []string, max int) ([]string, error) {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, triggerRuleAddressNotFoundErr()
	}
	if len(out) > max {
		return nil, triggerRuleConditionInvalidErr("动作点位不能超过 %d 个", max)
	}
	return out, nil
}

// validateConditions 校验每条条件的点位可采集、Kind 可比较、算子/操作数合法。
//
// 这里刻意复用 deviceAddressService 的 validateAddressThreshold 与
// kindOfAddressType：比较符矩阵与操作数规则只有一份真源。
func (s *TriggerRuleService) validateConditions(ctx context.Context, db *gorm.DB, conds triggerrule.Conditions) error {
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, g := range conds.Groups {
		for _, c := range g.Conditions {
			if _, ok := seen[c.AddressID]; !ok {
				seen[c.AddressID] = struct{}{}
				ids = append(ids, c.AddressID)
			}
		}
	}
	addrs, err := s.loadAddresses(ctx, db, ids)
	if err != nil {
		return err
	}
	devices, err := s.loadDevices(ctx, db, addrs)
	if err != nil {
		return err
	}
	protocols, err := s.loadProtocolNames(ctx, db, devices)
	if err != nil {
		return err
	}
	props, err := s.loadPropertyAccessModes(ctx, db, addrs)
	if err != nil {
		return err
	}
	groups, err := s.loadGroups(ctx, db, addrs)
	if err != nil {
		return err
	}

	for _, g := range conds.Groups {
		for _, c := range g.Conditions {
			a, ok := addrs[c.AddressID]
			if !ok || a.Status != 1 {
				return triggerRuleAddressNotFoundErr()
			}
			dev, ok := devices[a.DeviceID]
			if !ok || dev.Status != 1 {
				return triggerRuleDeviceNotExistsErr()
			}
			if !strings.Contains(strings.ToUpper(a.RwPermission), "R") {
				return triggerRuleConditionInvalidErr("点位「%s」没有读权限，不会被采集", a.Name)
			}
			if a.PropertyID != "" {
				if mode, ok := props[a.PropertyID]; ok && !strings.Contains(strings.ToUpper(mode), "R") {
					return triggerRuleConditionInvalidErr("点位「%s」绑定的物模型属性为只写，不会被采集", a.Name)
				}
			}
			if a.GroupID != "" {
				grp, ok := groups[a.GroupID]
				if !ok || grp.Status != 1 || grp.DeviceID != a.DeviceID {
					return triggerRuleConditionInvalidErr("点位「%s」绑定的采集组不可用，不会被采集", a.Name)
				}
			}
			kind := kindOfAddressType(protocols[dev.ProtocolID], a.DataType)
			if kind == "" {
				return triggerRuleConditionInvalidErr("点位「%s」的数据类型无法识别", a.Name)
			}
			if kind == driver.KindTime {
				return triggerRuleConditionInvalidErr("点位「%s」是时间类型，不支持触发条件", a.Name)
			}
			if err := validateAddressThreshold(true, c.Op, c.Values, kind); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *TriggerRuleService) loadAddresses(ctx context.Context, db *gorm.DB, ids []string) (map[string]po.DeviceAddress, error) {
	out := make(map[string]po.DeviceAddress, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []po.DeviceAddress
	if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, a := range rows {
		out[a.ID] = a
	}
	return out, nil
}

func (s *TriggerRuleService) loadDevices(ctx context.Context, db *gorm.DB, addrs map[string]po.DeviceAddress) (map[string]po.Device, error) {
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, a := range addrs {
		if _, ok := seen[a.DeviceID]; ok {
			continue
		}
		seen[a.DeviceID] = struct{}{}
		ids = append(ids, a.DeviceID)
	}
	out := make(map[string]po.Device, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []po.Device
	if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, d := range rows {
		out[d.ID] = d
	}
	return out, nil
}

func (s *TriggerRuleService) loadProtocolNames(ctx context.Context, db *gorm.DB, devices map[string]po.Device) (map[string]string, error) {
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, d := range devices {
		if d.ProtocolID == "" {
			continue
		}
		if _, ok := seen[d.ProtocolID]; ok {
			continue
		}
		seen[d.ProtocolID] = struct{}{}
		ids = append(ids, d.ProtocolID)
	}
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []po.IotProtocol
	if err := db.Select("id", "name").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, p := range rows {
		out[p.ID] = p.Name
	}
	return out, nil
}

func (s *TriggerRuleService) loadPropertyAccessModes(ctx context.Context, db *gorm.DB, addrs map[string]po.DeviceAddress) (map[string]string, error) {
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, a := range addrs {
		if a.PropertyID == "" {
			continue
		}
		if _, ok := seen[a.PropertyID]; ok {
			continue
		}
		seen[a.PropertyID] = struct{}{}
		ids = append(ids, a.PropertyID)
	}
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []po.ThingModelProperty
	if err := db.Select("id", "access_mode").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, p := range rows {
		out[p.ID] = p.AccessMode
	}
	return out, nil
}

func (s *TriggerRuleService) loadGroups(ctx context.Context, db *gorm.DB, addrs map[string]po.DeviceAddress) (map[string]po.CollectorGroup, error) {
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, a := range addrs {
		if a.GroupID == "" {
			continue
		}
		if _, ok := seen[a.GroupID]; ok {
			continue
		}
		seen[a.GroupID] = struct{}{}
		ids = append(ids, a.GroupID)
	}
	out := make(map[string]po.CollectorGroup, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []po.CollectorGroup
	if err := db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, g := range rows {
		out[g.ID] = g
	}
	return out, nil
}

// GetTriggerRuleByID 查询规则详情。
func (s *TriggerRuleService) GetTriggerRuleByID(ctx context.Context, id string) (*vo.TriggerRuleVO, error) {
	var rule po.TriggerRule
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&rule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, triggerRuleNotExistsErr()
		}
		return nil, err
	}
	records, err := s.enrichTriggerRuleVOs(ctx, []po.TriggerRule{rule})
	if err != nil {
		return nil, err
	}
	return &records[0], nil
}

// PageTriggerRule 规则分页。
func (s *TriggerRuleService) PageTriggerRule(ctx context.Context, req *dto.PageTriggerRuleDTO) (*response.PageVo[vo.TriggerRuleVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.TriggerRule{})
	if req.Code != "" {
		db = db.Where("code LIKE ?", "%"+strings.TrimSpace(req.Code)+"%")
	}
	if req.Name != "" {
		db = db.Where("name LIKE ?", "%"+strings.TrimSpace(req.Name)+"%")
	}
	if req.Status != nil {
		db = db.Where("status = ?", *req.Status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []po.TriggerRule
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).
		Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	records, err := s.enrichTriggerRuleVOs(ctx, rows)
	if err != nil {
		return nil, err
	}
	return &response.PageVo[vo.TriggerRuleVO]{
		Page: req.Page, Size: req.Size, Total: total, Records: records,
	}, nil
}

// PageTriggerRuleRecord 触发历史分页。
func (s *TriggerRuleService) PageTriggerRuleRecord(ctx context.Context, req *dto.PageTriggerRuleRecordDTO) (*response.PageVo[vo.TriggerRuleRecordVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.TriggerRuleRecord{})
	if req.RuleID != "" {
		db = db.Where("rule_id = ?", req.RuleID)
	}
	if req.RuleCode != "" {
		db = db.Where("rule_code LIKE ?", "%"+strings.TrimSpace(req.RuleCode)+"%")
	}
	if req.RuleName != "" {
		db = db.Where("rule_name LIKE ?", "%"+strings.TrimSpace(req.RuleName)+"%")
	}
	if req.Status != "" {
		db = db.Where("status = ?", req.Status)
	}
	if req.StartTime != "" {
		db = db.Where("fired_at >= ?", req.StartTime)
	}
	if req.EndTime != "" {
		db = db.Where("fired_at <= ?", req.EndTime)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	var rows []po.TriggerRuleRecord
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).
		Order("fired_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	records := make([]vo.TriggerRuleRecordVO, len(rows))
	for i, r := range rows {
		records[i] = vo.TriggerRuleRecordVO{
			ID:               r.ID,
			TriggerID:        r.TriggerID,
			RuleID:           r.RuleID,
			RuleCode:         r.RuleCode,
			RuleName:         r.RuleName,
			FiredAt:          r.FiredAt,
			Status:           r.Status,
			ConditionJSON:    r.ConditionJSON,
			ActionResultJSON: r.ActionResultJSON,
			DurationMs:       r.DurationMs,
			Error:            r.Error,
			CreatedAt:        r.CreatedAt,
		}
	}
	return &response.PageVo[vo.TriggerRuleRecordVO]{
		Page: req.Page, Size: req.Size, Total: total, Records: records,
	}, nil
}

// RecentTriggers 最近结束的触发（新→旧，仅内存）。
//
// 设备名/通道名在读取时按 ID 现查，规则改名/通道改名后立即反映；事件本身是
// 触发时刻的快照（规则名、点位值、错误），不会因后续改配置而变。
func (s *TriggerRuleService) RecentTriggers(ctx context.Context, ruleID string,
	limit int) []vo.TriggerRecentVO {
	out := []vo.TriggerRecentVO{}
	if s.live == nil {
		return out
	}
	if limit <= 0 || limit > triggerrule.RecentTriggerRingSize {
		limit = triggerrule.RecentTriggerRingSize
	}
	events := s.live.RecentTriggers(ruleID, limit)
	if len(events) == 0 {
		return out
	}

	channelIDs := make([]string, 0, len(events))
	deviceIDs := make([]string, 0, len(events))
	seenChannel := make(map[string]struct{}, len(events))
	seenDevice := make(map[string]struct{}, len(events))
	for _, ev := range events {
		if ev.ChannelID != "" {
			if _, ok := seenChannel[ev.ChannelID]; !ok {
				seenChannel[ev.ChannelID] = struct{}{}
				channelIDs = append(channelIDs, ev.ChannelID)
			}
		}
		if ev.ActionDeviceID != "" {
			if _, ok := seenDevice[ev.ActionDeviceID]; !ok {
				seenDevice[ev.ActionDeviceID] = struct{}{}
				deviceIDs = append(deviceIDs, ev.ActionDeviceID)
			}
		}
	}
	channelNames := make(map[string]string, len(channelIDs))
	if len(channelIDs) > 0 {
		var channels []po.PushChannel
		if err := s.sqliteDB.WithContext(ctx).Select("id", "name").
			Where("id IN ?", channelIDs).Find(&channels).Error; err == nil {
			for _, c := range channels {
				channelNames[c.ID] = c.Name
			}
		}
	}
	deviceNames := make(map[string]string, len(deviceIDs))
	if len(deviceIDs) > 0 {
		var devices []po.Device
		if err := s.sqliteDB.WithContext(ctx).Select("id", "name").
			Where("id IN ?", deviceIDs).Find(&devices).Error; err == nil {
			for _, d := range devices {
				deviceNames[d.ID] = d.Name
			}
		}
	}

	out = make([]vo.TriggerRecentVO, 0, len(events))
	for _, ev := range events {
		item := vo.TriggerRecentVO{
			Seq:              ev.Seq,
			TriggerID:        ev.TriggerID,
			RuleID:           ev.RuleID,
			RuleCode:         ev.RuleCode,
			RuleName:         ev.RuleName,
			Status:           ev.Status,
			DurationMs:       ev.DurationMs,
			Error:            ev.Error,
			ActionDeviceID:   ev.ActionDeviceID,
			ActionDeviceName: deviceNames[ev.ActionDeviceID],
			ChannelID:        ev.ChannelID,
			ChannelName:      channelNames[ev.ChannelID],
			Records:          make([]vo.TriggerRecentRecordVO, 0, len(ev.Records)),
		}
		if !ev.FiredAt.IsZero() {
			item.FiredAt = ev.FiredAt.Format("2006-01-02 15:04:05.000")
		}
		for _, r := range ev.Records {
			item.Records = append(item.Records, vo.TriggerRecentRecordVO{
				AddressID:   r.AddressID,
				AddressName: r.AddressName,
				Value:       r.Value,
				Kind:        r.Kind,
				Quality:     r.Quality,
			})
		}
		out = append(out, item)
	}
	return out
}

// enrichTriggerRuleVOs 批量补全设备名、通道名与引用点位（含 Kind），避免逐行查询。
func (s *TriggerRuleService) enrichTriggerRuleVOs(ctx context.Context, rules []po.TriggerRule) ([]vo.TriggerRuleVO, error) {
	out := make([]vo.TriggerRuleVO, len(rules))
	if len(rules) == 0 {
		return out, nil
	}
	db := s.sqliteDB.WithContext(ctx)

	channelIDs := make([]string, 0)
	seenChannel := make(map[string]struct{})
	allAddrIDs := make(map[string]struct{})
	for _, r := range rules {
		if _, ok := seenChannel[r.TargetChannelID]; !ok && r.TargetChannelID != "" {
			seenChannel[r.TargetChannelID] = struct{}{}
			channelIDs = append(channelIDs, r.TargetChannelID)
		}
		var actionIDs []string
		_ = json.Unmarshal([]byte(r.ActionAddressIDs), &actionIDs)
		for _, id := range actionIDs {
			allAddrIDs[id] = struct{}{}
		}
		if conds, err := triggerrule.ParseConditions([]byte(r.ConditionsJSON)); err == nil {
			for _, g := range conds.Groups {
				for _, c := range g.Conditions {
					allAddrIDs[c.AddressID] = struct{}{}
				}
			}
		}
	}
	addrIDs := make([]string, 0, len(allAddrIDs))
	for id := range allAddrIDs {
		addrIDs = append(addrIDs, id)
	}
	addrs, err := s.loadAddresses(ctx, db, addrIDs)
	if err != nil {
		return nil, err
	}
	devices, err := s.loadDevices(ctx, db, addrs)
	if err != nil {
		return nil, err
	}
	protocols, err := s.loadProtocolNames(ctx, db, devices)
	if err != nil {
		return nil, err
	}
	channelNames := make(map[string]string, len(channelIDs))
	if len(channelIDs) > 0 {
		var channels []po.PushChannel
		if err := db.Select("id", "name").Where("id IN ?", channelIDs).Find(&channels).Error; err != nil {
			return nil, err
		}
		for _, c := range channels {
			channelNames[c.ID] = c.Name
		}
	}

	addrVO := func(id string) vo.TriggerRuleAddressVO {
		a, ok := addrs[id]
		if !ok {
			return vo.TriggerRuleAddressVO{ID: id}
		}
		dev := devices[a.DeviceID]
		return vo.TriggerRuleAddressVO{
			ID:           a.ID,
			DeviceID:     a.DeviceID,
			DeviceName:   dev.Name,
			ProtocolName: protocols[dev.ProtocolID],
			Name:         a.Name,
			Label:        a.Label,
			DataType:     a.DataType,
			Kind:         kindOfAddressType(protocols[dev.ProtocolID], a.DataType),
			RwPermission: a.RwPermission,
		}
	}

	for i, r := range rules {
		var actionIDs []string
		_ = json.Unmarshal([]byte(r.ActionAddressIDs), &actionIDs)
		actions := make([]vo.TriggerRuleAddressVO, 0, len(actionIDs))
		for _, id := range actionIDs {
			actions = append(actions, addrVO(id))
		}

		condAddresses := make([]vo.TriggerRuleAddressVO, 0)
		condCount := 0
		var condRaw json.RawMessage
		if conds, err := triggerrule.ParseConditions([]byte(r.ConditionsJSON)); err == nil {
			condRaw = []byte(r.ConditionsJSON)
			seen := make(map[string]struct{})
			for _, g := range conds.Groups {
				for _, c := range g.Conditions {
					condCount++
					if _, ok := seen[c.AddressID]; ok {
						continue
					}
					seen[c.AddressID] = struct{}{}
					condAddresses = append(condAddresses, addrVO(c.AddressID))
				}
			}
		} else {
			condRaw = []byte(r.ConditionsJSON)
		}

		out[i] = vo.TriggerRuleVO{
			ID:                 r.ID,
			Code:               r.Code,
			Name:               r.Name,
			Description:        r.Description,
			Status:             r.Status,
			ActionDeviceID:     r.ActionDeviceID,
			ActionDeviceName:   devices[r.ActionDeviceID].Name,
			ActionAddressIDs:   actionIDs,
			ActionAddresses:    actions,
			TargetChannelID:    r.TargetChannelID,
			TargetChannelName:  channelNames[r.TargetChannelID],
			ConditionsJSON:     condRaw,
			ConditionAddresses: condAddresses,
			ConditionCount:     condCount,
			CooldownMs:         r.CooldownMs,
			TimeoutMs:          r.TimeoutMs,
			CreatedAt:          r.CreatedAt,
			UpdatedAt:          r.UpdatedAt,
		}
	}
	return out, nil
}
