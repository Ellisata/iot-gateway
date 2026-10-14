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

	"iot-gateway/alarm"
	"iot-gateway/driver"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/testutil/drivertest"
)

// newThresholdTestDB 建阈值测试库：地址表 + 报警表（清理路径要读 alarm 表）。
func newThresholdTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "addr_threshold_test.db")
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
		&po.Alarm{}, &po.PointThresholdRecord{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func one() *int  { v := 1; return &v }
func zero() *int { v := 0; return &v }

// TestValidateAddressThreshold 覆盖报警条件校验纯函数的每条规则。
//
// 可用比较符集合来自 driver.OperatorsForKind（前后端共用的唯一真源），
// 所以这里也顺带钉住了「数值/布尔/文本/时间各能配什么」这条边界。
func TestValidateAddressThreshold(t *testing.T) {
	tests := []struct {
		caseName string
		enabled  bool
		op       string
		values   []string
		kind     string
		wantErr  string // 期望错误信息包含的子串，"" 表示通过
	}{
		// 未启用：不做任何检查，算子与操作数照常持久化
		{caseName: "未启用时不校验", enabled: false, kind: driver.KindTime},
		{caseName: "未启用的残缺条件也放行", enabled: false, op: driver.OpGt, kind: ""},

		// 类型不支持
		{caseName: "时间类型被拒", enabled: true, op: driver.OpEq, values: []string{"x"}, kind: driver.KindTime, wantErr: "时间类型不支持"},
		{caseName: "未知类型被拒", enabled: true, op: driver.OpEq, values: []string{"1"}, kind: "", wantErr: "无法识别"},

		// 算子缺失 / 与类型不匹配
		{caseName: "启用但没选算子", enabled: true, values: []string{"40"}, kind: driver.KindFloat, wantErr: "必须选择比较符"},
		{caseName: "数值配区间", enabled: true, op: driver.OpRange, values: []string{"10", "40"}, kind: driver.KindFloat},
		{caseName: "数值配大于", enabled: true, op: driver.OpGt, values: []string{"40"}, kind: driver.KindFloat},
		{caseName: "数值配属于集合被拒", enabled: true, op: driver.OpIn, values: []string{"a"}, kind: driver.KindFloat, wantErr: "比较符只能是"},
		{caseName: "布尔配区间被拒", enabled: true, op: driver.OpRange, values: []string{"0", "1"}, kind: driver.KindBool, wantErr: "比较符只能是"},
		{caseName: "文本配大于被拒", enabled: true, op: driver.OpGt, values: []string{"a"}, kind: driver.KindString, wantErr: "比较符只能是"},

		// range 的操作数
		{caseName: "区间只给一个操作数被拒", enabled: true, op: driver.OpRange, values: []string{"40"}, kind: driver.KindFloat, wantErr: "两个操作数"},
		{caseName: "区间三个操作数被拒", enabled: true, op: driver.OpRange, values: []string{"1", "2", "3"}, kind: driver.KindFloat, wantErr: "两个操作数"},
		{caseName: "区间两侧都空被拒", enabled: true, op: driver.OpRange, values: []string{"", ""}, kind: driver.KindFloat, wantErr: "至少填下限或上限"},
		{caseName: "区间单侧留空合法", enabled: true, op: driver.OpRange, values: []string{"", "40"}, kind: driver.KindFloat},
		{caseName: "区间下限等于上限被拒", enabled: true, op: driver.OpRange, values: []string{"40", "40"}, kind: driver.KindFloat, wantErr: "下限必须小于上限"},
		{caseName: "区间下限大于上限被拒", enabled: true, op: driver.OpRange, values: []string{"50", "40"}, kind: driver.KindFloat, wantErr: "下限必须小于上限"},
		{caseName: "区间下限非数值被拒", enabled: true, op: driver.OpRange, values: []string{"abc", "40"}, kind: driver.KindFloat, wantErr: "下限必须是数值"},
		{caseName: "区间上限非数值被拒", enabled: true, op: driver.OpRange, values: []string{"10", "abc"}, kind: driver.KindFloat, wantErr: "上限必须是数值"},
		// 0 是合法边界：不能因为"零值像未设置"就误判
		{caseName: "区间上限为 0 合法", enabled: true, op: driver.OpRange, values: []string{"-10", "0"}, kind: driver.KindFloat},

		// 单比较算子的操作数
		{caseName: "大于缺操作数被拒", enabled: true, op: driver.OpGt, values: nil, kind: driver.KindFloat, wantErr: "一个操作数"},
		{caseName: "大于两个操作数被拒", enabled: true, op: driver.OpGt, values: []string{"1", "2"}, kind: driver.KindFloat, wantErr: "一个操作数"},
		{caseName: "大于操作数非数值被拒", enabled: true, op: driver.OpGt, values: []string{"abc"}, kind: driver.KindFloat, wantErr: "必须是数值"},
		{caseName: "数值等于合法", enabled: true, op: driver.OpEq, values: []string{"0"}, kind: driver.KindInt},
		{caseName: "uint 也支持区间", enabled: true, op: driver.OpRange, values: []string{"0", "100"}, kind: driver.KindUInt},

		// 布尔
		{caseName: "布尔等于1合法", enabled: true, op: driver.OpEq, values: []string{"1"}, kind: driver.KindBool},
		{caseName: "布尔等于0合法", enabled: true, op: driver.OpEq, values: []string{"0"}, kind: driver.KindBool},
		{caseName: "布尔等于true合法", enabled: true, op: driver.OpEq, values: []string{"true"}, kind: driver.KindBool},
		{caseName: "布尔不等于false合法", enabled: true, op: driver.OpNe, values: []string{"False"}, kind: driver.KindBool},
		{caseName: "布尔操作数非真值被拒", enabled: true, op: driver.OpEq, values: []string{"on"}, kind: driver.KindBool, wantErr: "只能是 1、0、true 或 false"},
		{caseName: "布尔缺操作数被拒", enabled: true, op: driver.OpEq, values: nil, kind: driver.KindBool, wantErr: "需要一个操作数"},

		// 文本
		{caseName: "文本等于合法", enabled: true, op: driver.OpEq, values: []string{"FAULT"}, kind: driver.KindString},
		{caseName: "文本不等号合法", enabled: true, op: driver.OpNe, values: []string{"RUNNING"}, kind: driver.KindString},
		{caseName: "文本等于空值被拒", enabled: true, op: driver.OpEq, values: []string{""}, kind: driver.KindString, wantErr: "非空的比较值"},
		{caseName: "文本属于集合合法", enabled: true, op: driver.OpIn, values: []string{"FAULT", "OVERLOAD"}, kind: driver.KindString},
		{caseName: "文本属于空集合被拒", enabled: true, op: driver.OpIn, values: []string{"", "  "}, kind: driver.KindString, wantErr: "至少需要一个非空取值"},
		{caseName: "文本属于单元素合法", enabled: true, op: driver.OpIn, values: []string{"FAULT"}, kind: driver.KindString},
	}

	for _, c := range tests {
		t.Run(c.caseName, func(t *testing.T) {
			err := validateAddressThreshold(c.enabled, c.op, c.values, c.kind)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过，实际报错 %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望报错含 %q，实际通过", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("错误信息应含 %q，实际 %q", c.wantErr, err.Error())
			}
		})
	}
}

