// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/testutil/drivertest"
)

// TestResolveAddressLayout 钉住「布局名 × 值宽度」的校验矩阵（协议无关部分）。
// 与驱动 internal/layout 的排列表、前端下拉三处一致；改一处必须改三处。
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
		{"DCBAHGFE", 8, "DCBAHGFE", true},
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

// TestResolveAddressLayoutForScope 协议能力门禁：只有 Modbus 与 FINS 接受
// 非空布局，FINS 不接受字符串；空串（跟随设备）对所有协议放行。
func TestResolveAddressLayoutForScope(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		scope   string
		size    int
		layout  string
		ok      bool
		reasons string
	}{
		{"modbus 16 位", "BADC", "modbus", 2, "BADC", true, ""},
		{"modbus 字符串", "ABCD", "modbus", 0, "ABCD", true, ""},
		{"modbus 64 位新布局", "dcbahgfe", "modbus", 8, "DCBAHGFE", true, ""},
		{"fins 32 位", "cdab", "fins", 4, "CDAB", true, ""},
		{"fins 64 位新布局", "DCBAHGFE", "fins", 8, "DCBAHGFE", true, ""},
		{"fins 字符串拒绝", "ABCD", "fins", 0, "", false, "字符串点位不支持"},
		{"fins 位宽不匹配", "ABCDEFGH", "fins", 2, "", false, "不适用于该数据类型"},
		{"未知协议拒绝", "ABCD", "s7", 4, "", false, "不支持点位级字节序布局"},
		{"合成协议拒绝", "ABCD", "testsynthetic", 4, "", false, "不支持点位级字节序布局"},
		{"空串任意协议放行", "跟随设备", "s7", 4, "", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout, reason := resolveAddressLayoutForScope(tt.raw, tt.scope, "Test."+tt.scope, tt.size)
			if gotOK := reason == ""; gotOK != tt.ok || layout != tt.layout {
				t.Errorf("resolveAddressLayoutForScope(%q, %q, %d) = (%q, %q), want (%q, ok=%v)",
					tt.raw, tt.scope, tt.size, layout, reason, tt.layout, tt.ok)
			}
			if tt.reasons != "" && !strings.Contains(reason, tt.reasons) {
				t.Errorf("reason = %q, want contains %q", reason, tt.reasons)
			}
		})
	}
}

// seedFinsDevice 为布局用例准备一个 scope=fins 的合成协议与设备。
// 网关侧测试不拉起真实 FINS 插件（见 drivertest 包注释）。
func seedFinsDevice(t *testing.T) (*DeviceAddressService, context.Context) {
	t.Helper()
	drivertest.EnsureSyntheticFinsProtocol()
	db := newThresholdTestDB(t)
	if err := db.Create(&po.IotProtocol{ID: "proto-fins", Name: drivertest.TestFinsProtocol}).Error; err != nil {
		t.Fatalf("seed protocol: %v", err)
	}
	if err := db.Create(&po.Device{ID: "dev-fins", Name: "FINS 设备", ProtocolID: "proto-fins"}).Error; err != nil {
		t.Fatalf("seed device: %v", err)
	}
	return NewDeviceAddressService(db, NewAlarmService(db)), context.Background()
}

// TestCreateDeviceAddressByteOrderLayout 点位级布局必须落库，并能由
// GetDeviceAddressByID 回填 —— VO 是编辑框回填的唯一来源，漏了就是「保存了却看不到」。
func TestCreateDeviceAddressByteOrderLayout(t *testing.T) {
	svc, ctx := seedFinsDevice(t)

	if err := svc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID:        "dev-fins",
		Name:            "D100",
		Label:           "温度2",
		CommonDataType:  driver.TypeFloat,
		RwPermission:    "R",
		ScanFrequency:   1000,
		ByteOrderLayout: "cdab", // 大小写归一
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	var a po.DeviceAddress
	if err := svc.sqliteDB.Where("device_id = ? AND name = ?", "dev-fins", "D100").First(&a).Error; err != nil {
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

// TestCreateDeviceAddressLayoutDCBAHGFE 新补齐的 64 位布局按原值落库。
func TestCreateDeviceAddressLayoutDCBAHGFE(t *testing.T) {
	svc, ctx := seedFinsDevice(t)

	if err := svc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID:        "dev-fins",
		Name:            "D200",
		Label:           "累计量",
		CommonDataType:  driver.TypeDouble,
		RwPermission:    "R",
		ScanFrequency:   1000,
		ByteOrderLayout: "dcbahgfe",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	var a po.DeviceAddress
	if err := svc.sqliteDB.Where("device_id = ? AND name = ?", "dev-fins", "D200").First(&a).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if a.ByteOrderLayout != "DCBAHGFE" {
		t.Fatalf("落库布局 = %q, want DCBAHGFE", a.ByteOrderLayout)
	}
}

// TestCreateDeviceAddressRejectsWrongWidthLayout 布局与数据类型宽度不匹配必须
// 在保存时拒绝：驱动对不适用的布局会报解析失败，放过去就是「能采、值却错了」。
func TestCreateDeviceAddressRejectsWrongWidthLayout(t *testing.T) {
	svc, ctx := seedFinsDevice(t)

	err := svc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID:        "dev-fins",
		Name:            "D110",
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

// TestCreateDeviceAddressRejectsLayoutOnUnsupportedProtocol 未实现点位级布局的
// 协议必须在保存时拒绝，而不是存进去后被驱动静默忽略。
func TestCreateDeviceAddressRejectsLayoutOnUnsupportedProtocol(t *testing.T) {
	svc, ctx := seedTransformDevice(t) // 测试协议 scope=testsynthetic

	err := svc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID:        "dev-t",
		Name:            "40013",
		Label:           "温度5",
		CommonDataType:  driver.TypeFloat,
		RwPermission:    "R",
		ScanFrequency:   1000,
		ByteOrderLayout: "ABCD",
	})
	if err == nil || !strings.Contains(err.Error(), "不支持点位级字节序布局") {
		t.Fatalf("不受支持协议的布局应被拒绝，实际 err=%v", err)
	}
}

// TestCreateDeviceAddressFinsStringRejectsLayout FINS 字符串没有点位级布局：
// 即使布局名本身是 ABCD（Modbus 字符串允许），也必须拒绝。
func TestCreateDeviceAddressFinsStringRejectsLayout(t *testing.T) {
	svc, ctx := seedFinsDevice(t)

	err := svc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID:        "dev-fins",
		Name:            "D300",
		Label:           "批次号",
		CommonDataType:  driver.TypeString,
		RwPermission:    "R",
		ScanFrequency:   1000,
		ByteOrderLayout: "ABCD",
	})
	if err == nil || !strings.Contains(err.Error(), "字符串点位不支持") {
		t.Fatalf("FINS 字符串布局应被拒绝，实际 err=%v", err)
	}
}

// TestUpdateDeviceAddressLayoutTriState 更新是三态的：省略不动、空串清空（回到
// 跟随设备）、非空覆写。表单清空布局走的就是「空串」这条。
func TestUpdateDeviceAddressLayoutTriState(t *testing.T) {
	svc, ctx := seedFinsDevice(t)

	if err := svc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID:        "dev-fins",
		Name:            "D120",
		Label:           "温度4",
		CommonDataType:  driver.TypeFloat,
		RwPermission:    "R",
		ScanFrequency:   1000,
		ByteOrderLayout: "DCBA",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	var a po.DeviceAddress
	if err := svc.sqliteDB.Where("device_id = ? AND name = ?", "dev-fins", "D120").First(&a).Error; err != nil {
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
