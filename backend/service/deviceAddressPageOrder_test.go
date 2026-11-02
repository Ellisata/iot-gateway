// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/model/dto"
	"iot-gateway/model/po"
)

// TestPageDeviceAddressOrderNewestFirstAndStable 设备点位配置页与实时监控页
// 统一为「新增在前」：created_at DESC, id DESC。
//
// 所有点位 created_at 相同（点扫/批量导入常在同一秒写入）时，只按 created_at
// 排序会让 SQLite 自由决定次序，翻页出现重复或漏行；id（UUIDv7，时间有序）
// 作为第二排序键兜底。
func TestPageDeviceAddressOrderNewestFirstAndStable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "addr_page_order_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.DeviceAddress{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	const ts = "2026-11-02 10:00:00"
	rows := make([]po.DeviceAddress, 0, 5)
	for i := 1; i <= 5; i++ {
		rows = append(rows, po.DeviceAddress{
			ID: "a" + string(rune('0'+i)), DeviceID: "d1", Name: "p" + string(rune('0'+i)),
			CommonDataType: "Short", DataType: "int16", RwPermission: "R",
			ScanFrequency: 1000, Status: 1, CreatedAt: ts, UpdatedAt: ts,
		})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed addresses: %v", err)
	}

	svc := NewDeviceAddressService(db, nil)
	var got []string
	for page := 1; page <= 3; page++ {
		out, err := svc.PageDeviceAddress(context.Background(), &dto.PageDeviceAddressDTO{
			DeviceID: "d1", Page: page, Size: 2,
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, r := range out.Records {
			got = append(got, r.ID)
		}
	}

	if want := "a5,a4,a3,a2,a1"; strings.Join(got, ",") != want {
		t.Errorf("分页顺序 = %v, want %s（新增在前且同秒按 id 稳定）", got, want)
	}
}
