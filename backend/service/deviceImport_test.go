// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"

	"iot-gateway/formjson"
	"iot-gateway/model/po"
	"iot-gateway/utils"
)

// newImportTestDB 构建导入测试库（临时文件 SQLite，含 device / iot_protocol / thing_model 表）。
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
	if err := db.AutoMigrate(&po.Device{}, &po.IotProtocol{}, &po.ThingModel{}); err != nil {
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
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: "ModBus.TCP"}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &DeviceService{sqliteDB: db}
	ctx := context.Background()

	// 构造测试 Excel：表头 + 4 数据行 + 1 全空行
	var buf bytes.Buffer
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	for i, h := range []string{"名称", "协议", "描述", "编码"} {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	rows := [][4]string{
		{"导入设备1", "ModBus.TCP", "描述1", "code-1"},
		{"导入设备2", "Unknown.Proto", "", "code-2"}, // 协议不存在 → 失败
		{"导入设备1", "ModBus.TCP", "", "code-3"},    // 文件内重名 → 失败
		{"导入设备3", "ModBus.TCP", "", "code-4"},    // 成功
		{"", "", "", ""}, // 全空行 → 跳过
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
	codes := make(map[string]bool, len(devices))
	for _, d := range devices {
		if d.ProtocolID != "proto-1" {
			t.Errorf("设备 %s 协议ID = %s, want proto-1", d.Name, d.ProtocolID)
		}
		if d.ProtocolJSON != "{}" {
			t.Errorf("设备 %s protocol_json = %q, want {}", d.Name, d.ProtocolJSON)
		}
		if d.ScanIdentity != "" {
			t.Errorf("通用模板导入不应生成扫描身份，实际 %q", d.ScanIdentity)
		}
		if d.Status != 1 {
			t.Errorf("设备 %s status = %d, want 1", d.Name, d.Status)
		}
		// 编码必填，且文件内/库内唯一。
		if d.Code == "" || codes[d.Code] {
			t.Errorf("设备 %s 编码 = %q（为空或重复）", d.Name, d.Code)
		}
		codes[d.Code] = true
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

// TestImportDevicesRequiresCode 编码是必填列：留空/非法/重复只让该行失败。
func TestImportDevicesRequiresCode(t *testing.T) {
	db := newImportTestDB(t)
	seedImportProtocol(t, db, "proto-1", "ModBus.TCP", "")
	svc := &DeviceService{sqliteDB: db}

	rows := [][]string{
		{"名称", "协议", "描述", "编码"},
		{"设备A", "ModBus.TCP", "", ""},         // 编码为空
		{"设备B", "ModBus.TCP", "", "1bad"},     // 格式非法（数字开头）
		{"设备C", "ModBus.TCP", "", "dup-code"}, // 成功
		{"设备D", "ModBus.TCP", "", "dup-code"}, // 文件内编码重复
	}
	result, err := svc.ImportDevices(context.Background(), buildImportXlsx(t, rows))
	if err != nil {
		t.Fatalf("ImportDevices: %v", err)
	}
	if result.Total != 4 || result.Success != 1 || result.Failed != 3 {
		t.Fatalf("result = %+v", result)
	}
	wantReasons := []string{"编码为空", "设备编码不合法", "设备编码已存在"}
	for i, want := range wantReasons {
		if i >= len(result.Errors) || !strings.Contains(result.Errors[i].Reason, want) {
			t.Errorf("第 %d 条失败 = %+v, want 包含 %q", i+1, result.Errors, want)
		}
	}
}

// TestGenerateImportTemplate 验证通用模板：首表只有表头，示例行在第二张表。
func TestGenerateImportTemplate(t *testing.T) {
	db := newImportTestDB(t)
	svc := &DeviceService{sqliteDB: db}
	buf, err := svc.GenerateImportTemplate(context.Background(), "")
	if err != nil {
		t.Fatalf("GenerateImportTemplate: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("reopen template: %v", err)
	}
	defer f.Close()

	first := f.GetSheetName(0)
	rows, err := f.GetRows(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("首表行数 = %d, want 1（仅表头）: %v", len(rows), rows)
	}
	want := []string{"名称 *", "协议 *", "描述", "编码 *", "物模型", "状态"}
	if len(rows[0]) != len(want) {
		t.Fatalf("表头 = %v, want %v", rows[0], want)
	}
	for i := range want {
		if rows[0][i] != want[i] {
			t.Errorf("第 %d 列 = %q, want %q", i+1, rows[0][i], want[i])
		}
	}
	example, err := f.GetRows("示例")
	if err != nil {
		t.Fatalf("示例表: %v", err)
	}
	if len(example) < 2 || example[0][0] != "名称 *" || example[1][0] == "" {
		t.Errorf("示例表 = %v", example)
	}
	// 图例在示例数据下方（第 4 行），解释星号与表头配色。
	if len(example) < 4 || len(example[3]) == 0 || !strings.Contains(example[3][0], "*") {
		t.Errorf("示例表图例缺失: %v", example)
	}
}

// buildImportXlsx 用行数据构造内存 Excel（首行即表头）。
func buildImportXlsx(t *testing.T, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	for i, row := range rows {
		for j, value := range row {
			if value == "" {
				continue
			}
			cell, _ := excelize.CoordinatesToCellName(j+1, i+1)
			if err := f.SetCellValue(sheet, cell, value); err != nil {
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

const importTestProtocolForm = `{
  "rule": [
    {"type":"input","field":"host","title":"{{$t.host}}","$required":true},
    {"type":"inputNumber","field":"port","title":"{{$t.port}}","$required":true},
    {"type":"select","field":"byteOrder","title":"{{$t.byteOrder}}","$required":false,
     "options":[{"label":"{{$t.opt_default}}","value":""},{"label":"{{$t.opt_big}}","value":"BIG_ENDIAN"},{"label":"{{$t.opt_bool}}","value":true}]}
  ],
  "options": {"language": {
    "zh-cn": {"host":"设备IP","port":"设备端口","byteOrder":"字节序","opt_default":"默认","opt_big":"高字在前","opt_bool":"布尔选项"},
    "en": {"host":"Device IP","port":"Device port","byteOrder":"Byte order","opt_default":"Default","opt_big":"High word first","opt_bool":"Boolean option"}
  }}
}`

func seedImportProtocol(t *testing.T, db *gorm.DB, id, name, form string) {
	t.Helper()
	if err := db.Create(&po.IotProtocol{ID: id, Name: name, FormJSON: form}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestBuildDeviceImportPlan(t *testing.T) {
	t.Run("旧三列模板缺编码", func(t *testing.T) {
		_, err := buildDeviceImportPlan([]string{"名称", "协议", "描述"})
		if err == nil || !strings.Contains(err.Error(), "缺少「编码」") {
			t.Fatalf("err = %v, want 缺少编码列", err)
		}
	})

	t.Run("四列模板", func(t *testing.T) {
		plan, err := buildDeviceImportPlan([]string{"名称", "协议", "描述", "编码"})
		if err != nil {
			t.Fatal(err)
		}
		if plan.NameCol != 0 || plan.ProtocolCol != 1 || plan.DescriptionCol != 2 || plan.CodeCol != 3 {
			t.Errorf("plan = %+v", plan)
		}
		if plan.ModelCol != -1 || plan.StatusCol != -1 || plan.hasParams() {
			t.Errorf("四列模板不应识别出其余列: %+v", plan)
		}
	})

	t.Run("星号后置表头", func(t *testing.T) {
		plan, err := buildDeviceImportPlan([]string{"名称 *", "协议 *", "描述", "编码 *", "设备IP [host] *", "端口 [port]"})
		if err != nil {
			t.Fatal(err)
		}
		if plan.NameCol != 0 || plan.ProtocolCol != 1 || plan.CodeCol != 3 {
			t.Errorf("基础列 = %+v", plan)
		}
		if len(plan.Params) != 2 || plan.Params[0].Field.Key != "host" || plan.Params[1].Field.Key != "port" {
			t.Errorf("参数列 = %+v", plan.Params)
		}
	})

	t.Run("英文六列", func(t *testing.T) {
		plan, err := buildDeviceImportPlan([]string{"*Name", "*Protocol", "Description", "Code", "Thing model", "Status"})
		if err != nil {
			t.Fatal(err)
		}
		if plan.NameCol != 0 || plan.ProtocolCol != 1 || plan.CodeCol != 3 || plan.ModelCol != 4 || plan.StatusCol != 5 {
			t.Errorf("plan = %+v", plan)
		}
	})

	t.Run("乱序与参数列", func(t *testing.T) {
		plan, err := buildDeviceImportPlan([]string{"设备端口[port]", "编码", "备注", "名称", "协议", "设备IP *[host]"})
		if err != nil {
			t.Fatal(err)
		}
		if plan.NameCol != 3 || plan.ProtocolCol != 4 || plan.CodeCol != 1 {
			t.Errorf("基础列 = %+v", plan)
		}
		if len(plan.Params) != 2 || plan.Params[0].Field.Key != "port" || plan.Params[1].Field.Key != "host" {
			t.Errorf("参数列 = %+v", plan.Params)
		}
		if plan.Params[1].Field.Title != "设备IP" {
			t.Errorf("必填参数标题 = %q", plan.Params[1].Field.Title)
		}
	})

	for _, tt := range []struct {
		name   string
		header []string
		want   string
	}{
		{"缺名称", []string{"协议", "描述"}, "缺少「名称」"},
		{"缺协议", []string{"名称", "描述"}, "缺少「协议」"},
		{"缺编码", []string{"名称", "协议"}, "缺少「编码」"},
		{"基础列重复", []string{"名称", "name", "协议"}, "列重复"},
		{"参数列重复", []string{"名称", "协议", "a[host]", "b[host]"}, "协议参数列"},
		{"空表头", nil, "内容为空"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildDeviceImportPlan(tt.header)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want 包含 %q", err, tt.want)
			}
		})
	}
}

func TestConvertParamValue(t *testing.T) {
	selectField := formjson.Field{
		Key:   "byteOrder",
		Title: "字节序",
		Type:  "select",
		Options: []formjson.Option{
			{Label: "默认", Value: json.RawMessage(`""`)},
			{Label: "高字在前", Value: json.RawMessage(`"BIG_ENDIAN"`)},
			{Label: "数字选项", Value: json.RawMessage(`3`)},
			{Label: "布尔选项", Value: json.RawMessage(`true`)},
		},
	}

	tests := []struct {
		name     string
		field    formjson.Field
		cell     string
		wantRaw  string // "" 表示 nil
		wantFail string
	}{
		{name: "input 字符串", field: formjson.Field{Type: "input", Title: "设备IP"}, cell: "10.0.0.1", wantRaw: `"10.0.0.1"`},
		{name: "可选留空", field: formjson.Field{Type: "input", Title: "备注"}, cell: " ", wantRaw: ""},
		{name: "必填留空", field: formjson.Field{Type: "input", Title: "设备IP", Required: true}, cell: "", wantFail: "为必填项"},
		{name: "数字整数", field: formjson.Field{Type: "inputNumber", Title: "端口"}, cell: "502", wantRaw: "502"},
		{name: "数字小数", field: formjson.Field{Type: "inputNumber", Title: "系数"}, cell: "-2.5", wantRaw: "-2.5"},
		{name: "数字科学计数", field: formjson.Field{Type: "inputNumber", Title: "系数"}, cell: "1e3", wantRaw: "1e3"},
		{name: "数字非法", field: formjson.Field{Type: "inputNumber", Title: "端口"}, cell: "abc", wantFail: "必须是数字"},
		{name: "数字非法前导零", field: formjson.Field{Type: "inputNumber", Title: "端口"}, cell: "0502", wantFail: "必须是数字"},
		{name: "select 按 label", field: selectField, cell: "高字在前", wantRaw: `"BIG_ENDIAN"`},
		{name: "select 按原始值", field: selectField, cell: "BIG_ENDIAN", wantRaw: `"BIG_ENDIAN"`},
		{name: "select 数字原始值", field: selectField, cell: "数字选项", wantRaw: "3"},
		{name: "select 布尔原始值", field: selectField, cell: "布尔选项", wantRaw: "true"},
		{name: "select 空选项", field: selectField, cell: "默认", wantRaw: `""`},
		{name: "select 非法", field: selectField, cell: "不存在", wantFail: "不在可选项中"},
		{name: "switch true", field: formjson.Field{Type: "switch", Title: "启用"}, cell: "是", wantRaw: "true"},
		{name: "switch false", field: formjson.Field{Type: "switch", Title: "启用"}, cell: "否", wantRaw: "false"},
		{name: "switch 非法", field: formjson.Field{Type: "switch", Title: "启用"}, cell: "maybe", wantFail: "true/false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, reason := convertParamValue(tt.field, tt.cell)
			if tt.wantFail != "" {
				if reason == "" || !strings.Contains(reason, tt.wantFail) {
					t.Errorf("reason = %q, want 包含 %q", reason, tt.wantFail)
				}
				return
			}
			if reason != "" {
				t.Fatalf("unexpected reason: %s", reason)
			}
			if tt.wantRaw == "" {
				if raw != nil {
					t.Errorf("raw = %s, want nil", raw)
				}
				return
			}
			if string(raw) != tt.wantRaw {
				t.Errorf("raw = %s, want %s", raw, tt.wantRaw)
			}
		})
	}
}

func TestParseImportStatus(t *testing.T) {
	for _, tt := range []struct {
		cell string
		want int
		fail bool
	}{
		{"", 1, false}, {"启用", 1, false}, {"Enabled", 1, false}, {"1", 1, false}, {"true", 1, false},
		{"禁用", 0, false}, {"停用", 0, false}, {"disabled", 0, false}, {"0", 0, false}, {"false", 0, false},
		{"maybe", 0, true},
	} {
		got, reason := parseImportStatus(tt.cell)
		if tt.fail {
			if reason == "" {
				t.Errorf("%q 应失败", tt.cell)
			}
			continue
		}
		if reason != "" || got != tt.want {
			t.Errorf("%q = %d/%q, want %d", tt.cell, got, reason, tt.want)
		}
	}
}

func TestThingModelIndexResolve(t *testing.T) {
	idx := thingModelIndex{
		byName: map[string]po.ThingModel{
			"温度计":  {ID: "tm1", Name: "温度计", Code: "temp", Status: 1},
			"停用模型": {ID: "tm2", Name: "停用模型", Code: "off", Status: 0},
			"歧义":   {ID: "tm3", Name: "歧义", Code: "x", Status: 1},
		},
		byCode: map[string]po.ThingModel{
			"temp": {ID: "tm1", Name: "温度计", Code: "temp", Status: 1},
			"off":  {ID: "tm2", Name: "停用模型", Code: "off", Status: 0},
			"歧义":   {ID: "tm4", Name: "另一个", Code: "歧义", Status: 1},
		},
	}

	if id, reason := idx.resolve(""); id != "" || reason != "" {
		t.Errorf("空值 = %q/%q", id, reason)
	}
	if id, reason := idx.resolve("温度计"); id != "tm1" || reason != "" {
		t.Errorf("按名称 = %q/%q", id, reason)
	}
	if id, reason := idx.resolve("temp"); id != "tm1" || reason != "" {
		t.Errorf("按标识 = %q/%q", id, reason)
	}
	if _, reason := idx.resolve("不存在"); !strings.Contains(reason, "不存在") {
		t.Errorf("不存在 reason = %q", reason)
	}
	if _, reason := idx.resolve("停用模型"); !strings.Contains(reason, "已停用") {
		t.Errorf("停用 reason = %q", reason)
	}
	if _, reason := idx.resolve("歧义"); !strings.Contains(reason, "不明确") {
		t.Errorf("歧义 reason = %q", reason)
	}
}

func TestImportDevicesWithProtocolParams(t *testing.T) {
	db := newImportTestDB(t)
	seedImportProtocol(t, db, "proto-1", "ModBus.TCP", importTestProtocolForm)
	models := []po.ThingModel{
		{ID: "tm1", Name: "温度计", Code: "temp", Status: 1},
		{ID: "tm2", Name: "停用模型", Code: "off", Status: 1},
	}
	if err := db.Create(&models).Error; err != nil {
		t.Fatal(err)
	}
	// status 列有 default:1，GORM 会把零值当成未设置；显式更新成停用。
	if err := db.Model(&po.ThingModel{}).Where("id = ?", "tm2").Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	svc := &DeviceService{sqliteDB: db}

	rows := [][]string{
		{"*名称", "*协议", "描述", "编码", "物模型", "状态", "设备IP *[host]", "设备端口 *[port]", "字节序[byteOrder]"},
		{"设备1", "ModBus.TCP", "d1", "code-1", "温度计", "禁用", "10.0.0.1", "502", "布尔选项"},
		{"设备2", "ModBus.TCP", "", "", "", "", "10.0.0.2", "502", "不存在"},    // select 非法
		{"设备3", "ModBus.TCP", "", "", "", "", "", "502", ""},               // 必填为空
		{"设备4", "ModBus.TCP", "", "", "", "", "10.0.0.4", "abc", ""},       // 端口非数字
		{"设备5", "ModBus.TCP", "", "", "停用模型", "", "10.0.0.5", "502", ""},   // 物模型停用
		{"设备6", "ModBus.TCP", "", "code-1", "", "", "10.0.0.6", "502", ""}, // 编码重复
	}

	result, err := svc.ImportDevices(context.Background(), buildImportXlsx(t, rows))
	if err != nil {
		t.Fatalf("ImportDevices: %v", err)
	}
	if result.Total != 6 || result.Success != 1 || result.Failed != 5 {
		t.Fatalf("result = %+v", result)
	}

	var device po.Device
	if err := db.Where("name = ?", "设备1").First(&device).Error; err != nil {
		t.Fatal(err)
	}
	if device.Code != "code-1" || device.ModelID != "tm1" || device.Status != 0 {
		t.Errorf("设备1 = %+v", device)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(device.ProtocolJSON), &config); err != nil {
		t.Fatalf("protocol_json = %q: %v", device.ProtocolJSON, err)
	}
	if config["host"] != "10.0.0.1" {
		t.Errorf("host = %#v", config["host"])
	}
	if config["port"] != float64(502) {
		t.Errorf("port = %#v (%T), want number", config["port"], config["port"])
	}
	if config["byteOrder"] != true {
		t.Errorf("byteOrder = %#v (%T), want bool true", config["byteOrder"], config["byteOrder"])
	}
	if want := "ModBus.TCP|10.0.0.1|502|0"; device.ScanIdentity != want {
		t.Errorf("scanIdentity = %q, want %q", device.ScanIdentity, want)
	}
}

func TestDeriveScanIdentity(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		values   map[string]json.RawMessage
		want     string
	}{
		{
			name:     "host/port/unitId",
			protocol: "ModBus.TCP",
			values: map[string]json.RawMessage{
				"host":   json.RawMessage(`"10.0.0.1"`),
				"port":   json.RawMessage(`502`),
				"unitId": json.RawMessage(`3`),
			},
			want: "ModBus.TCP|10.0.0.1|502|3",
		},
		{
			name:     "host 归一为小写并去空白",
			protocol: "Siemens.S7",
			values: map[string]json.RawMessage{
				"host": json.RawMessage(`" PLC.Example.COM "`),
				"port": json.RawMessage(`102`),
			},
			want: "Siemens.S7|plc.example.com|102|0",
		},
		{
			name:     "FINS dstNode 作为站号",
			protocol: "Omron.FINS.TCP",
			values: map[string]json.RawMessage{
				"host":    json.RawMessage(`"10.0.0.2"`),
				"port":    json.RawMessage(`9600`),
				"dstNode": json.RawMessage(`"7"`),
			},
			want: "Omron.FINS.TCP|10.0.0.2|9600|7",
		},
		{
			name:     "FINS 未填 dstNode 时按 IP 末段推导",
			protocol: "Omron.FINS.TCP",
			values: map[string]json.RawMessage{
				"host": json.RawMessage(`"10.0.0.12"`),
				"port": json.RawMessage(`9600`),
			},
			want: "Omron.FINS.TCP|10.0.0.12|9600|12",
		},
		{
			name:     "字符串端口",
			protocol: "ModBus.TCP",
			values: map[string]json.RawMessage{
				"host": json.RawMessage(`"10.0.0.3"`),
				"port": json.RawMessage(`"1502"`),
			},
			want: "ModBus.TCP|10.0.0.3|1502|0",
		},
		{
			name:     "没有 host 不生成",
			protocol: "ModBus.RTU",
			values: map[string]json.RawMessage{
				"comPort": json.RawMessage(`"COM1"`),
				"unitId":  json.RawMessage(`1`),
			},
			want: "",
		},
		{
			name:     "host 为空不生成",
			protocol: "ModBus.TCP",
			values: map[string]json.RawMessage{
				"host": json.RawMessage(`""`),
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveScanIdentity(tt.protocol, tt.values); got != tt.want {
				t.Errorf("deriveScanIdentity = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestImportDevicesRejectsMixedProtocols(t *testing.T) {
	db := newImportTestDB(t)
	seedImportProtocol(t, db, "proto-1", "ModBus.TCP", importTestProtocolForm)
	seedImportProtocol(t, db, "proto-2", "OPC.UA", importTestProtocolForm)
	svc := &DeviceService{sqliteDB: db}

	rows := [][]string{
		{"名称", "协议", "编码", "设备IP *[host]", "设备端口 *[port]", "字节序[byteOrder]"},
		{"A", "ModBus.TCP", "code-a", "10.0.0.1", "502", ""},
		{"B", "OPC.UA", "code-b", "10.0.0.2", "502", ""},
	}
	_, err := svc.ImportDevices(context.Background(), buildImportXlsx(t, rows))
	if err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("err = %v, want 整表协议不一致", err)
	}
	var count int64
	db.Model(&po.Device{}).Count(&count)
	if count != 0 {
		t.Errorf("混协议时库内条数 = %d, want 0", count)
	}
}

func TestImportDevicesRejectsStaleOrIncompleteTemplate(t *testing.T) {
	tests := []struct {
		name   string
		header []string
		want   string
	}{
		{
			name:   "参数列不属于协议",
			header: []string{"名称", "协议", "编码", "设备IP *[host]", "未知[unknown]"},
			want:   "不是协议",
		},
		{
			name:   "缺必填参数列",
			header: []string{"名称", "协议", "编码", "设备端口 *[port]"},
			want:   "必填参数",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newImportTestDB(t)
			seedImportProtocol(t, db, "proto-1", "ModBus.TCP", importTestProtocolForm)
			svc := &DeviceService{sqliteDB: db}
			row := append([]string{"设备", "ModBus.TCP", "dev-1"}, make([]string, len(tt.header)-3)...)
			_, err := svc.ImportDevices(context.Background(), buildImportXlsx(t, [][]string{tt.header, row}))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want 包含 %q", err, tt.want)
			}
			var count int64
			db.Model(&po.Device{}).Count(&count)
			if count != 0 {
				t.Errorf("库内条数 = %d, want 0", count)
			}
		})
	}
}

func TestGenerateImportTemplateForProtocol(t *testing.T) {
	db := newImportTestDB(t)
	seedImportProtocol(t, db, "proto-1", "ModBus.TCP", importTestProtocolForm)
	svc := &DeviceService{sqliteDB: db}

	buf, err := svc.GenerateImportTemplate(context.Background(), "proto-1")
	if err != nil {
		t.Fatalf("GenerateImportTemplate: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		t.Fatal(err)
	}
	wantHeaders := []string{"名称 *", "协议 *", "描述", "编码 *", "物模型", "状态", "设备IP [host] *", "设备端口 [port] *", "字节序 [byteOrder]"}
	if len(rows) != 1 || len(rows[0]) != len(wantHeaders) {
		t.Fatalf("表头 = %v", rows)
	}
	for i := range wantHeaders {
		if rows[0][i] != wantHeaders[i] {
			t.Errorf("第 %d 列 = %q, want %q", i+1, rows[0][i], wantHeaders[i])
		}
	}
	// 协议列锁死为该协议，字节序列有下拉。
	validations, err := f.GetDataValidations(f.GetSheetName(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(validations) < 2 {
		t.Errorf("下拉校验 = %+v, want >= 2", validations)
	}

	// 必填列与选填列使用不同表头样式。
	requiredStyle, err := f.GetCellStyle(f.GetSheetName(0), "A1")
	if err != nil {
		t.Fatal(err)
	}
	optionalStyle, err := f.GetCellStyle(f.GetSheetName(0), "C1")
	if err != nil {
		t.Fatal(err)
	}
	if requiredStyle == optionalStyle {
		t.Error("必填列与选填列的表头样式应不同")
	}

	// 英文请求头：基础列与 {{$t.key}} 标题都跟着语言走。
	enCtx := utils.SetAreaToCtx(context.Background(), "en")
	enBuf, err := svc.GenerateImportTemplate(enCtx, "proto-1")
	if err != nil {
		t.Fatal(err)
	}
	enFile, err := excelize.OpenReader(bytes.NewReader(enBuf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer enFile.Close()
	enRows, err := enFile.GetRows(enFile.GetSheetName(0))
	if err != nil {
		t.Fatal(err)
	}
	if enRows[0][0] != "Name *" || enRows[0][1] != "Protocol *" || enRows[0][6] != "Device IP [host] *" {
		t.Errorf("英文表头 = %v", enRows[0])
	}

	if _, err := svc.GenerateImportTemplate(context.Background(), "missing"); err == nil {
		t.Error("协议不存在时应报错")
	}
}
