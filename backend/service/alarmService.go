package service

import (
	"context"
	"time"

	"gorm.io/gorm"

	"iot-gateway/alarm"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
)

// AlarmService 断联报警查询与清理服务。
// 报警记录的生成由 alarm.Tracker（采集侧设备引擎 / 巡检侧通道监控）负责，
// 本服务只读查询 + 目标停用/删除时的清理。
type AlarmService struct {
	sqliteDB *gorm.DB
}

// NewAlarmService 创建报警服务
func NewAlarmService(sqliteDB *gorm.DB) *AlarmService {
	return &AlarmService{sqliteDB: sqliteDB}
}

// ListActiveTargets 当前活跃（未恢复）的离线报警，即离线目标列表。
// targetType 传 alarm.TypeDevice/TypeChannel 仅取一类，空串取全部。
// 按首次发生时间倒序返回。
func (s *AlarmService) ListActiveTargets(ctx context.Context, targetType string) ([]vo.AlarmVO, error) {
	q := s.sqliteDB.WithContext(ctx).
		Where("status = ? AND alarm_type = ?", alarm.StatusActive, alarm.TypeOffline)
	if targetType != "" {
		q = q.Where("target_type = ?", targetType)
	}
	var rows []po.Alarm
	if err := q.Order("first_occur_time DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return toAlarmVOs(rows), nil
}

// ListActiveAlarms 当前活跃离线报警（设备 + 通道），= 离线目标列表。
func (s *AlarmService) ListActiveAlarms(ctx context.Context) ([]vo.AlarmVO, error) {
	return s.ListActiveTargets(ctx, "")
}

// PageAlarms 报警历史分页查询（目标名 / 类型 / 状态过滤）。
func (s *AlarmService) PageAlarms(ctx context.Context, req *dto.PageAlarmDTO) (*response.PageVo[vo.AlarmVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.Alarm{})

	if req.TargetName != "" {
		db = db.Where("target_name LIKE ?", "%"+req.TargetName+"%")
	}
	if req.TargetType != "" {
		db = db.Where("target_type = ?", req.TargetType)
	}
	if req.AlarmType != "" {
		db = db.Where("alarm_type = ?", req.AlarmType)
	}
	if req.Status != "" {
		db = db.Where("status = ?", req.Status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var rows []po.Alarm
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).Order("first_occur_time DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	return &response.PageVo[vo.AlarmVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: toAlarmVOs(rows),
	}, nil
}

// IsDeviceOnline 判断设备当前是否在线（无该设备 active 离线报警视为在线）。
// 查询失败保守视为在线，避免误报。
func (s *AlarmService) IsDeviceOnline(ctx context.Context, deviceID string) bool {
	var n int64
	if err := s.sqliteDB.WithContext(ctx).Model(&po.Alarm{}).
		Where("target_id = ? AND target_type = ? AND status = ?",
			deviceID, alarm.TypeDevice, alarm.StatusActive).
		Count(&n).Error; err != nil {
		return true
	}
	return n == 0
}

// ClearDeviceAlarm 设备停用/删除时清除其活跃报警（历史记录保留）。
func (s *AlarmService) ClearDeviceAlarm(ctx context.Context, deviceID string) error {
	return s.clearTargetAlarm(ctx, deviceID, alarm.TypeDevice)
}

// ClearChannelAlarm 推送通道停用/删除时清除其活跃报警（历史记录保留）。
func (s *AlarmService) ClearChannelAlarm(ctx context.Context, channelID string) error {
	return s.clearTargetAlarm(ctx, channelID, alarm.TypeChannel)
}

// clearTargetAlarm 清除指定类型目标的活跃离线报警（历史记录保留）。
func (s *AlarmService) clearTargetAlarm(ctx context.Context, targetID, targetType string) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	return s.sqliteDB.WithContext(ctx).Model(&po.Alarm{}).
		Where("target_id = ? AND target_type = ? AND status = ?", targetID, targetType, alarm.StatusActive).
		Updates(map[string]interface{}{
			"status":     alarm.StatusCleared,
			"clear_time": now,
			"updated_at": now,
		}).Error
}

// toAlarmVOs 报警 PO -> VO
func toAlarmVOs(rows []po.Alarm) []vo.AlarmVO {
	vos := make([]vo.AlarmVO, len(rows))
	for i, a := range rows {
		vos[i] = vo.AlarmVO{
			ID:             a.ID,
			TargetID:       a.TargetID,
			TargetName:     a.TargetName,
			TargetType:     a.TargetType,
			AlarmType:      a.AlarmType,
			AlarmTypeName:  alarmTypeName(a.AlarmType),
			Level:          a.Level,
			Content:        a.Content,
			Status:         a.Status,
			StatusName:     alarmStatusName(a.Status),
			FirstOccurTime: a.FirstOccurTime,
			LastOccurTime:  a.LastOccurTime,
			ClearTime:      a.ClearTime,
			CreatedAt:      a.CreatedAt,
			UpdatedAt:      a.UpdatedAt,
		}
	}
	return vos
}

// alarmTypeName 报警类型中文说明。
func alarmTypeName(t string) string {
	switch t {
	case alarm.TypeOffline:
		return "断联报警"
	case alarm.TypeRecover:
		return "恢复记录"
	}
	return t
}

// alarmStatusName 报警状态中文说明。
func alarmStatusName(s string) string {
	switch s {
	case alarm.StatusActive:
		return "未恢复"
	case alarm.StatusCleared:
		return "已恢复"
	}
	return s
}
