// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
)

func newThingModelTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "thing_model_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(
		&po.ThingModel{}, &po.ThingModelProperty{}, &po.Device{}, &po.DeviceAddress{},
		&po.IotProtocol{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func wantThingModelErrCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误码 %s，实际没有错误", code)
	}
	var appErr *appError.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("期望 *appError.AppError，实际 %T: %v", err, err)
	}
	if appErr.Code != code {
		t.Fatalf("错误码 = %s，期望 %s（%v）", appErr.Code, code, err)
	}
}

func seedThingModel(t *testing.T, svc *ThingModelService, code, name, category string) po.ThingModel {
	t.Helper()
	if err := svc.CreateThingModel(context.Background(), &dto.CreateThingModelDTO{
		Code: code, Name: name, Category: category,
	}); err != nil {
		t.Fatalf("create thing model %q: %v", code, err)
	}
	var row po.ThingModel
	if err := svc.sqliteDB.Where("code = ?", code).First(&row).Error; err != nil {
		t.Fatalf("load thing model %q: %v", code, err)
	}
	return row
}

func seedThingModelProperty(t *testing.T, svc *ThingModelService, modelID, identifier string) po.ThingModelProperty {
	t.Helper()
	if err := svc.CreateThingModelProperty(context.Background(), &dto.CreateThingModelPropertyDTO{
		ModelID:    modelID,
		Identifier: identifier,
		Name:       identifier,
		Kind:       "float",
		AccessMode: "R",
	}); err != nil {
		t.Fatalf("create property %q: %v", identifier, err)
	}
	var row po.ThingModelProperty
	if err := svc.sqliteDB.Where("model_id = ? AND identifier = ?", modelID, identifier).First(&row).Error; err != nil {
		t.Fatalf("load property %q: %v", identifier, err)
	}
	return row
}

func TestThingModelCRUDValidation(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "th_sensor", "温湿度传感器", "sensor")
	if model.Version != "1.0.0" || model.Status != 1 {
		t.Fatalf("默认值不符: version=%q status=%d", model.Version, model.Status)
	}

	// 唯一性与格式
	wantThingModelErrCode(t, svc.CreateThingModel(ctx, &dto.CreateThingModelDTO{
		Code: "th_sensor", Name: "另一个名字",
	}), enums.ThingModelExistsEnum.GetCode())
	wantThingModelErrCode(t, svc.CreateThingModel(ctx, &dto.CreateThingModelDTO{
		Code: "1bad", Name: "非法标识",
	}), enums.ThingModelCodeInvalidEnum.GetCode())
	wantThingModelErrCode(t, svc.CreateThingModel(ctx, &dto.CreateThingModelDTO{
		Code: "th_ok", Name: "非法分类", Category: "unknown",
	}), enums.ThingModelCategoryInvalidEnum.GetCode())

	// code 不可改，name 可改
	wantThingModelErrCode(t, svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: model.ID, Code: "th_renamed", Name: "温湿度传感器", Category: "sensor",
	}), enums.ThingModelCodeImmutableEnum.GetCode())
	if err := svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: model.ID, Name: "温湿度传感器-新", Category: "meter", Version: "2.0.0",
	}); err != nil {
		t.Fatalf("update thing model: %v", err)
	}

	// 属性：格式、类型、唯一性
	wantThingModelErrCode(t, svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "-bad", Name: "坏标识", Kind: "float", AccessMode: "R",
	}), enums.ThingModelIdentifierInvalidEnum.GetCode())
	wantThingModelErrCode(t, svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "temp", Name: "温度", Kind: "number", AccessMode: "R",
	}), enums.ParamValidEnum.GetCode())

	prop := seedThingModelProperty(t, svc, model.ID, "temperature")
	wantThingModelErrCode(t, svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "temperature", Name: "温度2", Kind: "float", AccessMode: "R",
	}), enums.ThingModelPropertyExistsEnum.GetCode())

	// 属性标识符不可改，其余字段可改
	wantThingModelErrCode(t, svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: "temp2", Name: "温度", Kind: "float", AccessMode: "R",
	}), enums.ThingModelIdentifierImmutableEnum.GetCode())
	if err := svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Name: "进风温度", Kind: "float", AccessMode: "RW", Unit: "℃", SortOrder: 20,
	}); err != nil {
		t.Fatalf("update property: %v", err)
	}

	// 列表与分页计数
	list, err := svc.ListThingModelProperties(ctx, model.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list properties = %v, err = %v", list, err)
	}
	if list[0].Name != "进风温度" || list[0].AccessMode != "RW" {
		t.Fatalf("property 未更新: %+v", list[0])
	}
	page, err := svc.PageThingModel(ctx, &dto.PageThingModelDTO{
		Page: 1, Size: 10, Keyword: "温湿度", Category: "meter",
	})
	if err != nil {
		t.Fatalf("page thing model: %v", err)
	}
	if page.Total != 1 || len(page.Records) != 1 || page.Records[0].PropertyCount != 1 {
		t.Fatalf("page result = %+v", page)
	}

	// 删除：属性 → 模型
	if err := svc.DeleteThingModelProperty(ctx, prop.ID); err != nil {
		t.Fatalf("delete property: %v", err)
	}
	if err := svc.DeleteThingModel(ctx, model.ID); err != nil {
		t.Fatalf("delete thing model: %v", err)
	}
}

