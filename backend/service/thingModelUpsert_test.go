// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"fmt"
	"testing"

	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
)

func intPtr(v int) *int { return &v }

func thingModelUpsertReq(models ...dto.OpenApiThingModelUpsertDTO) *dto.OpenApiThingModelBatchUpsertDTO {
	return &dto.OpenApiThingModelBatchUpsertDTO{Models: models}
}

func thingModelUpsertProp(identifier, name, kind, accessMode string) dto.OpenApiThingModelPropertyUpsertDTO {
	p := dto.OpenApiThingModelPropertyUpsertDTO{Identifier: identifier, Name: name}
	if kind != "" {
		p.Kind = strPtr(kind)
	}
	if accessMode != "" {
		p.AccessMode = strPtr(accessMode)
	}
	return p
}

func wantModelItemAction(t *testing.T, res *vo.OpenApiThingModelBatchUpsertVO, index int, action string) vo.OpenApiThingModelUpsertItemVO {
	t.Helper()
	if res == nil || index >= len(res.Models) {
		t.Fatalf("响应缺少第 %d 个模型结果", index)
	}
	item := res.Models[index]
	if item.Action != action {
		t.Fatalf("模型 %q action = %q，期望 %q（code=%s msg=%s）",
			item.Code, item.Action, action, item.ErrorCode, item.ErrorMessage)
	}
	return item
}

func loadThingModelByCode(t *testing.T, svc *ThingModelService, code string) po.ThingModel {
	t.Helper()
	var row po.ThingModel
	if err := svc.sqliteDB.Where("code = ?", code).First(&row).Error; err != nil {
		t.Fatalf("load model %q: %v", code, err)
	}
	return row
}

func loadThingModelProperties(t *testing.T, svc *ThingModelService, modelID string) []po.ThingModelProperty {
	t.Helper()
	var rows []po.ThingModelProperty
	if err := svc.sqliteDB.Where("model_id = ?", modelID).Order("sort_order ASC, id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load properties: %v", err)
	}
	return rows
}

