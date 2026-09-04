// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"iot-gateway/alarm"
	"iot-gateway/appError"
	"iot-gateway/collector"
	"iot-gateway/driver"
	"iot-gateway/enums"
	"iot-gateway/excelutil"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
)

// DeviceService 设备服务
type DeviceService struct {
	sqliteDB     *gorm.DB
	collector    *collector.Engine
	alarmService *AlarmService
}

// NewDeviceService 创建设备服务
func NewDeviceService(sqliteDB *gorm.DB, engine *collector.Engine, alarmService *AlarmService) *DeviceService {
	return &DeviceService{sqliteDB: sqliteDB, collector: engine, alarmService: alarmService}
}

// CreateDevice 创建设备
func (s *DeviceService) CreateDevice(ctx context.Context, req *dto.CreateDeviceDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 检查名称是否已存在
		var existing po.Device
		if err := tx.Where("name = ?", req.Name).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if existing.ID != "" {
			return appError.NewAppErrorCtx(enums.DeviceExistsEnum.GetCode(), enums.DeviceExistsEnum.GetMessage(), enums.DeviceExistsEnum.GetMsgKey())
		}

		now := time.Now().Format("2006-01-02 15:04:05")
		protocolJson := ""
		if req.ProtocolJSON != nil {
			protocolJson = string(req.ProtocolJSON)
		}
		device := &po.Device{
			Name:         req.Name,
			ProtocolID:   req.ProtocolID,
			ProtocolJSON: protocolJson,
			Description:  req.Description,
			Status:       1,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := tx.Create(device).Error; err != nil {
			return err
		}
		return nil
	})
}

// UpdateDevice 更新设备
func (s *DeviceService) UpdateDevice(ctx context.Context, req *dto.UpdateDeviceDTO) error {
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 检查设备是否存在
		var existing po.Device
		id := req.ID
		if err := tx.Where("id = ?", id).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(enums.DeviceNotExistsEnum.GetCode(), enums.DeviceNotExistsEnum.GetMessage(), enums.DeviceNotExistsEnum.GetMsgKey())
			}
			return err
		}

		// 检查名称是否被占用（修改名称时）
		if req.Name != "" && req.Name != existing.Name {
			var dup po.Device
			if err := tx.Where("name = ? AND id != ?", req.Name, id).First(&dup).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			if dup.ID != "" {
				return appError.NewAppErrorCtx(enums.DeviceExistsEnum.GetCode(), enums.DeviceExistsEnum.GetMessage(), enums.DeviceExistsEnum.GetMsgKey())
			}
		}
		protocolJson := ""
		if req.ProtocolJSON != nil {
			protocolJson = string(req.ProtocolJSON)
		}

		updates := make(map[string]interface{})
		if req.Name != "" {
			updates["name"] = req.Name
		}
		if req.ProtocolID != "" {
			updates["protocol_id"] = req.ProtocolID
		}
		if req.ProtocolJSON != nil {
			updates["protocol_json"] = protocolJson
		}
		if req.Description != "" {
			updates["description"] = req.Description
		}
		if req.Status != nil {
			updates["status"] = *req.Status
		}
		if len(updates) == 0 {
			return nil
		}
		updates["updated_at"] = time.Now().Format("2006-01-02 15:04:05")

		return tx.Model(&po.Device{}).Where("id = ?", id).Updates(updates).Error
	})
	if err != nil {
		return err
	}

	// 设备停用（status=0）：清除其活跃断联报警，历史保留。
	// 事务外执行（尽力而为，失败不影响设备更新结果）。
	if req.Status != nil && *req.Status == 0 && s.alarmService != nil {
		if err := s.alarmService.ClearDeviceAlarm(ctx, req.ID); err != nil {
			logger.Warn("device: clear alarm for disabled device %s failed: %v", req.ID, err)
		}
	}
	return nil
}

// DeleteDevice 删除设备
func (s *DeviceService) DeleteDevice(ctx context.Context, id string) error {
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).Delete(&po.Device{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return appError.NewAppErrorCtx(enums.DeviceNotExistsEnum.GetCode(), enums.DeviceNotExistsEnum.GetMessage(), enums.DeviceNotExistsEnum.GetMsgKey())
		}
		return nil
	})
	if err != nil {
		return err
	}

	// 删除后清除其活跃断联报警，历史保留（尽力而为）。
	if s.alarmService != nil {
		if err := s.alarmService.ClearDeviceAlarm(ctx, id); err != nil {
			logger.Warn("device: clear alarm for deleted device %s failed: %v", id, err)
		}
	}
	return nil
}

