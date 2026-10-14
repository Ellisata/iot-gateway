// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/utils"
)

// defaultApiKeyName 默认开放接口密钥的名称，与 database/sqlite 播种时写入的名字
// 必须一致（那边是 seedOpenApiSecret.go 的 defaultOpenApiSecretName）。
//
// 这个名字就是那条记录的身份：删除与改名都按它判定，所以运维改不了它 ——
// 一旦允许改名，守卫就跟着名字一起被改掉了。
const defaultApiKeyName = "default"

// OpenApiSecretService 开放接口密钥服务
type OpenApiSecretService struct {
	sqliteDB *gorm.DB

	mu     sync.RWMutex
	keySet map[string]struct{} // 密钥内存缓存，避免每次鉴权都查库；nil 表示未加载成功，回退查库
}

// NewOpenApiSecretService 创建开放接口密钥服务，并预加载全部密钥到内存
func NewOpenApiSecretService(sqliteDB *gorm.DB) *OpenApiSecretService {
	s := &OpenApiSecretService{sqliteDB: sqliteDB}
	s.refreshKeySet()
	return s
}

// refreshKeySet 全量重载密钥到内存。密钥表数据量小，写入后整体刷新即可，
// 无需逐条增删维护。
func (s *OpenApiSecretService) refreshKeySet() {
	var keys []string
	if err := s.sqliteDB.Model(&po.OpenApiSecret{}).Pluck("`key`", &keys).Error; err != nil {
		logger.Error("open api secret: preload key set failed: %v", err)
		return
	}
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[k] = struct{}{}
	}
	s.mu.Lock()
	s.keySet = set
	s.mu.Unlock()
}

// CreateOpenApiSecret 创建密钥
func (s *OpenApiSecretService) CreateOpenApiSecret(ctx context.Context, req *dto.CreateOpenApiSecretDTO) error {
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 检查名称是否已存在
		var existing po.OpenApiSecret
		if err := tx.Where("name = ?", req.Name).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if existing.ID != "" {
			return appError.NewAppErrorCtx(enums.OpenApiSecretExistsEnum.GetCode(), enums.OpenApiSecretExistsEnum.GetMessage(), enums.OpenApiSecretExistsEnum.GetMsgKey())
		}

		key, err := utils.GenerateSecretKey()
		if err != nil {
			return err
		}
		now := time.Now().Format("2006-01-02 15:04:05")
		record := &po.OpenApiSecret{
			Name:      req.Name,
			Key:       key,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := tx.Create(record).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.refreshKeySet()
	return nil
}

// UpdateOpenApiSecretName 编辑密钥名称
func (s *OpenApiSecretService) UpdateOpenApiSecretName(ctx context.Context, req *dto.UpdateOpenApiSecretNameDTO) error {
	return s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing po.OpenApiSecret
		if err := tx.Where("id = ?", req.ID).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(enums.OpenApiSecretNotExistsEnum.GetCode(), enums.OpenApiSecretNotExistsEnum.GetMessage(), enums.OpenApiSecretNotExistsEnum.GetMsgKey())
			}
			return err
		}

		// 默认密钥不允许改名：名称就是它的身份，改名等于把删除守卫一起改掉
		//（守卫按 defaultApiKeyName 判定，见该常量）。
		if existing.Name == defaultApiKeyName && req.Name != existing.Name {
			return appError.NewAppErrorCtx(enums.OpenApiSecretBuiltinEnum.GetCode(), enums.OpenApiSecretBuiltinEnum.GetMessage(), enums.OpenApiSecretBuiltinEnum.GetMsgKey())
		}

		// 检查名称是否被占用（修改名称时）
		if req.Name != existing.Name {
			var dup po.OpenApiSecret
			if err := tx.Where("name = ? AND id != ?", req.Name, req.ID).First(&dup).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}
			if dup.ID != "" {
				return appError.NewAppErrorCtx(enums.OpenApiSecretExistsEnum.GetCode(), enums.OpenApiSecretExistsEnum.GetMessage(), enums.OpenApiSecretExistsEnum.GetMsgKey())
			}
		}

		return tx.Model(&po.OpenApiSecret{}).Where("id = ?", req.ID).Updates(map[string]any{
			"name":       req.Name,
			"updated_at": time.Now().Format("2006-01-02 15:04:05"),
		}).Error
	})
}