// TestBatchUpsertOpenApiThingModelsCreateAndUpdate 覆盖核心语义：
// 首次按 code / identifier 创建，再次提交原地更新；省略的可选字段保持原值。
func TestBatchUpsertOpenApiThingModelsCreateAndUpdate(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	req := thingModelUpsertReq(dto.OpenApiThingModelUpsertDTO{
		Code:     "th_sensor",
		Name:     "温湿度传感器",
		Category: strPtr("sensor"),
		Properties: []dto.OpenApiThingModelPropertyUpsertDTO{
			thingModelUpsertProp("temperature", "温度", "float", "R"),
			thingModelUpsertProp("humidity", "湿度", "float", "R"),
		},
	})

	res, err := svc.BatchUpsertOpenApiThingModels(ctx, req)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if res.Total != 1 || res.Success != 1 || res.Created != 1 || res.Updated != 0 || res.Failed != 0 {
		t.Fatalf("首次统计不符: %+v", res)
	}
	if res.PropertiesCreated != 2 || res.PropertiesUpdated != 0 {
		t.Fatalf("首次属性统计不符: created=%d updated=%d", res.PropertiesCreated, res.PropertiesUpdated)
	}
	item := wantModelItemAction(t, res, 0, vo.ThingModelUpsertActionCreated)
	if item.ID == "" || len(item.Properties) != 2 {
		t.Fatalf("首次结果缺少 ID/属性明细: %+v", item)
	}
	for _, p := range item.Properties {
		if p.Action != vo.ThingModelUpsertActionCreated || p.ID == "" {
			t.Fatalf("属性结果不符: %+v", p)
		}
	}

	model := loadThingModelByCode(t, svc, "th_sensor")
	if model.Version != "1.0.0" || model.Status != 1 || model.Category != "sensor" {
		t.Fatalf("创建默认值不符: %+v", model)
	}
	props := loadThingModelProperties(t, svc, model.ID)
	if len(props) != 2 {
		t.Fatalf("属性数 = %d，期望 2", len(props))
	}
	if props[0].SortOrder != 10 || props[1].SortOrder != 20 {
		t.Fatalf("自动排序不符: %d, %d", props[0].SortOrder, props[1].SortOrder)
	}

	// 第二次：同一 code，改模型名与属性名，省略 kind/accessMode/unit 等。
	req = thingModelUpsertReq(dto.OpenApiThingModelUpsertDTO{
		Code: "th_sensor",
		Name: "温湿度传感器V2",
		Properties: []dto.OpenApiThingModelPropertyUpsertDTO{
			{Identifier: "temperature", Name: "环境温度"},
		},
	})
	res, err = svc.BatchUpsertOpenApiThingModels(ctx, req)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if res.Updated != 1 || res.Created != 0 || res.Failed != 0 || res.PropertiesUpdated != 1 {
		t.Fatalf("二次统计不符: %+v", res)
	}
	item = wantModelItemAction(t, res, 0, vo.ThingModelUpsertActionUpdated)
	if item.ID != model.ID {
		t.Fatalf("更新后模型 ID 变了: %s -> %s", model.ID, item.ID)
	}
	if got := loadThingModelByCode(t, svc, "th_sensor"); got.Name != "温湿度传感器V2" || got.Category != "sensor" {
		t.Fatalf("更新未保留省略字段: %+v", got)
	}
	props = loadThingModelProperties(t, svc, model.ID)
	if len(props) != 2 {
		t.Fatalf("更新后属性数 = %d，期望 2（不得删除未提交属性）", len(props))
	}
	if props[0].Name != "环境温度" || props[0].Kind != "float" || props[0].AccessMode != "R" {
		t.Fatalf("省略 kind/accessMode 被清空: %+v", props[0])
	}

	// 幂等：同一请求再跑一次仍是 updated，行数不变。
	res, err = svc.BatchUpsertOpenApiThingModels(ctx, req)
	if err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	if res.Updated != 1 || loadCount(t, svc) != 1 {
		t.Fatalf("幂等失败: %+v", res)
	}
}

func loadCount(t *testing.T, svc *ThingModelService) int64 {
	t.Helper()
	var n int64
	if err := svc.sqliteDB.Model(&po.ThingModel{}).Count(&n).Error; err != nil {
		t.Fatalf("count models: %v", err)
	}
	return n
}

// TestBatchUpsertOpenApiThingModelsAutoSort 新增属性从模型内当前最大值 +10 顺延。
func TestBatchUpsertOpenApiThingModelsAutoSort(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	if _, err := svc.BatchUpsertOpenApiThingModels(ctx, thingModelUpsertReq(dto.OpenApiThingModelUpsertDTO{
		Code:       "th_sort",
		Name:       "排序测试",
		Properties: []dto.OpenApiThingModelPropertyUpsertDTO{thingModelUpsertProp("a", "A", "int", "R")},
	})); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := svc.BatchUpsertOpenApiThingModels(ctx, thingModelUpsertReq(dto.OpenApiThingModelUpsertDTO{
		Code: "th_sort",
		Name: "排序测试",
		Properties: []dto.OpenApiThingModelPropertyUpsertDTO{
			thingModelUpsertProp("b", "B", "int", "R"),
			{Identifier: "c", Name: "C", Kind: strPtr("int"), AccessMode: strPtr("R"), SortOrder: intPtr(100)},
			thingModelUpsertProp("d", "D", "int", "R"),
		},
	})); err != nil {
		t.Fatalf("append: %v", err)
	}

	model := loadThingModelByCode(t, svc, "th_sort")
	props := loadThingModelProperties(t, svc, model.ID)
	want := map[string]int{"a": 10, "b": 20, "c": 100, "d": 110}
	for _, p := range props {
		if p.SortOrder != want[p.Identifier] {
			t.Errorf("%s sortOrder = %d，期望 %d", p.Identifier, p.SortOrder, want[p.Identifier])
		}
	}
}

