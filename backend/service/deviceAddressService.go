// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/excelutil"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
)

// DeviceAddressService 设备地址服务
type DeviceAddressService struct {
	sqliteDB *gorm.DB
	// alarmService 仅用于删除点位时清掉它的活跃质量报警（见 DeleteDeviceAddress
	// 的说明）。可为 nil（测试里常这么构造）。
	alarmService *AlarmService
}

// NewDeviceAddressService 创建设备地址服务
func NewDeviceAddressService(sqliteDB *gorm.DB, alarmService *AlarmService) *DeviceAddressService {
	return &DeviceAddressService{sqliteDB: sqliteDB, alarmService: alarmService}
}

// CreateDeviceAddress 创建设备地址
func (s *DeviceAddressService) CreateDeviceAddress(ctx context.Context, req *dto.CreateDeviceAddressDTO) error {
	db := s.sqliteDB.WithContext(ctx)

	// 检查设备是否存在
	var device po.Device
	if err := db.Where("id = ?", req.DeviceID).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return appError.NewAppErrorCtx(enums.DeviceNotExistsEnum.GetCode(), enums.DeviceNotExistsEnum.GetMessage(), enums.DeviceNotExistsEnum.GetMsgKey())
		}
		return err
	}

	// 检查地址名称是否已存在
	var existing po.DeviceAddress
	if err := db.Where("device_id = ? AND name = ?", req.DeviceID, req.Name).First(&existing).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if existing.ID != "" {
		return appError.NewAppErrorCtx(enums.DeviceAddressExistsEnum.GetCode(), enums.DeviceAddressExistsEnum.GetMessage(), enums.DeviceAddressExistsEnum.GetMsgKey())
	}

	// 解析数据类型：commonDataType 为通用类型名，data_type 存协议内部类型名
	common := req.CommonDataType
	if common == "" {
		common = req.DataType // 兼容旧请求：直接传通用类型名
	}
	if common == "" {
		return appError.NewAppErrorCtx(enums.ParamValidEnum.GetCode(), "数据类型不能为空", enums.ParamValidEnum.GetMsgKey())
	}
	internal, err := s.resolveDataType(s.getProtocolName(ctx, device.ProtocolID), common)
	if err != nil {
		return err
	}

	// 报警条件校验：可用比较符由数据类型的 Kind 决定（矩阵见 driver.OperatorsForKind）
	enabled, op, values := thresholdFields(req.Threshold)
	if err := validateAddressThreshold(enabled, op, values,
		kindOfAddressType(s.getProtocolName(ctx, device.ProtocolID), internal)); err != nil {
		return err
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	addr := &po.DeviceAddress{
		DeviceID:         req.DeviceID,
		Name:             req.Name,
		Label:            req.Label,
		CommonDataType:   common,
		DataType:         internal,
		RwPermission:     req.RwPermission,
		ScanFrequency:    req.ScanFrequency,
		Description:      req.Description,
		Status:           1,
		ThresholdEnabled: boolToInt(enabled),
		ThresholdOp:      op,
		ThresholdValue:   marshalThresholdValues(values),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if addr.ScanFrequency <= 0 {
		addr.ScanFrequency = 1000
	}

	if err := db.Create(addr).Error; err != nil {
		return err
	}
	return nil
}

// thresholdFields 把请求里的条件配置解成 (启用, 算子, 操作数)；Threshold 为 nil 时取零值。
func thresholdFields(t *dto.DeviceAddressThresholdDTO) (bool, string, []string) {
	if t == nil {
		return false, "", nil
	}
	op := ""
	if t.Op != nil {
		op = strings.TrimSpace(*t.Op)
	}
	return t.Enabled != nil && *t.Enabled == 1, op, t.Values
}

// kindOfAddressType 查某协议下的内部数据类型名对应的 Kind（查不到返回空串）。
// 协议/类型查不到时返回空串：判不了就不放行，避免配出一个永不生效的报警条件。
func kindOfAddressType(protocolName, internalType string) string {
	return driver.GetTypeRegistry().ForProtocol(driver.ScopeOf(protocolName)).KindOf(internalType)
}

// validateAddressThreshold 校验报警条件（纯函数，便于单测）。
//
// kind 由调用方按点位的协议内部类型算出（driver.KindXxx）。可用比较符集合来自
// driver.OperatorsForKind —— 那是前后端与采集判定共用的唯一真源，这里不另写一份。
//
// 未启用时**不做任何检查**：算子与操作数照常持久化，运维关掉开关再打开不该丢掉配置。
func validateAddressThreshold(enabled bool, op string, values []string, kind string) error {
	if !enabled {
		return nil
	}
	paramErr := func(msg string) error {
		return appError.NewAppErrorCtx(enums.ParamValidEnum.GetCode(), msg, enums.ParamValidEnum.GetMsgKey())
	}

	if kind == driver.KindTime {
		// 时间值比较几乎总是想表达"多久没更新了"，那比的是当前时间、不是配置的边界，
		// 是另一个功能。显式拒绝好过给一个语义可疑的开关。
		return paramErr("时间类型不支持报警条件（如需「数据陈旧」告警请另提需求）")
	}
	if kind == "" {
		return paramErr("该数据类型不支持报警条件（无法识别其数据类型类别）")
	}
	if op == "" {
		return paramErr("启用报警条件时必须选择比较符")
	}
	if !driver.OperatorAllowed(kind, op) {
		return paramErr(fmt.Sprintf("该数据类型的比较符只能是：%s",
			strings.Join(driver.OperatorsForKind(kind), " / ")))
	}

	// 操作数校验（按算子）
	switch op {
	case driver.OpRange:
		// **必须两个槽位**（[下限, 上限]），空串表示该侧不检查。
		// 不允许只传一个：位置语义会让 ["40"] 被理解成"下限 40"，
		// 而填一个数的人多半是想说"上限 40" —— 与其猜，不如要求写清位置。
		if len(values) != 2 {
			return paramErr("区间比较符需要两个操作数（下限、上限），不检查的那侧留空")
		}
		var lo, hi *float64
		if len(values) > 0 && strings.TrimSpace(values[0]) != "" {
			v, err := strconv.ParseFloat(strings.TrimSpace(values[0]), 64)
			if err != nil {
				return paramErr("区间下限必须是数值")
			}
			lo = &v
		}
		if len(values) > 1 && strings.TrimSpace(values[1]) != "" {
			v, err := strconv.ParseFloat(strings.TrimSpace(values[1]), 64)
			if err != nil {
				return paramErr("区间上限必须是数值")
			}
			hi = &v
		}
		if lo == nil && hi == nil {
			return paramErr("区间比较符至少填下限或上限中的一个")
		}
		if lo != nil && hi != nil && *lo >= *hi {
			return paramErr("区间下限必须小于上限")
		}
		return nil

	case driver.OpGt, driver.OpGe, driver.OpLt, driver.OpLe:
		if _, err := singleNumberOperand(values, op); err != nil {
			return paramErr(err.Error())
		}
		return nil
	}

	// eq / ne / in 的语义按 Kind 分（数值比数值、布尔比真值、文本比字符串）
	if kind == driver.KindBool {
		if len(values) != 1 {
			return paramErr("布尔条件需要一个操作数（1 或 0）")
		}
		if !isBoolLiteral(values[0]) {
			return paramErr("布尔条件的操作数只能是 1、0、true 或 false")
		}
		return nil
	}
	if kind == driver.KindString {
		if op == driver.OpIn {
			if len(nonEmptyValues(values)) == 0 {
				return paramErr("集合比较符至少需要一个非空取值")
			}
			return nil
		}
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			return paramErr("文本条件需要一个非空的比较值")
		}
		return nil
	}
	// 数值的 eq / ne
	if _, err := singleNumberOperand(values, op); err != nil {
		return paramErr(err.Error())
	}
	return nil
}

