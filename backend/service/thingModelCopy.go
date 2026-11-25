// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"iot-gateway/enums"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
)

const (
	// thingModelCopySuffix 副本的标识/名称后缀，与 MQTT 订阅复制同一约定（-copy）。
	thingModelCopySuffix = "-copy"
	// thingModelCopyMaxSeq 连续复制时最多尝试多少个序号（-copy、-copy2 …）。
	thingModelCopyMaxSeq = 100
	// 与 CreateThingModelDTO / UpdateThingModelDTO 的 binding 上限保持一致。
	thingModelCodeMaxLen = 64
	thingModelNameMaxLen = 100
)

// CopyThingModel 复制物模型为一条新记录。
//
// 只复制基础信息（标识/名称/分类/版本/描述/状态），**不复制属性**：属性绑定的
// 设备点位是 1:1 的，副本沿用同一批绑定会直接违反该约束；空模型的属性由用户
// 自行添加。分类按原值保留（开放接口可能已下发平台自定义分类，复制不该因为
// 它不在本地内置枚举里而失败）。
//
// 标识与名称各有唯一索引，副本自动追加 -copy 后缀并逐个查重（-copy2、-copy3…），
// 因此同一模型可以连续复制多次。
func (s *ThingModelService) CopyThingModel(ctx context.Context, id string) (*vo.ThingModelCopyVO, error) {
	var src po.ThingModel
	if err := s.sqliteDB.WithContext(ctx).Where("id = ?", id).First(&src).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, thingModelErr(enums.ThingModelNotExistsEnum)
		}
		return nil, err
	}

	now := nowString()
	clone := src
	clone.ID = "" // 交给 BeforeCreate 生成 UUIDv7
	clone.CreatedAt = now
	clone.UpdatedAt = now

	// 查重与插入放同一事务：SQLite 单写者会把两者串行化，避免并发复制撞名。
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		code, name, err := uniqueThingModelCopyNames(tx, src.Code, src.Name)
		if err != nil {
			return err
		}
		clone.Code = code
		clone.Name = name
		if err := tx.Create(&clone).Error; err != nil {
			return err
		}
		// status 的 struct tag 带 default:1：停用副本（0）在 Create 时会被省略、
		// 落成数据库默认值 1，插入后显式写回（同 mqttSubscription 副本的处理）。
		if src.Status == 0 {
			return tx.Model(&po.ThingModel{}).Where("id = ?", clone.ID).
				Update("status", 0).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &vo.ThingModelCopyVO{ID: clone.ID, Code: clone.Code, Name: clone.Name}, nil
}

// uniqueThingModelCopyNames 依次尝试「原名-copy」「原名-copy2」…，返回第一组
// 同时未被占用的标识与名称。
//
// 标识与名称共用同一个序号：任一候选撞车都整体跳到下一个序号，这样同一模型
// 反复复制时两侧序号始终一致，人工对照不会错位。
func uniqueThingModelCopyNames(tx *gorm.DB, code, name string) (string, string, error) {
	for seq := 1; seq <= thingModelCopyMaxSeq; seq++ {
		suffix := thingModelCopySuffix
		if seq > 1 {
			suffix = fmt.Sprintf("%s%d", thingModelCopySuffix, seq)
		}
		candidateCode := truncateCopyBase(code, thingModelCodeMaxLen-utf8.RuneCountInString(suffix)) + suffix
		candidateName := truncateCopyBase(name, thingModelNameMaxLen-utf8.RuneCountInString(suffix)) + suffix

		var count int64
		if err := tx.Model(&po.ThingModel{}).
			Where("code = ? OR name = ?", candidateCode, candidateName).
			Count(&count).Error; err != nil {
			return "", "", err
		}
		if count == 0 {
			return candidateCode, candidateName, nil
		}
	}
	return "", "", fmt.Errorf("无法为 %q 生成不重复的副本标识与名称", code)
}

// truncateCopyBase 把原名截断到 max 个字符，给后缀腾出位置，保证拼上后缀后
// 仍满足标识/名称的长度上限。按字符（rune）截断而不是按字节：名称可能含中文，
// 按字节切会切出半个 UTF-8 序列。
func truncateCopyBase(base string, max int) string {
	runes := []rune(strings.TrimSpace(base))
	if max < 1 {
		max = 1
	}
	if len(runes) > max {
		runes = runes[:max]
	}
	return string(runes)
}
