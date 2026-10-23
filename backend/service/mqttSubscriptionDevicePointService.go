// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
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
	"iot-gateway/subscribe"
)

const devicePointTimeLayout = "2006-01-02 15:04:05"

// 导入模板列：前 5 列强校验，与订阅映射里的身份字段 + 元信息字段一一对应。
var devicePointImportHeaders = []string{"设备ID", "点位ID", "设备名称", "点位标签", "类型"}

// 手工维护允许的类型类别。空值表示"未知"，自动上报的推断值不受此限制。
var devicePointKinds = map[string]bool{
	driver.KindBool:   true,
	driver.KindInt:    true,
	driver.KindUInt:   true,
	driver.KindFloat:  true,
	driver.KindString: true,
	driver.KindTime:   true,
}

// devicePointFields 校验并归一后的可入库字段。
type devicePointFields struct {
	deviceID  string
	addressID string
	name      string
	label     string
	kind      string
}

// normalizeDevicePointFields 去空白、校验长度与类型类别。
//
// existingKind 只在编辑时传入：自动上报可能留下任意类型字符串（映射里写什么就是什么），
// 若强行要求它属于六种标准类别，用户不动类型字段也会保存失败。返回 reason 为空表示通过。
func normalizeDevicePointFields(deviceID, addressID, name, label, kind, existingKind string) (devicePointFields, string) {
	f := devicePointFields{
		deviceID:  strings.TrimSpace(deviceID),
		addressID: strings.TrimSpace(addressID),
		name:      strings.TrimSpace(name),
		label:     strings.TrimSpace(label),
		kind:      strings.ToLower(strings.TrimSpace(kind)),
	}
	switch {
	case f.deviceID == "":
		return f, "设备ID为空"
	case utf8.RuneCountInString(f.deviceID) > 128:
		return f, "设备ID不能超过128个字符"
	case f.addressID == "":
		return f, "点位ID为空"
	case utf8.RuneCountInString(f.addressID) > 128:
		return f, "点位ID不能超过128个字符"
	case utf8.RuneCountInString(f.name) > 100:
		return f, "设备名称不能超过100个字符"
	case utf8.RuneCountInString(f.label) > 100:
		return f, "点位标签不能超过100个字符"
	}
	if f.kind != "" && !devicePointKinds[f.kind] &&
		f.kind != strings.ToLower(strings.TrimSpace(existingKind)) {
		return f, "类型须为 bool/int/uint/float/string/time 之一"
	}
	return f, ""
}

func newDevicePointRow(f devicePointFields, now string) po.MqttSubscriptionDevicePoint {
	return po.MqttSubscriptionDevicePoint{
		DeviceID:           f.deviceID,
		DeviceAddressID:    f.addressID,
		DeviceName:         f.name,
		DeviceAddressLabel: f.label,
		Kind:               f.kind,
		Source:             po.MqttSubscriptionDevicePointSourceManual,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func paramValid(reason string) error {
	return appError.NewAppError(enums.ParamValidEnum.GetCode(), reason)
}

// CreateMqttSubscriptionDevicePoint 手工新增一条订阅设备/点位。
func (s *MqttSubscriptionService) CreateMqttSubscriptionDevicePoint(ctx context.Context, req *dto.CreateMqttSubscriptionDevicePointDTO) error {
	f, reason := normalizeDevicePointFields(req.DeviceID, req.DeviceAddressID,
		req.DeviceName, req.DeviceAddressLabel, req.Kind, "")
	if reason != "" {
		return paramValid(reason)
	}

	row := newDevicePointRow(f, time.Now().Format(devicePointTimeLayout))
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.MqttSubscriptionDevicePoint
		err := tx.Where("device_id = ? AND device_address_id = ?", f.deviceID, f.addressID).
			First(&existing).Error
		if err == nil {
			return appError.NewAppErrorCtx(enums.MqttSubscriptionDevicePointExistsEnum.GetCode(),
				enums.MqttSubscriptionDevicePointExistsEnum.GetMessage(),
				enums.MqttSubscriptionDevicePointExistsEnum.GetMsgKey())
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&row).Error
	})
}

// UpdateMqttSubscriptionDevicePoint 编辑元信息并把该行标记为手工维护。
//
// 自然键只用于定位；提交值（含空串）无条件覆盖，表示用户明确要清空。
func (s *MqttSubscriptionService) UpdateMqttSubscriptionDevicePoint(ctx context.Context, req *dto.UpdateMqttSubscriptionDevicePointDTO) error {
	var existing po.MqttSubscriptionDevicePoint
	err := s.sqliteDB.WithContext(ctx).
		Where("device_id = ? AND device_address_id = ?", strings.TrimSpace(req.DeviceID), strings.TrimSpace(req.DeviceAddressID)).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError.NewAppErrorCtx(enums.MqttSubscriptionDevicePointNotExistsEnum.GetCode(),
			enums.MqttSubscriptionDevicePointNotExistsEnum.GetMessage(),
			enums.MqttSubscriptionDevicePointNotExistsEnum.GetMsgKey())
	}
	if err != nil {
		return err
	}

	f, reason := normalizeDevicePointFields(req.DeviceID, req.DeviceAddressID,
		req.DeviceName, req.DeviceAddressLabel, req.Kind, existing.Kind)
	if reason != "" {
		return paramValid(reason)
	}

	return s.sqliteDB.WithContext(ctx).Model(&po.MqttSubscriptionDevicePoint{}).
		Where("device_id = ? AND device_address_id = ?", f.deviceID, f.addressID).
		Updates(map[string]interface{}{
			"device_name":          f.name,
			"device_address_label": f.label,
			"kind":                 f.kind,
			"source":               po.MqttSubscriptionDevicePointSourceManual,
			"updated_at":           time.Now().Format(devicePointTimeLayout),
		}).Error
}