// TestPageThingModelProperties 钉住属性分页：翻页切片、排序与 maxSortOrder
// （前端新增属性靠它接着末尾排，取不到就会插到中间）。
func TestPageThingModelProperties(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "th_meter", "电表", "meter")
	for i, identifier := range []string{"voltage", "current", "power"} {
		if err := svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
			ModelID: model.ID, Identifier: identifier, Name: identifier,
			Kind: "float", AccessMode: "R", SortOrder: (i + 1) * 10,
		}); err != nil {
			t.Fatalf("create property %q: %v", identifier, err)
		}
	}

	first, err := svc.PageThingModelProperties(ctx, &dto.PageThingModelPropertyDTO{
		ModelID: model.ID, Page: 1, Size: 2,
	})
	if err != nil {
		t.Fatalf("page properties: %v", err)
	}
	if first.Total != 3 || len(first.Records) != 2 {
		t.Fatalf("第一页 = total %d / %d 条，期望 3 / 2", first.Total, len(first.Records))
	}
	if first.Records[0].Identifier != "voltage" || first.Records[1].Identifier != "current" {
		t.Fatalf("第一页顺序不对（应按 sort_order 升序）: %+v", first.Records)
	}
	// 最大值来自全量而不是当前页：第二页只有一条，仍应是 30
	second, err := svc.PageThingModelProperties(ctx, &dto.PageThingModelPropertyDTO{
		ModelID: model.ID, Page: 2, Size: 2,
	})
	if err != nil {
		t.Fatalf("page properties (2): %v", err)
	}
	if len(second.Records) != 1 || second.Records[0].Identifier != "power" {
		t.Fatalf("第二页 = %+v，期望只剩 power", second.Records)
	}
	if first.MaxSortOrder != 30 || second.MaxSortOrder != 30 {
		t.Fatalf("maxSortOrder = %d / %d，期望都是 30", first.MaxSortOrder, second.MaxSortOrder)
	}

	// 没有属性的模型：空页 + maxSortOrder 0
	empty := seedThingModel(t, svc, "th_empty", "空模型", "")
	emptyPage, err := svc.PageThingModelProperties(ctx, &dto.PageThingModelPropertyDTO{
		ModelID: empty.ID, Page: 1, Size: 10,
	})
	if err != nil {
		t.Fatalf("page empty model: %v", err)
	}
	if emptyPage.Total != 0 || len(emptyPage.Records) != 0 || emptyPage.MaxSortOrder != 0 {
		t.Fatalf("空模型分页 = %+v", emptyPage)
	}

	wantThingModelErrCode(t, func() error {
		_, err := svc.PageThingModelProperties(ctx, &dto.PageThingModelPropertyDTO{Page: 1, Size: 10})
		return err
	}(), enums.ParamValidEnum.GetCode())
	wantThingModelErrCode(t, func() error {
		_, err := svc.PageThingModelProperties(ctx, &dto.PageThingModelPropertyDTO{
			ModelID: "not-exists", Page: 1, Size: 10,
		})
		return err
	}(), enums.ThingModelNotExistsEnum.GetCode())
}

func TestThingModelDeleteGuards(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "th_meter", "电表", "meter")
	prop := seedThingModelProperty(t, svc, model.ID, "voltage")

	device := po.Device{
		Name: "电表1", ProtocolID: "ModBus.TCP", ProtocolJSON: "{}", ModelID: model.ID,
		Status: 1, CreatedAt: "2026-01-01 00:00:00", UpdatedAt: "2026-01-01 00:00:00",
	}
	if err := db.Create(&device).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}

	// 设备绑定模型 → 模型不可删
	wantThingModelErrCode(t, svc.DeleteThingModel(ctx, model.ID), enums.ThingModelInUseEnum.GetCode())

	// 点位绑定属性 → 属性不可删
	addr := po.DeviceAddress{
		DeviceID: device.ID, Name: "A相电压", Label: "A相电压",
		CommonDataType: "Float", DataType: "float32", RwPermission: "R", PropertyID: prop.ID,
		Status: 1, CreatedAt: "2026-01-01 00:00:00", UpdatedAt: "2026-01-01 00:00:00",
	}
	if err := db.Create(&addr).Error; err != nil {
		t.Fatalf("create address: %v", err)
	}
	wantThingModelErrCode(t, svc.DeleteThingModelProperty(ctx, prop.ID), enums.ThingModelPropertyInUseEnum.GetCode())

	// 即使设备解绑模型，只要点位仍指向该属性，删模型仍要拒绝（防止留下悬空绑定）
	if err := db.Model(&po.Device{}).Where("id = ?", device.ID).Update("model_id", "").Error; err != nil {
		t.Fatalf("unbind device: %v", err)
	}
	wantThingModelErrCode(t, svc.DeleteThingModel(ctx, model.ID), enums.ThingModelInUseEnum.GetCode())
}
