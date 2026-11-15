// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
)

// OpenApiRealtimeService 开放接口的设备实时数据查询。
//
// 只做「按自然键定位设备」这一层薄封装，真正的快照逻辑复用 RealtimeService：
// 管理端按 UUID 查询，外部系统通常只知道下发时用的设备编码 code。两条路最终
// 落到同一个 GetDeviceSnapshot，避免实时值语义在开放接口与管理端之间漂移。
type OpenApiRealtimeService struct {
	sqliteDB *gorm.DB
	realtime *RealtimeService
}

// NewOpenApiRealtimeService 创建开放接口实时数据服务。
func NewOpenApiRealtimeService(sqliteDB *gorm.DB, realtime *RealtimeService) *OpenApiRealtimeService {
	return &OpenApiRealtimeService{sqliteDB: sqliteDB, realtime: realtime}
}

// ResolveDeviceID 把开放接口传入的 deviceId / code 归一成网关设备 UUID。
// 同时给出时以 deviceId 为准；都为空或编码不存在时返回可读错误。
func (s *OpenApiRealtimeService) ResolveDeviceID(ctx context.Context, deviceID, code string) (string, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID != "" {
		return deviceID, nil
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return "", appError.NewAppError(enums.ParamValidEnum.GetCode(), "deviceId 与 code 至少提供一个")
	}
	var device po.Device
	if err := s.sqliteDB.WithContext(ctx).Select("id").Where("code = ?", code).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", appError.NewAppErrorCtx(enums.DeviceNotExistsEnum.GetCode(),
				enums.DeviceNotExistsEnum.GetMessage(), enums.DeviceNotExistsEnum.GetMsgKey())
		}
		return "", err
	}
	return device.ID, nil
}

// Snapshot 按 deviceId 或设备编码 code 查询实时数据快照。
func (s *OpenApiRealtimeService) Snapshot(ctx context.Context, req *dto.OpenApiRealtimeSnapshotDTO) (*vo.RealtimeSnapshotVO, error) {
	deviceID, err := s.ResolveDeviceID(ctx, req.DeviceID, req.Code)
	if err != nil {
		return nil, err
	}
	return s.realtime.GetDeviceSnapshot(ctx, &dto.RealtimeSnapshotDTO{
		Page:       req.Page,
		Size:       req.Size,
		DeviceID:   deviceID,
		Name:       req.Name,
		AddressIDs: req.AddressIDs,
	})
}
