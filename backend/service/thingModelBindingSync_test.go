// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"gorm.io/gorm"

	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/testutil/drivertest"
)

// seedBindingProtocol 准备合成协议行：设备通过 protocol_id 解析协议名，
// 再经 driver 类型注册表判出点位 kind。
func seedBindingProtocol(t *testing.T, db *gorm.DB) {
	t.Helper()
	drivertest.EnsureSyntheticProtocol()
	if err := db.Create(&po.IotProtocol{ID: "proto-bind", Name: drivertest.TestProtocol}).Error; err != nil {
		t.Fatalf("seed protocol: %v", err)
	}
}

func seedBindingDevice(t *testing.T, db *gorm.DB, name, modelID string) po.Device {
	t.Helper()
	dev := po.Device{
		Code: name, Name: name, ProtocolID: "proto-bind",
		ProtocolJSON: "{}", ModelID: modelID, Status: 1,
	}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("seed device %q: %v", name, err)
	}
	return dev
}

func seedBindingAddress(t *testing.T, db *gorm.DB, deviceID, name, dataType, commonType, rw, propertyID string) po.DeviceAddress {
	t.Helper()
	addr := po.DeviceAddress{
		DeviceID: deviceID, Name: name, Label: name,
		CommonDataType: commonType, DataType: dataType,
		RwPermission: rw, PropertyID: propertyID,
		ScanFrequency: 1000, Status: 1,
	}
	if err := db.Create(&addr).Error; err != nil {
		t.Fatalf("seed address %q: %v", name, err)
	}
	return addr
}

func reloadDevice(t *testing.T, db *gorm.DB, id string) po.Device {
	t.Helper()
	var dev po.Device
	if err := db.Where("id = ?", id).First(&dev).Error; err != nil {
		t.Fatalf("reload device %s: %v", id, err)
	}
	return dev
}

func reloadAddress(t *testing.T, db *gorm.DB, id string) po.DeviceAddress {
	t.Helper()
	var addr po.DeviceAddress
	if err := db.Where("id = ?", id).First(&addr).Error; err != nil {
		t.Fatalf("reload address %s: %v", id, err)
	}
	return addr
}

func TestCreateThingModelBindsDevices(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	svc := NewThingModelService(db)
	ctx := context.Background()

	dev1 := seedBindingDevice(t, db, "bind-dev-1", "")
	dev2 := seedBindingDevice(t, db, "bind-dev-2", "")

	// 去重、空白、空串都应被归一
	if err := svc.CreateThingModel(ctx, &dto.CreateThingModelDTO{
		Code: "m_bind", Name: "绑定设备模型", Category: "sensor",
		DeviceIDs: []string{dev1.ID, " " + dev2.ID + " ", dev1.ID, ""},
	}); err != nil {
		t.Fatalf("create model with devices: %v", err)
	}
	var model po.ThingModel
	if err := db.Where("code = ?", "m_bind").First(&model).Error; err != nil {
		t.Fatalf("load model: %v", err)
	}
	if got := reloadDevice(t, db, dev1.ID).ModelID; got != model.ID {
		t.Fatalf("dev1.model_id = %q, want %q", got, model.ID)
	}
	if got := reloadDevice(t, db, dev2.ID).ModelID; got != model.ID {
		t.Fatalf("dev2.model_id = %q, want %q", got, model.ID)
	}

	// 不存在的设备：整单回滚，模型行也不能留下
	wantThingModelErrCode(t, svc.CreateThingModel(ctx, &dto.CreateThingModelDTO{
		Code: "m_missing_dev", Name: "缺设备模型", DeviceIDs: []string{"no-such-device"},
	}), enums.DeviceNotExistsEnum.GetCode())
	var count int64
	if err := db.Model(&po.ThingModel{}).Where("code = ?", "m_missing_dev").Count(&count).Error; err != nil {
		t.Fatalf("count model: %v", err)
	}
	if count != 0 {
		t.Fatalf("失败创建留下了模型行 count=%d", count)
	}

	// 已有点位绑定属性的设备不能绑到新模型（41013）
	lockedModel := seedThingModel(t, svc, "m_locked", "锁定模型", "sensor")
	lockedProp := seedThingModelProperty(t, svc, lockedModel.ID, "temperature")
	lockedDev := seedBindingDevice(t, db, "bind-dev-locked", lockedModel.ID)
	seedBindingAddress(t, db, lockedDev.ID, "D100", "float32", driver.TypeFloat, "R", lockedProp.ID)

	wantThingModelErrCode(t, svc.CreateThingModel(ctx, &dto.CreateThingModelDTO{
		Code: "m_conflict", Name: "冲突模型", DeviceIDs: []string{lockedDev.ID},
	}), enums.ThingModelBindingLockedEnum.GetCode())

	// 超过 200 个去重后的 ID
	tooMany := make([]string, 0, thingModelBindingLimit+1)
	for i := 0; i <= thingModelBindingLimit; i++ {
		tooMany = append(tooMany, fmt.Sprintf("dev-%03d", i))
	}
	wantThingModelErrCode(t, svc.CreateThingModel(ctx, &dto.CreateThingModelDTO{
		Code: "m_too_many", Name: "超限模型", DeviceIDs: tooMany,
	}), enums.ThingModelBindingLimitEnum.GetCode())
}

