// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
)

// CollectorGroupService 采集组服务。
//
// 采集组是设备下的点位分组，组持有采集周期。绑定到组的点位由组周期驱动采集，
// 未分组点位继续按自身 scan_frequency 采集。
type CollectorGroupService struct {
	sqliteDB *gorm.DB
}

// NewCollectorGroupService 创建采集组服务。
func NewCollectorGroupService(sqliteDB *gorm.DB) *CollectorGroupService {
	return &CollectorGroupService{sqliteDB: sqliteDB}
}

const (
	collectorGroupMinIntervalMs = 100
	collectorGroupMaxIntervalMs = 600000
	collectorGroupMaxNameRunes  = 64
)

// validateCollectorGroupName 校验组名。
//
// 组名会进入 MQTT 主题（${group} 占位符），因此拒绝 / + # 与控制字符；
// legacy 是未分组记录的保留 topic 值，也不能占用。允许中文与空格。
func validateCollectorGroupName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > collectorGroupMaxNameRunes {
		return false
	}
	if strings.EqualFold(name, "legacy") {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '+' || r == '#' || r == 0 || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func collectorGroupNameInvalidErr() error {
	return appError.NewAppErrorCtx(enums.CollectorGroupNameInvalidEnum.GetCode(),
		enums.CollectorGroupNameInvalidEnum.GetMessage(), enums.CollectorGroupNameInvalidEnum.GetMsgKey())
}

func collectorGroupIntervalInvalidErr() error {
	return appError.NewAppErrorCtx(enums.CollectorGroupIntervalInvalidEnum.GetCode(),
		enums.CollectorGroupIntervalInvalidEnum.GetMessage(), enums.CollectorGroupIntervalInvalidEnum.GetMsgKey())
}

func collectorGroupNotExistsErr() error {
	return appError.NewAppErrorCtx(enums.CollectorGroupNotExistsEnum.GetCode(),
		enums.CollectorGroupNotExistsEnum.GetMessage(), enums.CollectorGroupNotExistsEnum.GetMsgKey())
}

// CreateCollectorGroup 创建采集组。
func (s *CollectorGroupService) CreateCollectorGroup(ctx context.Context, req *dto.CreateCollectorGroupDTO) error {
	db := s.sqliteDB.WithContext(ctx)

	var device po.Device
	if err := db.Where("id = ?", req.DeviceID).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return appError.NewAppErrorCtx(enums.DeviceNotExistsEnum.GetCode(),
				enums.DeviceNotExistsEnum.GetMessage(), enums.DeviceNotExistsEnum.GetMsgKey())
		}
		return err
	}

	name := strings.TrimSpace(req.Name)
	if !validateCollectorGroupName(name) {
		return collectorGroupNameInvalidErr()
	}
	if req.IntervalMs < collectorGroupMinIntervalMs || req.IntervalMs > collectorGroupMaxIntervalMs {
		return collectorGroupIntervalInvalidErr()
	}

	var dup po.CollectorGroup
	if err := db.Where("device_id = ? AND name = ?", req.DeviceID, name).First(&dup).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if dup.ID != "" {
		return appError.NewAppErrorCtx(enums.CollectorGroupExistsEnum.GetCode(),
			enums.CollectorGroupExistsEnum.GetMessage(), enums.CollectorGroupExistsEnum.GetMsgKey())
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	group := &po.CollectorGroup{
		DeviceID:    req.DeviceID,
		Name:        name,
		IntervalMs:  req.IntervalMs,
		Description: req.Description,
		Status:      1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	return db.Create(group).Error
}

// UpdateCollectorGroup 更新采集组（设备不可改）。
func (s *CollectorGroupService) UpdateCollectorGroup(ctx context.Context, req *dto.UpdateCollectorGroupDTO) error {
	db := s.sqliteDB.WithContext(ctx)

	var existing po.CollectorGroup
	if err := db.Where("id = ?", req.ID).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return collectorGroupNotExistsErr()
		}
		return err
	}

	updates := make(map[string]interface{})
	if name := strings.TrimSpace(req.Name); name != "" {
		if !validateCollectorGroupName(name) {
			return collectorGroupNameInvalidErr()
		}
		if name != existing.Name {
			var dup po.CollectorGroup
			if err := db.Where("device_id = ? AND name = ? AND id != ?", existing.DeviceID, name, req.ID).
				First(&dup).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if dup.ID != "" {
				return appError.NewAppErrorCtx(enums.CollectorGroupExistsEnum.GetCode(),
					enums.CollectorGroupExistsEnum.GetMessage(), enums.CollectorGroupExistsEnum.GetMsgKey())
			}
		}
		updates["name"] = name
	}
	if req.IntervalMs != 0 {
		if req.IntervalMs < collectorGroupMinIntervalMs || req.IntervalMs > collectorGroupMaxIntervalMs {
			return collectorGroupIntervalInvalidErr()
		}
		updates["interval_ms"] = req.IntervalMs
	}
	updates["description"] = req.Description
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	updates["updated_at"] = time.Now().Format("2006-01-02 15:04:05")
	return db.Model(&po.CollectorGroup{}).Where("id = ?", req.ID).Updates(updates).Error
}

