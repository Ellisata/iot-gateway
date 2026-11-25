// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/testutil/drivertest"
)

func strPtr(v string) *string { return &v }

// TestThingModelDeviceAndAddressBinding 覆盖点位↔属性的绑定规则：
// 属性可来自任意模型、kind 一致、读写覆盖、已绑定时禁止改类型。
func TestThingModelDeviceAndAddressBinding(t *testing.T) {
	drivertest.EnsureSyntheticProtocol()
	db := newThingModelTestDB(t)
	ctx := context.Background()

	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: drivertest.TestProtocol}).Error; err != nil {
		t.Fatalf("seed protocol: %v", err)
	}

	modelSvc := NewThingModelService(db)
	deviceSvc := NewDeviceService(db, nil, nil)
	addressSvc := NewDeviceAddressService(db, nil)

	model := seedThingModel(t, modelSvc, "th_sensor", "温湿度传感器", "sensor")
	prop := seedThingModelProperty(t, modelSvc, model.ID, "temperature") // kind=float, access=R

	// 设备不再绑定模型。
	if err := deviceSvc.CreateDevice(ctx, &dto.CreateDeviceDTO{
		Code: "bind-test-dev", Name: "绑定测试设备", ProtocolID: "proto-1",
		ProtocolJSON: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("create device: %v", err)
	}
	var device po.Device
	if err := db.Where("name = ?", "绑定测试设备").First(&device).Error; err != nil {
		t.Fatalf("load device: %v", err)
	}

	// 正确的绑定：float 点位 + R 权限 → float 属性 + R
	if err := addressSvc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID: device.ID, Name: "D100", Label: "进风温度",
		CommonDataType: driver.TypeFloat, RwPermission: "R", ScanFrequency: 1000,
		PropertyID: prop.ID,
	}); err != nil {
		t.Fatalf("bind address to property: %v", err)
	}
	var addr po.DeviceAddress
	if err := db.Where("device_id = ? AND name = ?", device.ID, "D100").First(&addr).Error; err != nil {
		t.Fatalf("load address: %v", err)
	}
	addrVO, err := addressSvc.GetDeviceAddressByID(ctx, addr.ID)
	if err != nil {
		t.Fatalf("get address: %v", err)
	}
	if addrVO.PropertyIdentifier != "temperature" || addrVO.PropertyName != "temperature" {
		t.Fatalf("address VO 未补全属性信息: %+v", addrVO)
	}
	// 点位可绑定任意模型的属性，identifier 只在模型内唯一：列表页要靠模型名消歧。
	if addrVO.PropertyModelID != model.ID || addrVO.PropertyModelName != "温湿度传感器" {
		t.Fatalf("address VO 未补全所属物模型: %+v", addrVO)
	}

	// 另一模型的属性也能直接绑到同一台设备的点位（不再要求设备绑定模型）。
	otherModel := seedThingModel(t, modelSvc, "th_meter", "电表", "meter")
	otherProp := seedThingModelProperty(t, modelSvc, otherModel.ID, "voltage")
	if err := addressSvc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID: device.ID, Name: "D103", Label: "跨模型",
		CommonDataType: driver.TypeFloat, RwPermission: "R", ScanFrequency: 1000,
		PropertyID: otherProp.ID,
	}); err != nil {
		t.Fatalf("bind cross-model property: %v", err)
	}

	// kind 只是语义说明：字符串点位也能直接绑到 float 属性上（不再报 41016）。
	noteProp := seedThingModelProperty(t, modelSvc, model.ID, "kindnote")
	if err := addressSvc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID: device.ID, Name: "D101", Label: "类型仅说明",
		CommonDataType: driver.TypeString, RwPermission: "R", ScanFrequency: 1000,
		PropertyID: noteProp.ID,
	}); err != nil {
		t.Fatalf("bind kind-mismatched point: %v", err)
	}

	// 读写不覆盖：R 点位不能绑 RW 属性
	if err := modelSvc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "setpoint", Name: "设定值",
		Kind: "float", AccessMode: "RW",
	}); err != nil {
		t.Fatalf("create rw property: %v", err)
	}
	var rwProp po.ThingModelProperty
	if err := db.Where("model_id = ? AND identifier = ?", model.ID, "setpoint").First(&rwProp).Error; err != nil {
		t.Fatalf("load rw property: %v", err)
	}
	wantThingModelErrCode(t, addressSvc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID: device.ID, Name: "D102", Label: "权限不符",
		CommonDataType: driver.TypeFloat, RwPermission: "R", ScanFrequency: 1000,
		PropertyID: rwProp.ID,
	}), enums.ThingModelAccessMismatchEnum.GetCode())

	// 属性被点位绑定后：kind 只作说明，可随时改；access_mode 仍是兼容性前提，改它 41018。
	if err := modelSvc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: "temperature", Name: "温度",
		Kind: "string", AccessMode: "R",
	}); err != nil {
		t.Fatalf("change bound property kind: %v", err)
	}
	wantThingModelErrCode(t, modelSvc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: "temperature", Name: "温度",
		Kind: "string", AccessMode: "RW",
	}), enums.ThingModelPropertyBoundImmutableEnum.GetCode())

	// 解除点位绑定后可以改读写模式。
	if err := addressSvc.UpdateDeviceAddress(ctx, &dto.UpdateDeviceAddressDTO{
		ID: addr.ID, Label: "进风温度", PropertyID: strPtr(""),
	}); err != nil {
		t.Fatalf("unbind address: %v", err)
	}
	if err := modelSvc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: "temperature", Name: "温度",
		Kind: "string", AccessMode: "RW",
	}); err != nil {
		t.Fatalf("change property access after unbind: %v", err)
	}
}
