// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"iot-gateway/enums"
	"iot-gateway/model/po"
)

// buildThingModelImportXlsx 按行构造测试 Excel（首行即表头）。
func buildThingModelImportXlsx(t *testing.T, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	for i, row := range rows {
		for j, v := range row {
			cell, err := excelize.CoordinatesToCellName(j+1, i+1)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellValue(sheet, cell, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestImportThingModels 覆盖：合法入库 / 标识重复 / 名称重复（库内与文件内）/
// 分类非法 / 空名称 / 默认版本 / 返回汇总。
func TestImportThingModels(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	seedThingModel(t, svc, "th_existing", "已存在模型", "sensor")

	data := buildThingModelImportXlsx(t, [][]string{
		{"模型标识", "名称", "分类", "版本", "描述"},
		{"th_sensor", "温湿度传感器", "传感器（sensor）", "1.0.0", "温湿度"}, // 成功：模板下拉写法
		{"th_sensor", "另一个传感器", "meter", "", ""},               // 标识重复（文件内）
		{"th_meter", "已存在模型", "meter", "", ""},                 // 名称重复（库内）
		{"th_dup_name", "温湿度传感器", "other", "", ""},             // 名称重复（文件内）
		{"th_bad_cat", "分类错误", "sensorx", "", ""},              // 分类非法
		{"th_no_name", "", "sensor", "", ""},                   // 名称为空
		{"th_plc", "控制器", "PLC / 控制器", "", "中文名写法"},            // 成功：中文展示名
	})

	result, err := svc.ImportThingModels(context.Background(), data)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.Total != 7 || result.Success != 2 || result.Failed != 5 {
		t.Fatalf("汇总 = total %d / success %d / failed %d，期望 7/2/5: %+v",
			result.Total, result.Success, result.Failed, result.Errors)
	}

	var created []string
	if err := db.Model(&po.ThingModel{}).
		Where("code IN ?", []string{"th_sensor", "th_plc"}).
		Pluck("code", &created).Error; err != nil {
		t.Fatalf("query created: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("成功导入的模型数 = %d，期望 2", len(created))
	}

	var plc struct {
		Category string
		Version  string
	}
	if err := db.Table("thing_model").Select("category, version").
		Where("code = ?", "th_plc").Scan(&plc).Error; err != nil {
		t.Fatalf("query th_plc: %v", err)
	}
	if plc.Category != "plc" || plc.Version != "1.0.0" {
		t.Fatalf("中文名分类/默认版本 = %q/%q，期望 \"plc\"/\"1.0.0\"", plc.Category, plc.Version)
	}

	var sensorCategory string
	if err := db.Table("thing_model").Select("category").
		Where("code = ?", "th_sensor").Scan(&sensorCategory).Error; err != nil {
		t.Fatalf("query th_sensor: %v", err)
	}
	if sensorCategory != "sensor" {
		t.Fatalf("「传感器（sensor）」落库分类 = %q，期望 \"sensor\"", sensorCategory)
	}
}

// TestImportThingModelProperties 覆盖：合法入库 / 标识重复（库内与文件内）/
// 类型与读写模式非法 / 排序非法 / 排序留空自动顺延 / 返回汇总。
func TestImportThingModelProperties(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	model := seedThingModel(t, svc, "th_sensor", "温湿度传感器", "sensor")
	seedThingModelProperty(t, svc, model.ID, "temp")

	data := buildThingModelImportXlsx(t, [][]string{
		{"属性标识", "名称", "数据类型", "读写模式", "单位", "排序", "描述"},
		{"temp", "库存温度", "float", "R", "", "", ""},              // 标识重复（库内）
		{"humidity", "湿度", "float", "R", "%", "", ""},           // 成功：code 写法，自动排序 10
		{"pressure", "压力", "浮点", "r", "", "", ""},               // 成功：中文名 + code 大小写归一，自动排序 20
		{"humidity", "重复湿度", "float", "R", "", "", ""},          // 标识重复（文件内）
		{"bad_kind", "类型错误", "decimal", "R", "", "", ""},        // 类型非法
		{"bad_access", "权限错误", "float", "X", "", "", ""},        // 读写模式非法
		{"neg_sort", "排序错误", "float", "R", "", "-1", ""},        // 排序非法
		{"flow", "流量", "浮点（float）", "读写（RW）", "m³/h", "30", ""}, // 成功：模板下拉写法，显式排序 30
	})

	result, err := svc.ImportThingModelProperties(context.Background(), model.ID, data)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.Total != 8 || result.Success != 3 || result.Failed != 5 {
		t.Fatalf("汇总 = total %d / success %d / failed %d，期望 8/3/5: %+v",
			result.Total, result.Success, result.Failed, result.Errors)
	}

	var rows []struct {
		Identifier string
		SortOrder  int
	}
	if err := db.Table("thing_model_property").Select("identifier, sort_order").
		Where("model_id = ? AND identifier IN ?", model.ID, []string{"humidity", "pressure", "flow"}).
		Order("sort_order ASC").Scan(&rows).Error; err != nil {
		t.Fatalf("query properties: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("成功导入的属性数 = %d，期望 3", len(rows))
	}
	wantOrder := map[string]int{"humidity": 10, "pressure": 20, "flow": 30}
	for _, r := range rows {
		if wantOrder[r.Identifier] != r.SortOrder {
			t.Fatalf("%s 排序 = %d，期望 %d", r.Identifier, r.SortOrder, wantOrder[r.Identifier])
		}
	}

	// pressure 的读写模式经大小写归一后应落成 R
	var access string
	if err := db.Table("thing_model_property").Select("access_mode").
		Where("model_id = ? AND identifier = ?", model.ID, "pressure").Scan(&access).Error; err != nil {
		t.Fatalf("query pressure: %v", err)
	}
	if access != "R" {
		t.Fatalf("pressure 读写模式 = %q，期望 R", access)
	}
}

// TestThingModelImportTemplateDropdowns 模板里的枚举列必须是下拉选择，
// 否则用户手填的取值与导入校验不一致时，只能在导入后才看到失败。
func TestThingModelImportTemplateDropdowns(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)

	readValidations := func(buf *bytes.Buffer) []*excelize.DataValidation {
		t.Helper()
		f, err := excelize.OpenReader(buf)
		if err != nil {
			t.Fatalf("open template: %v", err)
		}
		defer f.Close()
		dvs, err := f.GetDataValidations(f.GetSheetName(0))
		if err != nil {
			t.Fatalf("get validations: %v", err)
		}
		return dvs
	}

	modelTpl, err := svc.GenerateThingModelImportTemplate()
	if err != nil {
		t.Fatalf("generate model template: %v", err)
	}
	modelDVs := readValidations(modelTpl)
	if len(modelDVs) != 1 {
		t.Fatalf("物模型模板下拉数 = %d，期望 1", len(modelDVs))
	}
	if modelDVs[0].Type != "list" || !strings.HasPrefix(modelDVs[0].Sqref, "C2:C") ||
		!strings.Contains(modelDVs[0].Formula1, "传感器（sensor）") ||
		!strings.Contains(modelDVs[0].Formula1, "PLC / 控制器（plc）") {
		t.Fatalf("分类列下拉不符合预期：%+v", modelDVs[0])
	}

	propTpl, err := svc.GenerateThingModelPropertyImportTemplate()
	if err != nil {
		t.Fatalf("generate property template: %v", err)
	}
	propDVs := readValidations(propTpl)
	if len(propDVs) != 2 {
		t.Fatalf("属性模板下拉数 = %d，期望 2", len(propDVs))
	}
	if !strings.HasPrefix(propDVs[0].Sqref, "C2:C") || !strings.Contains(propDVs[0].Formula1, "浮点（float）") {
		t.Fatalf("数据类型列下拉不符合预期：%+v", propDVs[0])
	}
	if !strings.HasPrefix(propDVs[1].Sqref, "D2:D") || !strings.Contains(propDVs[1].Formula1, "只读（R）") {
		t.Fatalf("读写模式列下拉不符合预期：%+v", propDVs[1])
	}
}

// TestImportThingModelPropertiesModelNotFound 确保模型不存在时整批拒绝。
func TestImportThingModelPropertiesModelNotFound(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)

	data := buildThingModelImportXlsx(t, [][]string{
		{"属性标识", "名称", "数据类型", "读写模式", "单位", "排序", "描述"},
		{"humidity", "湿度", "float", "R", "%", "10", ""},
	})
	_, err := svc.ImportThingModelProperties(context.Background(), "missing", data)
	wantThingModelErrCode(t, err, enums.ThingModelNotExistsEnum.GetCode())
}
