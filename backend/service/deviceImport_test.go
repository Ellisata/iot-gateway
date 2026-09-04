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

// newImportTestDB 构建导入测试库（临时文件 SQLite，含 device / iot_protocol 表）。
func newImportTestDB(t *testing.T) *gorm.DB {
	dbPath := filepath.Join(t.TempDir(), "import_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.Device{}, &po.IotProtocol{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestValidateImportRow 覆盖设备导入单行校验的纯函数：
// 合法通过 / 名称为空 / 名称超长 / 协议为空 / 协议不存在。
// 去重由 excelutil.Import 统一处理，此处不测。
func TestValidateImportRow(t *testing.T) {
	protocolMap := map[string]string{"ModBus.TCP": "p1"}

	tests := []struct {
		label        string
		rowName      string
		protocolName string
		protocolMap  map[string]string
		want         string // "" 表示通过
	}{
		{label: "合法行", rowName: "设备1", protocolName: "ModBus.TCP", protocolMap: protocolMap, want: ""},
		{label: "名称为空", rowName: "", protocolName: "ModBus.TCP", protocolMap: protocolMap, want: "名称为空"},
		{label: "协议为空", rowName: "设备1", protocolName: "", protocolMap: protocolMap, want: "协议为空"},
		{label: "协议不存在", rowName: "设备1", protocolName: "Unknown.Proto", protocolMap: protocolMap, want: "协议不存在"},
	}

	for _, tt := range tests {
		got := validateImportRow(tt.rowName, tt.protocolName, tt.protocolMap)
		if tt.want == "" && got != "" {
			t.Errorf("%s: 应通过，实际失败: %s", tt.label, got)
		}
		if tt.want != "" && !strings.Contains(got, tt.want) {
			t.Errorf("%s: 原因 = %q，期望包含 %q", tt.label, got, tt.want)
		}
	}

	// 名称长度边界：100 字符通过、101 字符失败
	okName := strings.Repeat("名", 100)
	if got := validateImportRow(okName, "ModBus.TCP", protocolMap); got != "" {
		t.Errorf("100字符名称应通过，实际失败: %q", got)
	}
	longName := strings.Repeat("名", 101)
	if got := validateImportRow(longName, "ModBus.TCP", protocolMap); !strings.Contains(got, "不能超过") {
		t.Errorf("101字符名称应失败，实际: %q", got)
	}
}

// TestImportDevices 端到端验证 Excel 导入：解析、逐行校验、部分导入、汇总统计、落库。
func TestImportDevices(t *testing.T) {
	db := newImportTestDB(t)
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: "ModBus.TCP", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &DeviceService{sqliteDB: db}
	ctx := context.Background()

	// 构造测试 Excel：表头 + 4 数据行 + 1 全空行
	var buf bytes.Buffer
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	for i, h := range []string{"名称", "协议", "描述"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	rows := [][3]string{
		{"导入设备1", "ModBus.TCP", "描述1"},
		{"导入设备2", "Unknown.Proto", ""}, // 协议不存在 → 失败
		{"导入设备1", "ModBus.TCP", ""},    // 文件内重名 → 失败
		{"导入设备3", "ModBus.TCP", ""},    // 成功
		{"", "", ""},                   // 全空行 → 跳过
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

	result, err := svc.ImportDevices(ctx, buf.Bytes())
	if err != nil {
		t.Fatalf("ImportDevices: %v", err)
	}
	if result.Total != 4 {
		t.Errorf("Total = %d, want 4", result.Total)
	}
	if result.Success != 2 {
		t.Errorf("Success = %d, want 2", result.Success)
	}
	if result.Failed != 2 {
		t.Errorf("Failed = %d, want 2", result.Failed)
	}

	// 失败明细：行号（含表头）+ 原因
	foundUnknown, foundDup := false, false
	for _, e := range result.Errors {
		switch {
		case e.Reason == "协议不存在：Unknown.Proto":
			foundUnknown = true
			if e.Row != 3 {
				t.Errorf("未知协议行号 = %d, want 3", e.Row)
			}
		case e.Reason == "设备名称已存在":
			foundDup = true
			if e.Row != 4 {
				t.Errorf("重复名行号 = %d, want 4", e.Row)
			}
		}
	}
	if !foundUnknown || !foundDup {
		t.Errorf("errors = %+v, 缺少协议不存在/名称重复", result.Errors)
	}

	// 落库验证：2 台设备，protocol_json="{}"、协议ID、状态
	var devices []po.Device
	if err := db.Find(&devices).Error; err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("设备数 = %d, want 2", len(devices))
	}
	for _, d := range devices {
		if d.ProtocolID != "proto-1" {
			t.Errorf("设备 %s 协议ID = %s, want proto-1", d.Name, d.ProtocolID)
		}
		if d.ProtocolJSON != "{}" {
			t.Errorf("设备 %s protocol_json = %q, want {}", d.Name, d.ProtocolJSON)
		}
		if d.Status != 1 {
			t.Errorf("设备 %s status = %d, want 1", d.Name, d.Status)
		}
	}

	// 再次导入同文件 → 全部失败（库内已存在 / 协议仍未知）
	result2, err := svc.ImportDevices(ctx, buf.Bytes())
	if err != nil {
		t.Fatalf("二次导入: %v", err)
	}
	if result2.Success != 0 || result2.Failed != 4 {
		t.Errorf("二次导入 Success/Failed = %d/%d, want 0/4", result2.Success, result2.Failed)
	}
}

// TestGenerateImportTemplate 验证模板生成：可重新打开、表头正确、含示例行。
func TestGenerateImportTemplate(t *testing.T) {
	svc := &DeviceService{}
	buf, err := svc.GenerateImportTemplate()
	if err != nil {
		t.Fatalf("GenerateImportTemplate: %v", err)
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
	if rows[0][0] != "名称" || rows[0][1] != "协议" || rows[0][2] != "描述" {
		t.Errorf("表头 = %v, want [名称 协议 描述]", rows[0])
	}
}
