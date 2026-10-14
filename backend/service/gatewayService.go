// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
)

// GatewayService 网关自身身份服务。
//
// iot_gateway 是**单行表**：整个网关只有一条身份记录，由首次启动的
// seedGatewayIdentity 播种（见 database/sqlite/seedGateway.go）。
// 因此本服务所有读写都不带主键条件，直接取那一行。
type GatewayService struct {
	sqliteDB *gorm.DB
}

// NewGatewayService 创建网关身份服务
func NewGatewayService(sqliteDB *gorm.DB) *GatewayService {
	return &GatewayService{sqliteDB: sqliteDB}
}

// GetGateway 读取网关自身身份
func (s *GatewayService) GetGateway(ctx context.Context) (*vo.GatewayVO, error) {
	var gw po.IotGateway
	if err := s.sqliteDB.WithContext(ctx).First(&gw).Error; err != nil {
		// 行不存在是**合法状态**而不是异常：InitDB 里身份播种失败是刻意非致命的
		// （见 database/sqlite/database.go），此时网关照常运行，只是没有身份行。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, appError.NewAppErrorCtx(
				enums.GatewayNotExistsEnum.GetCode(),
				enums.GatewayNotExistsEnum.GetMessage(),
				enums.GatewayNotExistsEnum.GetMsgKey())
		}
		return nil, err
	}

	return &vo.GatewayVO{
		ID:              gw.ID,
		Name:            gw.Name,
		Port:            gw.Port,
		FirmwareVersion: gw.FirmwareVersion,
		Description:     gw.Description,
		Status:          gw.Status,
		CreatedAt:       gw.CreatedAt,
		UpdatedAt:       gw.UpdatedAt,
	}, nil
}

// UpdateGateway 更新网关名称与描述。
//
// 只写 name / description / updated_at 三列，id 与 firmware_version 原样不动 ——
// 字段少是刻意的，见 dto.UpdateGatewayDTO 的注释。
//
// 这里**不做重名检查**：单行表里唯一可能与之冲突的就是它自己，写一段永远不会命中的
// 查重代码只会让读的人以为这张表可能是多行的。name 上的唯一索引保留着，
// 那是为了挡住「同一个库被两个进程同时初始化」这种情形（同 seedGateway.go）。
func (s *GatewayService) UpdateGateway(ctx context.Context, req *dto.UpdateGatewayDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.IotGateway
		if err := tx.First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(
					enums.GatewayNotExistsEnum.GetCode(),
					enums.GatewayNotExistsEnum.GetMessage(),
					enums.GatewayNotExistsEnum.GetMsgKey())
			}
			return err
		}

		return tx.Model(&po.IotGateway{}).Where("id = ?", existing.ID).Updates(map[string]any{
			"name":        req.Name,
			"description": req.Description,
			"updated_at":  time.Now().Format("2006-01-02 15:04:05"),
		}).Error
	})
}
