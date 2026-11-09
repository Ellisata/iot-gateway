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
	"iot-gateway/testutil/drivertest"
)

// newDeviceAddressImportTestDB 构建设备地址导入测试库（临时文件 SQLite）。
//
// trigger_rule 也建出来：删除设备前会检查触发规则引用（见
// ensureDevicesNotReferencedByTriggerRules），所有删除测试都会读到这张表。
// push_channel 同理：删除采集组前会检查通道订阅（见
// ensureCollectorGroupNotReferencedByPushChannels）。
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
	if err := db.AutoMigrate(&po.Device{}, &po.IotProtocol{}, &po.DeviceAddress{},
		&po.CollectorGroup{}, &po.TriggerRule{}, &po.PushChannel{}); err != nil {
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
		{caseName: "读写权限组合写法", name: "温度", label: "车间温度", commonType: "单精度浮点（Float）", rwPermission: "读写（RW）", scanFreq: "", wantReason: "", wantFreq: 1000},
		{caseName: "读写权限中文名", name: "温度", label: "车间温度", commonType: "Float", rwPermission: "只读", scanFreq: "", wantReason: "", wantFreq: 1000},
		{caseName: "名称为空", name: "", label: "车间温度", commonType: "Float", rwPermission: "R", scanFreq: "1000", wantReason: "名称为空"},
		{caseName: "标签为空", name: "温度", label: "", commonType: "Float", rwPermission: "R", scanFreq: "1000", wantReason: "标签为空"},
		{caseName: "通用数据类型为空", name: "温度", label: "车间温度", commonType: "", rwPermission: "R", scanFreq: "1000", wantReason: "通用数据类型为空"},
		{caseName: "读写权限非法", name: "温度", label: "车间温度", commonType: "Float", rwPermission: "X", scanFreq: "1000", wantReason: "读写权限须为"},
		{caseName: "扫描频率带可读单位", name: "温度", label: "车间温度", commonType: "Float", rwPermission: "R", scanFreq: "1000（1 秒，默认）", wantReason: "", wantFreq: 1000},
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
//
// 用 drivertest 的合成协议：本测试验的是导入流程（校验顺序、类型映射、
// 部分成功时的落库），与具体是哪个协议无关 —— 绑定真实协议会让本用例的
// 成败取决于那个协议的类型表内容。
func TestImportDeviceAddresses(t *testing.T) {
	drivertest.EnsureSyntheticProtocol()

	db := newDeviceAddressImportTestDB(t)
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: drivertest.TestProtocol}).Error; err != nil {
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
		{"湿度", "车间湿度", "UnknownType", "R", "1000", ""},      // 协议不支持该类型 → 失败
		{"压力", "车间压力", "Short", "读写（RW）", "500（0.5 秒）", ""}, // 成功：权限与频率都用可读写法
		{"温度", "车间温度2", "Float", "R", "1000", ""},           // 文件内重名 → 失败
		{"", "车间", "Float", "R", "1000", ""},                // 名称为空 → 失败
		{"电流", "车间电流", "Float", "X", "1000", ""},            // 读写权限非法 → 失败
		{"电压", "车间电压", "Float", "R", "abc", ""},             // 扫描频率非数字 → 失败
		{"转速", "车间转速", "单精度浮点（Float）", "只读", "200", "描述8"},  // 成功：类型与权限都用可读写法
		{"", "", "", "", "", ""},                            // 全空行 → 跳过
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
		"读写权限须为 R/W/RW（或 只读/只写/读写）",
		"扫描频率格式不正确（填毫秒整数，或从下拉选择）",
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
	if a := mapping["压力"]; a.DataType != "int16" || a.RwPermission != "RW" || a.ScanFrequency != 500 || a.CommonDataType != "Short" {
		t.Errorf("压力地址落库异常: %+v", a)
	}
	if a := mapping["转速"]; a.DataType != "float32" || a.ScanFrequency != 200 || a.CommonDataType != "Float" || a.RwPermission != "R" {
		t.Errorf("转速地址落库异常（可读写法应归一为 code）: %+v", a)
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

// TestGenerateDeviceAddressImportTemplate 验证地址导入模板：可重新打开、表头正确、
// 含示例行，且数据类型/读写权限两列带可读下拉。
func TestGenerateDeviceAddressImportTemplate(t *testing.T) {
	svc := &DeviceAddressService{}
	buf, err := svc.GenerateDeviceAddressImportTemplate(context.Background(), "")
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

	dvs, err := f.GetDataValidations(sheet)
	if err != nil {
		t.Fatalf("get validations: %v", err)
	}
	if len(dvs) != 3 {
		t.Fatalf("下拉数 = %d，期望 3", len(dvs))
	}
	if !strings.HasPrefix(dvs[0].Sqref, "C2:C") ||
		!strings.Contains(dvs[0].Formula1, "单精度浮点（Float）") ||
		!strings.Contains(dvs[0].Formula1, "布尔（Boolean）") {
		t.Fatalf("通用数据类型列下拉不符合预期：%+v", dvs[0])
	}
	if !strings.HasPrefix(dvs[1].Sqref, "D2:D") ||
		!strings.Contains(dvs[1].Formula1, "只读（R）") ||
		!strings.Contains(dvs[1].Formula1, "读写（RW）") {
		t.Fatalf("读写权限列下拉不符合预期：%+v", dvs[1])
	}
	if !strings.HasPrefix(dvs[2].Sqref, "E2:E") ||
		!strings.Contains(dvs[2].Formula1, "1000（1 秒") {
		t.Fatalf("扫描频率列下拉不符合预期：%+v", dvs[2])
	}
	if dvs[2].ShowErrorMessage {
		t.Errorf("扫描频率应允许手工输入下拉项以外的正整数")
	}
}

// TestGenerateDeviceAddressImportTemplateByProtocol 传 deviceId 时按协议给出
// 通用 + 扩展类型；未传时回退到通用列表，因此前者的下拉项更多。
func TestGenerateDeviceAddressImportTemplateByProtocol(t *testing.T) {
	drivertest.EnsureSyntheticProtocol()
	db := newDeviceAddressImportTestDB(t)
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: drivertest.TestProtocol}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&po.Device{ID: "dev-1", Name: "测试设备", ProtocolID: "proto-1", ProtocolJSON: "{}", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &DeviceAddressService{sqliteDB: db}
	buf, err := svc.GenerateDeviceAddressImportTemplate(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("GenerateDeviceAddressImportTemplate: %v", err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("reopen template: %v", err)
	}
	defer f.Close()
	dvs, err := f.GetDataValidations(f.GetSheetName(0))
	if err != nil || len(dvs) != 3 {
		t.Fatalf("下拉 = %+v, err = %v", dvs, err)
	}
	// 合成协议声明了扩展类型 Int/Real，应出现在类型下拉里。
	if !strings.Contains(dvs[0].Formula1, "Int") || !strings.Contains(dvs[0].Formula1, "Real") {
		t.Fatalf("类型下拉缺少协议扩展类型：%+v", dvs[0])
	}
}
