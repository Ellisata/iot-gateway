// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"

	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
)

// OpenApiService 开放接口对外查询服务。
// 仅聚合现有领域服务的只读查询并映射为裁剪后的开放 VO，
// 不承载任何写操作；新增对外开放接口时在此扩展。
type OpenApiService struct {
	deviceService        *DeviceService
	deviceAddressService *DeviceAddressService
}

// NewOpenApiService 创建开放接口对外查询服务
func NewOpenApiService(deviceService *DeviceService, deviceAddressService *DeviceAddressService) *OpenApiService {
	return &OpenApiService{
		deviceService:        deviceService,
		deviceAddressService: deviceAddressService,
	}
}

// PageOpenApiDevices 对外设备分页查询（复用 PageDevice 的在线状态富化逻辑，
// 输出剔除 protocolJson 的裁剪 VO）
func (s *OpenApiService) PageOpenApiDevices(ctx context.Context, req *dto.PageDeviceDTO) (*response.PageVo[vo.OpenApiDeviceVO], error) {
	page, err := s.deviceService.PageDevice(ctx, req)
	if err != nil {
		return nil, err
	}

	records := make([]vo.OpenApiDeviceVO, len(page.Records))
	for i, d := range page.Records {
		records[i] = vo.OpenApiDeviceVO{
			ID:              d.ID,
			Name:            d.Name,
			ProtocolName:    d.ProtocolName,
			Description:     d.Description,
			Status:          d.Status,
			Online:          d.Online,
			LastSuccessTime: d.LastSuccessTime,
			CreatedAt:       d.CreatedAt,
			UpdatedAt:       d.UpdatedAt,
		}
	}
	return &response.PageVo[vo.OpenApiDeviceVO]{
		Page:    page.Page,
		Size:    page.Size,
		Total:   page.Total,
		Records: records,
	}, nil
}

// ListOpenApiDeviceNames 根据设备ID列表批量查询设备名称（供外部系统缓存设备ID → 名称映射）。
// 返回列表仅含库中实际存在的设备，顺序与入参 ids 一致。
func (s *OpenApiService) ListOpenApiDeviceNames(ctx context.Context, ids []string) ([]vo.OpenApiDeviceNameVO, error) {
	nameMap, err := s.deviceService.GetDeviceNamesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	// 输出顺序与入参一致，仅含库中实际存在的设备
	result := make([]vo.OpenApiDeviceNameVO, 0, len(ids))
	for _, id := range ids {
		if name, ok := nameMap[id]; ok {
			result = append(result, vo.OpenApiDeviceNameVO{ID: id, Name: name})
		}
	}
	return result, nil
}

// ListOpenApiAddressLabels 根据设备ID数组 + 点位ID数组批量查询点位标签
// （供外部系统缓存 点位ID → 标签 映射）。
// 返回列表仅含库中实际存在的点位，顺序与入参 addressIds 一致。
func (s *OpenApiService) ListOpenApiAddressLabels(ctx context.Context, deviceIDs, addressIDs []string) ([]vo.OpenApiAddressLabelVO, error) {
	addrs, err := s.deviceAddressService.GetAddressesByIDs(ctx, deviceIDs, addressIDs)
	if err != nil {
		return nil, err
	}
	addrByID := make(map[string]po.DeviceAddress, len(addrs))
	deviceIDSet := make(map[string]bool, len(addrs))
	for _, a := range addrs {
		addrByID[a.ID] = a
		deviceIDSet[a.DeviceID] = true
	}
	// 批量补齐所属设备名称（按实际命中的设备ID去重查询）
	deviceNames := make(map[string]string, len(deviceIDSet))
	hitDeviceIDs := make([]string, 0, len(deviceIDSet))
	for id := range deviceIDSet {
		hitDeviceIDs = append(hitDeviceIDs, id)
	}
	if names, err := s.deviceService.GetDeviceNamesByIDs(ctx, hitDeviceIDs); err == nil {
		deviceNames = names
	}
	// 输出顺序与入参 addressIds 一致，仅含库中实际存在的点位
	result := make([]vo.OpenApiAddressLabelVO, 0, len(addressIDs))
	for _, id := range addressIDs {
		if a, ok := addrByID[id]; ok {
			result = append(result, vo.OpenApiAddressLabelVO{
				ID:         a.ID,
				DeviceID:   a.DeviceID,
				DeviceName: deviceNames[a.DeviceID],
				Label:      a.Label,
			})
		}
	}
	return result, nil
}

func (s *OpenApiService) PageOpenApiDeviceAddresses(ctx context.Context, req *dto.PageDeviceAddressDTO) (*response.PageVo[vo.OpenApiDeviceAddressVO], error) {
	page, err := s.deviceAddressService.PageDeviceAddress(ctx, req)
	if err != nil {
		return nil, err
	}

	records := make([]vo.OpenApiDeviceAddressVO, len(page.Records))
	for i, a := range page.Records {
		records[i] = vo.OpenApiDeviceAddressVO{
			ID:             a.ID,
			DeviceID:       a.DeviceID,
			Name:           a.Name,
			Label:          a.Label,
			CommonDataType: a.CommonDataType,
			DataType:       a.DataType,
			RwPermission:   a.RwPermission,
			ScanFrequency:  a.ScanFrequency,
			Description:    a.Description,
			Status:         a.Status,
		}
	}
	return &response.PageVo[vo.OpenApiDeviceAddressVO]{
		Page:    page.Page,
		Size:    page.Size,
		Total:   page.Total,
		Records: records,
	}, nil
}