// DeleteMqttSubscriptionDevicePoint 删除一条手工维护的订阅设备/点位。
//
// 只允许删 source=manual 的行：auto 行由订阅报文推导而来，删掉之后下一条报文
// 就会把它重新收录，删除对自动行没有任何持久效果。两种拒绝分开返回，是因为
// 下一步动作不同：不存在说明本来就没有这条点位；auto 行说明它不受手工维护，
// 想改成手工维护应该去编辑（编辑会把 source 置为 manual）。
func (s *MqttSubscriptionService) DeleteMqttSubscriptionDevicePoint(ctx context.Context, req *dto.DeleteMqttSubscriptionDevicePointDTO) error {
	deviceID := strings.TrimSpace(req.DeviceID)
	addressID := strings.TrimSpace(req.DeviceAddressID)

	var existing po.MqttSubscriptionDevicePoint
	err := s.sqliteDB.WithContext(ctx).
		Where("device_id = ? AND device_address_id = ?", deviceID, addressID).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError.NewAppErrorCtx(enums.MqttSubscriptionDevicePointNotExistsEnum.GetCode(),
			enums.MqttSubscriptionDevicePointNotExistsEnum.GetMessage(),
			enums.MqttSubscriptionDevicePointNotExistsEnum.GetMsgKey())
	}
	if err != nil {
		return err
	}
	if existing.Source != po.MqttSubscriptionDevicePointSourceManual {
		return appError.NewAppErrorCtx(enums.MqttSubscriptionDevicePointAutoNotDeletableEnum.GetCode(),
			enums.MqttSubscriptionDevicePointAutoNotDeletableEnum.GetMessage(),
			enums.MqttSubscriptionDevicePointAutoNotDeletableEnum.GetMsgKey())
	}

	if err := s.sqliteDB.WithContext(ctx).
		Where("device_id = ? AND device_address_id = ?", deviceID, addressID).
		Delete(&po.MqttSubscriptionDevicePoint{}).Error; err != nil {
		return err
	}
	// 删掉的是"手工锁"，该点位应回到自动收录 —— 作废指纹后下一条报文就会重新收录。
	s.invalidateDevicePoints([]subscribe.DevicePointKey{{DeviceID: deviceID, DeviceAddressID: addressID}})
	return nil
}

// invalidateDevicePoints 作废落库器里这些点位的元信息指纹，使下一条报文重新
// 收录 / 同步它们。manager 在单元测试里可能为 nil。
func (s *MqttSubscriptionService) invalidateDevicePoints(keys []subscribe.DevicePointKey) {
	if s.manager == nil || len(keys) == 0 {
		return
	}
	s.manager.InvalidateDevicePoints(keys)
}

// RestoreMqttSubscriptionDevicePointAuto 把手工艺维护的行交还给自动收录。
//
// 只改 source，元信息原样保留；随后作废落库器里该点位的元信息指纹，使下一条
// 报文到达时重新 upsert、按报文值覆盖元信息（不作废的话，指纹未变时 Record
// 会一直跳过，要等元信息变化或网关重启才生效）。已是 auto 的行幂等成功。
func (s *MqttSubscriptionService) RestoreMqttSubscriptionDevicePointAuto(ctx context.Context, req *dto.RestoreMqttSubscriptionDevicePointAutoDTO) error {
	deviceID := strings.TrimSpace(req.DeviceID)
	addressID := strings.TrimSpace(req.DeviceAddressID)

	var existing po.MqttSubscriptionDevicePoint
	err := s.sqliteDB.WithContext(ctx).
		Where("device_id = ? AND device_address_id = ?", deviceID, addressID).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError.NewAppErrorCtx(enums.MqttSubscriptionDevicePointNotExistsEnum.GetCode(),
			enums.MqttSubscriptionDevicePointNotExistsEnum.GetMessage(),
			enums.MqttSubscriptionDevicePointNotExistsEnum.GetMsgKey())
	}
	if err != nil {
		return err
	}
	if existing.Source == po.MqttSubscriptionDevicePointSourceAuto {
		return nil
	}

	if err := s.sqliteDB.WithContext(ctx).Model(&po.MqttSubscriptionDevicePoint{}).
		Where("device_id = ? AND device_address_id = ?", deviceID, addressID).
		Update("source", po.MqttSubscriptionDevicePointSourceAuto).Error; err != nil {
		return err
	}
	// 指纹没变时 Record 不会入队；作废它，下一条报文才会重新按报文值覆盖元信息。
	s.invalidateDevicePoints([]subscribe.DevicePointKey{{DeviceID: deviceID, DeviceAddressID: addressID}})
	return nil
}