// TestCreateStringEqConditionRoundTrip 字符串点位的「等于」条件能通过校验并原样落库。
//
// 这是一条**跨层**的守卫：条件的可用性取决于三处对齐 —— 前端的比较符下拉、
// 保存时的 Kind 解析、以及采集/判定时的 Kind 匹配。任一处把 string 认错，
// 症状都是"配了等于条件却永远不报警"，且**没有任何报错**（判定侧对认不出的
// 类型是中性处理）。所以这里断言到落库的每一个字段。
func TestCreateStringEqConditionRoundTrip(t *testing.T) {
	drivertest.EnsureSyntheticProtocol()
	db := newThresholdTestDB(t)
	svc := NewDeviceAddressService(db, NewAlarmService(db))
	ctx := context.Background()

	if err := db.Create(&po.IotProtocol{ID: "proto-str", Name: drivertest.TestProtocol}).Error; err != nil {
		t.Fatalf("seed protocol: %v", err)
	}
	if err := db.Create(&po.Device{ID: "dev-s", Name: "s", ProtocolID: "proto-str"}).Error; err != nil {
		t.Fatalf("seed device: %v", err)
	}

	if err := svc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID:       "dev-s",
		Name:           "S1",
		Label:          "状态字",
		CommonDataType: driver.TypeString,
		RwPermission:   "R",
		ScanFrequency:  1000,
		Threshold: &dto.DeviceAddressThresholdDTO{
			Enabled: one(),
			Op:      strp(driver.OpEq),
			Values:  []string{"FAULT"},
		},
	}); err != nil {
		t.Fatalf("字符串点位配等于条件应被接受，实际报错：%v", err)
	}

	var a po.DeviceAddress
	if err := db.Where("device_id = ?", "dev-s").First(&a).Error; err != nil {
		t.Fatalf("load address: %v", err)
	}
	if a.DataType != "string" {
		t.Fatalf("内部类型应为 string，实际 %q", a.DataType)
	}
	if a.ThresholdEnabled != 1 || a.ThresholdOp != driver.OpEq || a.ThresholdValue != `["FAULT"]` {
		t.Fatalf("条件未原样落库：enabled=%d op=%q value=%q",
			a.ThresholdEnabled, a.ThresholdOp, a.ThresholdValue)
	}

	// 读回时（列表/详情的 VO）也要带上，否则编辑框打开就是空的
	got, err := svc.GetDeviceAddressByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.ThresholdOp != driver.OpEq || got.ThresholdValue != `["FAULT"]` {
		t.Fatalf("VO 未回传条件：op=%q value=%q", got.ThresholdOp, got.ThresholdValue)
	}
}