func TestUpdateThingModelSyncsDevices(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "m_sync", "同步模型", "sensor")
	keepDev := seedBindingDevice(t, db, "sync-keep", model.ID)
	removeDev := seedBindingDevice(t, db, "sync-remove", model.ID)
	freeDev := seedBindingDevice(t, db, "sync-free", "")
	addDev := seedBindingDevice(t, db, "sync-add", "")

	// 省略 DeviceIDs = 不改绑定
	if err := svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: model.ID, Name: model.Name, Category: model.Category,
	}); err != nil {
		t.Fatalf("update without deviceIds: %v", err)
	}
	if got := reloadDevice(t, db, removeDev.ID).ModelID; got != model.ID {
		t.Fatalf("nil deviceIds 不应解绑: model_id=%q", got)
	}

	// 同请求一加一减
	desired := []string{keepDev.ID, freeDev.ID}
	if err := svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: model.ID, Name: model.Name, Category: model.Category, DeviceIDs: &desired,
	}); err != nil {
		t.Fatalf("sync add/remove: %v", err)
	}
	if got := reloadDevice(t, db, removeDev.ID).ModelID; got != "" {
		t.Fatalf("removeDev 应被解绑, got %q", got)
	}
	if got := reloadDevice(t, db, freeDev.ID).ModelID; got != model.ID {
		t.Fatalf("freeDev 应绑定, got %q", got)
	}
	if got := reloadDevice(t, db, keepDev.ID).ModelID; got != model.ID {
		t.Fatalf("keepDev 应保持绑定, got %q", got)
	}
	if got := reloadDevice(t, db, addDev.ID).ModelID; got != "" {
		t.Fatalf("未出现在列表里的 addDev 不应被绑定, got %q", got)
	}

	// 空数组 = 全部解绑
	empty := []string{}
	if err := svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: model.ID, Name: model.Name, Category: model.Category, DeviceIDs: &empty,
	}); err != nil {
		t.Fatalf("unbind all: %v", err)
	}
	if got := reloadDevice(t, db, keepDev.ID).ModelID; got != "" {
		t.Fatalf("keepDev 应被解绑, got %q", got)
	}
	if got := reloadDevice(t, db, freeDev.ID).ModelID; got != "" {
		t.Fatalf("freeDev 应被解绑, got %q", got)
	}

	// 解绑一个点位已绑属性的设备 → 41013，且同请求里的其它解绑要回滚
	boundDev := seedBindingDevice(t, db, "sync-bound", model.ID)
	otherDev := seedBindingDevice(t, db, "sync-other", model.ID)
	prop := seedThingModelProperty(t, svc, model.ID, "temperature")
	boundAddr := seedBindingAddress(t, db, boundDev.ID, "D200", "float32", driver.TypeFloat, "R", prop.ID)
	// desired 只留 otherDev：boundDev 被解绑且它绑着属性 → 41013
	keepOther := []string{otherDev.ID}
	wantThingModelErrCode(t, svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: model.ID, Name: model.Name, Category: model.Category, DeviceIDs: &keepOther,
	}), enums.ThingModelBindingLockedEnum.GetCode())
	if got := reloadDevice(t, db, otherDev.ID).ModelID; got != model.ID {
		t.Fatalf("失败请求应回滚 otherDev 的绑定, got %q", got)
	}
	if got := reloadDevice(t, db, boundDev.ID).ModelID; got != model.ID {
		t.Fatalf("失败请求应回滚 boundDev 的绑定, got %q", got)
	}

	// 先解除点位上的属性绑定，后面才有"无锁设备"可解绑
	if err := db.Model(&po.DeviceAddress{}).Where("id = ?", boundAddr.ID).
		Update("property_id", "").Error; err != nil {
		t.Fatalf("unbind address: %v", err)
	}

	// 停用模型 + 新增设备 → 41012
	disabled := 0
	wantThingModelErrCode(t, svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: model.ID, Name: model.Name, Category: model.Category,
		Status: &disabled, DeviceIDs: &[]string{addDev.ID},
	}), enums.ThingModelDisabledEnum.GetCode())
	if got := reloadDevice(t, db, addDev.ID).ModelID; got != "" {
		t.Fatalf("失败请求不应绑定 addDev, got %q", got)
	}

	// 停用 + 只移除 → 允许
	unbindAll := []string{}
	if err := svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: model.ID, Name: model.Name, Category: model.Category,
		Status: &disabled, DeviceIDs: &unbindAll,
	}); err != nil {
		t.Fatalf("disable + unbind all: %v", err)
	}
	if got := reloadDevice(t, db, boundDev.ID).ModelID; got != "" {
		t.Fatalf("boundDev 应被解绑, got %q", got)
	}

	// 同一次请求里启用 + 加设备 → 允许（先落 status 再校验）
	enabled := 1
	addOne := []string{addDev.ID}
	if err := svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: model.ID, Name: model.Name, Category: model.Category,
		Status: &enabled, DeviceIDs: &addOne,
	}); err != nil {
		t.Fatalf("enable + bind: %v", err)
	}
	if got := reloadDevice(t, db, addDev.ID).ModelID; got != model.ID {
		t.Fatalf("addDev 应已绑定, got %q", got)
	}
}

