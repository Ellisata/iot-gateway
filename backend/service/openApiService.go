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
	alarmService         *AlarmService
	// mqttSubscriptionService 提供订阅设备/点位索引的只读查询
	// （mqtt_subscription_device_point，由 MQTT 订阅收包侧异步维护）。
	mqttSubscriptionService *MqttSubscriptionService
}

// NewOpenApiService 创建开放接口对外查询服务
func NewOpenApiService(deviceService *DeviceService, deviceAddressService *DeviceAddressService, alarmService *AlarmService, mqttSubscriptionService *MqttSubscriptionService) *OpenApiService {
	return &OpenApiService{
		deviceService:           deviceService,
		deviceAddressService:    deviceAddressService,
		alarmService:            alarmService,
		mqttSubscriptionService: mqttSubscriptionService,
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
		records[i] = openApiDeviceVO(d)
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
		records[i] = openApiAddressVO(a)
	}
	return &response.PageVo[vo.OpenApiDeviceAddressVO]{
		Page:    page.Page,
		Size:    page.Size,
		Total:   page.Total,
		Records: records,
	}, nil
}

// SyncDevicePoints 游标分批同步「设备 + 其全部点位」，供外部系统全量遍历。
//
// 形状与理由（对外契约见 docs/openApi.md 4.5）：
//   - **游标而非 page/size**：同步期间有人增删设备/点位时，OFFSET 会让未读区间整体
//     位移、漏读或重读且不报错；设备 id 是 UUIDv7，按 id 升序续读不受影响。
//   - **多取一条判 hasMore**：请求 size 条、实取 size+1 条；多出来的那条只用来判断
//     "还有下一页"，随后裁掉。否则最后一页恰好装满时会多跑一次必然为空的请求。
//   - **嵌套而非扁平分段**：设备不会因为"没有点位"而从流里消失（Addresses 恒为 []）。
//     代价是单设备点位很多时该页偏大 —— size 上限 100 与之为界，见文档。
//
// 返回的 NextAfter 在 hasMore 为 false 时是空串，调用方的循环照
// `after=""` → 拉 → `HasMore ? after = NextAfter : 停` 写。
func (s *OpenApiService) SyncDevicePoints(ctx context.Context, req *dto.OpenApiSyncDevicePointsDTO) (*response.CursorVo[vo.OpenApiSyncDeviceVO], error) {
	devices, err := s.deviceService.ListDevicesAfterID(ctx, req.After, req.Size+1)
	if err != nil {
		return nil, err
	}

	hasMore := len(devices) > req.Size
	if hasMore {
		devices = devices[:req.Size]
	}

	result := &response.CursorVo[vo.OpenApiSyncDeviceVO]{
		Size:    req.Size,
		HasMore: hasMore,
		// 空切片而非 nil：无记录时 records 也应是 []，与 addresses 的处理一致。
		Records: make([]vo.OpenApiSyncDeviceVO, 0, len(devices)),
	}
	if len(devices) == 0 {
		return result, nil
	}
	if hasMore {
		result.NextAfter = devices[len(devices)-1].ID
	}

	// 一次查完本页设备的所有点位，再按 device_id 分组挂到各自设备下。
	deviceIDs := make([]string, len(devices))
	for i, d := range devices {
		deviceIDs[i] = d.ID
	}
	addrs, err := s.deviceAddressService.ListAddressesByDeviceIDs(ctx, deviceIDs)
	if err != nil {
		return nil, err
	}
	addrsByDevice := make(map[string][]vo.OpenApiDeviceAddressVO, len(devices))
	for _, a := range addrs {
		addrsByDevice[a.DeviceID] = append(addrsByDevice[a.DeviceID], openApiAddressVO(a))
	}

	for _, d := range devices {
		addresses := addrsByDevice[d.ID]
		if addresses == nil {
			addresses = []vo.OpenApiDeviceAddressVO{}
		}
		result.Records = append(result.Records, vo.OpenApiSyncDeviceVO{
			OpenApiDeviceVO: openApiDeviceVO(d),
			Addresses:       addresses,
		})
	}
	return result, nil
}