// strp 取字符串字面量的指针（DTO 里 Op 是可空指针）。
func strp(s string) *string { return &s }

// TestMarshalThresholdValues 操作数落库为 JSON 数组，空白项被去掉；
// 全空时落空串（等价于未配置，采集侧据此判定 Inert）。
func TestMarshalThresholdValues(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"区间", []string{"10", "40"}, `["10","40"]`},
		{"单侧留空保留位置", []string{"", "40"}, `["40"]`},
		{"单值", []string{"FAULT"}, `["FAULT"]`},
		{"集合", []string{"A", "B"}, `["A","B"]`},
		{"空白项被去掉", []string{" A ", "", "B"}, `["A","B"]`},
		{"全空落空串", []string{"", "  "}, ""},
		{"nil 落空串", nil, ""},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			if got := marshalThresholdValues(c.values); got != c.want {
				t.Fatalf("marshalThresholdValues(%v) = %q，期望 %q", c.values, got, c.want)
			}
		})
	}
}

// TestUpdateDeviceAddressClearsThresholdAlarmWhenInert 把阈值改成空配置时，
// 活跃的超限报警必须被清掉，且**不得**动同一点位的质量报警。
//
// 这是采集侧"空配置不发信号"的必要配套：没有任何信号会来恢复它，
// 不显式清就永久挂在"未恢复"上（与点位删除时的 ClearPointAlarm 同一理由）。
func TestUpdateDeviceAddressClearsThresholdAlarmWhenInert(t *testing.T) {
	db := newThresholdTestDB(t)
	svc := NewDeviceAddressService(db, NewAlarmService(db))
	ctx := context.Background()

	if err := db.Create(&po.Device{ID: "dev-1", Name: "1#炉", ProtocolID: "proto-1"}).Error; err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if err := db.Create(&po.DeviceAddress{
		ID: "pt-1", DeviceID: "dev-1", Name: "D0", Label: "温度",
		CommonDataType: "Float", DataType: "float", RwPermission: "R", Status: 1,
		ThresholdEnabled: 1, ThresholdOp: driver.OpGt, ThresholdValue: `["40"]`,
	}).Error; err != nil {
		t.Fatalf("seed address: %v", err)
	}

	// 两条活跃报警：一条超限（待清），一条质量（必须留着）
	for _, a := range []po.Alarm{
		{ID: "al-thr", TargetID: "pt-1", TargetName: "1#炉.温度", TargetType: alarm.TypePointThreshold,
			AlarmType: alarm.TypeThreshold, DeviceID: "dev-1", Status: alarm.StatusActive,
			FirstOccurTime: "2026-01-01 00:00:00", LastOccurTime: "2026-01-01 00:00:00"},
		{ID: "al-qa", TargetID: "pt-1", TargetName: "1#炉.温度", TargetType: alarm.TypePoint,
			AlarmType: alarm.TypeQuality, DeviceID: "dev-1", Status: alarm.StatusActive,
			FirstOccurTime: "2026-01-01 00:00:00", LastOccurTime: "2026-01-01 00:00:00"},
	} {
		if err := db.Create(&a).Error; err != nil {
			t.Fatalf("seed alarm: %v", err)
		}
	}

	// 关掉阈值开关（enabled=0 → inert）
	if err := svc.UpdateDeviceAddress(ctx, &dto.UpdateDeviceAddressDTO{
		ID: "pt-1", Label: "温度",
		Threshold: &dto.DeviceAddressThresholdDTO{Enabled: zero()},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	var thr po.Alarm
	if err := db.Where("id = ?", "al-thr").First(&thr).Error; err != nil {
		t.Fatalf("load threshold alarm: %v", err)
	}
	if thr.Status != alarm.StatusCleared {
		t.Fatalf("超限报警应被清成 cleared，实际 %s", thr.Status)
	}

	var qa po.Alarm
	if err := db.Where("id = ?", "al-qa").First(&qa).Error; err != nil {
		t.Fatalf("load quality alarm: %v", err)
	}
	if qa.Status != alarm.StatusActive {
		t.Fatalf("质量报警不得被顺手清掉，实际 %s", qa.Status)
	}

	// 阈值列本身也要落库为"未启用"
	var addr po.DeviceAddress
	if err := db.Where("id = ?", "pt-1").First(&addr).Error; err != nil {
		t.Fatalf("load address: %v", err)
	}
	if addr.ThresholdEnabled != 0 {
		t.Fatalf("开关应落库为 0，实际 %d", addr.ThresholdEnabled)
	}
}

// TestUpdateDeviceAddressKeepsThresholdWhenOmitted 请求里不带 threshold 时不得改动阈值配置 ——
// 这是嵌套对象存在的理由（平铺字段会让"未传"与"传 null"无法区分）。
func TestUpdateDeviceAddressKeepsThresholdWhenOmitted(t *testing.T) {
	db := newThresholdTestDB(t)
	svc := NewDeviceAddressService(db, NewAlarmService(db))
	ctx := context.Background()

	if err := db.Create(&po.Device{ID: "dev-1", Name: "1#炉", ProtocolID: "proto-1"}).Error; err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if err := db.Create(&po.DeviceAddress{
		ID: "pt-1", DeviceID: "dev-1", Name: "D0", Label: "温度",
		CommonDataType: "Float", DataType: "float", RwPermission: "R", Status: 1,
		ThresholdEnabled: 1, ThresholdOp: driver.OpRange, ThresholdValue: `["10","40"]`,
	}).Error; err != nil {
		t.Fatalf("seed address: %v", err)
	}

	// 只改描述，不带 threshold
	if err := svc.UpdateDeviceAddress(ctx, &dto.UpdateDeviceAddressDTO{
		ID: "pt-1", Label: "温度", Description: "改个描述",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	var addr po.DeviceAddress
	if err := db.Where("id = ?", "pt-1").First(&addr).Error; err != nil {
		t.Fatalf("load address: %v", err)
	}
	if addr.ThresholdEnabled != 1 || addr.ThresholdOp != driver.OpRange || addr.ThresholdValue != `["10","40"]` {
		t.Fatalf("不带 threshold 的部分更新不得动条件配置，实际 %+v", addr)
	}
}

// TestClearPointAlarmClearsBothPointAlarmTypes 点位被删除时，质量与超限两类活跃报警都要清 ——
// 点位没了，哪一类挂着都是永久"未恢复"。
func TestClearPointAlarmClearsBothPointAlarmTypes(t *testing.T) {
	db := newThresholdTestDB(t)
	svc := NewAlarmService(db)
	ctx := context.Background()

	for _, a := range []po.Alarm{
		{ID: "al-thr", TargetID: "pt-1", TargetName: "1#炉.温度", TargetType: alarm.TypePointThreshold,
			AlarmType: alarm.TypeThreshold, DeviceID: "dev-1", Status: alarm.StatusActive,
			FirstOccurTime: "2026-01-01 00:00:00", LastOccurTime: "2026-01-01 00:00:00"},
		{ID: "al-qa", TargetID: "pt-1", TargetName: "1#炉.温度", TargetType: alarm.TypePoint,
			AlarmType: alarm.TypeQuality, DeviceID: "dev-1", Status: alarm.StatusActive,
			FirstOccurTime: "2026-01-01 00:00:00", LastOccurTime: "2026-01-01 00:00:00"},
		// 另一台设备的报警，不得被连坐
		{ID: "al-other", TargetID: "pt-2", TargetName: "2#炉.压力", TargetType: alarm.TypePoint,
			AlarmType: alarm.TypeQuality, DeviceID: "dev-2", Status: alarm.StatusActive,
			FirstOccurTime: "2026-01-01 00:00:00", LastOccurTime: "2026-01-01 00:00:00"},
	} {
		if err := db.Create(&a).Error; err != nil {
			t.Fatalf("seed alarm: %v", err)
		}
	}

	if err := svc.ClearPointAlarm(ctx, "pt-1"); err != nil {
		t.Fatalf("clear: %v", err)
	}

	for _, id := range []string{"al-thr", "al-qa"} {
		var a po.Alarm
		if err := db.Where("id = ?", id).First(&a).Error; err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if a.Status != alarm.StatusCleared {
			t.Fatalf("%s 应被清成 cleared，实际 %s", id, a.Status)
		}
	}

	var other po.Alarm
	if err := db.Where("id = ?", "al-other").First(&other).Error; err != nil {
		t.Fatalf("load al-other: %v", err)
	}
	if other.Status != alarm.StatusActive {
		t.Fatalf("其它设备的报警不得被连坐，实际 %s", other.Status)
	}
}

// TestPageThresholdRecords 越限记录分页：过滤、排序、字段映射（含边界中文名）。
func TestPageThresholdRecords(t *testing.T) {
	db := newThresholdTestDB(t)
	svc := NewAlarmService(db)
	ctx := context.Background()

	rows := []po.PointThresholdRecord{
		{ID: "r-1", DeviceID: "dev-1", DeviceName: "1#炉", AddressID: "pt-1", AddressName: "温度",
			Value: "45.2", Kind: "float", Bound: "high",
			ThresholdOp: driver.OpRange, ThresholdValue: `["10","40"]`, OccurredAt: "2026-01-01 10:00:00"},
		{ID: "r-2", DeviceID: "dev-1", DeviceName: "1#炉", AddressID: "pt-2", AddressName: "压力",
			Value: "3.5", Kind: "float", Bound: "low",
			ThresholdOp: driver.OpRange, ThresholdValue: `["10","40"]`, OccurredAt: "2026-01-01 11:00:00"},
		{ID: "r-3", DeviceID: "dev-2", DeviceName: "2#炉", AddressID: "pt-3", AddressName: "温度",
			Value: "88", Kind: "int", Bound: driver.OpGt,
			ThresholdOp: driver.OpGt, ThresholdValue: `["50"]`, OccurredAt: "2026-01-01 12:00:00"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed records: %v", err)
	}

	// 不带过滤：全部，且按发生时间倒序
	page, err := svc.PageThresholdRecords(ctx, &dto.PageThresholdRecordDTO{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if page.Total != 3 || len(page.Records) != 3 {
		t.Fatalf("期望 3 条，实际 total=%d len=%d", page.Total, len(page.Records))
	}
	if page.Records[0].ID != "r-3" || page.Records[2].ID != "r-1" {
		t.Fatalf("应按发生时间倒序，实际 %s ... %s", page.Records[0].ID, page.Records[2].ID)
	}
	// bound 的中文名：range 命中哪一侧是 high/low，其余算子就是算子名
	if page.Records[2].BoundName != "超上限" {
		t.Fatalf("bound=high 应映射为「超上限」，实际 %q", page.Records[2].BoundName)
	}
	if page.Records[1].BoundName != "低于下限" {
		t.Fatalf("bound=low 应映射为「低于下限」，实际 %q", page.Records[1].BoundName)
	}
	if page.Records[0].BoundName != "大于" {
		t.Fatalf("bound=gt 应映射为「大于」，实际 %q", page.Records[0].BoundName)
	}
	// 条件快照原样透出（前端据此展示"当时是按什么条件判的"）
	if page.Records[2].ThresholdOp != driver.OpRange || page.Records[2].ThresholdValue != `["10","40"]` {
		t.Fatalf("条件快照应原样透出，实际 %+v", page.Records[2])
	}

	// 按设备过滤
	page, err = svc.PageThresholdRecords(ctx, &dto.PageThresholdRecordDTO{Page: 1, Size: 10, DeviceID: "dev-2"})
	if err != nil {
		t.Fatalf("page by device: %v", err)
	}
	if page.Total != 1 || page.Records[0].ID != "r-3" {
		t.Fatalf("按设备过滤应只剩 1 条，实际 total=%d", page.Total)
	}

	// 按点位名模糊过滤
	page, err = svc.PageThresholdRecords(ctx, &dto.PageThresholdRecordDTO{Page: 1, Size: 10, Name: "温度"})
	if err != nil {
		t.Fatalf("page by name: %v", err)
	}
	if page.Total != 2 {
		t.Fatalf("按名称模糊过滤应剩 2 条，实际 %d", page.Total)
	}
}
