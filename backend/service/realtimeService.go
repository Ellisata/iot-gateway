// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"time"

	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/realtime"
)

// staleGraceFactor 判定「值已过期」的宽限倍数：距上次更新超过 3 个采集周期即视为过期。
// 取 3 是为容忍偶发的单次轮询延迟与重连，又不至于让掉线设备的旧值长期显示为新鲜。
const staleGraceFactor = 3

// defaultScanFrequency 与采集引擎建任务时的兜底一致（见 newGatewayTask）：
// 未配置或配置为非正数时按 1 秒采集。
const defaultScanFrequency = 1000

// RealtimeService 设备实时数据服务。
//
// 数据由两部分拼成：点位清单与点位元信息取自 SQLite（唯一真源），
// 最新值取自 realtime.Store 的内存快照。以库中的活跃点位为基准对齐，
// 好处是已停用/已删除的点位不会因为快照里还留着值而出现在页面上。
type RealtimeService struct {
	sqliteDB      *gorm.DB
	store         *realtime.Store
	engine        *collector.Engine
	deviceService *DeviceService
}

// NewRealtimeService 创建设备实时数据服务
func NewRealtimeService(sqliteDB *gorm.DB, store *realtime.Store, engine *collector.Engine, deviceService *DeviceService) *RealtimeService {
	return &RealtimeService{
		sqliteDB:      sqliteDB,
		store:         store,
		engine:        engine,
		deviceService: deviceService,
	}
}

// GetDeviceSnapshot 查询单台设备的实时数据快照（分页）。
func (s *RealtimeService) GetDeviceSnapshot(ctx context.Context, req *dto.RealtimeSnapshotDTO) (*vo.RealtimeSnapshotVO, error) {
	// 设备状态直接复用 DeviceService：在线判定（active 断联报警）与
	// 最近成功采集时间（引擎内存态）的逻辑只有一份，避免两处漂移。
	device, err := s.deviceService.GetDeviceByID(ctx, req.DeviceID)
	if err != nil {
		return nil, err
	}

	db := s.sqliteDB.WithContext(ctx).Model(&po.DeviceAddress{}).
		Where("device_id = ? AND status = 1", req.DeviceID)
	if req.Name != "" {
		like := "%" + req.Name + "%"
		db = db.Where("name LIKE ? OR label LIKE ?", like, like)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var addrs []po.DeviceAddress
	// 排序必须带 id 兜底：created_at 只精确到秒，同秒创建的点位仅按它排序时
	// 次序不确定，前端在轮询中翻页会看到重复或漏行。id 为 UUIDv7（时间有序），可作稳定次序。
	if err := db.Order("created_at ASC, id ASC").
		Offset((req.Page - 1) * req.Size).Limit(req.Size).
		Find(&addrs).Error; err != nil {
		return nil, err
	}

	ids := make([]string, len(addrs))
	for i := range addrs {
		ids[i] = addrs[i].ID
	}
	values := s.store.Lookup(req.DeviceID, ids)

	now := time.Now()
	records := make([]vo.RealtimePointVO, len(addrs))
	for i, a := range addrs {
		item := vo.RealtimePointVO{
			AddressID:      a.ID,
			Name:           a.Name,
			Label:          a.Label,
			DataType:       a.DataType,
			CommonDataType: a.CommonDataType,
			RwPermission:   a.RwPermission,
			ScanFrequency:  a.ScanFrequency,
		}
		if p, ok := values[a.ID]; ok {
			item.Value = p.Value
			item.Quality = p.Quality
			item.Collected = true
			item.UpdatedAt = p.UpdatedAt.Format("2006-01-02 15:04:05")
			item.Stale = isStale(now, p.UpdatedAt, a.ScanFrequency)
		}
		records[i] = item
	}

	return &vo.RealtimeSnapshotVO{
		DeviceID:         device.ID,
		DeviceName:       device.Name,
		ProtocolName:     device.ProtocolName,
		Online:           device.Online,
		Collecting:       s.isCollecting(req.DeviceID),
		CollectorRunning: s.engine.IsRunning(),
		LastSuccessTime:  device.LastSuccessTime,
		ServerTime:       now.Format("2006-01-02 15:04:05"),
		Page:             req.Page,
		Size:             req.Size,
		Total:            total,
		Records:          records,
	}, nil
}

// isStale 判断一个值是否已过期：距上次更新超过 staleGraceFactor 个采集周期。
// 按点位自身的采集周期判定，而非全表统一阈值 —— 60 秒周期的点位更新于 30 秒前
// 仍是新鲜的，而 1 秒周期的点位同样时间就是明显掉数了。
func isStale(now, updatedAt time.Time, scanFrequency int) bool {
	if scanFrequency <= 0 {
		scanFrequency = defaultScanFrequency
	}
	ttl := time.Duration(scanFrequency) * time.Millisecond * staleGraceFactor
	return now.Sub(updatedAt) > ttl
}

// isCollecting 判断设备是否已接入采集调度（有协议驱动且至少一个活跃点位）。
// 引擎未运行时其活跃设备列表为空，故此处一并覆盖了「采集已停止」的情况
// （页面另有 collectorRunning 字段用于区分措辞）。
func (s *RealtimeService) isCollecting(deviceID string) bool {
	for _, id := range s.engine.ActiveDeviceIDs() {
		if id == deviceID {
			return true
		}
	}
	return false
}
