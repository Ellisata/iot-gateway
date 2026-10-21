// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/configFile"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/runtimecfg"
)

func newSystemConfigTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "system_config_test.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.SystemConfig{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newSystemConfigService(t *testing.T) (*SystemConfigService, *runtimecfg.Manager) {
	t.Helper()
	cfg := &configFile.Config{
		Discovery:  configFile.DiscoveryConfig{Enabled: true},
		DeviceScan: configFile.DeviceScanConfig{Enabled: true},
		Push:       configFile.PushConfig{ChangeOnly: true},
		Plugin:     configFile.PluginConfig{Enabled: true},
	}
	mgr := runtimecfg.NewManager(newSystemConfigTestDB(t), cfg)
	return NewSystemConfigService(mgr), mgr
}

func TestSystemConfigServiceList(t *testing.T) {
	svc, _ := newSystemConfigService(t)

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Groups) != 4 {
		t.Fatalf("分组数 = %d，want 4", len(got.Groups))
	}
	for _, g := range got.Groups {
		if len(g.Fields) != 1 {
			t.Errorf("分组 %s 有 %d 个字段，want 1", g.Key, len(g.Fields))
		}
		for _, f := range g.Fields {
			if !f.Value || f.Source != "default" || f.LabelKey == "" || f.DescKey == "" {
				t.Errorf("%s 的初始状态不正确: %+v", f.Key, f)
			}
		}
	}
}

func TestSystemConfigServiceUpdateAndRestartFlag(t *testing.T) {
	svc, _ := newSystemConfigService(t)
	ctx := context.Background()

	got, err := svc.Update(ctx, &dto.UpdateSystemConfigDTO{
		Key:   "deviceScan.enabled",
		Value: json.RawMessage("false"),
	}, "tester")
	if err != nil {
		t.Fatalf("Update deviceScan: %v", err)
	}
	if got.Value || got.RequiresRestart {
		t.Fatalf("deviceScan.enabled 应立即生效: %+v", got)
	}

	got, err = svc.Update(ctx, &dto.UpdateSystemConfigDTO{
		Key:   "plugin.enabled",
		Value: json.RawMessage("false"),
	}, "tester")
	if err != nil {
		t.Fatalf("Update plugin: %v", err)
	}
	if got.Value || !got.RequiresRestart {
		t.Fatalf("plugin.enabled 应保存后要求重启: %+v", got)
	}
}

func TestSystemConfigServiceErrorMapping(t *testing.T) {
	svc, _ := newSystemConfigService(t)
	ctx := context.Background()

	cases := []struct {
		name string
		dto  *dto.UpdateSystemConfigDTO
		code string
	}{
		{
			name: "未知 key",
			dto:  &dto.UpdateSystemConfigDTO{Key: "nope", Value: json.RawMessage("true")},
			code: enums.SystemConfigUnknownKeyEnum.GetCode(),
		},
		{
			name: "值类型不符",
			dto:  &dto.UpdateSystemConfigDTO{Key: "push.changeOnly", Value: json.RawMessage(`"true"`)},
			code: enums.SystemConfigInvalidValueEnum.GetCode(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Update(ctx, tc.dto, "tester")
			var appErr *appError.AppError
			if !errors.As(err, &appErr) {
				t.Fatalf("期望 *appError.AppError, got %T: %v", err, err)
			}
			if appErr.Code != tc.code {
				t.Fatalf("错误码 = %s，want %s", appErr.Code, tc.code)
			}
		})
	}
}
