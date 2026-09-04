// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"

	"iot-gateway/model/po"
)

// newDeviceAddressImportTestDB 构建设备地址导入测试库（临时文件 SQLite，含三张表）。
func newDeviceAddressImportTestDB(t *testing.T) *gorm.DB {
	dbPath := filepath.Join(t.TempDir(), "addr_import_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.Device{}, &po.IotProtocol{}, &po.DeviceAddress{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestValidateAddressImportRow 覆盖地址导入单行校验的纯函数：
// 名称/标签必填与长度、读写权限枚举、扫描频率解析与默认值。
// 去重由 excelutil.Import 统一处理，此处不测。
func TestValidateAddressImportRow(t *testing.T) {
	tests := []struct {
		caseName     string
		name         string
		label        string
		commonType   string
		rwPermission string
		scanFreq     string
		wantReason   string
		wantFreq     int
	}{
		{caseName: "合法行默认频率", name: "温度", label: "车间温度", commonType: "Float", rwPermission: "R", scanFreq: "", wantReason: "", wantFreq: 1000},
		{caseName: "合法行指定频率", name: "温度", label: "车间温度", commonType: "Float", rwPermission: "RW", scanFreq: "500", wantReason: "", wantFreq: 500},
		{caseName: "名称为空", name: "", label: "车间温度", commonType: "Float", rwPermission: "R", scanFreq: "1000", wantReason: "名称为空"},
		{caseName: "标签为空", name: "温度", label: "", commonType: "Float", rwPermission: "R", scanFreq: "1000", wantReason: "标签为空"},
		{caseName: "通用数据类型为空", name: "温度", label: "车间温度", commonType: "", rwPermission: "R", scanFreq: "1000", wantReason: "通用数据类型为空"},
		{caseName: "读写权限非法", name: "温度", label: "车间温度", commonType: "Float", rwPermission: "X", scanFreq: "1000", wantReason: "读写权限须为"},
		{caseName: "扫描频率非数字", name: "温度", label: "车间温度", commonType: "Float", rwPermission: "R", scanFreq: "abc", wantReason: "扫描频率格式不正确"},
		{caseName: "扫描频率<=0按默认", name: "温度", label: "车间温度", commonType: "Float", rwPermission: "R", scanFreq: "0", wantReason: "", wantFreq: 1000},
	}

	for _, tt := range tests {
		reason, freq := validateAddressImportRow(tt.name, tt.label, tt.commonType, tt.rwPermission, tt.scanFreq)
		if tt.wantReason == "" {
			if reason != "" {
				t.Errorf("%s: 应通过，实际失败: %s", tt.caseName, reason)
			}
			if freq != tt.wantFreq {
				t.Errorf("%s: 频率 = %d, want %d", tt.caseName, freq, tt.wantFreq)
			}
		} else if !strings.Contains(reason, tt.wantReason) {
			t.Errorf("%s: 原因 = %q，期望包含 %q", tt.caseName, reason, tt.wantReason)
		}
	}

	// 名称/标签长度边界：100 字符通过、101 字符失败
	if reason, _ := validateAddressImportRow(strings.Repeat("名", 100), "标签", "Float", "R", ""); reason != "" {
		t.Errorf("100字符名称应通过，实际: %q", reason)
	}
	if reason, _ := validateAddressImportRow(strings.Repeat("名", 101), "标签", "Float", "R", ""); !strings.Contains(reason, "不能超过") {
		t.Errorf("101字符名称应失败，实际: %q", reason)
	}
}

// TestImportDeviceAddresses 端到端验证地址导入：解析、逐行校验、数据类型映射、部分导入、落库。
func TestImportDeviceAddresses(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: "ModBus.TCP", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&po.Device{ID: "dev-1", Name: "测试设备", ProtocolID: "proto-1", ProtocolJSON: "{}", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &DeviceAddressService{sqliteDB: db}
	ctx := context.Background()

	// 构造测试 Excel：表头 + 8 数据行 + 1 全空行
	var buf bytes.Buffer
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	for i, h := range []string{"名称", "标签", "通用数据类型", "读写权限", "扫描频率", "描述"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	rows := [][6]string{
		{"温度", "车间温度", "Float", "R", "1000", "描述1"},
		{"湿度", "车间湿度", "UnknownType", "R", "1000", ""}, // 协议不支持该类型 → 失败
		{"压力", "车间压力", "Short", "RW", "500", ""},       // 成功
		{"温度", "车间温度2", "Float", "R", "1000", ""},      // 文件内重名 → 失败
		{"", "车间", "Float", "R", "1000", ""},           // 名称为空 → 失败
		{"电流", "车间电流", "Float", "X", "1000", ""},       // 读写权限非法 → 失败
		{"电压", "车间电压", "Float", "R", "abc", ""},        // 扫描频率非数字 → 失败
		{"转速", "车间转速", "Float", "R", "200", "描述8"},     // 成功
		{"", "", "", "", "", ""},                       // 全空行 → 跳过
	}
	for i, r := range rows {
		for j, v := range r {
			if v == "" {
				continue
			}
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			_ = f.SetCellValue(sheet, cell, v)
		}
	}
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}

	result, err := svc.ImportDeviceAddresses(ctx, "dev-1", buf.Bytes())
	if err != nil {
		t.Fatalf("ImportDeviceAddresses: %v", err)
	}
	if result.Total != 8 {
		t.Errorf("Total = %d, want 8", result.Total)
	}
	if result.Success != 3 {
		t.Errorf("Success = %d, want 3", result.Success)
	}
	if result.Failed != 5 {
		t.Errorf("Failed = %d, want 5", result.Failed)
	}

	// 失败明细覆盖各类原因
	reasonSet := map[string]bool{}
	for _, e := range result.Errors {
		reasonSet[e.Reason] = true
	}
	for _, want := range []string{
		"协议不支持该数据类型：UnknownType",
		"设备地址名称已存在",
		"名称为空",
		"读写权限须为 R/W/RW",
		"扫描频率格式不正确",
	} {
		if !reasonSet[want] {
			t.Errorf("失败明细缺少原因 %q, 实际 %+v", want, result.Errors)
		}
	}

	// 落库验证：3 台地址，数据类型已按协议映射、扫描频率、读写权限
	var addrs []po.DeviceAddress
	if err := db.Where("device_id = ?", "dev-1").Find(&addrs).Error; err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 3 {
		t.Fatalf("地址数 = %d, want 3", len(addrs))
	}
	mapping := map[string]po.DeviceAddress{}
	for _, a := range addrs {
		mapping[a.Name] = a
	}
	if a := mapping["温度"]; a.DataType != "float32" || a.RwPermission != "R" || a.ScanFrequency != 1000 || a.CommonDataType != "Float" {
		t.Errorf("温度地址落库异常: %+v", a)
	}
	if a := mapping["压力"]; a.DataType != "int16" || a.RwPermission != "RW" || a.ScanFrequency != 500 {
		t.Errorf("压力地址落库异常: %+v", a)
	}
	if a := mapping["转速"]; a.DataType != "float32" || a.ScanFrequency != 200 {
		t.Errorf("转速地址落库异常: %+v", a)
	}

	// 再次导入同文件 → 全部失败
	result2, err := svc.ImportDeviceAddresses(ctx, "dev-1", buf.Bytes())
	if err != nil {
		t.Fatalf("二次导入: %v", err)
	}
	if result2.Success != 0 || result2.Failed != 8 {
		t.Errorf("二次导入 Success/Failed = %d/%d, want 0/8", result2.Success, result2.Failed)
	}
}

// TestImportDeviceAddressesDeviceNotFound 设备不存在时导入返回错误。
func TestImportDeviceAddressesDeviceNotFound(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	svc := &DeviceAddressService{sqliteDB: db}

	var buf bytes.Buffer
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	for i, h := range []string{"名称", "标签", "通用数据类型", "读写权限", "扫描频率", "描述"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	_ = f.Write(&buf)

	if _, err := svc.ImportDeviceAddresses(context.Background(), "nope", buf.Bytes()); err == nil {
		t.Error("设备不存在时应返回错误")
	}
}

// TestGenerateDeviceAddressImportTemplate 验证地址导入模板：可重新打开、表头正确、含示例行。
func TestGenerateDeviceAddressImportTemplate(t *testing.T) {
	svc := &DeviceAddressService{}
	buf, err := svc.GenerateDeviceAddressImportTemplate()
	if err != nil {
		t.Fatalf("GenerateDeviceAddressImportTemplate: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("reopen template: %v", err)
	}
	defer f.Close()
	sheet := f.GetSheetName(0)
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("模板行数 = %d, want >= 2", len(rows))
	}
	if rows[0][0] != "名称" || rows[0][1] != "标签" || rows[0][2] != "通用数据类型" || rows[0][3] != "读写权限" || rows[0][4] != "扫描频率" || rows[0][5] != "描述" {
		t.Errorf("表头 = %v", rows[0])
	}
}
