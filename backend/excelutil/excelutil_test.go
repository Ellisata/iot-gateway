// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package excelutil

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

// testItem 测试用的最小实体
type testItem struct {
	ID    string `gorm:"primaryKey"`
	Name  string `gorm:"column:name;uniqueIndex"`
	Value string
}

func (testItem) TableName() string { return "test_item" }

func newTestDB(t *testing.T) *gorm.DB {
	dbPath := filepath.Join(t.TempDir(), "excelutil_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&testItem{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// buildTestXlsx 按行构造测试 Excel（首行即表头）。
func buildTestXlsx(t *testing.T, rows [][]string) []byte {
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	for i, r := range rows {
		for j, v := range r {
			if v == "" {
				continue
			}
			cell, _ := excelize.CoordinatesToCellName(j+1, i+1)
			_ = f.SetCellValue(sheet, cell, v)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestImport 覆盖框架：合法入库 / 空行跳过 / 库内与文件内去重 / 汇总计数 / 落库。
func TestImport(t *testing.T) {
	db := newTestDB(t)
	// 预置一条已存在记录，验证库内去重
	if err := db.Create(&testItem{ID: "exist", Name: "exist", Value: "old"}).Error; err != nil {
		t.Fatal(err)
	}

	data := buildTestXlsx(t, [][]string{
		{"名称", "备注"},     // 表头
		{"A", "一"},       // 成功
		{"B", "二"},       // 成功
		{"A", "文件内重"},    // 文件内重复 → 失败
		{"exist", "库内重"}, // 库内重复 → 失败
		{"", ""},         // 空行跳过
		{"C", "三"},       // 成功
	})

	result, err := Import(context.Background(), db, data, Options[testItem]{
		Headers:      []string{"名称", "备注"},
		ExistingKeys: []string{"exist"},
		Cols:         2,
		DupMsg:       "名称已存在",
		Parse: func(row []string) (*testItem, string, string) {
			name := Cell(row, 0)
			if name == "" {
				return nil, name, "名称为空"
			}
			return &testItem{ID: name, Name: name, Value: Cell(row, 1)}, name, ""
		},
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.Total != 5 {
		t.Errorf("Total = %d, want 5", result.Total)
	}
	if result.Success != 3 {
		t.Errorf("Success = %d, want 3", result.Success)
	}
	if result.Failed != 2 {
		t.Errorf("Failed = %d, want 2", result.Failed)
	}
	// 失败明细：文件内重复(A, 行4) 与 库内重复(exist, 行5)
	if len(result.Errors) != 2 || result.Errors[0].Row != 4 || result.Errors[1].Row != 5 || result.Errors[0].Reason != "名称已存在" {
		t.Errorf("errors = %+v", result.Errors)
	}

	var count int64
	db.Model(&testItem{}).Count(&count)
	if count != 4 { // exist + A + B + C
		t.Errorf("库内条数 = %d, want 4", count)
	}
}

// TestImportHeaderMismatch 表头不符应报错。
func TestImportHeaderMismatch(t *testing.T) {
	db := newTestDB(t)
	data := buildTestXlsx(t, [][]string{{"名称", "错了"}, {"A", "一"}})
	_, err := Import(context.Background(), db, data, Options[testItem]{
		Headers: []string{"名称", "备注"},
		Cols:    2,
		Parse: func(row []string) (*testItem, string, string) {
			return &testItem{ID: Cell(row, 0), Name: Cell(row, 0)}, Cell(row, 0), ""
		},
	})
	if err == nil || !strings.Contains(err.Error(), "模板表头不正确") {
		t.Errorf("表头错误时应报错, got %v", err)
	}
}

// TestImportOnlyHeader 仅表头无数据应返回空汇总（不报错）。
func TestImportOnlyHeader(t *testing.T) {
	db := newTestDB(t)
	data := buildTestXlsx(t, [][]string{{"名称", "备注"}})
	result, err := Import(context.Background(), db, data, Options[testItem]{
		Headers: []string{"名称", "备注"},
		Cols:    2,
		Parse: func(row []string) (*testItem, string, string) {
			return &testItem{ID: Cell(row, 0), Name: Cell(row, 0)}, Cell(row, 0), ""
		},
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.Total != 0 || result.Failed != 0 {
		t.Errorf("仅表头应返回空汇总, got %+v", result)
	}
}

// TestBuildTemplate 验证模板生成：可重新打开、表头正确、含示例行。
func TestBuildTemplate(t *testing.T) {
	buf, err := BuildTemplate(
		[]string{"名称", "协议", "描述"},
		[]string{"示例", "ModBus.TCP", "说明"},
		[]float64{30, 20, 40},
	)
	if err != nil {
		t.Fatalf("BuildTemplate: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("reopen template: %v", err)
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("模板行数 = %d, want >= 2", len(rows))
	}
	if rows[0][0] != "名称" || rows[0][1] != "协议" || rows[0][2] != "描述" {
		t.Errorf("表头 = %v", rows[0])
	}
	if rows[1][0] != "示例" {
		t.Errorf("示例行 = %v", rows[1])
	}
}