func TestCreateThingModelPropertyBindsAddresses(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "m_points", "点位模型", "sensor")
	dev := seedBindingDevice(t, db, "points-dev", model.ID)
	dev2 := seedBindingDevice(t, db, "points-dev-2", model.ID)
	addr1 := seedBindingAddress(t, db, dev.ID, "PD100", "float32", driver.TypeFloat, "R", "")
	// 一个属性可以绑多台设备的点位，但同一设备最多一个点位
	addr2 := seedBindingAddress(t, db, dev2.ID, "PD101", "float32", driver.TypeFloat, "R", "")

	if err := svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "temperature", Name: "温度",
		Kind: "float", AccessMode: "R",
		AddressIDs: []string{addr1.ID, " " + addr2.ID + " ", addr1.ID},
	}); err != nil {
		t.Fatalf("create property with points: %v", err)
	}
	var prop po.ThingModelProperty
	if err := db.Where("model_id = ? AND identifier = ?", model.ID, "temperature").First(&prop).Error; err != nil {
		t.Fatalf("load property: %v", err)
	}
	if got := reloadAddress(t, db, addr1.ID).PropertyID; got != prop.ID {
		t.Fatalf("addr1.property_id = %q, want %q", got, prop.ID)
	}
	if got := reloadAddress(t, db, addr2.ID).PropertyID; got != prop.ID {
		t.Fatalf("addr2.property_id = %q, want %q", got, prop.ID)
	}

	// 同一设备对同一属性绑两个点位 → 41021（迁移 027 的部分唯一索引是硬约束）
	dup1 := seedBindingAddress(t, db, dev.ID, "PD106", "float32", driver.TypeFloat, "R", "")
	dup2 := seedBindingAddress(t, db, dev.ID, "PD107", "float32", driver.TypeFloat, "R", "")
	wantThingModelErrCode(t, svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "dup_points", Name: "同设备多点",
		Kind: "float", AccessMode: "R", AddressIDs: []string{dup1.ID, dup2.ID},
	}), enums.ThingModelDevicePointDupEnum.GetCode())

	// 点位已绑其他属性 → 41019
	otherProp := seedThingModelProperty(t, svc, model.ID, "setpoint")
	wantThingModelErrCode(t, svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "steal", Name: "抢绑",
		Kind: "float", AccessMode: "R", AddressIDs: []string{addr1.ID},
	}), enums.ThingModelPointBoundElsewhereEnum.GetCode())
	_ = otherProp

	// 跨模型 → 41014
	otherModel := seedThingModel(t, svc, "m_other", "另一个模型", "sensor")
	otherDev := seedBindingDevice(t, db, "points-other-dev", otherModel.ID)
	crossAddr := seedBindingAddress(t, db, otherDev.ID, "PD102", "float32", driver.TypeFloat, "R", "")
	wantThingModelErrCode(t, svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "cross", Name: "跨模型",
		Kind: "float", AccessMode: "R", AddressIDs: []string{crossAddr.ID},
	}), enums.ThingModelBindingMismatchEnum.GetCode())

	// kind 不符 → 41016
	strAddr := seedBindingAddress(t, db, dev.ID, "PD103", "string", driver.TypeString, "R", "")
	wantThingModelErrCode(t, svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "kind_bad", Name: "类型不符",
		Kind: "float", AccessMode: "R", AddressIDs: []string{strAddr.ID},
	}), enums.ThingModelKindMismatchEnum.GetCode())

	// 权限不覆盖 → 41017（R 点位绑 RW 属性）
	rwAddr := seedBindingAddress(t, db, dev.ID, "PD104", "float32", driver.TypeFloat, "R", "")
	wantThingModelErrCode(t, svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "access_bad", Name: "权限不符",
		Kind: "float", AccessMode: "RW", AddressIDs: []string{rwAddr.ID},
	}), enums.ThingModelAccessMismatchEnum.GetCode())

	// 点位不存在 → 40012
	wantThingModelErrCode(t, svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "missing_addr", Name: "点位不存在",
		Kind: "float", AccessMode: "R", AddressIDs: []string{"no-such-address"},
	}), enums.DeviceAddressNotExistsEnum.GetCode())

	// 停用模型 → 41012
	disabled := 0
	if err := svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: model.ID, Name: model.Name, Category: model.Category, Status: &disabled,
	}); err != nil {
		t.Fatalf("disable model: %v", err)
	}
	freshAddr := seedBindingAddress(t, db, dev.ID, "PD105", "float32", driver.TypeFloat, "R", "")
	wantThingModelErrCode(t, svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "disabled_model", Name: "停用模型",
		Kind: "float", AccessMode: "R", AddressIDs: []string{freshAddr.ID},
	}), enums.ThingModelDisabledEnum.GetCode())
}