// singleNumberOperand 校验「恰好一个可解析为数值的操作数」，返回其错误（nil 表示通过）。
func singleNumberOperand(values []string, op string) (float64, error) {
	if len(values) != 1 {
		return 0, fmt.Errorf("比较符 %s 需要且只需要一个操作数", op)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(values[0]), 64)
	if err != nil {
		return 0, fmt.Errorf("比较符 %s 的操作数必须是数值", op)
	}
	return v, nil
}

// isBoolLiteral 判断是否为可识别的布尔字面量（与 alarm 侧的归一化一致）。
func isBoolLiteral(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "0", "true", "false":
		return true
	}
	return false
}

// nonEmptyValues 去掉空白项后的操作数（保持原顺序）。
func nonEmptyValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// marshalThresholdValues 把操作数序列化成落库用的 JSON 数组字符串。
func marshalThresholdValues(values []string) string {
	clean := nonEmptyValues(values)
	if len(clean) == 0 {
		return ""
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return "" // []string 不会失败；真失败了退回空串（等价于未配置）
	}
	return string(raw)
}

// UpdateDeviceAddress 更新设备地址
func (s *DeviceAddressService) UpdateDeviceAddress(ctx context.Context, req *dto.UpdateDeviceAddressDTO) error {
	db := s.sqliteDB.WithContext(ctx)

	// 检查设备地址是否存在
	var existing po.DeviceAddress
	id := req.ID
	if err := db.Where("id = ?", id).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return appError.NewAppErrorCtx(enums.DeviceAddressNotExistsEnum.GetCode(), enums.DeviceAddressNotExistsEnum.GetMessage(), enums.DeviceAddressNotExistsEnum.GetMsgKey())
		}
		return err
	}

	// 检查名称是否被占用（修改名称时）
	if req.Name != "" && req.Name != existing.Name {
		var dup po.DeviceAddress
		if err := db.Where("device_id = ? AND name = ? AND id != ?", existing.DeviceID, req.Name, id).First(&dup).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if dup.ID != "" {
			return appError.NewAppErrorCtx(enums.DeviceAddressExistsEnum.GetCode(), enums.DeviceAddressExistsEnum.GetMessage(), enums.DeviceAddressExistsEnum.GetMsgKey())
		}
	}

	updates := make(map[string]interface{})
	if req.Name != "" {
		updates["name"] = req.Name
	}
	// label 为必填字段，binding 已保证非空，始终随更新写入
	updates["label"] = req.Label
	if req.CommonDataType != "" {
		// 通用类型名 → 协议内部类型名，两者同步更新
		var device po.Device
		if err := db.Where("id = ?", existing.DeviceID).First(&device).Error; err != nil {
			return err
		}
		internal, err := s.resolveDataType(s.getProtocolName(ctx, device.ProtocolID), req.CommonDataType)
		if err != nil {
			return err
		}
		updates["common_data_type"] = req.CommonDataType
		updates["data_type"] = internal
	} else if req.DataType != "" {
		// 兼容旧请求：dataType 视为通用类型名，映射后同步更新；
		// 无法映射时报错，不再直接存内部名（保证 data_type 恒为有效内部名）。
		var device po.Device
		if err := db.Where("id = ?", existing.DeviceID).First(&device).Error; err != nil {
			return err
		}
		internal, err := s.resolveDataType(s.getProtocolName(ctx, device.ProtocolID), req.DataType)
		if err != nil {
			return err
		}
		updates["common_data_type"] = req.DataType
		updates["data_type"] = internal
	}
	if req.RwPermission != "" {
		updates["rw_permission"] = req.RwPermission
	}
	if req.ScanFrequency > 0 {
		updates["scan_frequency"] = req.ScanFrequency
	}
	updates["description"] = req.Description
	if req.Status != nil {
		updates["status"] = *req.Status
	}

	// 报警条件：整个对象省略 = 不动（见 dto.DeviceAddressThresholdDTO）。传了就先校验再整份覆盖。
	thresholdInert := false
	if req.Threshold != nil {
		enabled, op, values := thresholdFields(req.Threshold)
		// Kind 按更新后的生效类型判断：本次若一起改了类型就用新类型，否则用库里现有的。
		internalType := existing.DataType
		if v, ok := updates["data_type"]; ok {
			internalType = v.(string)
		}
		var dev po.Device
		if err := db.Where("id = ?", existing.DeviceID).First(&dev).Error; err != nil {
			return err
		}
		protocolName := s.getProtocolName(ctx, dev.ProtocolID)
		if err := validateAddressThreshold(enabled, op, values, kindOfAddressType(protocolName, internalType)); err != nil {
			return err
		}
		stored := marshalThresholdValues(values)
		updates["threshold_enabled"] = boolToInt(enabled)
		updates["threshold_op"] = op
		updates["threshold_value"] = stored
		thresholdInert = !enabled || op == "" || stored == ""
	}

	if len(updates) == 0 {
		return nil
	}
	updates["updated_at"] = time.Now().Format("2006-01-02 15:04:05")

	if err := db.Model(&po.DeviceAddress{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return err
	}
	// 改成空配置后采集侧不再发任何信号（见 collector.PointThreshold.Inert），
	// 活跃超限报警若不显式清掉就永久挂在"未恢复"上。只清超限那一类 ——
	// 改阈值不该顺手清掉同一点位无关的采集质量报警。失败只记日志（收尾动作，不回滚配置）。
	if thresholdInert && s.alarmService != nil {
		if err := s.alarmService.ClearPointThresholdAlarm(ctx, id); err != nil {
			logger.Warn("deviceAddress: clear threshold alarm for address %s failed: %v", id, err)
		}
	}
	return nil
}

