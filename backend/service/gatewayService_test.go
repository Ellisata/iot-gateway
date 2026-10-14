// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
)

// newGatewayTestDB 构建网关身份服务测试库（临时文件 SQLite，跨连接可见）。
func newGatewayTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "gateway_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.IotGateway{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// seedGatewayRow 插入唯一那行身份，模拟首启播种的结果。
func seedGatewayRow(t *testing.T, db *gorm.DB) po.IotGateway {
	t.Helper()

	row := po.IotGateway{
		ID:              "c087c727cd1a4a3eb87b3aa713629e0f",
		Name:            "line-3-gateway",
		Port:            9082,
		FirmwareVersion: "1.0.0",
		Description:     "原描述",
		Status:          1,
		CreatedAt:       "2026-09-16 10:00:00",
		UpdatedAt:       "2026-09-16 10:00:00",
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create gateway row: %v", err)
	}
	return row
}

func TestGetGateway(t *testing.T) {
	db := newGatewayTestDB(t)
	seedGatewayRow(t, db)

	got, err := NewGatewayService(db).GetGateway(context.Background())
	if err != nil {
		t.Fatalf("GetGateway: %v", err)
	}
	if got.ID != "c087c727cd1a4a3eb87b3aa713629e0f" || got.Name != "line-3-gateway" {
		t.Errorf("返回的字段不对: %+v", got)
	}
	if got.FirmwareVersion != "1.0.0" || got.Port != 9082 || got.Description != "原描述" {
		t.Errorf("返回的字段不对: %+v", got)
	}
}

// 身份行不存在是合法状态（InitDB 里播种失败刻意非致命），必须映射成业务错误，
// 而不是把 gorm.ErrRecordNotFound 原样抛给上层变成系统错误。
func TestGetGatewayNotFound(t *testing.T) {
	db := newGatewayTestDB(t)

	_, err := NewGatewayService(db).GetGateway(context.Background())
	if err == nil {
		t.Fatal("空表时应返回错误")
	}

	var appErr *appError.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("期望 *appError.AppError, got %T: %v", err, err)
	}
	if appErr.Code != enums.GatewayNotExistsEnum.GetCode() {
		t.Errorf("code = %q, want %q", appErr.Code, enums.GatewayNotExistsEnum.GetCode())
	}
}

func TestUpdateGateway(t *testing.T) {
	db := newGatewayTestDB(t)
	before := seedGatewayRow(t, db)

	svc := NewGatewayService(db)
	err := svc.UpdateGateway(context.Background(), &dto.UpdateGatewayDTO{
		Name:        "packaging-line-3",
		Description: "三号包装线网关",
	})
	if err != nil {
		t.Fatalf("UpdateGateway: %v", err)
	}

	var after po.IotGateway
	if err := db.First(&after).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Name != "packaging-line-3" || after.Description != "三号包装线网关" {
		t.Errorf("可编辑字段没有写进去: %+v", after)
	}
	// id 与 firmware_version 是只读的，任何更新路径都不得动它们。
	if after.ID != before.ID {
		t.Errorf("id 被改了: %q -> %q", before.ID, after.ID)
	}
	if after.FirmwareVersion != before.FirmwareVersion {
		t.Errorf("firmware_version 被改了: %q -> %q", before.FirmwareVersion, after.FirmwareVersion)
	}
	if after.CreatedAt != before.CreatedAt {
		t.Errorf("created_at 被改了: %q -> %q", before.CreatedAt, after.CreatedAt)
	}
	if after.UpdatedAt == before.UpdatedAt {
		t.Error("updated_at 没有被刷新")
	}
}

// 描述允许清空 —— 更新是「整体覆盖这两个字段」，不是「只写非空字段」。
func TestUpdateGatewayClearsDescription(t *testing.T) {
	db := newGatewayTestDB(t)
	seedGatewayRow(t, db)

	svc := NewGatewayService(db)
	if err := svc.UpdateGateway(context.Background(), &dto.UpdateGatewayDTO{Name: "line-3-gateway"}); err != nil {
		t.Fatalf("UpdateGateway: %v", err)
	}

	var after po.IotGateway
	if err := db.First(&after).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Description != "" {
		t.Errorf("description = %q, want 空串", after.Description)
	}
}

func TestUpdateGatewayNotFound(t *testing.T) {
	db := newGatewayTestDB(t)

	err := NewGatewayService(db).UpdateGateway(context.Background(),
		&dto.UpdateGatewayDTO{Name: "whatever"})
	if err == nil {
		t.Fatal("空表时应返回错误")
	}

	var appErr *appError.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("期望 *appError.AppError, got %T: %v", err, err)
	}
	if appErr.Code != enums.GatewayNotExistsEnum.GetCode() {
		t.Errorf("code = %q, want %q", appErr.Code, enums.GatewayNotExistsEnum.GetCode())
	}
}