func TestUpdateThingModelPropertySyncsAddresses(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "m_prop_sync", "属性同步模型", "sensor")
	dev := seedBindingDevice(t, db, "prop-sync-dev", model.ID)
	dev2 := seedBindingDevice(t, db, "prop-sync-dev-2", model.ID)
	addr1 := seedBindingAddress(t, db, dev.ID, "PS100", "float32", driver.TypeFloat, "R", "")
	addr2 := seedBindingAddress(t, db, dev2.ID, "PS101", "float32", driver.TypeFloat, "R", "")

	if err := svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "temperature", Name: "温度",
		Kind: "float", AccessMode: "R", AddressIDs: []string{addr1.ID, addr2.ID},
	}); err != nil {
		t.Fatalf("create property: %v", err)
	}
	var prop po.ThingModelProperty
	if err := db.Where("model_id = ? AND identifier = ?", model.ID, "temperature").First(&prop).Error; err != nil {
		t.Fatalf("load property: %v", err)
	}

	// 省略 addressIds = 不改绑定
	if err := svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "温度2",
		Kind: "float", AccessMode: "R",
	}); err != nil {
		t.Fatalf("update without addressIds: %v", err)
	}
	if got := reloadAddress(t, db, addr1.ID).PropertyID; got != prop.ID {
		t.Fatalf("nil addressIds 不应解绑: %q", got)
	}

	// 空数组 = 全部解绑，行仍在
	empty := []string{}
	if err := svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "温度",
		Kind: "float", AccessMode: "R", AddressIDs: &empty,
	}); err != nil {
		t.Fatalf("unbind all points: %v", err)
	}
	if got := reloadAddress(t, db, addr1.ID).PropertyID; got != "" {
		t.Fatalf("addr1 应被解绑, got %q", got)
	}
	if got := reloadAddress(t, db, addr2.ID).PropertyID; got != "" {
		t.Fatalf("addr2 应被解绑, got %q", got)
	}

	// 同请求改 kind + 清空绑定 → 允许
	if err := svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "温度",
		Kind: "string", AccessMode: "R", AddressIDs: &empty,
	}); err != nil {
		t.Fatalf("change kind with unbind all: %v", err)
	}

	// 重新绑定一条 float 点位，并把 kind 改回 float
	rebind := []string{addr1.ID}
	if err := db.Model(&po.ThingModelProperty{}).Where("id = ?", prop.ID).
		Updates(map[string]any{"kind": "float"}).Error; err != nil {
		t.Fatalf("reset kind: %v", err)
	}
	if err := svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "温度",
		Kind: "float", AccessMode: "R", AddressIDs: &rebind,
	}); err != nil {
		t.Fatalf("rebind point: %v", err)
	}

	// 同一设备追加第二个点位 → 41021（同设备上保留的 addr1 与新增点冲突）
	dupAddr := seedBindingAddress(t, db, dev.ID, "PS103", "float32", driver.TypeFloat, "R", "")
	dupAdd := []string{addr1.ID, dupAddr.ID}
	wantThingModelErrCode(t, svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "温度",
		Kind: "float", AccessMode: "R", AddressIDs: &dupAdd,
	}), enums.ThingModelDevicePointDupEnum.GetCode())

	// 保留一条绑定时改 kind → 41018
	wantThingModelErrCode(t, svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "温度",
		Kind: "string", AccessMode: "R", AddressIDs: &rebind,
	}), enums.ThingModelPropertyBoundImmutableEnum.GetCode())

	// 抢别的属性的点位 → 41019
	otherProp := seedThingModelProperty(t, svc, model.ID, "setpoint")
	steal := []string{addr1.ID}
	if err := db.Model(&po.DeviceAddress{}).Where("id = ?", addr1.ID).
		Update("property_id", otherProp.ID).Error; err != nil {
		t.Fatalf("move address to other property: %v", err)
	}
	wantThingModelErrCode(t, svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "温度",
		Kind: "float", AccessMode: "R", AddressIDs: &steal,
	}), enums.ThingModelPointBoundElsewhereEnum.GetCode())

	// 模型事后停用：带原绑定改名仍可保存（保留的绑定不重复校验）
	if err := db.Model(&po.DeviceAddress{}).Where("id = ?", addr1.ID).
		Update("property_id", prop.ID).Error; err != nil {
		t.Fatalf("restore address binding: %v", err)
	}
	disabled := 0
	if err := svc.UpdateThingModel(ctx, &dto.UpdateThingModelDTO{
		ID: model.ID, Name: model.Name, Category: model.Category, Status: &disabled,
	}); err != nil {
		t.Fatalf("disable model: %v", err)
	}
	same := []string{addr1.ID}
	if err := svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "停用后改名",
		Kind: "float", AccessMode: "R", AddressIDs: &same,
	}); err != nil {
		t.Fatalf("rename with retained binding on disabled model: %v", err)
	}

	// 停用模型新增点位 → 41012（放在另一台设备上，避免先撞同设备唯一约束）
	newAddr := seedBindingAddress(t, db, dev2.ID, "PS102", "float32", driver.TypeFloat, "R", "")
	add := []string{addr1.ID, newAddr.ID}
	wantThingModelErrCode(t, svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "停用后加点",
		Kind: "float", AccessMode: "R", AddressIDs: &add,
	}), enums.ThingModelDisabledEnum.GetCode())
}