// DeleteDeviceAddress 删除设备地址。
// 删除后清除该点位的活跃质量报警（历史保留，尽力而为）——
// 点位没了就不会再有采集信号来恢复它，不清就会永久挂在"未恢复"上。
func (s *DeviceAddressService) DeleteDeviceAddress(ctx context.Context, id string) error {
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).Delete(&po.DeviceAddress{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return appError.NewAppErrorCtx(enums.DeviceAddressNotExistsEnum.GetCode(), enums.DeviceAddressNotExistsEnum.GetMessage(), enums.DeviceAddressNotExistsEnum.GetMsgKey())
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.clearPointAlarms(ctx, []string{id})
	return nil
}

// BatchDeleteDeviceAddress 批量删除设备地址，返回实际删除的行数。
//
// 一条语句删完，而不是循环调用 DeleteDeviceAddress：单条删除在 id 不存在时会报错
// 并回滚，而批量场景下"某个地址刚被别人删掉"是常态，用它会让**整批都删不掉**。
// 所以这里不做逐条存在性校验，也不因个别 id 缺失而失败 —— 删了多少就说多少，
// 由调用方决定怎么提示（见 vo.BatchDeleteAddressResultVO）。
//
// device_id 参与 WHERE：只删这台设备名下的地址，见 BatchDeleteDeviceAddressDTO 的说明。
// 删除后同样清除这些点位的活跃质量报警（与单条删除一致）。
func (s *DeviceAddressService) BatchDeleteDeviceAddress(ctx context.Context, deviceID string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var deleted int64
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("device_id = ? AND id IN ?", deviceID, ids).Delete(&po.DeviceAddress{})
		if result.Error != nil {
			return result.Error
		}
		deleted = result.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.clearPointAlarms(ctx, ids)
	return deleted, nil
}

// clearPointAlarms 清除一批点位的活跃质量报警（历史保留，尽力而为）。
// 失败只记日志：清理是删除操作的收尾，不该让它把"点位已删掉"这个既成事实回滚。
func (s *DeviceAddressService) clearPointAlarms(ctx context.Context, ids []string) {
	if s.alarmService == nil {
		return
	}
	for _, id := range ids {
		if err := s.alarmService.ClearPointAlarm(ctx, id); err != nil {
			logger.Warn("deviceAddress: clear quality alarm for deleted address %s failed: %v", id, err)
		}
	}
}

// GetDeviceAddressByID 根据ID查询设备地址
func (s *DeviceAddressService) GetDeviceAddressByID(ctx context.Context, id string) (*vo.DeviceAddressVO, error) {
	var addr po.DeviceAddress
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&addr).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(enums.DeviceAddressNotExistsEnum.GetCode(), enums.DeviceAddressNotExistsEnum.GetMessage(), enums.DeviceAddressNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}

	out := addressVO(addr)
	return &out, nil
}

