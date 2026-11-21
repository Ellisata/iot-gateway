// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
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

func seedBindingDevice(t *testing.T, db *gorm.DB, name string) po.Device {
	t.Helper()
	dev := po.Device{
		Code: name, Name: name, ProtocolID: "proto-bind",
		ProtocolJSON: "{}", Status: 1,
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

func reloadAddress(t *testing.T, db *gorm.DB, id string) po.DeviceAddress {
	t.Helper()
	var addr po.DeviceAddress
	if err := db.Where("id = ?", id).First(&addr).Error; err != nil {
		t.Fatalf("reload address %s: %v", id, err)
	}
	return addr
}

func TestCreateThingModelPropertyBindsAddresses(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "m_points", "点位模型", "sensor")
	dev := seedBindingDevice(t, db, "points-dev")
	dev2 := seedBindingDevice(t, db, "points-dev-2")
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

	// 其他设备的点位也能绑定（设备不绑定模型，跨模型不再是错误）
	otherDev := seedBindingDevice(t, db, "points-other-dev")
	crossAddr := seedBindingAddress(t, db, otherDev.ID, "PD102", "float32", driver.TypeFloat, "R", "")
	if err := svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "cross", Name: "跨模型",
		Kind: "float", AccessMode: "R", AddressIDs: []string{crossAddr.ID},
	}); err != nil {
		t.Fatalf("bind point from another device: %v", err)
	}
	var crossProp po.ThingModelProperty
	if err := db.Where("model_id = ? AND identifier = ?", model.ID, "cross").First(&crossProp).Error; err != nil {
		t.Fatalf("load cross property: %v", err)
	}
	if got := reloadAddress(t, db, crossAddr.ID).PropertyID; got != crossProp.ID {
		t.Fatalf("crossAddr.property_id = %q, want %q", got, crossProp.ID)
	}

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
	dev := seedBindingDevice(t, db, "prop-sync-dev")
	dev2 := seedBindingDevice(t, db, "prop-sync-dev-2")
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

func TestListThingModelPointOptions(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "m_point_opts", "点位选项模型", "sensor")
	dev := seedBindingDevice(t, db, "point-opts-dev")
	prop := seedThingModelProperty(t, svc, model.ID, "temperature")
	bound := seedBindingAddress(t, db, dev.ID, "OP100", "float32", driver.TypeFloat, "R", prop.ID)
	unbound := seedBindingAddress(t, db, dev.ID, "OP101", "string", driver.TypeString, "R", "")

	// 其他设备的点位同样出现：属性与设备没有绑定关系。
	otherDev := seedBindingDevice(t, db, "point-opts-other")
	otherAddr := seedBindingAddress(t, db, otherDev.ID, "OP102", "float32", driver.TypeFloat, "R", "")

	opts, err := svc.ListThingModelPointOptions(ctx)
	if err != nil {
		t.Fatalf("list point options: %v", err)
	}
	if len(opts) != 3 {
		t.Fatalf("点位选项数 = %d, want 3: %+v", len(opts), opts)
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
	// 「已绑定属性」要带模型名：属性标识只在模型内唯一，同名属性可能跨模型。
	if boundOpt.ModelID != model.ID || boundOpt.ModelName != model.Name {
		t.Fatalf("绑定属性的模型信息未回填: %+v", boundOpt)
	}
	if unboundOpt := opts[byID[unbound.ID]]; unboundOpt.Kind != "string" || unboundOpt.PropertyID != "" {
		t.Fatalf("未绑定点位选项不完整: %+v", unboundOpt)
	}
	if otherOpt := opts[byID[otherAddr.ID]]; otherOpt.DeviceID != otherDev.ID {
		t.Fatalf("其他设备点位缺失: %+v", otherOpt)
	}
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

	devA := seedBindingDevice(t, db, "prop-points-dev-a")
	devB := seedBindingDevice(t, db, "prop-points-dev-b")
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
	dev := seedBindingDevice(t, db, "addr-unique-dev")

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