func TestListThingModelDeviceOptions(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "m_options", "选项模型", "sensor")
	prop := seedThingModelProperty(t, svc, model.ID, "temperature")
	lockedDev := seedBindingDevice(t, db, "options-locked", model.ID)
	seedBindingAddress(t, db, lockedDev.ID, "OD100", "float32", driver.TypeFloat, "R", prop.ID)
	freeDev := seedBindingDevice(t, db, "options-free", "")
	otherDev := seedBindingDevice(t, db, "options-other", "")

	res, err := svc.ListThingModelDeviceOptions(ctx, model.ID, "")
	if err != nil {
		t.Fatalf("list device options: %v", err)
	}
	opts := res.Records
	if res.Truncated {
		t.Fatalf("只查 3 台设备不应标记 truncated")
	}
	if len(opts) != 3 {
		t.Fatalf("设备选项数 = %d, want 3", len(opts))
	}
	// 绑定当前模型的设备必须排在首批结果最前，编辑态打开时才能回填已选标签
	if opts[0].ID != lockedDev.ID {
		t.Fatalf("首条应为已绑定当前模型的设备 %s，实际 %s", lockedDev.ID, opts[0].ID)
	}
	byID := make(map[string]int, len(opts))
	for i, opt := range opts {
		byID[opt.ID] = i
	}
	if !opts[byID[lockedDev.ID]].Locked {
		t.Fatalf("lockedDev 应标记 locked")
	}
	if opts[byID[freeDev.ID]].Locked || opts[byID[otherDev.ID]].Locked {
		t.Fatalf("无绑定属性的设备不应 locked")
	}
	if got := opts[byID[lockedDev.ID]].ModelName; got != model.Name {
		t.Fatalf("modelName = %q, want %q", got, model.Name)
	}

	// keyword 同时匹配名称与编码，且在带 modelId 时可以缩小到目标设备
	filtered, err := svc.ListThingModelDeviceOptions(ctx, model.ID, "options-free")
	if err != nil {
		t.Fatalf("search device options: %v", err)
	}
	if len(filtered.Records) != 1 || filtered.Records[0].ID != freeDev.ID {
		t.Fatalf("keyword 搜索结果不符: %+v", filtered.Records)
	}
	byCode, err := svc.ListThingModelDeviceOptions(ctx, "", "options-other")
	if err != nil {
		t.Fatalf("search by code: %v", err)
	}
	if len(byCode.Records) != 1 || byCode.Records[0].ID != otherDev.ID {
		t.Fatalf("按编码搜索结果不符: %+v", byCode.Records)
	}

	// 超过上限：只返回 200 条并置 truncated，提示前端继续输入
	var bulk []po.Device
	for i := 0; i <= thingModelDeviceOptionLimit; i++ {
		name := fmt.Sprintf("bulk-%03d", i)
		bulk = append(bulk, po.Device{
			Code: name, Name: name, ProtocolID: "proto-bind", ProtocolJSON: "{}", Status: 1,
		})
	}
	if err := db.CreateInBatches(&bulk, 50).Error; err != nil {
		t.Fatalf("seed bulk devices: %v", err)
	}
	truncated, err := svc.ListThingModelDeviceOptions(ctx, "", "bulk-")
	if err != nil {
		t.Fatalf("search bulk: %v", err)
	}
	if !truncated.Truncated {
		t.Fatalf("命中 %d 条时应标记 truncated", len(truncated.Records))
	}
	if len(truncated.Records) != thingModelDeviceOptionLimit {
		t.Fatalf("截断后条数 = %d, want %d", len(truncated.Records), thingModelDeviceOptionLimit)
	}

	wantThingModelErrCode(t, func() error {
		_, err := svc.ListThingModelDeviceOptions(ctx, "no-such-model", "")
		return err
	}(), enums.ThingModelNotExistsEnum.GetCode())
}

