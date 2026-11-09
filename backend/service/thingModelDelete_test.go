// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"testing"

	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
)

func wantDeleteAction(t *testing.T, action, errorCode, wantAction, wantCode string) {
	t.Helper()
	if action != wantAction {
		t.Fatalf("action = %q，期望 %q", action, wantAction)
	}
	if wantCode != "" && errorCode != wantCode {
		t.Fatalf("errorCode = %q，期望 %q", errorCode, wantCode)
	}
}

// TestBatchDeleteOpenApiThingModels 覆盖按 code 批量删除、部分成功、
// 以及「删模型连带删属性」。
func TestBatchDeleteOpenApiThingModels(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	m1 := seedThingModel(t, svc, "th_a", "模型A", "sensor")
	seedThingModel(t, svc, "th_b", "模型B", "meter")
	seedThingModelProperty(t, svc, m1.ID, "temperature")

	res, err := svc.BatchDeleteOpenApiThingModels(ctx, &dto.OpenApiThingModelBatchDeleteDTO{
		Codes: []string{"th_a", "th_b", " th_missing "},
	})
	if err != nil {
		t.Fatalf("batch delete: %v", err)
	}
	if res.Requested != 3 || res.Deleted != 2 || res.Failed != 1 {
		t.Fatalf("统计不符: %+v", res)
	}
	wantDeleteAction(t, res.Results[0].Action, res.Results[0].ErrorCode, vo.ThingModelDeleteActionDeleted, "")
	wantDeleteAction(t, res.Results[1].Action, res.Results[1].ErrorCode, vo.ThingModelDeleteActionDeleted, "")
	wantDeleteAction(t, res.Results[2].Action, res.Results[2].ErrorCode, vo.ThingModelDeleteActionFailed,
		enums.ThingModelNotExistsEnum.GetCode())
	if res.Results[2].Code != "th_missing" {
		t.Fatalf("失败条目的 code 未 trim: %q", res.Results[2].Code)
	}

	var modelCount, propCount int64
	if err := db.Model(&po.ThingModel{}).Count(&modelCount).Error; err != nil {
		t.Fatalf("count models: %v", err)
	}
	if err := db.Model(&po.ThingModelProperty{}).Count(&propCount).Error; err != nil {
		t.Fatalf("count properties: %v", err)
	}
	if modelCount != 0 || propCount != 0 {
		t.Fatalf("删除后剩 model=%d prop=%d，期望 0/0（属性应随模型删除）", modelCount, propCount)
	}
}

// TestBatchDeleteOpenApiThingModelsBound 设备绑定 / 属性被点位绑定都拒绝删除。
func TestBatchDeleteOpenApiThingModelsBound(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	// 模型被设备绑定
	devModel := seedThingModel(t, svc, "th_dev", "设备模型", "plc")
	if err := db.Create(&po.Device{ID: "dev-1", Name: "1号PLC", ModelID: devModel.ID}).Error; err != nil {
		t.Fatalf("seed device: %v", err)
	}

	// 模型的属性被点位绑定
	addrModel := seedThingModel(t, svc, "th_addr", "点位模型", "sensor")
	prop := seedThingModelProperty(t, svc, addrModel.ID, "temperature")
	if err := db.Create(&po.DeviceAddress{
		ID: "addr-1", DeviceID: "dev-2", Name: "addr-1", RwPermission: "R",
		CommonDataType: "float", DataType: "float32", PropertyID: prop.ID,
	}).Error; err != nil {
		t.Fatalf("bind address: %v", err)
	}

	res, err := svc.BatchDeleteOpenApiThingModels(ctx, &dto.OpenApiThingModelBatchDeleteDTO{
		Codes: []string{"th_dev", "th_addr"},
	})
	if err != nil {
		t.Fatalf("batch delete: %v", err)
	}
	if res.Deleted != 0 || res.Failed != 2 {
		t.Fatalf("统计不符: %+v", res)
	}
	for i := range res.Results {
		wantDeleteAction(t, res.Results[i].Action, res.Results[i].ErrorCode,
			vo.ThingModelDeleteActionFailed, enums.ThingModelInUseEnum.GetCode())
	}

	var n int64
	if err := db.Model(&po.ThingModel{}).Count(&n).Error; err != nil {
		t.Fatalf("count models: %v", err)
	}
	if n != 2 {
		t.Fatalf("被拒绝的模型不应被删除，实际剩 %d 个", n)
	}
}

// TestBatchDeleteOpenApiThingModelProperties 覆盖属性批量删除与逐条错误码。
func TestBatchDeleteOpenApiThingModelProperties(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "th_prop", "属性模型", "sensor")
	seedThingModelProperty(t, svc, model.ID, "temperature")
	bound := seedThingModelProperty(t, svc, model.ID, "humidity")
	if err := db.Create(&po.DeviceAddress{
		ID: "addr-1", DeviceID: "dev-1", Name: "addr-1", RwPermission: "R",
		CommonDataType: "float", DataType: "float32", PropertyID: bound.ID,
	}).Error; err != nil {
		t.Fatalf("bind address: %v", err)
	}

	res, err := svc.BatchDeleteOpenApiThingModelProperties(ctx, &dto.OpenApiThingModelPropertyBatchDeleteDTO{
		Items: []dto.OpenApiThingModelPropertyDeleteItemDTO{
			{ModelCode: "th_prop", Identifier: "temperature"},
			{ModelCode: "th_prop", Identifier: "humidity"},
			{ModelCode: "th_missing", Identifier: "temperature"},
			{ModelCode: "th_prop", Identifier: "missing"},
			{ModelCode: "th_prop", Identifier: "  "},
		},
	})
	if err != nil {
		t.Fatalf("batch delete properties: %v", err)
	}
	if res.Requested != 5 || res.Deleted != 1 || res.Failed != 4 {
		t.Fatalf("统计不符: %+v", res)
	}
	want := []string{"", enums.ThingModelPropertyInUseEnum.GetCode(),
		enums.ThingModelNotExistsEnum.GetCode(), enums.ThingModelPropertyNotExistsEnum.GetCode(),
		enums.ParamValidEnum.GetCode()}
	for i, code := range want {
		wantAction := vo.ThingModelDeleteActionFailed
		if i == 0 {
			wantAction = vo.ThingModelDeleteActionDeleted
		}
		wantDeleteAction(t, res.Results[i].Action, res.Results[i].ErrorCode, wantAction, code)
	}

	var remaining []po.ThingModelProperty
	if err := db.Where("model_id = ?", model.ID).Find(&remaining).Error; err != nil {
		t.Fatalf("load properties: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Identifier != "humidity" {
		t.Fatalf("剩余属性不符: %+v", remaining)
	}
}

// TestBatchDeleteOpenApiThingModelsEmpty 空请求走服务层时也拒绝。
func TestBatchDeleteOpenApiThingModelsEmpty(t *testing.T) {
	svc := NewThingModelService(newThingModelTestDB(t))
	if _, err := svc.BatchDeleteOpenApiThingModels(context.Background(),
		&dto.OpenApiThingModelBatchDeleteDTO{}); err == nil {
		t.Fatal("空 codes 应返回参数错误")
	}
	if _, err := svc.BatchDeleteOpenApiThingModelProperties(context.Background(),
		&dto.OpenApiThingModelPropertyBatchDeleteDTO{}); err == nil {
		t.Fatal("空 items 应返回参数错误")
	}
}
