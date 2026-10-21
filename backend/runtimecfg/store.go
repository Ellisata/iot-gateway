// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package runtimecfg

import (
	"context"

	"gorm.io/gorm"

	"iot-gateway/model/po"
)

// overrideStore 是 Manager 对持久化的全部依赖。抽成接口只为了单测能注入
// 失败 / 内存实现；生产路径只有 gormStore 一个实现。
type overrideStore interface {
	List(ctx context.Context) ([]po.SystemConfig, error)
	Save(ctx context.Context, key Key, value, updatedBy, updatedAt string) error
	Delete(ctx context.Context, key Key) error
}

// gormStore system_config 表的 GORM 实现。
//
// 写操作刻意不用事务：调用方（Manager.Update / Reset）在落库**之后**才应用
// 新值，应用失败时再用一次补偿写回旧值。把落库与应用放进同一个事务会让
// 单写连接的 SQLite 在 applier 等待协程退出（discovery Stop）期间被占住，
// 拖住采集与 outbox 落盘。
type gormStore struct {
	db *gorm.DB
}

func (s *gormStore) List(ctx context.Context) ([]po.SystemConfig, error) {
	var rows []po.SystemConfig
	err := s.db.WithContext(ctx).Find(&rows).Error
	return rows, err
}

func (s *gormStore) Save(ctx context.Context, key Key, value, updatedBy, updatedAt string) error {
	// ON CONFLICT 是 SQLite 语法；本项目只用 SQLite。
	return s.db.WithContext(ctx).Exec(`
		INSERT INTO system_config (key, value, updated_by, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value      = excluded.value,
			updated_by = excluded.updated_by,
			updated_at = excluded.updated_at`,
		string(key), value, updatedBy, updatedAt).Error
}

func (s *gormStore) Delete(ctx context.Context, key Key) error {
	return s.db.WithContext(ctx).
		Where("key = ?", string(key)).
		Delete(&po.SystemConfig{}).Error
}