func TestListThingModelPointOptions(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "m_point_opts", "点位选项模型", "sensor")
	dev := seedBindingDevice(t, db, "point-opts-dev", model.ID)
	prop := seedThingModelProperty(t, svc, model.ID, "temperature")
	bound := seedBindingAddress(t, db, dev.ID, "OP100", "float32", driver.TypeFloat, "R", prop.ID)
	unbound := seedBindingAddress(t, db, dev.ID, "OP101", "string", driver.TypeString, "R", "")

	// 未绑定该模型的设备点位不应出现
	otherModel := seedThingModel(t, svc, "m_point_other", "别的模型", "sensor")
	otherDev := seedBindingDevice(t, db, "point-opts-other", otherModel.ID)
	seedBindingAddress(t, db, otherDev.ID, "OP102", "float32", driver.TypeFloat, "R", "")

	opts, err := svc.ListThingModelPointOptions(ctx, model.ID)
	if err != nil {
		t.Fatalf("list point options: %v", err)
	}
	if len(opts) != 2 {
		t.Fatalf("点位选项数 = %d, want 2: %+v", len(opts), opts)
	}
	byID := make(map[string]int, len(opts))
	for i, opt := range opts {
		byID[opt.AddressID] = i
	}
	boundOpt := opts[byID[bound.ID]]
	if boundOpt.Kind != "float" || boundOpt.PropertyID != prop.ID ||
		boundOpt.PropertyIdentifier != "temperature" || boundOpt.PropertyName != "temperature" {
		t.Fatalf("绑定点位选项不完整: %+v", boundOpt)
	}
	if boundOpt.DeviceName != dev.Name || boundOpt.DeviceCode != dev.Code {
		t.Fatalf("设备信息未回填: %+v", boundOpt)
	}
	unboundOpt := opts[byID[unbound.ID]]
	if unboundOpt.Kind != "string" || unboundOpt.PropertyID != "" {
		t.Fatalf("未绑定点位选项不完整: %+v", unboundOpt)
	}

	wantThingModelErrCode(t, func() error {
		_, err := svc.ListThingModelPointOptions(ctx, "")
		return err
	}(), enums.ParamValidEnum.GetCode())
	wantThingModelErrCode(t, func() error {
		_, err := svc.ListThingModelPointOptions(ctx, "no-such-model")
		return err
	}(), enums.ThingModelNotExistsEnum.GetCode())
}

