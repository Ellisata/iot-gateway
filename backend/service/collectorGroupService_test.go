// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
)

func errCode(t *testing.T, err error) string {
	t.Helper()
	var ae *appError.AppError
	if !errors.As(err, &ae) {
		t.Fatalf("error is not *appError.AppError: %v", err)
	}
	return ae.Code
}

// TestCollectorGroupCRUDAndDeleteGuard 覆盖组名/周期校验、重名、删除守卫与解绑后删除。
func TestCollectorGroupCRUDAndDeleteGuard(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	ctx := context.Background()

	if err := db.Create(&po.Device{ID: "dev-1", Name: "dev1", ProtocolID: "proto-1", ProtocolJSON: "{}", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&po.DeviceAddress{ID: "a1", DeviceID: "dev-1", Name: "40001", Label: "温度", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewCollectorGroupService(db)

	if err := svc.CreateCollectorGroup(ctx, &dto.CreateCollectorGroupDTO{
		DeviceID: "dev-1", Code: "fast", Name: "fast", IntervalMs: 500,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 非法编码：格式不对与保留字 legacy。
	for _, code := range []string{"1bad", "a/b", "legacy"} {
		err := svc.CreateCollectorGroup(ctx, &dto.CreateCollectorGroupDTO{
			DeviceID: "dev-1", Code: code, Name: "name-" + code, IntervalMs: 500,
		})
		if got := errCode(t, err); got != enums.CollectorGroupCodeInvalidEnum.GetCode() {
			t.Errorf("code %q error code = %s, want %s", code, got, enums.CollectorGroupCodeInvalidEnum.GetCode())
		}
	}

	// 非法组名：保留字 legacy 与含 / 的名字。
	for _, name := range []string{"legacy", "a/b"} {
		err := svc.CreateCollectorGroup(ctx, &dto.CreateCollectorGroupDTO{
			DeviceID: "dev-1", Code: "name-" + strings.ReplaceAll(name, "/", "-"), Name: name, IntervalMs: 500,
		})
		if got := errCode(t, err); got != enums.CollectorGroupNameInvalidEnum.GetCode() {
			t.Errorf("name %q error code = %s, want %s", name, got, enums.CollectorGroupNameInvalidEnum.GetCode())
		}
	}

	// 同设备重名。
	err := svc.CreateCollectorGroup(ctx, &dto.CreateCollectorGroupDTO{
		DeviceID: "dev-1", Code: "fast-2", Name: "fast", IntervalMs: 500,
	})
	if got := errCode(t, err); got != enums.CollectorGroupExistsEnum.GetCode() {
		t.Errorf("duplicate name code = %s, want %s", got, enums.CollectorGroupExistsEnum.GetCode())
	}

	// 同设备编码重复。
	err = svc.CreateCollectorGroup(ctx, &dto.CreateCollectorGroupDTO{
		DeviceID: "dev-1", Code: "fast", Name: "fast-other", IntervalMs: 500,
	})
	if got := errCode(t, err); got != enums.CollectorGroupCodeExistsEnum.GetCode() {
		t.Errorf("duplicate code = %s, want %s", got, enums.CollectorGroupCodeExistsEnum.GetCode())
	}

	list, err := svc.ListCollectorGroups(ctx, &dto.ListCollectorGroupsDTO{DeviceID: "dev-1"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "fast" || list[0].Code != "fast" || list[0].PointCount != 0 {
		t.Fatalf("list = %+v", list)
	}
	groupID := list[0].ID

	// 编码创建后不可修改。
	err = svc.UpdateCollectorGroup(ctx, &dto.UpdateCollectorGroupDTO{ID: groupID, Code: "fast-renamed"})
	if got := errCode(t, err); got != enums.CollectorGroupCodeImmutableEnum.GetCode() {
		t.Errorf("immutable code = %s, want %s", got, enums.CollectorGroupCodeImmutableEnum.GetCode())
	}

	// 绑定点位。
	addrSvc := NewDeviceAddressService(db, nil)
	assigned, err := addrSvc.BatchAssignCollectorGroup(ctx, "dev-1", groupID, []string{"a1"})
	if err != nil {
		t.Fatalf("batch assign: %v", err)
	}
	if assigned != 1 {
		t.Fatalf("assigned = %d, want 1", assigned)
	}
	// 列表返回的点位数必须反映绑定结果（聚合查询）。
	list, err = svc.ListCollectorGroups(ctx, &dto.ListCollectorGroupsDTO{DeviceID: "dev-1"})
	if err != nil {
		t.Fatalf("list after assign: %v", err)
	}
	if len(list) != 1 || list[0].PointCount != 1 {
		t.Fatalf("point count after assign = %+v, want 1", list)
	}

	// 仍被点位绑定时拒绝删除。
	err = svc.DeleteCollectorGroup(ctx, groupID)
	if got := errCode(t, err); got != enums.CollectorGroupInUseEnum.GetCode() {
		t.Errorf("delete in-use code = %s, want %s", got, enums.CollectorGroupInUseEnum.GetCode())
	}

	// 解绑后可删除。
	if _, err := addrSvc.BatchAssignCollectorGroup(ctx, "dev-1", "", []string{"a1"}); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if err := svc.DeleteCollectorGroup(ctx, groupID); err != nil {
		t.Fatalf("delete after unassign: %v", err)
	}
}

// TestBatchAssignCollectorGroupRejectsCrossDevice 组必须属于点位所在设备。
func TestBatchAssignCollectorGroupRejectsCrossDevice(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	ctx := context.Background()

	for _, id := range []string{"dev-1", "dev-2"} {
		if err := db.Create(&po.Device{ID: id, Name: id, ProtocolID: "proto-1", ProtocolJSON: "{}", Status: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&po.DeviceAddress{ID: "a1", DeviceID: "dev-1", Name: "40001", Label: "温度", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&po.CollectorGroup{ID: "g2", DeviceID: "dev-2", Code: "other", Name: "other", IntervalMs: 500, Status: 1}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewDeviceAddressService(db, nil)
	_, err := svc.BatchAssignCollectorGroup(ctx, "dev-1", "g2", []string{"a1"})
	if got := errCode(t, err); got != enums.CollectorGroupDeviceMismatchEnum.GetCode() {
		t.Errorf("cross-device code = %s, want %s", got, enums.CollectorGroupDeviceMismatchEnum.GetCode())
	}
}
