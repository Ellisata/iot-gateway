// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"testing"

	"iot-gateway/enums"
	"iot-gateway/model/po"
)

// TestDeleteDeviceRejectedWhenGroupSubscribedByPushChannel 设备删除会级联删掉名下
// 采集组，因此任一组被推送通道订阅时单条与批量删除都必须拒绝。
func TestDeleteDeviceRejectedWhenGroupSubscribedByPushChannel(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	ctx := context.Background()
	svc := NewDeviceService(db, nil, nil)

	seedDevices(t, db, "dev-1", "dev-2", "dev-3")
	groups := []po.CollectorGroup{
		{ID: "g1", DeviceID: "dev-1", Code: "fast", Name: "fast", IntervalMs: 500, Status: 1},
		{ID: "g1b", DeviceID: "dev-1", Code: "fast2", Name: "fast2", IntervalMs: 500, Status: 1},
		{ID: "g2", DeviceID: "dev-2", Code: "slow", Name: "slow", IntervalMs: 500, Status: 1},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatalf("seed groups: %v", err)
	}

	// 停用的通道同样算引用：重新启用时配置不会丢。
	if err := db.Create(&po.PushChannel{
		ID:         "ch-1",
		Type:       "mqtt",
		Name:       "ch-1",
		Status:     0,
		ConfigJSON: `{"broker":"127.0.0.1","topic":"t","groupIds":["g1"]}`,
	}).Error; err != nil {
		t.Fatal(err)
	}

	err := svc.DeleteDevice(ctx, "dev-1")
	if got := errCode(t, err); got != enums.DeviceReferencedByPushChannelEnum.GetCode() {
		t.Errorf("delete device code = %s, want %s", got, enums.DeviceReferencedByPushChannelEnum.GetCode())
	}
	if n := countDevices(t, db, "dev-1"); n != 1 {
		t.Fatalf("dev-1 rows after rejected delete = %d, want 1", n)
	}

	// 未被订阅的设备不受影响（组随设备级联删除）。
	if err := svc.DeleteDevice(ctx, "dev-2"); err != nil {
		t.Fatalf("delete unsubscribed device: %v", err)
	}
	var g2 int64
	if err := db.Model(&po.CollectorGroup{}).Where("id = ?", "g2").Count(&g2).Error; err != nil {
		t.Fatal(err)
	}
	if g2 != 0 {
		t.Errorf("dev-2 group rows = %d, want 0 after cascade delete", g2)
	}

	// 批量删除：任意一台被引用就整批拒绝，不做部分删除。
	_, _, err = svc.BatchDeleteDevice(ctx, []string{"dev-1", "dev-3"})
	if got := errCode(t, err); got != enums.DeviceReferencedByPushChannelEnum.GetCode() {
		t.Errorf("batch delete code = %s, want %s", got, enums.DeviceReferencedByPushChannelEnum.GetCode())
	}
	for _, id := range []string{"dev-1", "dev-3"} {
		if n := countDevices(t, db, id); n != 1 {
			t.Errorf("%s rows after rejected batch delete = %d, want 1", id, n)
		}
	}

	// 从通道配置移除订阅后即可删除。
	if err := db.Model(&po.PushChannel{}).Where("id = ?", "ch-1").
		Update("config_json", `{"broker":"127.0.0.1","topic":"t"}`).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteDevice(ctx, "dev-1"); err != nil {
		t.Fatalf("delete after unsubscribing: %v", err)
	}
}
