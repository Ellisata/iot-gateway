// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/configFile"
	"iot-gateway/model/po"
	"iot-gateway/utils"
)

// newTestDB 建一个跑完迁移的空库。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	// 内存库必须限制为单连接，否则每条连接会拿到各自独立的库，
	// 迁移建的表与播种写的行会落在不同的内存库上。
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if err := runMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

// 取唯一一行用户，顺带断言只有一行。
func soleUser(t *testing.T, db *gorm.DB) po.User {
	t.Helper()

	var users []po.User
	if err := db.Find(&users).Error; err != nil {
		t.Fatalf("find users: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected exactly 1 user, got %d", len(users))
	}
	return users[0]
}

func TestSeedDefaultAdminUsesConfiguredPassword(t *testing.T) {
	db := newTestDB(t)

	cfg := &configFile.Config{Auth: configFile.AuthConfig{DefaultAdminPassword: "s3cret-from-config"}}
	if err := seedDefaultAdmin(db, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}

	admin := soleUser(t, db)
	if admin.Username != defaultAdminUsername {
		t.Errorf("username = %q, want %q", admin.Username, defaultAdminUsername)
	}
	if admin.Status != 1 {
		t.Errorf("status = %d, want 1", admin.Status)
	}
	if admin.ID == "" || admin.CreatedAt == "" || admin.UpdatedAt == "" {
		t.Errorf("id/created_at/updated_at 不能为空, got %+v", admin)
	}
	if !utils.CheckPassword("s3cret-from-config", admin.Password) {
		t.Error("配置里的口令校验不通过")
	}
}

func TestSeedDefaultAdminEnvOverridesConfig(t *testing.T) {
	t.Setenv(configFile.EnvAdminPassword, "s3cret-from-env")
	db := newTestDB(t)

	cfg := &configFile.Config{Auth: configFile.AuthConfig{DefaultAdminPassword: "s3cret-from-config"}}
	if err := seedDefaultAdmin(db, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}

	admin := soleUser(t, db)
	if !utils.CheckPassword("s3cret-from-env", admin.Password) {
		t.Error("环境变量没有盖过配置项")
	}
	if utils.CheckPassword("s3cret-from-config", admin.Password) {
		t.Error("配置项的口令不该生效")
	}
}

func TestSeedDefaultAdminGeneratesPasswordWhenUnset(t *testing.T) {
	t.Setenv(configFile.EnvAdminPassword, "")
	db := newTestDB(t)

	if err := seedDefaultAdmin(db, &configFile.Config{}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	first := soleUser(t, db)

	// 两次播种（不同库）必须得到不同口令 —— 哈希相同就说明退回成了固定口令。
	db2 := newTestDB(t)
	if err := seedDefaultAdmin(db2, &configFile.Config{}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if first.Password == soleUser(t, db2).Password {
		t.Error("两次随机播种得到了相同哈希，口令不是随机的")
	}
}

func TestSeedDefaultAdminIsIdempotent(t *testing.T) {
	db := newTestDB(t)

	cfg := &configFile.Config{Auth: configFile.AuthConfig{DefaultAdminPassword: "first"}}
	if err := seedDefaultAdmin(db, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 第二次换一个口令：表已非空，必须原样保留、不得覆盖。
	cfg.Auth.DefaultAdminPassword = "second"
	if err := seedDefaultAdmin(db, cfg); err != nil {
		t.Fatalf("seed again: %v", err)
	}

	if admin := soleUser(t, db); !utils.CheckPassword("first", admin.Password) {
		t.Error("重复播种覆盖了已有账号")
	}
}

func TestSeedDefaultAdminKeepsExistingAccounts(t *testing.T) {
	db := newTestDB(t)

	existing := &po.User{ID: utils.GenerateUUID(), Username: "operator", Password: "x", Status: 1}
	if err := db.Create(existing).Error; err != nil {
		t.Fatalf("create existing user: %v", err)
	}

	cfg := &configFile.Config{Auth: configFile.AuthConfig{DefaultAdminPassword: "unused"}}
	if err := seedDefaultAdmin(db, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var count int64
	if err := db.Model(&po.User{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("表非空时不应播种, want 1 user, got %d", count)
	}
}