// BatchDeleteMqttSubscriptionDevicePoint 批量删除手工维护的订阅设备/点位，
// 返回实际删除的行数。
//
// 与单条删除不同，个别条目不存在、或已变回 source=auto 不算错误：批量场景下
// "某个点位刚被别人删掉 / 刚被报文重新收录" 是常态，据此让整批失败只会让
// 用户反复重试都删不掉。只删 source=manual 的约束仍逐条生效，删了多少就说多少，
// 由调用方决定怎么提示（见 vo.BatchDeleteMqttSubscriptionDevicePointResultVO）。
func (s *MqttSubscriptionService) BatchDeleteMqttSubscriptionDevicePoint(ctx context.Context, items []dto.DeleteMqttSubscriptionDevicePointDTO) (int64, error) {
	if len(items) == 0 {
		return 0, nil
	}

	var deleted int64
	deletedKeys := make([]subscribe.DevicePointKey, 0, len(items))
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range items {
			deviceID := strings.TrimSpace(item.DeviceID)
			addressID := strings.TrimSpace(item.DeviceAddressID)
			res := tx.Where("device_id = ? AND device_address_id = ? AND source = ?",
				deviceID, addressID, po.MqttSubscriptionDevicePointSourceManual).
				Delete(&po.MqttSubscriptionDevicePoint{})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected > 0 {
				deleted += res.RowsAffected
				deletedKeys = append(deletedKeys, subscribe.DevicePointKey{
					DeviceID: deviceID, DeviceAddressID: addressID,
				})
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	// 事务提交后再作废指纹：事务内改库时落库协程拿不到写连接，作废也不会有报文能写进来。
	s.invalidateDevicePoints(deletedKeys)
	return deleted, nil
}

// ImportMqttSubscriptionDevicePoints 从 Excel(.xlsx) 批量导入/更新订阅设备与点位。
//
// 按自然键 upsert：已存在的行覆盖元信息并标记为 manual，不存在则新增。
// 单行校验/入库失败只记错误继续，汇总结果交给前端展示（与设备、设备地址导入一致）。
func (s *MqttSubscriptionService) ImportMqttSubscriptionDevicePoints(ctx context.Context, data []byte) (*vo.ExcelImportResultVO, error) {
	rows, err := excelutil.OpenRows(data)
	if err != nil {
		return nil, err
	}
	result := &vo.ExcelImportResultVO{Errors: []vo.ExcelImportErrorVO{}}
	if len(rows) < 2 {
		return result, nil
	}
	if err := excelutil.CheckHeader(rows, devicePointImportHeaders...); err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	now := time.Now().Format(devicePointTimeLayout)
	_ = s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := 1; i < len(rows); i++ {
			excelRow := i + 1 // 含表头行号
			row := rows[i]
			deviceID := excelutil.Cell(row, 0)
			addressID := excelutil.Cell(row, 1)
			name := excelutil.Cell(row, 2)
			label := excelutil.Cell(row, 3)
			kind := excelutil.Cell(row, 4)
			if deviceID == "" && addressID == "" && name == "" && label == "" && kind == "" {
				continue
			}
			result.Total++

			f, reason := normalizeDevicePointFields(deviceID, addressID, name, label, kind, "")
			displayName := strings.TrimSpace(deviceID) + " / " + strings.TrimSpace(addressID)
			if reason != "" {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{Row: excelRow, Name: displayName, Reason: reason})
				continue
			}
			key := f.deviceID + "\x00" + f.addressID
			if seen[key] {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{
					Row: excelRow, Name: displayName, Reason: "文件内设备ID+点位ID重复",
				})
				continue
			}
			if err := subscribe.UpsertManualDevicePoints(tx, []po.MqttSubscriptionDevicePoint{
				newDevicePointRow(f, now),
			}); err != nil {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{
					Row: excelRow, Name: displayName, Reason: "入库失败：" + err.Error(),
				})
				continue
			}
			seen[key] = true
			result.Success++
		}
		return nil
	})

	result.Failed = len(result.Errors)
	return result, nil
}

// GenerateMqttSubscriptionDevicePointImportTemplate 生成导入模板（.xlsx）。
func (s *MqttSubscriptionService) GenerateMqttSubscriptionDevicePointImportTemplate() (*bytes.Buffer, error) {
	return excelutil.BuildTemplate(
		devicePointImportHeaders,
		[]string{"d1", "a1", "电表1", "A相电流", "float"},
		[]float64{18, 18, 24, 24, 14},
	)
}