// GetDeviceByID 根据ID查询设备
func (s *DeviceService) GetDeviceByID(ctx context.Context, id string) (*vo.DeviceVO, error) {
	var device po.Device
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(enums.DeviceNotExistsEnum.GetCode(), enums.DeviceNotExistsEnum.GetMessage(), enums.DeviceNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}

	// 根据 protocol_id 获取协议名称
	protocolName := s.getProtocolName(ctx, device.ProtocolID)

	online := true
	if s.alarmService != nil {
		online = s.alarmService.IsDeviceOnline(ctx, device.ID)
	}

	return &vo.DeviceVO{
		ID:              device.ID,
		Name:            device.Name,
		ProtocolID:      device.ProtocolID,
		ProtocolName:    protocolName,
		ProtocolJSON:    device.ProtocolJSON,
		Description:     device.Description,
		Status:          device.Status,
		Online:          online,
		LastSuccessTime: s.deviceLastSuccess(device.ID),
		CreatedAt:       device.CreatedAt,
		UpdatedAt:       device.UpdatedAt,
	}, nil
}

// TestDeviceConnection 测试设备连接
// 直接以协议名 + 协议 JSON 配置测试连通性。不同协议的 protocolJson 字段各不相同，
// 故不依赖设备表，以原始 JSON 串传递，由各驱动自行解析。
func (s *DeviceService) TestDeviceConnection(ctx context.Context, req *dto.TestDeviceConnectionDTO) error {
	if req.ProtocolName == "" {
		return appError.NewAppError(enums.DevicePingFailEnum.GetCode(), "协议不存在")
	}
	if err := driver.PingDevice(req.ProtocolName, string(req.ProtocolJSON)); err != nil {
		logger.Error("%s", err.Error())
		return appError.NewAppError(enums.DevicePingFailEnum.GetCode(), enums.DevicePingFailEnum.GetMessage())
	}
	return nil
}