// PageDeviceAddress 设备地址分页查询（支持设备ID过滤 + 名称模糊检索）
func (s *DeviceAddressService) PageDeviceAddress(ctx context.Context, req *dto.PageDeviceAddressDTO) (*response.PageVo[vo.DeviceAddressVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.DeviceAddress{})

	if req.DeviceID != "" {
		db = db.Where("device_id = ?", req.DeviceID)
	}
	if req.Name != "" {
		db = db.Where("name LIKE ?", "%"+req.Name+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var addrs []po.DeviceAddress
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).Order("created_at DESC").Find(&addrs).Error; err != nil {
		return nil, err
	}

	records := make([]vo.DeviceAddressVO, len(addrs))
	for i, a := range addrs {
		records[i] = addressVO(a)
	}

	return &response.PageVo[vo.DeviceAddressVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
}

// GetAddressesByIDs 根据设备ID数组 + 点位ID数组批量查询点位（仅取 id/device_id/label 三列）。
// deviceIDs 作归属过滤：不属于这些设备的点位视为不存在。库中不存在的ID不出现在结果里；
// 任一列表为空直接返回空切片，不访问数据库。
func (s *DeviceAddressService) GetAddressesByIDs(ctx context.Context, deviceIDs, addressIDs []string) ([]po.DeviceAddress, error) {
	if len(deviceIDs) == 0 || len(addressIDs) == 0 {
		return nil, nil
	}
	var addrs []po.DeviceAddress
	if err := s.sqliteDB.WithContext(ctx).Select("id", "device_id", "label").
		Where("device_id IN ? AND id IN ?", deviceIDs, addressIDs).Find(&addrs).Error; err != nil {
		return nil, err
	}
	return addrs, nil
}

// ListAddressesByDeviceIDs 批量取这批设备名下的全部点位（供开放接口分批同步一次查完）。
//
// 一条 `device_id IN (…)` 查完，走已有的 idx_device_address_device_name
// （device_id 是前导列）；**不逐台设备查**，否则同步一台设备多一次查询。
// deviceIDs 为空直接返回 nil 且不访问数据库（照 GetAddressesByIDs 的先例）。
// 返回顺序按 (device_id, id)，由调用方自行分组。
func (s *DeviceAddressService) ListAddressesByDeviceIDs(ctx context.Context, deviceIDs []string) ([]vo.DeviceAddressVO, error) {
	if len(deviceIDs) == 0 {
		return nil, nil
	}
	var addrs []po.DeviceAddress
	if err := s.sqliteDB.WithContext(ctx).
		Where("device_id IN ?", deviceIDs).
		Order("device_id ASC, id ASC").Find(&addrs).Error; err != nil {
		return nil, err
	}
	records := make([]vo.DeviceAddressVO, len(addrs))
	for i, a := range addrs {
		records[i] = addressVO(a)
	}
	return records, nil
}

// addressVO 把点位行映射为 VO。三处查询出口（单条 / 4.4 分页 / 4.5 同步）共用，
// 避免字段增减时只改到其中一处。对外 VO 会在 OpenApiService 里进一步剔除阈值等内部列。
func addressVO(a po.DeviceAddress) vo.DeviceAddressVO {
	return vo.DeviceAddressVO{
		ID:               a.ID,
		DeviceID:         a.DeviceID,
		Name:             a.Name,
		Label:            a.Label,
		CommonDataType:   a.CommonDataType,
		DataType:         a.DataType,
		RwPermission:     a.RwPermission,
		ScanFrequency:    a.ScanFrequency,
		Description:      a.Description,
		Status:           a.Status,
		ThresholdEnabled: a.ThresholdEnabled,
		ThresholdOp:      a.ThresholdOp,
		ThresholdValue:   a.ThresholdValue,
		CreatedAt:        a.CreatedAt,
		UpdatedAt:        a.UpdatedAt,
	}
}

// getProtocolName 根据协议 ID 查询协议名称（iot_protocol.name），
// 协议不存在或查询失败时返回空串。
func (s *DeviceAddressService) getProtocolName(ctx context.Context, protocolID string) string {
	if protocolID == "" {
		return ""
	}
	var protocol po.IotProtocol
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", protocolID).First(&protocol).Error; err != nil {
		return ""
	}
	return protocol.Name
}

// resolveDataType 根据设备所属协议，将通用数据类型名映射为协议内部类型名。
// 协议不存在或该协议不支持该通用类型时返回错误。
func (s *DeviceAddressService) resolveDataType(protocolName, commonType string) (string, error) {
	internal, ok := driver.LookupDataType(protocolName, commonType)
	if !ok {
		return "", appError.NewAppErrorCtx(enums.DeviceAddressDataTypeErrEnum.GetCode(), enums.DeviceAddressDataTypeErrEnum.GetMessage(), enums.DeviceAddressDataTypeErrEnum.GetMsgKey())
	}
	return internal, nil
}

// ==================== Excel 批量导入设备地址 ====================

// ImportDeviceAddresses 从 Excel(.xlsx) 批量导入指定设备下的地址点位。
// 模板六列：名称 / 标签 / 通用数据类型 / 读写权限 / 扫描频率 / 描述。
// 按当前设备的协议将通用数据类型映射为协议内部类型；字段不合法或协议不支持该类型时跳过该行，
// 名称重复（库内或文件内）跳过该行；其余行部分导入并返回汇总，供前端展示失败明细。
func (s *DeviceAddressService) ImportDeviceAddresses(ctx context.Context, deviceID string, data []byte) (*vo.ExcelImportResultVO, error) {
	// 设备必须存在（决定协议与数据类型映射）
	var device po.Device
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", deviceID).First(&device).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(enums.DeviceNotExistsEnum.GetCode(), enums.DeviceNotExistsEnum.GetMessage(), enums.DeviceNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}
	protocolName := s.getProtocolName(ctx, device.ProtocolID)

	// 预加载该设备已有地址名，用于去重
	var names []string
	if err := s.sqliteDB.WithContext(ctx).Model(&po.DeviceAddress{}).Where("device_id = ?", deviceID).Pluck("name", &names).Error; err != nil {
		return nil, err
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	return excelutil.Import(ctx, s.sqliteDB, data, excelutil.Options[po.DeviceAddress]{
		Headers:      []string{"名称", "标签", "通用数据类型", "读写权限", "扫描频率", "描述"},
		ExistingKeys: names,
		Cols:         6,
		DupMsg:       enums.DeviceAddressExistsEnum.GetMessage(),
		Parse: func(row []string) (*po.DeviceAddress, string, string) {
			name := excelutil.Cell(row, 0)
			label := excelutil.Cell(row, 1)
			commonType := excelutil.Cell(row, 2)
			rwPermission := excelutil.Cell(row, 3)
			scanFreq := excelutil.Cell(row, 4)
			desc := excelutil.Cell(row, 5)
			reason, frequency := validateAddressImportRow(name, label, commonType, rwPermission, scanFreq)
			if reason != "" {
				return nil, name, reason
			}
			// 通用数据类型 → 协议内部类型（与单条创建走同一套映射）
			internal, ok := driver.LookupDataType(protocolName, commonType)
			if !ok {
				return nil, name, fmt.Sprintf("协议不支持该数据类型：%s", commonType)
			}
			return &po.DeviceAddress{
				DeviceID:       deviceID,
				Name:           name,
				Label:          label,
				CommonDataType: commonType,
				DataType:       internal,
				RwPermission:   rwPermission,
				ScanFrequency:  frequency,
				Description:    desc,
				Status:         1,
				CreatedAt:      now,
				UpdatedAt:      now,
			}, name, ""
		},
	})
}

// validateAddressImportRow 校验单行地址导入数据（纯函数，便于单测；去重由 excelutil.Import 统一处理）。
// 返回 (失败原因, 扫描频率)；reason 为 "" 表示通过，frequency 为解析后的扫描频率
// （空/缺省或 <=0 时按默认 1000 处理，与单条创建逻辑一致）。
func validateAddressImportRow(name, label, commonType, rwPermission, scanFreq string) (string, int) {
	if name == "" {
		return "名称为空", 0
	}
	if utf8.RuneCountInString(name) > 100 {
		return "名称不能超过100个字符", 0
	}
	if label == "" {
		return "标签为空", 0
	}
	if utf8.RuneCountInString(label) > 100 {
		return "标签不能超过100个字符", 0
	}
	if commonType == "" {
		return "通用数据类型为空", 0
	}
	if rwPermission != "R" && rwPermission != "W" && rwPermission != "RW" {
		return "读写权限须为 R/W/RW", 0
	}
	if scanFreq == "" {
		return "", 1000
	}
	freq, err := strconv.Atoi(scanFreq)
	if err != nil {
		return "扫描频率格式不正确", 0
	}
	if freq <= 0 {
		return "", 1000
	}
	return "", freq
}

// GenerateDeviceAddressImportTemplate 生成设备地址导入模板（.xlsx）：
// 表头「名称/标签/通用数据类型/读写权限/扫描频率/描述」+ 示例行 + 冻结首行。
func (s *DeviceAddressService) GenerateDeviceAddressImportTemplate() (*bytes.Buffer, error) {
	return excelutil.BuildTemplate(
		[]string{"名称", "标签", "通用数据类型", "读写权限", "扫描频率", "描述"},
		[]string{"D0", "车间温度", "Float", "R", "1000", "示例数据，可删除或覆盖"},
		[]float64{24, 24, 18, 14, 14, 36},
	)
}
