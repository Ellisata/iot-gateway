// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/model/dto"
	"iot-gateway/model/po"
)

// TestResolveAddressLayout 钉住「布局名 × 值宽度」的校验矩阵。
// 与驱动 modbusdrv/layout.go 的排列表、前端下拉三处一致；改一处必须改三处。
func TestResolveAddressLayout(t *testing.T) {
	tests := []struct {
		raw    string
		size   int
		layout string
		ok     bool
	}{
		{"", 4, "", true},
		{"跟随设备", 4, "", true},
		{"device", 4, "", true},
		{"ABCD", 4, "ABCD", true},
		{"cdab", 4, "CDAB", true},
		{"BADC", 4, "BADC", true},
		{"DCBA", 4, "DCBA", true},
		{"ABCD", 2, "ABCD", true},
		{"BADC", 2, "BADC", true},
		{"ABCDEFGH", 8, "ABCDEFGH", true},
		{"GHEFCDAB", 8, "GHEFCDAB", true},
		{"ABCD", 8, "", false},     // 8 字节必须用带 8 个字母的名字
		{"CDABGHEF", 4, "", false}, // 宽度不匹配
		{"ABCD", 1, "", false},     // 8 位类型不支持布局
		{"ABCD", 0, "ABCD", true},  // string 只支持 ABCD/BADC
		{"CDAB", 0, "", false},     // string 不支持跨寄存器布局
		{"XYZ", 4, "", false},      // 未知布局
	}
	for _, tt := range tests {
		layout, reason := resolveAddressLayout(tt.raw, tt.size)
		if gotOK := reason == ""; gotOK != tt.ok || layout != tt.layout {
			t.Errorf("resolveAddressLayout(%q, %d) = (%q, %q), want (%q, ok=%v)",
				tt.raw, tt.size, layout, reason, tt.layout, tt.ok)
		}
	}
}

// TestCreateDeviceAddressByteOrderLayout 点位级布局必须落库，并能由
// GetDeviceAddressByID 回填 —— VO 是编辑框回填的唯一来源，漏了就是「保存了却看不到」。
func TestCreateDeviceAddressByteOrderLayout(t *testing.T) {
	svc, ctx := seedTransformDevice(t)

	if err := svc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID:        "dev-t",
		Name:            "40010",
		Label:           "温度2",
		CommonDataType:  driver.TypeFloat,
		RwPermission:    "R",
		ScanFrequency:   1000,
		ByteOrderLayout: "cdab", // 大小写归一
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	var a po.DeviceAddress
	if err := svc.sqliteDB.Where("device_id = ? AND name = ?", "dev-t", "40010").First(&a).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if a.ByteOrderLayout != "CDAB" {
		t.Fatalf("落库布局 = %q, want CDAB", a.ByteOrderLayout)
	}
	got, err := svc.GetDeviceAddressByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.ByteOrderLayout != "CDAB" {
		t.Fatalf("VO 布局 = %q, want CDAB", got.ByteOrderLayout)
	}
}

// TestCreateDeviceAddressRejectsWrongWidthLayout 布局与数据类型宽度不匹配必须
// 在保存时拒绝：驱动对不适用的布局会报解析失败，放过去就是「能采、值却错了」。
func TestCreateDeviceAddressRejectsWrongWidthLayout(t *testing.T) {
	svc, ctx := seedTransformDevice(t)

	err := svc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID:        "dev-t",
		Name:            "40011",
		Label:           "温度3",
		CommonDataType:  driver.TypeShort, // 16 位
		RwPermission:    "R",
		ScanFrequency:   1000,
		ByteOrderLayout: "GHEFCDAB", // 64 位布局
	})
	if err == nil {
		t.Fatal("宽度不匹配的布局应被拒绝，却创建成功")
	}
}

// TestUpdateDeviceAddressLayoutTriState 更新是三态的：省略不动、空串清空（回到
// 跟随设备）、非空覆写。表单清空布局走的就是「空串」这条。
func TestUpdateDeviceAddressLayoutTriState(t *testing.T) {
	svc, ctx := seedTransformDevice(t)

	if err := svc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID:        "dev-t",
		Name:            "40012",
		Label:           "温度4",
		CommonDataType:  driver.TypeFloat,
		RwPermission:    "R",
		ScanFrequency:   1000,
		ByteOrderLayout: "DCBA",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	var a po.DeviceAddress
	if err := svc.sqliteDB.Where("device_id = ? AND name = ?", "dev-t", "40012").First(&a).Error; err != nil {
		t.Fatalf("load: %v", err)
	}

	empty := ""
	if err := svc.UpdateDeviceAddress(ctx, &dto.UpdateDeviceAddressDTO{
		ID:              a.ID,
		Label:           a.Label,
		ByteOrderLayout: &empty,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := svc.sqliteDB.Where("id = ?", a.ID).First(&a).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if a.ByteOrderLayout != "" {
		t.Fatalf("清空后布局 = %q, want 空串", a.ByteOrderLayout)
	}
}
