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

// TestThingModelDeviceAndAddressBinding 覆盖设备↔模型、点位↔属性的完整绑定规则：
// kind 一致、读写覆盖、模型匹配、已绑定时禁止换模型/改属性类型。
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

	// 设备绑定模型，详情回带模型名
	if err := deviceSvc.CreateDevice(ctx, &dto.CreateDeviceDTO{
		Code: "bind-test-dev", Name: "绑定测试设备", ProtocolID: "proto-1",
		ProtocolJSON: json.RawMessage(`{}`), ModelID: model.ID,
	}); err != nil {
		t.Fatalf("create device: %v", err)
	}
	var device po.Device
	if err := db.Where("name = ?", "绑定测试设备").First(&device).Error; err != nil {
		t.Fatalf("load device: %v", err)
	}
	if device.ModelID != model.ID {
		t.Fatalf("device.model_id = %q, want %q", device.ModelID, model.ID)
	}
	deviceVO, err := deviceSvc.GetDeviceByID(ctx, device.ID)
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if deviceVO.ModelName != "温湿度传感器" {
		t.Fatalf("modelName = %q", deviceVO.ModelName)
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

	// kind 不匹配：字符串点位不能绑 float 属性
	wantThingModelErrCode(t, addressSvc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID: device.ID, Name: "D101", Label: "类型不符",
		CommonDataType: driver.TypeString, RwPermission: "R", ScanFrequency: 1000,
		PropertyID: prop.ID,
	}), enums.ThingModelKindMismatchEnum.GetCode())

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

	// 模型不匹配：另一模型的属性不能绑到这台设备
	otherModel := seedThingModel(t, modelSvc, "th_meter", "电表", "meter")
	otherProp := seedThingModelProperty(t, modelSvc, otherModel.ID, "voltage")
	wantThingModelErrCode(t, addressSvc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID: device.ID, Name: "D103", Label: "跨模型",
		CommonDataType: driver.TypeFloat, RwPermission: "R", ScanFrequency: 1000,
		PropertyID: otherProp.ID,
	}), enums.ThingModelBindingMismatchEnum.GetCode())

	// 已有点位绑定属性时，设备不能更换模型
	wantThingModelErrCode(t, deviceSvc.UpdateDevice(ctx, &dto.UpdateDeviceDTO{
		ID: device.ID, ModelID: strPtr(otherModel.ID),
	}), enums.ThingModelBindingLockedEnum.GetCode())

	// 属性被点位绑定后，不能改 kind / access_mode
	wantThingModelErrCode(t, modelSvc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: "temperature", Name: "温度",
		Kind: "string", AccessMode: "R",
	}), enums.ThingModelPropertyBoundImmutableEnum.GetCode())

	// 解除点位绑定后，设备可以换模型
	if err := addressSvc.UpdateDeviceAddress(ctx, &dto.UpdateDeviceAddressDTO{
		ID: addr.ID, Label: "进风温度", PropertyID: strPtr(""),
	}); err != nil {
		t.Fatalf("unbind address: %v", err)
	}
	if err := deviceSvc.UpdateDevice(ctx, &dto.UpdateDeviceDTO{
		ID: device.ID, ModelID: strPtr(otherModel.ID),
	}); err != nil {
		t.Fatalf("change device model after unbind: %v", err)
	}
}