// TestBatchUpsertOpenApiThingModelsBoundPropertyGuard 已绑定点位的属性
// 改变 kind/accessMode 必须让整个模型回滚（模型元数据也不能半更新）。
func TestBatchUpsertOpenApiThingModelsBoundPropertyGuard(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	res, err := svc.BatchUpsertOpenApiThingModels(ctx, thingModelUpsertReq(dto.OpenApiThingModelUpsertDTO{
		Code:       "th_bound",
		Name:       "绑定测试",
		Properties: []dto.OpenApiThingModelPropertyUpsertDTO{thingModelUpsertProp("temperature", "温度", "float", "R")},
	}))
	if err != nil || res.Failed != 0 {
		t.Fatalf("seed: err=%v res=%+v", err, res)
	}
	model := loadThingModelByCode(t, svc, "th_bound")
	props := loadThingModelProperties(t, svc, model.ID)
	if err := db.Create(&po.DeviceAddress{
		ID: "addr-1", DeviceID: "dev-1", Name: "addr-1", RwPermission: "RW",
		CommonDataType: "float", DataType: "float32", PropertyID: props[0].ID,
	}).Error; err != nil {
		t.Fatalf("bind address: %v", err)
	}

	res, err = svc.BatchUpsertOpenApiThingModels(ctx, thingModelUpsertReq(dto.OpenApiThingModelUpsertDTO{
		Code: "th_bound",
		Name: "不该生效的新名字",
		Properties: []dto.OpenApiThingModelPropertyUpsertDTO{
			{Identifier: "temperature", Name: "温度", Kind: strPtr("int"), AccessMode: strPtr("R")},
		},
	}))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	item := wantModelItemAction(t, res, 0, vo.ThingModelUpsertActionFailed)
	if item.ErrorCode != enums.ThingModelPropertyBoundImmutableEnum.GetCode() {
		t.Fatalf("错误码 = %s，期望 %s", item.ErrorCode, enums.ThingModelPropertyBoundImmutableEnum.GetCode())
	}
	if len(item.Properties) != 1 || item.Properties[0].Identifier != "temperature" {
		t.Fatalf("失败明细未指向属性: %+v", item.Properties)
	}
	if got := loadThingModelByCode(t, svc, "th_bound"); got.Name != "绑定测试" {
		t.Fatalf("模型元数据被半更新: %q", got.Name)
	}
	if got := loadThingModelProperties(t, svc, model.ID); got[0].Kind != "float" {
		t.Fatalf("属性 kind 被改动: %q", got[0].Kind)
	}
}

// TestBatchUpsertOpenApiThingModelsDuplicates 批次内重复 code / identifier 的语义。
func TestBatchUpsertOpenApiThingModelsDuplicates(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	res, err := svc.BatchUpsertOpenApiThingModels(ctx, thingModelUpsertReq(
		dto.OpenApiThingModelUpsertDTO{Code: "th_dup", Name: "重复模型"},
		dto.OpenApiThingModelUpsertDTO{Code: "th_dup", Name: "重复模型2"},
	))
	if err != nil {
		t.Fatalf("dup code upsert: %v", err)
	}
	wantModelItemAction(t, res, 0, vo.ThingModelUpsertActionCreated)
	second := wantModelItemAction(t, res, 1, vo.ThingModelUpsertActionFailed)
	if second.ErrorCode != enums.ThingModelExistsEnum.GetCode() {
		t.Fatalf("批次内重复 code 错误码 = %s", second.ErrorCode)
	}
	if n := loadCount(t, svc); n != 1 {
		t.Fatalf("模型数 = %d，期望 1", n)
	}

	res, err = svc.BatchUpsertOpenApiThingModels(ctx, thingModelUpsertReq(dto.OpenApiThingModelUpsertDTO{
		Code: "th_dup_prop",
		Name: "重复属性",
		Properties: []dto.OpenApiThingModelPropertyUpsertDTO{
			thingModelUpsertProp("temperature", "温度", "float", "R"),
			thingModelUpsertProp("temperature", "温度2", "float", "R"),
		},
	}))
	if err != nil {
		t.Fatalf("dup identifier upsert: %v", err)
	}
	item := wantModelItemAction(t, res, 0, vo.ThingModelUpsertActionFailed)
	if item.ErrorCode != enums.ThingModelPropertyExistsEnum.GetCode() {
		t.Fatalf("批次内重复 identifier 错误码 = %s", item.ErrorCode)
	}
	var n int64
	if err := db.Model(&po.ThingModel{}).Where("code = ?", "th_dup_prop").Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("失败模型不应落库，实际 %d 行", n)
	}
}

