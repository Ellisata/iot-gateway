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
	"iot-gateway/model/vo"
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
	addr2 := seedBindingAddress(t, db, dev2.ID, "PD101", "float32", driver.TypeFloat, "R", "")

	if err := svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "temperature", Name: "温度",
		Kind: "float", AccessMode: "R",
		AddressIDs: []string{" " + addr1.ID + " "},
	}); err != nil {
		t.Fatalf("create property with point: %v", err)
	}
	var prop po.ThingModelProperty
	if err := db.Where("model_id = ? AND identifier = ?", model.ID, "temperature").First(&prop).Error; err != nil {
		t.Fatalf("load property: %v", err)
	}
	if got := reloadAddress(t, db, addr1.ID).PropertyID; got != prop.ID {
		t.Fatalf("addr1.property_id = %q, want %q", got, prop.ID)
	}
	if got := reloadAddress(t, db, addr2.ID).PropertyID; got != "" {
		t.Fatalf("addr2 不应被绑定, got %q", got)
	}

	// 属性与点位 1 对 1：一次绑两个（跨设备也算）→ 41022
	dup1 := seedBindingAddress(t, db, dev.ID, "PD106", "float32", driver.TypeFloat, "R", "")
	dup2 := seedBindingAddress(t, db, dev2.ID, "PD107", "float32", driver.TypeFloat, "R", "")
	wantThingModelErrCode(t, svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "dup_points", Name: "一次多点",
		Kind: "float", AccessMode: "R", AddressIDs: []string{dup1.ID, dup2.ID},
	}), enums.ThingModelPropertySinglePointEnum.GetCode())

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

	// kind 只是语义说明：string 点位也能绑到 float 属性上（不再 41016）。
	strAddr := seedBindingAddress(t, db, dev.ID, "PD103", "string", driver.TypeString, "R", "")
	if err := svc.CreateThingModelProperty(ctx, &dto.CreateThingModelPropertyDTO{
		ModelID: model.ID, Identifier: "kind_note", Name: "类型仅说明",
		Kind: "float", AccessMode: "R", AddressIDs: []string{strAddr.ID},
	}); err != nil {
		t.Fatalf("bind kind-mismatched point: %v", err)
	}
	var noteProp po.ThingModelProperty
	if err := db.Where("model_id = ? AND identifier = ?", model.ID, "kind_note").First(&noteProp).Error; err != nil {
		t.Fatalf("load note property: %v", err)
	}
	if got := reloadAddress(t, db, strAddr.ID).PropertyID; got != noteProp.ID {
		t.Fatalf("strAddr.property_id = %q, want %q", got, noteProp.ID)
	}

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
		Kind: "float", AccessMode: "R", AddressIDs: []string{addr1.ID},
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
		t.Fatalf("addr2 不应被绑定, got %q", got)
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

	// 1 对 1：同一个属性追加第二个点位（跨设备也算）→ 41022
	dupAddr := seedBindingAddress(t, db, dev2.ID, "PS103", "float32", driver.TypeFloat, "R", "")
	dupAdd := []string{addr1.ID, dupAddr.ID}
	wantThingModelErrCode(t, svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "温度",
		Kind: "float", AccessMode: "R", AddressIDs: &dupAdd,
	}), enums.ThingModelPropertySinglePointEnum.GetCode())

	// 换绑到另一台设备的一个点位：先解绑旧点、再绑新点，允许
	replace := []string{addr2.ID}
	if err := svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "温度",
		Kind: "float", AccessMode: "R", AddressIDs: &replace,
	}); err != nil {
		t.Fatalf("replace binding with another device point: %v", err)
	}
	if got := reloadAddress(t, db, addr1.ID).PropertyID; got != "" {
		t.Fatalf("换绑后 addr1 应被解绑, got %q", got)
	}
	if got := reloadAddress(t, db, addr2.ID).PropertyID; got != prop.ID {
		t.Fatalf("换绑后 addr2 应绑定到属性, got %q", got)
	}
	// 换回 addr1，后续 41018/41019 断言仍以 addr1 为保留绑定
	if err := svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "温度",
		Kind: "float", AccessMode: "R", AddressIDs: &rebind,
	}); err != nil {
		t.Fatalf("rebind addr1: %v", err)
	}

	// 保留一条绑定时：kind 只作说明可改，改 access_mode → 41018
	if err := svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "温度",
		Kind: "string", AccessMode: "R", AddressIDs: &rebind,
	}); err != nil {
		t.Fatalf("change bound property kind: %v", err)
	}
	wantThingModelErrCode(t, svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "温度",
		Kind: "string", AccessMode: "RW", AddressIDs: &rebind,
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

	// 停用模型换绑到新点位 → 41012（1 对 1 下是替换而非追加）
	newAddr := seedBindingAddress(t, db, dev2.ID, "PS102", "float32", driver.TypeFloat, "R", "")
	add := []string{newAddr.ID}
	wantThingModelErrCode(t, svc.UpdateThingModelProperty(ctx, &dto.UpdateThingModelPropertyDTO{
		ID: prop.ID, Identifier: prop.Identifier, Name: "停用后换绑",
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
	// 列表页展示「设备名称.点位名称」：取抽屉同排序下的第一条；未绑定为空。
	pageByProp := make(map[string]vo.ThingModelPropertyVO, len(page.Records))
	for _, p := range page.Records {
		pageByProp[p.ID] = p
	}
	if got := pageByProp[propA.ID]; got.PointDeviceName != devA.Name || got.PointName != "PP100" {
		t.Fatalf("属性 A 绑定点位展示 = %q.%q, want %q.PP100",
			got.PointDeviceName, got.PointName, devA.Name)
	}
	if got := pageByProp[propB.ID]; got.PointDeviceName != devA.Name || got.PointName != "PP102" {
		t.Fatalf("属性 B 绑定点位展示 = %q.%q, want %q.PP102",
			got.PointDeviceName, got.PointName, devA.Name)
	}
	if got := pageByProp[propC.ID]; got.PointDeviceName != "" || got.PointName != "" {
		t.Fatalf("属性 C 未绑定，展示字段应为空: %+v", got)
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

func TestDeviceAddressRejectsSecondPointForProperty(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	ctx := context.Background()

	modelSvc := NewThingModelService(db)
	addressSvc := NewDeviceAddressService(db, nil)
	model := seedThingModel(t, modelSvc, "m_addr_unique", "点位唯一模型", "sensor")
	prop := seedThingModelProperty(t, modelSvc, model.ID, "temperature")
	dev := seedBindingDevice(t, db, "addr-unique-dev")
	dev2 := seedBindingDevice(t, db, "addr-unique-dev-2")

	if err := addressSvc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID: dev.ID, Name: "AU100", Label: "温度一",
		CommonDataType: driver.TypeFloat, RwPermission: "R", ScanFrequency: 1000,
		PropertyID: prop.ID,
	}); err != nil {
		t.Fatalf("bind first address: %v", err)
	}

	// 同一设备第二个点位绑同一属性 → 41022（不是 SQLite UNIQUE 原文）
	wantThingModelErrCode(t, addressSvc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID: dev.ID, Name: "AU101", Label: "温度二",
		CommonDataType: driver.TypeFloat, RwPermission: "R", ScanFrequency: 1000,
		PropertyID: prop.ID,
	}), enums.ThingModelPropertySinglePointEnum.GetCode())

	// 另一台设备的点位绑同一属性同样被拒：属性与点位是 1 对 1
	wantThingModelErrCode(t, addressSvc.CreateDeviceAddress(ctx, &dto.CreateDeviceAddressDTO{
		DeviceID: dev2.ID, Name: "AU104", Label: "温度四",
		CommonDataType: driver.TypeFloat, RwPermission: "R", ScanFrequency: 1000,
		PropertyID: prop.ID,
	}), enums.ThingModelPropertySinglePointEnum.GetCode())

	// 先建一个未绑定点位，再编辑绑到同一属性 → 同样 41022
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
	}), enums.ThingModelPropertySinglePointEnum.GetCode())
}

