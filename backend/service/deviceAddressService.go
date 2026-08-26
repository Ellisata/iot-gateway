package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/driver"
	"iot-gateway/enums"
	"iot-gateway/excelutil"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
)

// DeviceAddressService 设备地址服务
type DeviceAddressService struct {
	sqliteDB *gorm.DB
}

// NewDeviceAddressService 创建设备地址服务
func NewDeviceAddressService(sqliteDB *gorm.DB) *DeviceAddressService {
	return &DeviceAddressService{sqliteDB: sqliteDB}
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

	now := time.Now().Format("2006-01-02 15:04:05")
	addr := &po.DeviceAddress{
		DeviceID:       req.DeviceID,
		Name:           req.Name,
		Label:          req.Label,
		CommonDataType: common,
		DataType:       internal,
		RwPermission:   req.RwPermission,
		ScanFrequency:  req.ScanFrequency,
		Description:    req.Description,
		Status:         1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if addr.ScanFrequency <= 0 {
		addr.ScanFrequency = 1000
	}

	if err := db.Create(addr).Error; err != nil {
		return err
	}
	return nil
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
	if len(updates) == 0 {
		return nil
	}
	updates["updated_at"] = time.Now().Format("2006-01-02 15:04:05")

	return db.Model(&po.DeviceAddress{}).Where("id = ?", id).Updates(updates).Error
}

// DeleteDeviceAddress 删除设备地址
func (s *DeviceAddressService) DeleteDeviceAddress(ctx context.Context, id string) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).Delete(&po.DeviceAddress{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return appError.NewAppErrorCtx(enums.DeviceAddressNotExistsEnum.GetCode(), enums.DeviceAddressNotExistsEnum.GetMessage(), enums.DeviceAddressNotExistsEnum.GetMsgKey())
		}
		return nil
	})
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

	return &vo.DeviceAddressVO{
		ID:             addr.ID,
		DeviceID:       addr.DeviceID,
		Name:           addr.Name,
		Label:          addr.Label,
		CommonDataType: addr.CommonDataType,
		DataType:       addr.DataType,
		RwPermission:   addr.RwPermission,
		ScanFrequency:  addr.ScanFrequency,
		Description:    addr.Description,
		Status:         addr.Status,
		CreatedAt:      addr.CreatedAt,
		UpdatedAt:      addr.UpdatedAt,
	}, nil
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
		records[i] = vo.DeviceAddressVO{
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

	return &response.PageVo[vo.DeviceAddressVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: records,
	}, nil
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