// DeleteOpenApiSecret 删除密钥。默认密钥（名为 default 那一条）不允许删除。
//
// 守卫放在 service 而不是 controller：这条路径有两个入口 —— 带 JWT 的
// /openApiSecret/delete/:id 与**无鉴权**的 /agentApi/openApiSecret/delete/:id，
// 把判定写在上层就得写两遍，漏一处就是一个能被 dsh 一句话删掉的默认密钥。
func (s *OpenApiSecretService) DeleteOpenApiSecret(ctx context.Context, id string) error {
	err := s.sqliteDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先查后删：守卫要按名称判定，而 Delete 的 RowsAffected 只告诉你
		// "删掉了几行"，说不出删掉的是不是默认密钥。
		var existing po.OpenApiSecret
		if err := tx.Where("id = ?", id).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError.NewAppErrorCtx(enums.OpenApiSecretNotExistsEnum.GetCode(), enums.OpenApiSecretNotExistsEnum.GetMessage(), enums.OpenApiSecretNotExistsEnum.GetMsgKey())
			}
			return err
		}
		if existing.Name == defaultApiKeyName {
			return appError.NewAppErrorCtx(enums.OpenApiSecretBuiltinEnum.GetCode(), enums.OpenApiSecretBuiltinEnum.GetMessage(), enums.OpenApiSecretBuiltinEnum.GetMsgKey())
		}
		return tx.Where("id = ?", id).Delete(&po.OpenApiSecret{}).Error
	})
	if err != nil {
		return err
	}
	s.refreshKeySet()
	return nil
}

// ListOpenApiSecret 查询密钥列表（无需分页）
func (s *OpenApiSecretService) ListOpenApiSecret(ctx context.Context) ([]vo.OpenApiSecretVO, error) {
	var records []po.OpenApiSecret
	if err := s.sqliteDB.WithContext(ctx).Order("created_at DESC").Find(&records).Error; err != nil {
		return nil, err
	}

	records2 := make([]vo.OpenApiSecretVO, len(records))
	for i, r := range records {
		records2[i] = vo.OpenApiSecretVO{
			ID:        r.ID,
			Name:      r.Name,
			Key:       r.Key,
			IsDefault: r.Name == defaultApiKeyName,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		}
	}
	return records2, nil
}

// ExistsOpenApiSecretKey 校验密钥的存在性与正确性（开放接口鉴权中间件使用）。
// 命中内存缓存不查库；缓存未加载成功时回退数据库查询，保证可用性。
func (s *OpenApiSecretService) ExistsOpenApiSecretKey(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	keySet := s.keySet
	s.mu.RUnlock()

	if keySet != nil {
		_, ok := keySet[key]
		return ok, nil
	}

	var count int64
	if err := s.sqliteDB.Model(&po.OpenApiSecret{}).Where("`key` = ?", key).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// DefaultOpenApiKey 返回默认密钥（名为 default 那一条）的密钥内容。
//
// 供局域网自动发现随广播发布。**没有默认行不算错误**，返回空串即可 ——
// 广播那边只是少发一个字段（omitempty），管理平台照样能发现网关，
// 只是要人工录入密钥。把它变成错误只会让发现流程凭空多一条失败路径。
func (s *OpenApiSecretService) DefaultOpenApiKey(ctx context.Context) (string, error) {
	var record po.OpenApiSecret
	err := s.sqliteDB.WithContext(ctx).Where("name = ?", defaultApiKeyName).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return record.Key, nil
}