// TestBatchUpsertOpenApiThingModelsNameCollision name 全局唯一仍然生效。
func TestBatchUpsertOpenApiThingModelsNameCollision(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	if _, err := svc.BatchUpsertOpenApiThingModels(ctx, thingModelUpsertReq(
		dto.OpenApiThingModelUpsertDTO{Code: "th_a", Name: "模型A"},
		dto.OpenApiThingModelUpsertDTO{Code: "th_b", Name: "模型B"},
	)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := svc.BatchUpsertOpenApiThingModels(ctx, thingModelUpsertReq(
		dto.OpenApiThingModelUpsertDTO{Code: "th_c", Name: "模型A"},
	))
	if err != nil {
		t.Fatalf("create collision: %v", err)
	}
	if got := wantModelItemAction(t, res, 0, vo.ThingModelUpsertActionFailed); got.ErrorCode != enums.ThingModelExistsEnum.GetCode() {
		t.Fatalf("新建撞名错误码 = %s", got.ErrorCode)
	}

	res, err = svc.BatchUpsertOpenApiThingModels(ctx, thingModelUpsertReq(
		dto.OpenApiThingModelUpsertDTO{Code: "th_b", Name: "模型A"},
	))
	if err != nil {
		t.Fatalf("update collision: %v", err)
	}
	if got := wantModelItemAction(t, res, 0, vo.ThingModelUpsertActionFailed); got.ErrorCode != enums.ThingModelExistsEnum.GetCode() {
		t.Fatalf("改名撞名错误码 = %s", got.ErrorCode)
	}
}

// TestBatchUpsertOpenApiThingModelsValidation 逐条校验错误码。
func TestBatchUpsertOpenApiThingModelsValidation(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	cases := []struct {
		name string
		req  dto.OpenApiThingModelUpsertDTO
		code string
	}{
		{"bad code", dto.OpenApiThingModelUpsertDTO{Code: "1bad", Name: "x"}, enums.ThingModelCodeInvalidEnum.GetCode()},
		{"empty name", dto.OpenApiThingModelUpsertDTO{Code: "th_ok", Name: ""}, enums.ParamValidEnum.GetCode()},
		{"bad category", dto.OpenApiThingModelUpsertDTO{Code: "th_ok", Name: "x", Category: strPtr("nope")}, enums.ThingModelCategoryInvalidEnum.GetCode()},
		{"bad status", dto.OpenApiThingModelUpsertDTO{Code: "th_ok", Name: "x", Status: intPtr(2)}, enums.ParamValidEnum.GetCode()},
		{
			"bad identifier",
			dto.OpenApiThingModelUpsertDTO{Code: "th_ok", Name: "x", Properties: []dto.OpenApiThingModelPropertyUpsertDTO{
				thingModelUpsertProp("1bad", "A", "float", "R"),
			}},
			enums.ThingModelIdentifierInvalidEnum.GetCode(),
		},
		{
			"bad kind",
			dto.OpenApiThingModelUpsertDTO{Code: "th_ok", Name: "x", Properties: []dto.OpenApiThingModelPropertyUpsertDTO{
				thingModelUpsertProp("a", "A", "decimal", "R"),
			}},
			enums.ParamValidEnum.GetCode(),
		},
		{
			"bad access",
			dto.OpenApiThingModelUpsertDTO{Code: "th_ok", Name: "x", Properties: []dto.OpenApiThingModelPropertyUpsertDTO{
				thingModelUpsertProp("a", "A", "float", "X"),
			}},
			enums.ParamValidEnum.GetCode(),
		},
		{
			"new property missing kind",
			dto.OpenApiThingModelUpsertDTO{Code: "th_missing_kind", Name: "x", Properties: []dto.OpenApiThingModelPropertyUpsertDTO{
				thingModelUpsertProp("a", "A", "", "R"),
			}},
			enums.ParamValidEnum.GetCode(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := svc.BatchUpsertOpenApiThingModels(ctx, thingModelUpsertReq(tc.req))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			item := wantModelItemAction(t, res, 0, vo.ThingModelUpsertActionFailed)
			if item.ErrorCode != tc.code {
				t.Fatalf("错误码 = %s，期望 %s（msg=%s）", item.ErrorCode, tc.code, item.ErrorMessage)
			}
		})
	}

	// 新建属性失败时必须回到事务前：模型不能被半创建。
	var n int64
	if err := db.Model(&po.ThingModel{}).Where("code = ?", "th_missing_kind").Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("缺 kind 的模型不应落库，实际 %d 行", n)
	}
}

