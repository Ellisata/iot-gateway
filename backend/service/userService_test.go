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
	"iot-gateway/utils"
)

// newUserTestDB 构建用户服务测试库（临时文件 SQLite，含 sys_user 表）。
func newUserTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "user_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// seedTestUser 写入一个测试用户，注意 id 与用户名刻意不同，
// 以便区分「按 id 定位」与「按用户名定位」两种实现。
func seedTestUser(t *testing.T, db *gorm.DB, id, username, plainPassword string) {
	t.Helper()

	hashed, err := utils.HashPassword(plainPassword)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user := &po.User{
		ID:       id,
		Username: username,
		Password: hashed,
		Status:   1,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
}

// encPassword 按前端的做法把明文密码 RSA 加密（服务层只接收密文）。
func encPassword(t *testing.T, plain string) string {
	t.Helper()

	cipher, err := utils.RSAEncrypt(plain, utils.PublicKey)
	if err != nil {
		t.Fatalf("rsa encrypt %q: %v", plain, err)
	}
	return cipher
}

// TestChangePasswordByUsername 覆盖修改密码的用户定位与各项校验。
// 定位一律以用户名为准：令牌里带的、以及登录时认定的身份都是用户名，
// 用 id 定位会让「用户名改了但令牌未换」之类的场景改错人。
func TestChangePasswordByUsername(t *testing.T) {
	const (
		userID       = "uuid-abc"
		username     = "alice"
		oldPassword  = "old-pass-123"
		newPassword  = "new-pass-456"
		mismatchPass = "new-pass-789"
	)

	tests := []struct {
		label       string
		username    string
		oldPassword string
		newPassword string
		confirm     string
		wantCode    string // "" 表示期望成功
	}{
		{
			label:       "按用户名修改成功",
			username:    username,
			oldPassword: oldPassword,
			newPassword: newPassword,
			confirm:     newPassword,
			wantCode:    "",
		},
		{
			label:       "用 id 当用户名查不到用户",
			username:    userID,
			oldPassword: oldPassword,
			newPassword: newPassword,
			confirm:     newPassword,
			wantCode:    enums.UserNotExistsEnum.GetCode(),
		},
		{
			label:       "用户名不存在",
			username:    "bob",
			oldPassword: oldPassword,
			newPassword: newPassword,
			confirm:     newPassword,
			wantCode:    enums.UserNotExistsEnum.GetCode(),
		},
		{
			label:       "旧密码错误",
			username:    username,
			oldPassword: "wrong-pass-000",
			newPassword: newPassword,
			confirm:     newPassword,
			wantCode:    enums.PasswordErrEnum.GetCode(),
		},
		{
			label:       "两次新密码不一致",
			username:    username,
			oldPassword: oldPassword,
			newPassword: newPassword,
			confirm:     mismatchPass,
			wantCode:    enums.ParamValidEnum.GetCode(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			db := newUserTestDB(t)
			seedTestUser(t, db, userID, username, oldPassword)

			err := NewUserService(db).ChangePassword(context.Background(), tt.username, &dto.ChangePasswordDTO{
				OldPassword:     encPassword(t, tt.oldPassword),
				NewPassword:     encPassword(t, tt.newPassword),
				ConfirmPassword: encPassword(t, tt.confirm),
			})

			var user po.User
			if err := db.Where("username = ?", username).First(&user).Error; err != nil {
				t.Fatalf("reload user: %v", err)
			}

			if tt.wantCode != "" {
				var appErr *appError.AppError
				if !errors.As(err, &appErr) {
					t.Fatalf("期望 *appError.AppError, got %T: %v", err, err)
				}
				if appErr.Code != tt.wantCode {
					t.Errorf("code = %q, want %q", appErr.Code, tt.wantCode)
				}
				// 失败的路径不得动密码
				if !utils.CheckPassword(oldPassword, user.Password) {
					t.Error("失败路径不应修改密码")
				}
				return
			}

			if err != nil {
				t.Fatalf("修改密码失败: %v", err)
			}
			if !utils.CheckPassword(newPassword, user.Password) {
				t.Error("新密码未生效")
			}
			if utils.CheckPassword(oldPassword, user.Password) {
				t.Error("旧密码仍然有效")
			}
		})
	}
}
