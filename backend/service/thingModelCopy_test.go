// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
)

func TestCopyThingModel(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	src := seedThingModel(t, svc, "th_sensor", "温湿度传感器", "sensor")
	seedThingModelProperty(t, svc, src.ID, "temperature")
	if err := svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: src.ID, Name: src.Name, Category: "sensor", Version: "2.1.0", Description: "复制源",
	}); err != nil {
		t.Fatalf("update source: %v", err)
	}

	copied, err := svc.CopyThingModel(ctx, src.ID)
	if err != nil {
		t.Fatalf("copy thing model: %v", err)
	}
	if copied.Code != "th_sensor-copy" || copied.Name != "温湿度传感器-copy" {
		t.Fatalf("副本标识/名称 = %q/%q", copied.Code, copied.Name)
	}

	var clone po.ThingModel
	if err := db.Where("id = ?", copied.ID).First(&clone).Error; err != nil {
		t.Fatalf("load copy: %v", err)
	}
	if clone.Category != "sensor" || clone.Version != "2.1.0" ||
		clone.Description != "复制源" || clone.Status != 1 {
		t.Fatalf("副本基础信息未原样复制: %+v", clone)
	}

	// 属性不随副本走：点位绑定是 1:1 的，沿用会直接违反约束
	var props int64
	if err := db.Model(&po.ThingModelProperty{}).Where("model_id = ?", copied.ID).
		Count(&props).Error; err != nil {
		t.Fatalf("count copy properties: %v", err)
	}
	if props != 0 {
		t.Fatalf("副本不应复制属性，实际 %d 个", props)
	}

	// 原型本身不受影响
	var original po.ThingModel
	if err := db.Where("id = ?", src.ID).First(&original).Error; err != nil {
		t.Fatalf("load source: %v", err)
	}
	if original.Code != "th_sensor" || original.Name != "温湿度传感器" {
		t.Fatalf("复制改动了原型: %+v", original)
	}

	// 连续复制：-copy 已占用，下一份落到 -copy2
	again, err := svc.CopyThingModel(ctx, src.ID)
	if err != nil {
		t.Fatalf("second copy: %v", err)
	}
	if again.Code != "th_sensor-copy2" || again.Name != "温湿度传感器-copy2" {
		t.Fatalf("第二次副本 = %q/%q", again.Code, again.Name)
	}
}

func TestCopyThingModelTruncatesLongNames(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	// 长标识（64，正好上限）与长名称（100，含中文，正好上限）
	longCode := "th_" + strings.Repeat("a", 61)
	longName := strings.Repeat("超长名称", 25)
	src := seedThingModel(t, svc, longCode, longName, "other")

	copied, err := svc.CopyThingModel(ctx, src.ID)
	if err != nil {
		t.Fatalf("copy long thing model: %v", err)
	}
	if n := utf8.RuneCountInString(copied.Code); n > thingModelCodeMaxLen {
		t.Fatalf("副本标识超长: %d > %d", n, thingModelCodeMaxLen)
	}
	if n := utf8.RuneCountInString(copied.Name); n > thingModelNameMaxLen {
		t.Fatalf("副本名称超长: %d > %d", n, thingModelNameMaxLen)
	}
	if !strings.HasSuffix(copied.Code, thingModelCopySuffix) ||
		!strings.HasSuffix(copied.Name, thingModelCopySuffix) {
		t.Fatalf("副本标识/名称缺少后缀: %q/%q", copied.Code, copied.Name)
	}
	if !utf8.ValidString(copied.Name) {
		t.Fatalf("副本名称不是合法 UTF-8: %q", copied.Name)
	}
}

func TestCopyThingModelKeepsDisabledStatus(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)
	ctx := context.Background()

	src := seedThingModel(t, svc, "th_off", "停用模型", "other")
	off := 0
	if err := svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: src.ID, Name: src.Name, Category: "other", Status: &off,
	}); err != nil {
		t.Fatalf("disable source: %v", err)
	}

	copied, err := svc.CopyThingModel(ctx, src.ID)
	if err != nil {
		t.Fatalf("copy disabled thing model: %v", err)
	}
	var clone po.ThingModel
	if err := db.Where("id = ?", copied.ID).First(&clone).Error; err != nil {
		t.Fatalf("load copy: %v", err)
	}
	// status 的 default:1 会让零值被 GORM 省略，这里确认服务层把它写回去了
	if clone.Status != 0 {
		t.Fatalf("停用模型的副本应保持停用，实际 status=%d", clone.Status)
	}
}

func TestCopyThingModelNotFound(t *testing.T) {
	db := newThingModelTestDB(t)
	svc := NewThingModelService(db)

	_, err := svc.CopyThingModel(context.Background(), "not-exists")
	wantThingModelErrCode(t, err, enums.ThingModelNotExistsEnum.GetCode())
}