func TestListThingModelDevices(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "m_bound_devs", "绑定设备列表", "sensor")
	prop := seedThingModelProperty(t, svc, model.ID, "temperature")
	lockedDev := seedBindingDevice(t, db, "bound-dev-locked", model.ID)
	seedBindingAddress(t, db, lockedDev.ID, "BD100", "float32", driver.TypeFloat, "R", prop.ID)
	plainDev := seedBindingDevice(t, db, "bound-dev-plain", model.ID)
	// 别的模型的设备不能混进来
	otherModel := seedThingModel(t, svc, "m_bound_other", "别的模型", "sensor")
	seedBindingDevice(t, db, "bound-dev-other", otherModel.ID)

	rows, err := svc.ListThingModelDevices(ctx, model.ID)
	if err != nil {
		t.Fatalf("list bound devices: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("已绑定设备数 = %d, want 2: %+v", len(rows), rows)
	}
	byID := make(map[string]int, len(rows))
	for i, r := range rows {
		byID[r.ID] = i
	}
	if _, ok := byID[lockedDev.ID]; !ok {
		t.Fatalf("缺少 lockedDev")
	}
	if _, ok := byID[plainDev.ID]; !ok {
		t.Fatalf("缺少 plainDev")
	}
	if got := rows[byID[lockedDev.ID]].ProtocolName; got != drivertest.TestProtocol {
		t.Fatalf("protocolName = %q, want %q", got, drivertest.TestProtocol)
	}

	emptyModel := seedThingModel(t, svc, "m_bound_empty", "空绑定模型", "sensor")
	empty, err := svc.ListThingModelDevices(ctx, emptyModel.ID)
	if err != nil {
		t.Fatalf("list empty model devices: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("空模型应返回空切片, got %+v", empty)
	}

	wantThingModelErrCode(t, func() error {
		_, err := svc.ListThingModelDevices(ctx, "")
		return err
	}(), enums.ParamValidEnum.GetCode())
	wantThingModelErrCode(t, func() error {
		_, err := svc.ListThingModelDevices(ctx, "no-such-model")
		return err
	}(), enums.ThingModelNotExistsEnum.GetCode())
}

func TestListThingModelPropertyPoints(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "m_prop_points", "属性点位模型", "sensor")
	propA := seedThingModelProperty(t, svc, model.ID, "temperature")
	propB := seedThingModelProperty(t, svc, model.ID, "pressure")
	propC := seedThingModelProperty(t, svc, model.ID, "humidity")

	devA := seedBindingDevice(t, db, "prop-points-dev-a", model.ID)
	devB := seedBindingDevice(t, db, "prop-points-dev-b", model.ID)
	addrA1 := seedBindingAddress(t, db, devA.ID, "PP100", "float32", driver.TypeFloat, "R", propA.ID)
	addrA2 := seedBindingAddress(t, db, devB.ID, "PP101", "float32", driver.TypeFloat, "R", propA.ID)
	seedBindingAddress(t, db, devA.ID, "PP102", "float32", driver.TypeFloat, "R", propB.ID)

	rows, err := svc.ListThingModelPropertyPoints(ctx, propA.ID)
	if err != nil {
		t.Fatalf("list property points: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("属性 A 绑定点位数 = %d, want 2: %+v", len(rows), rows)
	}
	byID := make(map[string]int, len(rows))
	for i, r := range rows {
		byID[r.AddressID] = i
	}
	if _, ok := byID[addrA1.ID]; !ok {
		t.Fatalf("缺少 addrA1")
	}
	if _, ok := byID[addrA2.ID]; !ok {
		t.Fatalf("缺少 addrA2")
	}
	if got := rows[byID[addrA1.ID]].DeviceName; got != devA.Name {
		t.Fatalf("deviceName = %q, want %q", got, devA.Name)
	}
	if got := rows[byID[addrA1.ID]].DeviceCode; got != devA.Code {
		t.Fatalf("deviceCode = %q, want %q", got, devA.Code)
	}

	// 属性分页/全量都要带 pointCount
	page, err := svc.PageThingModelProperties(ctx, &dto.PageThingModelPropertyDTO{
		ModelID: model.ID, Page: 1, Size: 50,
	})
	if err != nil {
		t.Fatalf("page properties: %v", err)
	}
	pageCounts := make(map[string]int64, len(page.Records))
	for _, p := range page.Records {
		pageCounts[p.ID] = p.PointCount
	}
	if pageCounts[propA.ID] != 2 || pageCounts[propB.ID] != 1 || pageCounts[propC.ID] != 0 {
		t.Fatalf("pointCount 不符: A=%d B=%d C=%d",
			pageCounts[propA.ID], pageCounts[propB.ID], pageCounts[propC.ID])
	}
	list, err := svc.ListThingModelProperties(ctx, model.ID)
	if err != nil {
		t.Fatalf("list properties: %v", err)
	}
	listCounts := make(map[string]int64, len(list))
	for _, p := range list {
		listCounts[p.ID] = p.PointCount
	}
	if listCounts[propA.ID] != 2 || listCounts[propC.ID] != 0 {
		t.Fatalf("全量 pointCount 不符: A=%d C=%d", listCounts[propA.ID], listCounts[propC.ID])
	}

	// 没有绑定物模型、或属性不存在
	empty, err := svc.ListThingModelPropertyPoints(ctx, propC.ID)
	if err != nil {
		t.Fatalf("list empty property points: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("未绑定点位的属性应返回空切片, got %+v", empty)
	}
	wantThingModelErrCode(t, func() error {
		_, err := svc.ListThingModelPropertyPoints(ctx, "")
		return err
	}(), enums.ParamValidEnum.GetCode())
	wantThingModelErrCode(t, func() error {
		_, err := svc.ListThingModelPropertyPoints(ctx, "no-such-property")
		return err
	}(), enums.ThingModelPropertyNotExistsEnum.GetCode())
}

func TestDeviceAddressRejectsSecondPointForSameProperty(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	ctx := context.Background()

	modelSvc := NewThingModelService(db)
	addressSvc := NewDeviceAddressService(db, nil)
	model := seedThingModel(t, modelSvc, "m_addr_unique", "点位唯一模型", "sensor")
	prop := seedThingModelProperty(t, modelSvc, model.ID, "temperature")
	dev := seedBindingDevice(t, db, "addr-unique-dev", model.ID)

	if err := addressSvc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID: dev.ID, Name: "AU100", Label: "温度一",
		CommonDataType: driver.TypeFloat, RwPermission: "R", ScanFrequency: 1000,
		PropertyID: prop.ID,
	}); err != nil {
		t.Fatalf("bind first address: %v", err)
	}

	// 同一设备第二个点位绑同一属性 → 41021（不是 SQLite UNIQUE 原文）
	wantThingModelErrCode(t, addressSvc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID: dev.ID, Name: "AU101", Label: "温度二",
		CommonDataType: driver.TypeFloat, RwPermission: "R", ScanFrequency: 1000,
		PropertyID: prop.ID,
	}), enums.ThingModelDevicePointDupEnum.GetCode())

	// 先建一个未绑定点位，再编辑绑到同一属性 → 同样 41021
	if err := addressSvc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID: dev.ID, Name: "AU102", Label: "温度三",
		CommonDataType: driver.TypeFloat, RwPermission: "R", ScanFrequency: 1000,
	}); err != nil {
		t.Fatalf("create unbound address: %v", err)
	}
	var second po.DeviceAddress
	if err := db.Where("device_id = ? AND name = ?", dev.ID, "AU102").First(&second).Error; err != nil {
		t.Fatalf("load second address: %v", err)
	}
	wantThingModelErrCode(t, addressSvc.UpdateDeviceAddress(ctx, &dto.UpdateDeviceAddressDTO{
		ID: second.ID, PropertyID: strPtr(prop.ID),
	}), enums.ThingModelDevicePointDupEnum.GetCode())
}