// PageDevice 设备分页查询（支持名称模糊检索）
func (s *DeviceService) PageDevice(ctx context.Context, req *dto.PageDeviceDTO) (*response.PageVo[vo.DeviceVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.Device{})

	if req.Name != "" {
		db = db.Where("name LIKE ?", "%"+req.Name+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var devices []po.Device
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).Order("created_at DESC").Find(&devices).Error; err != nil {
		return nil, err
	}

	// 一次性取当前离线设备集合（仅 target_type=device，避免混入通道报警），
	// 避免每台设备一次查询。
	offlineSet := make(map[string]bool)
	if s.alarmService != nil {
		if active, err := s.alarmService.ListActiveTargets(ctx, alarm.TypeDevice); err == nil {
			for _, a := range active {
				offlineSet[a.TargetID] = true
			}
		}
	}

	records := make([]vo.DeviceVO, len(devices))
	for i, d := range devices {
		protocolName := s.getProtocolName(ctx, d.ProtocolID)
		records[i] = vo.DeviceVO{
			ID:              d.ID,
			Name:            d.Name,
			ProtocolID:      d.ProtocolID,
			ProtocolName:    protocolName,
			ProtocolJSON:    d.ProtocolJSON,
			Description:     d.Description,
			Status:          d.Status,
			Online:          !offlineSet[d.ID],
			LastSuccessTime: s.deviceLastSuccess(d.ID),
			CreatedAt:       d.CreatedAt,
			UpdatedAt:       d.UpdatedAt,
		}
	}

	return &response.PageVo[vo.DeviceVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}

// getProtocolName 根据 protocol_id 查询协议名称
func (s *DeviceService) getProtocolName(ctx context.Context, protocolID string) string {
	if protocolID == "" {
		return ""
	}
	var protocol po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Select("name").Where("id = ?", protocolID).First(&protocol).Error; err != nil {
		return ""
	}
	return protocol.Name
}

// GetDeviceOverview 统计设备在线情况（只读聚合，无写库）。
// 口径：total=全部设备（启用+禁用）；enabled=启用设备；disabled=禁用设备；
// collected=正在被采集调度的启用设备（有驱动且至少一个活跃地址）；
// offline=collected 且存在 active 断联报警；online=collected-offline；
// unCollected=启用但未接入采集（协议不支持/无驱动/无活跃地址，永不判定在线/离线）；
// onlineRate=online/collected（分母用已接入采集，避免 unCollected 虚高在线率）。
func (s *DeviceService) GetDeviceOverview(ctx context.Context) (*vo.DeviceOverviewVO, error) {
	var devices []po.Device
	if err := s.sqliteDB.WithContext(ctx).Select("id", "status").Find(&devices).Error; err != nil {
		return nil, err
	}
	enabledSet := make(map[string]bool)
	disabledSet := make(map[string]bool)
	for _, d := range devices {
		if d.Status == 1 {
			enabledSet[d.ID] = true
		} else {
			disabledSet[d.ID] = true
		}
	}

	// 离线集合：active 的 device 断联报警（停用/删除设备时报警已被清除，天然只含启用且被采集的设备）
	offlineSet := make(map[string]bool)
	if s.alarmService != nil {
		if active, err := s.alarmService.ListActiveTargets(ctx, alarm.TypeDevice); err == nil {
			for _, a := range active {
				offlineSet[a.TargetID] = true
			}
		}
	}

	// 已接入采集集合：采集引擎当前调度的设备（引擎只加载 status=1 设备）
	collectedSet := make(map[string]bool)
	if s.collector != nil {
		for _, id := range s.collector.ActiveDeviceIDs() {
			collectedSet[id] = true
		}
	}

	return buildDeviceOverview(enabledSet, disabledSet, offlineSet, collectedSet), nil
}

// buildDeviceOverview 由四组设备集合计算在线情况（纯函数，便于单测）。
// 防御：offline 与 collected 只计入 enabled 集合内的设备，避免停用/脏数据污染统计。
func buildDeviceOverview(enabledSet, disabledSet, offlineSet, collectedSet map[string]bool) *vo.DeviceOverviewVO {
	enabled := len(enabledSet)
	total := enabled + len(disabledSet)
	collected, online, offline := 0, 0, 0
	for id := range collectedSet {
		if !enabledSet[id] {
			continue
		}
		collected++
		if offlineSet[id] {
			offline++
		} else {
			online++
		}
	}
	rate := 0.0
	if collected > 0 {
		rate = math.Round(float64(online)*100/float64(collected)) / 100
	}
	return &vo.DeviceOverviewVO{
		Total:       total,
		Enabled:     enabled,
		Disabled:    total - enabled,
		Collected:   collected,
		Online:      online,
		Offline:     offline,
		UnCollected: enabled - collected,
		OnlineRate:  rate,
	}
}

// deviceLastSuccess 返回设备最近一次成功采集时间（采集引擎未运行或设备未采集时为空串）。
func (s *DeviceService) deviceLastSuccess(deviceID string) string {
	if s.collector == nil {
		return ""
	}
	return s.collector.DeviceLastSuccessTime(deviceID)
}

// ==================== Excel 批量导入设备 ====================

// ImportDevices 从 Excel(.xlsx) 批量导入设备。
// 模板三列：名称 / 协议 / 描述。协议列按 iot_protocol.name 匹配，未知协议跳过该行并记录原因；
// 名称重复（库内或文件内）跳过该行；其余行部分导入并返回汇总，供前端展示失败明细。
// 导入仅建基本信息，协议参数留空 "{}"，连接参数后续在编辑界面补填。
func (s *DeviceService) ImportDevices(ctx context.Context, data []byte) (*vo.ExcelImportResultVO, error) {
	// 预加载现有设备名，用于去重
	var names []string
	if err := s.sqliteDB.WithContext(ctx).Model(&po.Device{}).Pluck("name", &names).Error; err != nil {
		return nil, err
	}

	// 预加载协议名 → ID 映射
	protocolMap := make(map[string]string)
	var protocols []po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Select("id", "name").Find(&protocols).Error; err != nil {
		return nil, err
	}
	for _, p := range protocols {
		protocolMap[p.Name] = p.ID
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	return excelutil.Import(ctx, s.sqliteDB, data, excelutil.Options[po.Device]{
		Headers:      []string{"名称", "协议", "描述"},
		ExistingKeys: names,
		Cols:         3,
		DupMsg:       enums.DeviceExistsEnum.GetMessage(),
		Parse: func(row []string) (*po.Device, string, string) {
			name := excelutil.Cell(row, 0)
			protocolName := excelutil.Cell(row, 1)
			desc := excelutil.Cell(row, 2)
			if reason := validateImportRow(name, protocolName, protocolMap); reason != "" {
				return nil, name, reason
			}
			return &po.Device{
				Name:         name,
				ProtocolID:   protocolMap[protocolName],
				ProtocolJSON: "{}",
				Description:  desc,
				Status:       1,
				CreatedAt:    now,
				UpdatedAt:    now,
			}, name, ""
		},
	})
}

// validateImportRow 校验单行设备导入数据（纯函数，便于单测；去重由 excelutil.Import 统一处理）。
// 返回 "" 表示通过，否则返回中文失败原因。
func validateImportRow(name, protocolName string, protocolMap map[string]string) string {
	if name == "" {
		return "名称为空"
	}
	if utf8.RuneCountInString(name) > 100 {
		return "名称不能超过100个字符"
	}
	if protocolName == "" {
		return "协议为空"
	}
	if _, ok := protocolMap[protocolName]; !ok {
		return fmt.Sprintf("协议不存在：%s", protocolName)
	}
	return ""
}

// GenerateImportTemplate 生成设备导入模板（.xlsx）：表头「名称/协议/描述」+ 示例行 + 冻结首行。
func (s *DeviceService) GenerateImportTemplate() (*bytes.Buffer, error) {
	return excelutil.BuildTemplate(
		[]string{"名称", "协议", "描述"},
		[]string{"示例：1号注塑机", "ModBus.TCP", "示例数据，可删除或覆盖"},
		[]float64{32, 22, 40},
	)
}