// DeleteCollectorGroup 删除采集组。
//
// 组被点位绑定时拒绝删除（与物模型删除守卫一致）：点位不会因为删组而悄悄
// 回到另一种采集周期，用户必须先显式解绑。
func (s *CollectorGroupService) DeleteCollectorGroup(ctx context.Context, id string) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var group po.CollectorGroup
		if err := tx.Where("id = ?", id).First(&group).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return collectorGroupNotExistsErr()
			}
			return err
		}

		var bound int64
		if err := tx.Model(&po.DeviceAddress{}).Where("group_id = ?", id).Count(&bound).Error; err != nil {
			return err
		}
		if bound > 0 {
			return appError.NewAppErrorCtx(enums.CollectorGroupInUseEnum.GetCode(),
				enums.CollectorGroupInUseEnum.GetMessage(), enums.CollectorGroupInUseEnum.GetMsgKey())
		}
		return tx.Where("id = ?", id).Delete(&po.CollectorGroup{}).Error
	})
}

// GetCollectorGroupByID 查询采集组详情。
func (s *CollectorGroupService) GetCollectorGroupByID(ctx context.Context, id string) (*vo.CollectorGroupVO, error) {
	var group po.CollectorGroup
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&group).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, collectorGroupNotExistsErr()
		}
		return nil, err
	}
	records, err := s.enrichCollectorGroupVOs(ctx, []po.CollectorGroup{group})
	if err != nil {
		return nil, err
	}
	return &records[0], nil
}

// PageCollectorGroup 设备维度的采集组分页查询。
func (s *CollectorGroupService) PageCollectorGroup(ctx context.Context, req *dto.PageCollectorGroupDTO) (*response.PageVo[vo.CollectorGroupVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.CollectorGroup{}).Where("device_id = ?", req.DeviceID)
	if req.Name != "" {
		db = db.Where("name LIKE ?", "%"+req.Name+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var groups []po.CollectorGroup
	offset := (req.Page - 1) * req.Size
	// created_at 只到秒，id 兜底保证翻页稳定（与设备点位分页同一理由）。
	if err := db.Offset(offset).Limit(req.Size).
		Order("created_at DESC, id DESC").Find(&groups).Error; err != nil {
		return nil, err
	}

	records, err := s.enrichCollectorGroupVOs(ctx, groups)
	if err != nil {
		return nil, err
	}
	return &response.PageVo[vo.CollectorGroupVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}

// ListCollectorGroups 采集组下拉列表。deviceId 为空时返回全部设备的组，
// 供推送通道跨设备订阅选择。
func (s *CollectorGroupService) ListCollectorGroups(ctx context.Context, req *dto.ListCollectorGroupsDTO) ([]vo.CollectorGroupVO, error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.CollectorGroup{})
	if req.DeviceID != "" {
		db = db.Where("device_id = ?", req.DeviceID)
	}
	var groups []po.CollectorGroup
	if err := db.Order("device_id ASC, name ASC").Find(&groups).Error; err != nil {
		return nil, err
	}
	return s.enrichCollectorGroupVOs(ctx, groups)
}

// enrichCollectorGroupVOs 批量补全设备名与点位数，避免逐行查询。
func (s *CollectorGroupService) enrichCollectorGroupVOs(ctx context.Context, groups []po.CollectorGroup) ([]vo.CollectorGroupVO, error) {
	records := make([]vo.CollectorGroupVO, len(groups))
	if len(groups) == 0 {
		return records, nil
	}

	deviceIDs := make([]string, 0, len(groups))
	groupIDs := make([]string, 0, len(groups))
	seenDevice := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		groupIDs = append(groupIDs, g.ID)
		if _, ok := seenDevice[g.DeviceID]; !ok {
			seenDevice[g.DeviceID] = struct{}{}
			deviceIDs = append(deviceIDs, g.DeviceID)
		}
	}

	var devices []po.Device
	if err := s.sqliteDB.WithContext(ctx).Select("id", "name").
		Where("id IN ?", deviceIDs).Find(&devices).Error; err != nil {
		return nil, err
	}
	deviceNames := make(map[string]string, len(devices))
	for _, d := range devices {
		deviceNames[d.ID] = d.Name
	}

	type countRow struct {
		GroupID string
		Count   int
	}
	var counts []countRow
	if err := s.sqliteDB.WithContext(ctx).Model(&po.DeviceAddress{}).
		Select("group_id, COUNT(*) AS count").
		Where("group_id IN ?", groupIDs).Group("group_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	pointCounts := make(map[string]int, len(counts))
	for _, c := range counts {
		pointCounts[c.GroupID] = c.Count
	}

	for i, g := range groups {
		records[i] = vo.CollectorGroupVO{
			ID:          g.ID,
			DeviceID:    g.DeviceID,
			DeviceName:  deviceNames[g.DeviceID],
			Name:        g.Name,
			IntervalMs:  g.IntervalMs,
			Description: g.Description,
			Status:      g.Status,
			PointCount:  pointCounts[g.ID],
			CreatedAt:   g.CreatedAt,
			UpdatedAt:   g.UpdatedAt,
		}
	}
	return records, nil
}
