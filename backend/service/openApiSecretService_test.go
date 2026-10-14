// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/model/dto"
	"iot-gateway/model/po"
)

func newSecretTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "secret_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.OpenApiSecret{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// seedSecret 直接插一行密钥，绕过服务（守卫在服务层，测试要能造出默认行）。
func seedSecret(t *testing.T, db *gorm.DB, name string) po.OpenApiSecret {
	t.Helper()

	row := po.OpenApiSecret{Name: name, Key: "key-" + name, CreatedAt: "2026-01-01 00:00:00"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create secret %q: %v", name, err)
	}
	return row
}

func TestDeleteOpenApiSecretRejectsDefault(t *testing.T) {
	db := newSecretTestDB(t)
	def := seedSecret(t, db, defaultApiKeyName)
	svc := NewOpenApiSecretService(db)

	if err := svc.DeleteOpenApiSecret(context.Background(), def.ID); err == nil {
		t.Fatal("默认密钥不应被删除")
	}

	var count int64
	if err := db.Model(&po.OpenApiSecret{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("默认密钥被删掉了, 剩余 %d 行", count)
	}
}

func TestDeleteOpenApiSecretRemovesOthers(t *testing.T) {
	db := newSecretTestDB(t)
	seedSecret(t, db, defaultApiKeyName)
	other := seedSecret(t, db, "生产密钥")
	svc := NewOpenApiSecretService(db)

	if err := svc.DeleteOpenApiSecret(context.Background(), other.ID); err != nil {
		t.Fatalf("删除普通密钥失败: %v", err)
	}

	var count int64
	if err := db.Model(&po.OpenApiSecret{}).Where("id = ?", other.ID).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Error("普通密钥未被删除")
	}
}

func TestUpdateOpenApiSecretNameRejectsDefault(t *testing.T) {
	db := newSecretTestDB(t)
	def := seedSecret(t, db, defaultApiKeyName)
	svc := NewOpenApiSecretService(db)

	err := svc.UpdateOpenApiSecretName(context.Background(), &dto.UpdateOpenApiSecretNameDTO{
		ID:   def.ID,
		Name: "改个名",
	})
	if err == nil {
		t.Fatal("默认密钥不应被改名")
	}

	var got po.OpenApiSecret
	if err := db.Where("id = ?", def.ID).First(&got).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	// 改名是守卫的唯一判据，一旦漏掉，删除守卫就跟着失效了。
	if got.Name != defaultApiKeyName {
		t.Errorf("默认密钥被改名成 %q", got.Name)
	}
}

func TestListOpenApiSecretMarksDefault(t *testing.T) {
	db := newSecretTestDB(t)
	seedSecret(t, db, defaultApiKeyName)
	seedSecret(t, db, "生产密钥")
	svc := NewOpenApiSecretService(db)

	list, err := svc.ListOpenApiSecret(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d secrets, want 2", len(list))
	}
	for _, item := range list {
		if want := item.Name == defaultApiKeyName; item.IsDefault != want {
			t.Errorf("%q 的 isDefault = %v, want %v", item.Name, item.IsDefault, want)
		}
	}
}

func TestDefaultOpenApiKey(t *testing.T) {
	db := newSecretTestDB(t)
	def := seedSecret(t, db, defaultApiKeyName)
	svc := NewOpenApiSecretService(db)

	key, err := svc.DefaultOpenApiKey(context.Background())
	if err != nil {
		t.Fatalf("DefaultOpenApiKey: %v", err)
	}
	if key != def.Key {
		t.Errorf("key = %q, want %q", key, def.Key)
	}
}

// TestDefaultOpenApiKeyMissingIsNotAnError 没有默认行（老库未升级、被人为改库）
// 时必须返回空串而不是错误：广播那边只是少发一个字段，不该让发现流程失败。
func TestDefaultOpenApiKeyMissingIsNotAnError(t *testing.T) {
	db := newSecretTestDB(t)
	seedSecret(t, db, "生产密钥")
	svc := NewOpenApiSecretService(db)

	key, err := svc.DefaultOpenApiKey(context.Background())
	if err != nil {
		t.Fatalf("缺默认密钥不应报错: %v", err)
	}
	if key != "" {
		t.Errorf("key = %q, want 空串", key)
	}
}