func TestPageThingModelPointDevices(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	svc := NewThingModelService(db)
	ctx := context.Background()

	devA := seedBindingDevice(t, db, "alarm-dev-a")
	devB := seedBindingDevice(t, db, "alarm-dev-b")
	devEmpty := seedBindingDevice(t, db, "alarm-dev-empty")
	seedBindingAddress(t, db, devA.ID, "PD200", "float32", driver.TypeFloat, "R", "")
	seedBindingAddress(t, db, devA.ID, "PD201", "float32", driver.TypeFloat, "R", "")
	seedBindingAddress(t, db, devB.ID, "PD202", "float32", driver.TypeFloat, "R", "")
	_ = devEmpty // 没有点位的设备不出现在候选里

	page, err := svc.PageThingModelPointDevices(ctx, &dto.PageThingModelPointDeviceDTO{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("page point devices: %v", err)
	}
	if page.Total != 2 || len(page.Records) != 2 {
		t.Fatalf("设备候选 = total %d / %d 条, want 2/2: %+v", page.Total, len(page.Records), page.Records)
	}
	counts := make(map[string]int64, len(page.Records))
	for _, r := range page.Records {
		counts[r.ID] = r.PointCount
	}
	if counts[devA.ID] != 2 || counts[devB.ID] != 1 {
		t.Fatalf("pointCount 不符: A=%d B=%d", counts[devA.ID], counts[devB.ID])
	}
	if _, ok := counts[devEmpty.ID]; ok {
		t.Fatalf("没有点位的设备不应出现在候选中")
	}

	// 关键词同时匹配名称与编码
	filtered, err := svc.PageThingModelPointDevices(ctx, &dto.PageThingModelPointDeviceDTO{
		Page: 1, Size: 10, Keyword: "alarm-dev-b",
	})
	if err != nil {
		t.Fatalf("page point devices with keyword: %v", err)
	}
	if filtered.Total != 1 || len(filtered.Records) != 1 || filtered.Records[0].ID != devB.ID {
		t.Fatalf("关键词过滤结果不符: %+v", filtered.Records)
	}

	// 分页：按名称排序，第一页是 alarm-dev-a
	first, err := svc.PageThingModelPointDevices(ctx, &dto.PageThingModelPointDeviceDTO{Page: 1, Size: 1})
	if err != nil {
		t.Fatalf("page point devices size 1: %v", err)
	}
	if first.Total != 2 || len(first.Records) != 1 || first.Records[0].ID != devA.ID {
		t.Fatalf("第一页应为 devA: %+v", first.Records)
	}
}

func TestPageThingModelPointOptions(t *testing.T) {
	db := newThingModelTestDB(t)
	seedBindingProtocol(t, db)
	svc := NewThingModelService(db)
	ctx := context.Background()

	model := seedThingModel(t, svc, "m_point_page", "点位分页模型", "sensor")
	prop := seedThingModelProperty(t, svc, model.ID, "temperature")
	dev := seedBindingDevice(t, db, "point-page-dev")
	other := seedBindingDevice(t, db, "point-page-other")
	bound := seedBindingAddress(t, db, dev.ID, "PAGE100", "float32", driver.TypeFloat, "R", prop.ID)
	unbound := seedBindingAddress(t, db, dev.ID, "PAGE101", "string", driver.TypeString, "R", "")
	pressure := seedBindingAddress(t, db, dev.ID, "PAGE102", "float32", driver.TypeFloat, "R", "")
	if err := db.Model(&po.DeviceAddress{}).Where("id = ?", pressure.ID).
		Update("label", "出口压力").Error; err != nil {
		t.Fatalf("relabel address: %v", err)
	}
	seedBindingAddress(t, db, other.ID, "PAGE200", "float32", driver.TypeFloat, "R", "")

	page, err := svc.PageThingModelPointOptions(ctx, &dto.PageThingModelPointOptionDTO{
		DeviceID: dev.ID, Page: 1, Size: 2,
	})
	if err != nil {
		t.Fatalf("page point options: %v", err)
	}
	if page.Total != 3 || len(page.Records) != 2 {
		t.Fatalf("点位候选 = total %d / %d 条, want 3/2", page.Total, len(page.Records))
	}
	for _, r := range page.Records {
		if r.DeviceID != dev.ID {
			t.Fatalf("应只返回所选设备的点位: %+v", r)
		}
	}

	// 名称/说明模糊匹配；[PAGE102] 的 label 是「出口压力」
	byKeyword, err := svc.PageThingModelPointOptions(ctx, &dto.PageThingModelPointOptionDTO{
		DeviceID: dev.ID, Page: 1, Size: 10, Keyword: "压力",
	})
	if err != nil {
		t.Fatalf("page point options with keyword: %v", err)
	}
	if byKeyword.Total != 1 || len(byKeyword.Records) != 1 ||
		byKeyword.Records[0].AddressID != pressure.ID {
		t.Fatalf("关键词过滤结果不符: %+v", byKeyword.Records)
	}

	// 绑定信息与 kind 仍要回填（与全量接口同一份映射）
	all, err := svc.PageThingModelPointOptions(ctx, &dto.PageThingModelPointOptionDTO{
		DeviceID: dev.ID, Page: 1, Size: 10,
	})
	if err != nil {
		t.Fatalf("page all point options: %v", err)
	}
	byID := make(map[string]vo.ThingModelPointOptionVO, len(all.Records))
	for _, r := range all.Records {
		byID[r.AddressID] = r
	}
	if got := byID[bound.ID]; got.Kind != "float" || got.PropertyID != prop.ID ||
		got.PropertyName != "temperature" || got.ModelName != model.Name {
		t.Fatalf("绑定点位信息未回填: %+v", got)
	}
	if got := byID[unbound.ID]; got.Kind != "string" || got.PropertyID != "" {
		t.Fatalf("未绑定点位信息不符: %+v", got)
	}

	// deviceId 必填
	wantThingModelErrCode(t, func() error {
		_, err := svc.PageThingModelPointOptions(ctx, &dto.PageThingModelPointOptionDTO{Page: 1, Size: 10})
		return err
	}(), enums.ParamValidEnum.GetCode())
}
