// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
)

func buildDevicePointXlsx(t *testing.T, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)
	for i, row := range rows {
		cell, err := excelize.CoordinatesToCellName(1, i+1)
		if err != nil {
			t.Fatalf("cell name: %v", err)
		}
		vals := make([]interface{}, len(row))
		for j, v := range row {
			vals[j] = v
		}
		if err := f.SetSheetRow(sheet, cell, &vals); err != nil {
			t.Fatalf("set sheet row %d: %v", i+1, err)
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	return buf.Bytes()
}

func TestMqttSubscriptionDevicePointCreateUpdate(t *testing.T) {
	svc := newTestSubscriptionService(t)
	ctx := context.Background()

	create := &dto.CreateMqttSubscriptionDevicePointDTO{
		DeviceID:           "d1",
		DeviceAddressID:    "a1",
		DeviceName:         "电表1",
		DeviceAddressLabel: "A相电流",
		Kind:               "Float", // 大小写不敏感
	}
	if err := svc.CreateMqttSubscriptionDevicePoint(ctx, create); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var row po.MqttSubscriptionDevicePoint
	if err := svc.sqliteDB.Where("device_id = ? AND device_address_id = ?", "d1", "a1").First(&row).Error; err != nil {
		t.Fatalf("load row: %v", err)
	}
	if row.Source != po.MqttSubscriptionDevicePointSourceManual {
		t.Errorf("source = %q, want manual", row.Source)
	}
	if row.Kind != "float" {
		t.Errorf("kind = %q, want float", row.Kind)
	}
	createdAt := row.CreatedAt
	if createdAt == "" || row.UpdatedAt == "" {
		t.Error("created_at / updated_at should be set")
	}

	if err := svc.CreateMqttSubscriptionDevicePoint(ctx, create); appErrorCode(t, err) != enums.MqttSubscriptionDevicePointExistsEnum.GetCode() {
		t.Fatalf("duplicate create code = %s", appErrorCode(t, err))
	}

	update := &dto.UpdateMqttSubscriptionDevicePointDTO{
		DeviceID:           "d1",
		DeviceAddressID:    "a1",
		DeviceName:         "电表一号",
		DeviceAddressLabel: "", // 明确清空
		Kind:               "int",
	}
	if err := svc.UpdateMqttSubscriptionDevicePoint(ctx, update); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := svc.sqliteDB.Where("device_id = ? AND device_address_id = ?", "d1", "a1").First(&row).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if row.DeviceName != "电表一号" || row.DeviceAddressLabel != "" || row.Kind != "int" {
		t.Errorf("metadata not updated: %+v", row)
	}
	if row.CreatedAt != createdAt {
		t.Errorf("created_at changed: %q -> %q", createdAt, row.CreatedAt)
	}
	if row.Source != po.MqttSubscriptionDevicePointSourceManual {
		t.Errorf("source = %q, want manual", row.Source)
	}

	missing := *update
	missing.DeviceAddressID = "nope"
	if err := svc.UpdateMqttSubscriptionDevicePoint(ctx, &missing); appErrorCode(t, err) != enums.MqttSubscriptionDevicePointNotExistsEnum.GetCode() {
		t.Fatalf("missing update code = %s", appErrorCode(t, err))
	}

	badKind := &dto.CreateMqttSubscriptionDevicePointDTO{
		DeviceID: "d2", DeviceAddressID: "a2", Kind: "double",
	}
	if err := svc.CreateMqttSubscriptionDevicePoint(ctx, badKind); appErrorCode(t, err) != enums.ParamValidEnum.GetCode() {
		t.Fatalf("invalid kind code = %s", appErrorCode(t, err))
	}

	longName := &dto.CreateMqttSubscriptionDevicePointDTO{
		DeviceID: "d3", DeviceAddressID: "a3", DeviceName: strings.Repeat("名", 101),
	}
	if err := svc.CreateMqttSubscriptionDevicePoint(ctx, longName); appErrorCode(t, err) != enums.ParamValidEnum.GetCode() {
		t.Fatalf("long name code = %s", appErrorCode(t, err))
	}
}

// TestMqttSubscriptionDevicePointDelete 只允许删手工行：自动行删了会被下一条报文
// 重新收录，因此拒绝并给出可据以行动的错误码，而不是假装删除成功。
func TestMqttSubscriptionDevicePointDelete(t *testing.T) {
	svc := newTestSubscriptionService(t)
	ctx := context.Background()

	seed := []po.MqttSubscriptionDevicePoint{
		{DeviceID: "d1", DeviceAddressID: "a1", DeviceName: "手工行",
			Source: po.MqttSubscriptionDevicePointSourceManual},
		{DeviceID: "d2", DeviceAddressID: "a2", DeviceName: "自动行",
			Source: po.MqttSubscriptionDevicePointSourceAuto},
	}
	if err := svc.sqliteDB.Create(&seed).Error; err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	// 自然键两侧空白按输入归一处理。
	if err := svc.DeleteMqttSubscriptionDevicePoint(ctx, &dto.DeleteMqttSubscriptionDevicePointDTO{
		DeviceID: " d1 ", DeviceAddressID: "a1 ",
	}); err != nil {
		t.Fatalf("Delete manual: %v", err)
	}
	var count int64
	if err := svc.sqliteDB.Model(&po.MqttSubscriptionDevicePoint{}).
		Where("device_id = ? AND device_address_id = ?", "d1", "a1").Count(&count).Error; err != nil {
		t.Fatalf("count manual: %v", err)
	}
	if count != 0 {
		t.Errorf("manual row still present after delete")
	}

	if err := svc.DeleteMqttSubscriptionDevicePoint(ctx, &dto.DeleteMqttSubscriptionDevicePointDTO{
		DeviceID: "d2", DeviceAddressID: "a2",
	}); appErrorCode(t, err) != enums.MqttSubscriptionDevicePointAutoNotDeletableEnum.GetCode() {
		t.Fatalf("auto delete code = %s", appErrorCode(t, err))
	}
	if err := svc.sqliteDB.Model(&po.MqttSubscriptionDevicePoint{}).
		Where("device_id = ? AND device_address_id = ?", "d2", "a2").Count(&count).Error; err != nil {
		t.Fatalf("count auto: %v", err)
	}
	if count != 1 {
		t.Errorf("auto row should be kept, count = %d", count)
	}

	if err := svc.DeleteMqttSubscriptionDevicePoint(ctx, &dto.DeleteMqttSubscriptionDevicePointDTO{
		DeviceID: "d1", DeviceAddressID: "a1",
	}); appErrorCode(t, err) != enums.MqttSubscriptionDevicePointNotExistsEnum.GetCode() {
		t.Fatalf("missing delete code = %s", appErrorCode(t, err))
	}
}

// TestMqttSubscriptionDevicePointBatchDelete 批量删除只跳过删不掉的行：
// 自动行与不存在的行不计入删除数，但也不让整批失败。
func TestMqttSubscriptionDevicePointBatchDelete(t *testing.T) {
	svc := newTestSubscriptionService(t)
	ctx := context.Background()

	seed := []po.MqttSubscriptionDevicePoint{
		{DeviceID: "d1", DeviceAddressID: "a1", Source: po.MqttSubscriptionDevicePointSourceManual},
		{DeviceID: "d2", DeviceAddressID: "a2", Source: po.MqttSubscriptionDevicePointSourceManual},
		{DeviceID: "d3", DeviceAddressID: "a3", Source: po.MqttSubscriptionDevicePointSourceAuto},
	}
	if err := svc.sqliteDB.Create(&seed).Error; err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	items := []dto.DeleteMqttSubscriptionDevicePointDTO{
		{DeviceID: "d1", DeviceAddressID: "a1"},
		{DeviceID: " d2 ", DeviceAddressID: "a2 "}, // 空白按输入归一
		{DeviceID: "d3", DeviceAddressID: "a3"},    // 自动行：拒删
		{DeviceID: "d9", DeviceAddressID: "a9"},    // 不存在：忽略
	}
	deleted, err := svc.BatchDeleteMqttSubscriptionDevicePoint(ctx, items)
	if err != nil {
		t.Fatalf("BatchDelete: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	var rows []po.MqttSubscriptionDevicePoint
	if err := svc.sqliteDB.Order("device_id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("find rows: %v", err)
	}
	if len(rows) != 1 || rows[0].DeviceID != "d3" || rows[0].Source != po.MqttSubscriptionDevicePointSourceAuto {
		t.Errorf("remaining rows = %+v, want only d3/auto", rows)
	}

	if deleted, err := svc.BatchDeleteMqttSubscriptionDevicePoint(ctx, nil); err != nil || deleted != 0 {
		t.Errorf("empty batch = (%d, %v), want (0, nil)", deleted, err)
	}
}

// TestMqttSubscriptionDevicePointRestoreAuto 恢复自动收录只改维护方式：
// 元信息保持当前值，已恢复的行幂等成功，不存在的行返回 60025。
func TestMqttSubscriptionDevicePointRestoreAuto(t *testing.T) {
	svc := newTestSubscriptionService(t)
	ctx := context.Background()

	seed := []po.MqttSubscriptionDevicePoint{
		{DeviceID: "d1", DeviceAddressID: "a1", DeviceName: "手工名", Kind: "int",
			Source: po.MqttSubscriptionDevicePointSourceManual},
		{DeviceID: "d2", DeviceAddressID: "a2", DeviceName: "自动名",
			Source: po.MqttSubscriptionDevicePointSourceAuto},
	}
	if err := svc.sqliteDB.Create(&seed).Error; err != nil {
		t.Fatalf("seed rows: %v", err)
	}

	if err := svc.RestoreMqttSubscriptionDevicePointAuto(ctx, &dto.RestoreMqttSubscriptionDevicePointAutoDTO{
		DeviceID: " d1 ", DeviceAddressID: "a1",
	}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	var row po.MqttSubscriptionDevicePoint
	if err := svc.sqliteDB.Where("device_id = ? AND device_address_id = ?", "d1", "a1").First(&row).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if row.Source != po.MqttSubscriptionDevicePointSourceAuto {
		t.Errorf("source = %q, want auto", row.Source)
	}
	// 元信息不受恢复操作影响，等报文元信息变化后才由自动 upsert 覆盖。
	if row.DeviceName != "手工名" || row.Kind != "int" {
		t.Errorf("metadata changed: %+v", row)
	}

	// 已是 auto 的行幂等成功。
	if err := svc.RestoreMqttSubscriptionDevicePointAuto(ctx, &dto.RestoreMqttSubscriptionDevicePointAutoDTO{
		DeviceID: "d2", DeviceAddressID: "a2",
	}); err != nil {
		t.Fatalf("Restore auto row: %v", err)
	}

	if err := svc.RestoreMqttSubscriptionDevicePointAuto(ctx, &dto.RestoreMqttSubscriptionDevicePointAutoDTO{
		DeviceID: "d9", DeviceAddressID: "a9",
	}); appErrorCode(t, err) != enums.MqttSubscriptionDevicePointNotExistsEnum.GetCode() {
		t.Fatalf("missing restore code = %s", appErrorCode(t, err))
	}
}

// TestMqttSubscriptionDevicePointUpdateKeepsUnknownKind 自动上报可能留下非标准的
// 类型字符串；编辑其他字段时不应对它报错，也不应静默改值。
func TestMqttSubscriptionDevicePointUpdateKeepsUnknownKind(t *testing.T) {
	svc := newTestSubscriptionService(t)
	ctx := context.Background()

	seed := po.MqttSubscriptionDevicePoint{
		DeviceID: "d1", DeviceAddressID: "a1", DeviceName: "电表1", Kind: "Double",
		Source: po.MqttSubscriptionDevicePointSourceAuto,
	}
	if err := svc.sqliteDB.Create(&seed).Error; err != nil {
		t.Fatalf("seed row: %v", err)
	}

	err := svc.UpdateMqttSubscriptionDevicePoint(ctx, &dto.UpdateMqttSubscriptionDevicePointDTO{
		DeviceID: "d1", DeviceAddressID: "a1", DeviceName: "改过的名字", Kind: "Double",
	})
	if err != nil {
		t.Fatalf("Update with existing kind: %v", err)
	}

	var row po.MqttSubscriptionDevicePoint
	if err := svc.sqliteDB.Where("device_id = ? AND device_address_id = ?", "d1", "a1").First(&row).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if row.DeviceName != "改过的名字" || row.Kind != "double" {
		t.Errorf("unexpected row: %+v", row)
	}
	if row.Source != po.MqttSubscriptionDevicePointSourceManual {
		t.Errorf("source = %q, want manual", row.Source)
	}
}

func TestMqttSubscriptionDevicePointImportUpsert(t *testing.T) {
	svc := newTestSubscriptionService(t)
	ctx := context.Background()

	existing := po.MqttSubscriptionDevicePoint{
		DeviceID: "d1", DeviceAddressID: "a1", DeviceName: "旧名称", DeviceAddressLabel: "旧标签",
		Kind: "int", Source: po.MqttSubscriptionDevicePointSourceAuto,
		CreatedAt: "2020-01-01 00:00:00", UpdatedAt: "2020-01-01 00:00:00",
	}
	if err := svc.sqliteDB.Create(&existing).Error; err != nil {
		t.Fatalf("seed existing row: %v", err)
	}

	data := buildDevicePointXlsx(t, [][]string{
		{"设备ID", "点位ID", "设备名称", "点位标签", "类型"},
		{"d1", "a1", "新名称", "新标签", "float"},
		{"d2", "a2", "电表2", "功率", "int"},
		{"d1", "a1", "重复行", "", ""},
		{"", "a3", "缺设备ID", "", ""},
	})
	result, err := svc.ImportMqttSubscriptionDevicePoints(ctx, data)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.Total != 4 || result.Success != 2 || result.Failed != 2 {
		t.Fatalf("result = %+v, want total=4 success=2 failed=2", result)
	}
	if result.Errors[0].Row != 4 || result.Errors[1].Row != 5 {
		t.Errorf("error rows = %+v, want 4/5", result.Errors)
	}

	var rows []po.MqttSubscriptionDevicePoint
	if err := svc.sqliteDB.Order("device_id ASC, device_address_id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("find rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	got := rows[0]
	if got.DeviceID != "d1" || got.DeviceName != "新名称" || got.DeviceAddressLabel != "新标签" || got.Kind != "float" {
		t.Errorf("existing row not upserted: %+v", got)
	}
	if got.Source != po.MqttSubscriptionDevicePointSourceManual {
		t.Errorf("existing row source = %q, want manual", got.Source)
	}
	if got.CreatedAt != "2020-01-01 00:00:00" {
		t.Errorf("created_at = %q, want preserved", got.CreatedAt)
	}
	if rows[1].DeviceID != "d2" || rows[1].Source != po.MqttSubscriptionDevicePointSourceManual {
		t.Errorf("new row = %+v, want d2/manual", rows[1])
	}

	// 表头不对时整体报错，而不是逐行失败。
	badHeader := buildDevicePointXlsx(t, [][]string{
		{"设备", "点位", "名称", "标签", "类型"},
		{"d3", "a3", "设备3", "标签", "float"},
	})
	if _, err := svc.ImportMqttSubscriptionDevicePoints(ctx, badHeader); appErrorCode(t, err) != enums.ParamValidEnum.GetCode() {
		t.Fatalf("bad header code = %s", appErrorCode(t, err))
	}
}

func TestMqttSubscriptionDevicePointImportTemplate(t *testing.T) {
	svc := newTestSubscriptionService(t)

	buf, err := svc.GenerateMqttSubscriptionDevicePointImportTemplate()
	if err != nil {
		t.Fatalf("Generate template: %v", err)
	}
	f, err := excelize.OpenReader(buf)
	if err != nil {
		t.Fatalf("open template: %v", err)
	}
	defer f.Close()

	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil {
		t.Fatalf("read template rows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("template is empty")
	}
	want := []string{"设备ID", "点位ID", "设备名称", "点位标签", "类型"}
	for i, h := range want {
		if i >= len(rows[0]) || rows[0][i] != h {
			t.Fatalf("header[%d] = %q, want %q (row=%v)", i, cellAt(rows[0], i), h, rows[0])
		}
	}
}

func cellAt(row []string, i int) string {
	if i < len(row) {
		return row[i]
	}
	return ""
}
