// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"

	"iot-gateway/model/dto"
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

// PageOpenApiDeviceAddresses 对外设备地址分页查询（按设备ID检索点位定义）
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