// SyncSubscriptionDevicePoints 游标分批同步「订阅设备 + 其全部点位」。
//
// 形态与理由完全对齐 4.5 的设备同步（docs/openApi.md 4.12）：
//   - 游标落在设备粒度（device_id 升序），点位嵌套在设备内；
//   - 多取一条判 hasMore，最后一页恰好装满时不多跑一次空请求；
//   - Points 恒为非 nil 切片。
//
// 数据来自订阅报文维护的元信息索引，不是本地 PLC 设备表 —— 没有协议、
// 在线状态等采集字段；设备列表也由点位行反推，故不存在零点位设备。
func (s *OpenApiService) SyncSubscriptionDevicePoints(ctx context.Context, req *dto.OpenApiSyncSubscriptionDevicePointsDTO) (*response.CursorVo[vo.OpenApiSyncSubscriptionDeviceVO], error) {
	deviceIDs, err := s.mqttSubscriptionService.ListDevicePointDeviceIDsAfter(ctx, req.After, req.Size+1)
	if err != nil {
		return nil, err
	}

	hasMore := len(deviceIDs) > req.Size
	if hasMore {
		deviceIDs = deviceIDs[:req.Size]
	}

	result := &response.CursorVo[vo.OpenApiSyncSubscriptionDeviceVO]{
		Size:    req.Size,
		HasMore: hasMore,
		// 空切片而非 nil：无记录时 records 也应是 []，与 4.5 一致。
		Records: make([]vo.OpenApiSyncSubscriptionDeviceVO, 0, len(deviceIDs)),
	}
	if len(deviceIDs) == 0 {
		return result, nil
	}
	if hasMore {
		result.NextAfter = deviceIDs[len(deviceIDs)-1]
	}

	rows, err := s.mqttSubscriptionService.ListDevicePointsByDeviceIDs(ctx, deviceIDs)
	if err != nil {
		return nil, err
	}
	pointsByDevice := make(map[string][]vo.OpenApiSyncSubscriptionPointVO, len(deviceIDs))
	deviceNames := make(map[string]string, len(deviceIDs))
	for i := range rows {
		r := &rows[i]
		// 同一设备的各点位理论上同名；历史/手工数据可能不一致，取第一个非空值。
		if _, ok := deviceNames[r.DeviceID]; !ok && r.DeviceName != "" {
			deviceNames[r.DeviceID] = r.DeviceName
		}
		pointsByDevice[r.DeviceID] = append(pointsByDevice[r.DeviceID], vo.OpenApiSyncSubscriptionPointVO{
			DeviceID:           r.DeviceID,
			DeviceAddressID:    r.DeviceAddressID,
			DeviceAddressLabel: r.DeviceAddressLabel,
			Kind:               r.Kind,
			Source:             r.Source,
			CreatedAt:          r.CreatedAt,
			UpdatedAt:          r.UpdatedAt,
		})
	}

	for _, deviceID := range deviceIDs {
		points := pointsByDevice[deviceID]
		if points == nil {
			points = []vo.OpenApiSyncSubscriptionPointVO{}
		}
		result.Records = append(result.Records, vo.OpenApiSyncSubscriptionDeviceVO{
			DeviceID:   deviceID,
			DeviceName: deviceNames[deviceID],
			Points:     points,
		})
	}
	return result, nil
}

// DeviceReliability 对外设备维度可靠性统计（窗口内可用率 / 故障段数 / 停机时长 / MTTR）。
//
// **纯直通，不映射成另一套开放 VO**：与 device / address 那些需要剔除 protocolJson、
// threshold* 等内部配置的接口不同，这份报表本来就**是**为外部系统设计的 ——
// DeviceReliabilityReportVO 的注释写明它用于「跨网关汇总」，且不含任何敏感字段
// （网关身份是给人看的标注，不是秘密）。另建一套同构 VO 只会多一处会漂移的字段映射。
func (s *OpenApiService) DeviceReliability(ctx context.Context, req *dto.DeviceReliabilityDTO) (*vo.DeviceReliabilityReportVO, error) {
	return s.alarmService.DeviceReliability(ctx, req)
}

// PointAbnormal 对外点位维度异常 top-N（采集质量异常与超限分开计数）。
//
// 直通理由同 DeviceReliability：PointAbnormalReportVO 已是面向外部的报表形态，
// 无内部字段可裁剪。两份报表形态刻意一致，调用方可用同一段代码处理。
func (s *OpenApiService) PointAbnormal(ctx context.Context, req *dto.PointAbnormalDTO) (*vo.PointAbnormalReportVO, error) {
	return s.alarmService.PointAbnormal(ctx, req)
}

// PageAlarms 对外报警历史分页查询（4.9）。
//
// 直通理由同 DeviceReliability：AlarmVO 只含报警本身的展示字段（目标、类型、
// 状态、发生/恢复时间），没有需要对外剔除的内部配置或凭据。另建一套同构 VO
// 只会多一处会漂移的字段映射，而这份数据的形状在管理端与外部系统之间本应一致。
func (s *OpenApiService) PageAlarms(ctx context.Context, req *dto.PageAlarmDTO) (*response.PageVo[vo.AlarmVO], error) {
	return s.alarmService.PageAlarms(ctx, req)
}

// ListActiveAlarms 对外当前活跃离线报警（= 离线设备 + 离线通道，4.10）。
// 直通理由同 PageAlarms。
func (s *OpenApiService) ListActiveAlarms(ctx context.Context) ([]vo.AlarmVO, error) {
	return s.alarmService.ListActiveAlarms(ctx)
}

// PageThresholdRecords 对外越限记录分页查询（4.11）。越限记录是原始样本留证，
// 与去抖后的报警是两件事，字段同样不含内部配置。直通理由同 PageAlarms。
func (s *OpenApiService) PageThresholdRecords(ctx context.Context, req *dto.PageThresholdRecordDTO) (*response.PageVo[vo.PointThresholdRecordVO], error) {
	return s.alarmService.PageThresholdRecords(ctx, req)
}

// openApiAddressVO 把点位裁剪为对外 VO（剔除 threshold* 等内部配置字段）。
// 4.4 分页与 4.5 同步共用，避免两处各写一份字段映射。
func openApiAddressVO(a vo.DeviceAddressVO) vo.OpenApiDeviceAddressVO {
	return vo.OpenApiDeviceAddressVO{
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
		CreatedAt:      a.CreatedAt,
		UpdatedAt:      a.UpdatedAt,
	}
}

// openApiDeviceVO 把设备裁剪为对外 VO（剔除 protocolJson 等敏感/内部字段）。
// 4.1 分页与 4.5 同步共用。
func openApiDeviceVO(d vo.DeviceVO) vo.OpenApiDeviceVO {
	return vo.OpenApiDeviceVO{
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