// TestBatchUpsertOpenApiThingModelsPartialSuccess 跨模型部分成功。
func TestBatchUpsertOpenApiThingModelsPartialSuccess(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	res, err := svc.BatchUpsertOpenApiThingModels(ctx, thingModelUpsertReq(
		dto.OpenApiThingModelUpsertDTO{Code: "th_ok", Name: "正常模型"},
		dto.OpenApiThingModelUpsertDTO{Code: "1bad", Name: "非法模型"},
	))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if res.Total != 2 || res.Success != 1 || res.Failed != 1 || res.Created != 1 {
		t.Fatalf("部分成功统计不符: %+v", res)
	}
	wantModelItemAction(t, res, 0, vo.ThingModelUpsertActionCreated)
	if got := wantModelItemAction(t, res, 1, vo.ThingModelUpsertActionFailed); got.ErrorCode != enums.ThingModelCodeInvalidEnum.GetCode() {
		t.Fatalf("第二条错误码 = %s", got.ErrorCode)
	}
	if n := loadCount(t, svc); n != 1 {
		t.Fatalf("模型数 = %d，期望 1", n)
	}
}

// TestBatchUpsertOpenApiThingModelsPropertyLimit 单模型属性上限 200。
func TestBatchUpsertOpenApiThingModelsPropertyLimit(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	props := make([]dto.OpenApiThingModelPropertyUpsertDTO, 0, openApiThingModelUpsertMaxProperties+1)
	for i := 0; i <= openApiThingModelUpsertMaxProperties; i++ {
		props = append(props, thingModelUpsertProp(fmt.Sprintf("p%d", i), fmt.Sprintf("P%d", i), "float", "R"))
	}
	res, err := svc.BatchUpsertOpenApiThingModels(ctx, thingModelUpsertReq(dto.OpenApiThingModelUpsertDTO{
		Code: "th_many", Name: "属性过多", Properties: props,
	}))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got := wantModelItemAction(t, res, 0, vo.ThingModelUpsertActionFailed); got.ErrorCode != enums.ParamValidEnum.GetCode() {
		t.Fatalf("错误码 = %s", got.ErrorCode)
	}
	if n := loadCount(t, svc); n != 0 {
		t.Fatalf("超限模型不应落库，实际 %d 行", n)
	}
}
