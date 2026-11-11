// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/mappingdsl"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/testutil/drivertest"
)

const scaleTransformJSON = `[{"op":"scale","factor":0.1}]`

func rawp(s string) *json.RawMessage {
	r := json.RawMessage(s)
	return &r
}

func seedTransformDevice(t *testing.T) (*DeviceAddressService, context.Context) {
	t.Helper()
	drivertest.EnsureSyntheticProtocol()
	db := newThresholdTestDB(t)
	if err := db.Create(&po.IotProtocol{ID: "proto-t", Name: drivertest.TestProtocol}).Error; err != nil {
		t.Fatalf("seed protocol: %v", err)
	}
	if err := db.Create(&po.Device{ID: "dev-t", Name: "1#炉", ProtocolID: "proto-t"}).Error; err != nil {
		t.Fatalf("seed device: %v", err)
	}
	return NewDeviceAddressService(db, NewAlarmService(db)), context.Background()
}

func TestCreateDeviceAddressTransformRoundTrip(t *testing.T) {
	svc, ctx := seedTransformDevice(t)

	if err := svc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID:       "dev-t",
		Name:           "40001",
		Label:          "温度",
		CommonDataType: driver.TypeShort,
		RwPermission:   "R",
		ScanFrequency:  1000,
		Transform:      json.RawMessage(scaleTransformJSON),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	var a po.DeviceAddress
	if err := svc.sqliteDB.Where("device_id = ?", "dev-t").First(&a).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	ts, err := mappingdsl.ParseNormalization(a.TransformJSON)
	if err != nil || len(ts) != 1 || ts[0].Op != mappingdsl.OpScale {
		t.Fatalf("transform_json 未落库：%q err=%v", a.TransformJSON, err)
	}

	// VO 是编辑框回填的唯一来源，漏了它就是"保存了却看不到"。
	got, err := svc.GetDeviceAddressByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.TransformJSON != a.TransformJSON {
		t.Fatalf("VO transformJson = %q, want %q", got.TransformJSON, a.TransformJSON)
	}
}

func TestCreateDeviceAddressRejectsScaleOnString(t *testing.T) {
	svc, ctx := seedTransformDevice(t)

	err := svc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID:       "dev-t",
		Name:           "S1",
		Label:          "状态字",
		CommonDataType: driver.TypeString,
		RwPermission:   "R",
		ScanFrequency:  1000,
		Transform:      json.RawMessage(scaleTransformJSON),
	})
	if err == nil || !strings.Contains(err.Error(), "仅适用于数值类型") {
		t.Fatalf("字符串点位配 scale 应被拒绝，实际 err=%v", err)
	}
}

func TestUpdateDeviceAddressTransformTriState(t *testing.T) {
	svc, ctx := seedTransformDevice(t)
	if err := svc.sqliteDB.Create(&po.DeviceAddress{
		ID: "pt-1", DeviceID: "dev-t", Name: "40001", Label: "温度",
		CommonDataType: driver.TypeShort, DataType: "int16", RwPermission: "R", Status: 1,
		TransformJSON: scaleTransformJSON,
	}).Error; err != nil {
		t.Fatalf("seed address: %v", err)
	}

	load := func() po.DeviceAddress {
		var a po.DeviceAddress
		if err := svc.sqliteDB.Where("id = ?", "pt-1").First(&a).Error; err != nil {
			t.Fatalf("load: %v", err)
		}
		return a
	}

	// 省略 = 不动
	if err := svc.UpdateDeviceAddress(ctx, &dto.UpdateDeviceAddressDTO{
		ID: "pt-1", Label: "温度", Description: "只改描述",
	}); err != nil {
		t.Fatalf("update omit: %v", err)
	}
	if load().TransformJSON != scaleTransformJSON {
		t.Fatal("省略 transform 不得改动已配置的流水线")
	}

	// [] = 清空
	if err := svc.UpdateDeviceAddress(ctx, &dto.UpdateDeviceAddressDTO{
		ID: "pt-1", Label: "温度", Transform: rawp(`[]`),
	}); err != nil {
		t.Fatalf("update clear: %v", err)
	}
	if load().TransformJSON != "" {
		t.Fatalf("[] 应清空流水线，实际 %q", load().TransformJSON)
	}

	// 数组 = 整份替换
	if err := svc.UpdateDeviceAddress(ctx, &dto.UpdateDeviceAddressDTO{
		ID: "pt-1", Label: "温度",
		Transform: rawp(`[{"op":"round","digits":2}]`),
	}); err != nil {
		t.Fatalf("update replace: %v", err)
	}
	ts, err := mappingdsl.ParseNormalization(load().TransformJSON)
	if err != nil || len(ts) != 1 || ts[0].Op != mappingdsl.OpRound {
		t.Fatalf("替换未生效：%q err=%v", load().TransformJSON, err)
	}
}

// 改类型但没传新流水线时，必须按新 Kind 复核库里那份 —— 否则数值点位改成
// string 后会留下一个每轮都失败回退的 scale。
func TestUpdateDeviceAddressRejectsStaleTransformOnTypeChange(t *testing.T) {
	svc, ctx := seedTransformDevice(t)
	if err := svc.sqliteDB.Create(&po.DeviceAddress{
		ID: "pt-1", DeviceID: "dev-t", Name: "40001", Label: "温度",
		CommonDataType: driver.TypeShort, DataType: "int16", RwPermission: "R", Status: 1,
		TransformJSON: scaleTransformJSON,
	}).Error; err != nil {
		t.Fatalf("seed address: %v", err)
	}

	err := svc.UpdateDeviceAddress(ctx, &dto.UpdateDeviceAddressDTO{
		ID: "pt-1", Label: "温度", CommonDataType: driver.TypeString,
	})
	if err == nil || !strings.Contains(err.Error(), "仅适用于数值类型") {
		t.Fatalf("改成字符串点位时存量 scale 应被拒绝，实际 err=%v", err)
	}
}

func TestValidateAddressTransforms(t *testing.T) {
	scale := []mappingdsl.Transform{{Op: mappingdsl.OpScale, Factor: floatPtr(0.1)}}
	round := []mappingdsl.Transform{{Op: mappingdsl.OpRound, Digits: 2}}
	trim := []mappingdsl.Transform{{Op: mappingdsl.OpTrim}}
	def := []mappingdsl.Transform{{Op: mappingdsl.OpDefault, Value: "abc"}}

	if err := validateAddressTransforms(scale, driver.KindInt); err != nil {
		t.Fatalf("数值点位配 scale 应通过: %v", err)
	}
	if err := validateAddressTransforms(round, driver.KindFloat); err != nil {
		t.Fatalf("数值点位配 round 应通过: %v", err)
	}
	if err := validateAddressTransforms(trim, driver.KindString); err != nil {
		t.Fatalf("字符串点位配 trim 应通过: %v", err)
	}
	if err := validateAddressTransforms(scale, driver.KindString); err == nil {
		t.Fatal("字符串点位配 scale 应被拒绝")
	}
	if err := validateAddressTransforms(def, driver.KindInt); err == nil {
		t.Fatal("数值点位的非数值 default 应被拒绝")
	}
	if err := validateAddressTransforms(nil, driver.KindString); err != nil {
		t.Fatalf("空流水线应通过: %v", err)
	}
}

func floatPtr(v float64) *float64 { return &v }
