// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/po"
)

func seedTriggerRule(t *testing.T, db *gorm.DB, rule po.TriggerRule) {
	t.Helper()
	if rule.Name == "" {
		rule.Name = "规则"
	}
	if rule.ActionAddressIDs == "" {
		rule.ActionAddressIDs = "[]"
	}
	if rule.ConditionsJSON == "" {
		rule.ConditionsJSON = `{"groups":[]}`
	}
	if rule.TargetChannelID == "" {
		rule.TargetChannelID = "chan-1"
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatalf("seed trigger rule: %v", err)
	}
}

func assertDeviceReferencedByTriggerRule(t *testing.T, err error) {
	t.Helper()
	var appErr *appError.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("错误类型 = %T (%v), want *appError.AppError", err, err)
	}
	if want := enums.DeviceReferencedByTriggerRuleEnum.GetCode(); appErr.Code != want {
		t.Fatalf("错误码 = %s, want %s (%v)", appErr.Code, want, err)
	}
}

func assertDeviceAddressReferencedByTriggerRule(t *testing.T, err error) {
	t.Helper()
	var appErr *appError.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("错误类型 = %T (%v), want *appError.AppError", err, err)
	}
	if want := enums.DeviceAddressReferencedByTriggerRuleEnum.GetCode(); appErr.Code != want {
		t.Fatalf("错误码 = %s, want %s (%v)", appErr.Code, want, err)
	}
}

func conditionReferencing(addressID string) string {
	return fmt.Sprintf(`{"groups":[{"conditions":[{"addressId":%q,"op":"gt","values":["1"]}]}]}`, addressID)
}

// TestDeleteDeviceRejectedWhenReferencedByTriggerRule 覆盖三种引用方式与启停两种状态：
// 条件点位、动作点位、动作设备本身都算引用；停用的规则同样拦（重新启用时引用必须完整）。
func TestDeleteDeviceRejectedWhenReferencedByTriggerRule(t *testing.T) {
	cases := []struct {
		name string
		rule po.TriggerRule
	}{
		{
			name: "条件点位",
			rule: po.TriggerRule{
				ID: "rule-cond", ActionDeviceID: "dev-2",
				ConditionsJSON: conditionReferencing("a1"),
			},
		},
		{
			name: "动作点位",
			rule: po.TriggerRule{
				ID: "rule-action-addr", ActionDeviceID: "dev-2",
				ActionAddressIDs: `["a1"]`,
			},
		},
		{
			name: "动作设备",
			rule: po.TriggerRule{
				ID: "rule-action-dev", ActionDeviceID: "dev-1", Status: 0,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newDeviceAddressImportTestDB(t)
			svc := NewDeviceService(db, nil, nil)
			ctx := context.Background()

			seedDevices(t, db, "dev-1", "dev-2")
			if err := db.Create(&[]po.DeviceAddress{
				batchDeleteAddr("a1", "dev-1", "D100"),
				batchDeleteAddr("b1", "dev-2", "D200"),
			}).Error; err != nil {
				t.Fatalf("seed addresses: %v", err)
			}
			seedTriggerRule(t, db, tc.rule)

			assertDeviceReferencedByTriggerRule(t, svc.DeleteDevice(ctx, "dev-1"))

			if n := countDevices(t, db, "dev-1"); n != 1 {
				t.Errorf("被引用时 dev-1 剩余 %d 行, want 1（必须拒绝删除）", n)
			}
			if n := countAddresses(t, db, "dev-1"); n != 1 {
				t.Errorf("被引用时 dev-1 剩余 %d 个点位, want 1（必须拒绝删除）", n)
			}

			// 删掉引用它的规则后即可删除：拦截的是引用状态，不是设备本身。
			if err := db.Where("id = ?", tc.rule.ID).Delete(&po.TriggerRule{}).Error; err != nil {
				t.Fatalf("delete rule: %v", err)
			}
			if err := svc.DeleteDevice(ctx, "dev-1"); err != nil {
				t.Fatalf("解除引用后 DeleteDevice: %v", err)
			}
			if n := countDevices(t, db, "dev-1"); n != 0 {
				t.Errorf("解除引用后 dev-1 剩余 %d 行, want 0", n)
			}
		})
	}
}

// TestDeleteDeviceAllowedWhenRuleReferencesOtherDevice 未被引用的设备照常删除，
// 不能因为库里有别的规则就把删除全部拦下。
func TestDeleteDeviceAllowedWhenRuleReferencesOtherDevice(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	svc := NewDeviceService(db, nil, nil)
	ctx := context.Background()

	seedDevices(t, db, "dev-1", "dev-2")
	if err := db.Create(&[]po.DeviceAddress{
		batchDeleteAddr("a1", "dev-1", "D100"),
		batchDeleteAddr("b1", "dev-2", "D200"),
	}).Error; err != nil {
		t.Fatalf("seed addresses: %v", err)
	}
	seedTriggerRule(t, db, po.TriggerRule{
		ID: "rule-other", ActionDeviceID: "dev-2",
		ActionAddressIDs: `["b1"]`,
		ConditionsJSON:   conditionReferencing("b1"),
	})

	if err := svc.DeleteDevice(ctx, "dev-1"); err != nil {
		t.Fatalf("DeleteDevice 未引用设备: %v", err)
	}
	if n := countAddresses(t, db, "dev-1"); n != 0 {
		t.Errorf("dev-1 剩余 %d 个点位, want 0", n)
	}
	if n := countDevices(t, db, "dev-2"); n != 1 {
		t.Errorf("dev-2 剩余 %d 行, want 1（不得被误删）", n)
	}
}

// TestBatchDeleteDeviceRejectedWhollyWhenAnyReferenced 批量删除只要有一台被引用就整批失败：
// 不做"删一半"的部分成功，库里的状态必须是一次都没动。
func TestBatchDeleteDeviceRejectedWhollyWhenAnyReferenced(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	svc := NewDeviceService(db, nil, nil)
	ctx := context.Background()

	seedDevices(t, db, "dev-1", "dev-2")
	if err := db.Create(&[]po.DeviceAddress{
		batchDeleteAddr("a1", "dev-1", "D100"),
		batchDeleteAddr("b1", "dev-2", "D200"),
	}).Error; err != nil {
		t.Fatalf("seed addresses: %v", err)
	}
	seedTriggerRule(t, db, po.TriggerRule{
		ID: "rule-1", ActionDeviceID: "dev-2",
		ConditionsJSON: conditionReferencing("a1"),
	})

	_, _, err := svc.BatchDeleteDevice(ctx, []string{"dev-1", "dev-2"})
	assertDeviceReferencedByTriggerRule(t, err)

	for _, id := range []string{"dev-1", "dev-2"} {
		if n := countDevices(t, db, id); n != 1 {
			t.Errorf("%s 剩余 %d 行, want 1（整批拒绝，一台都不该删）", id, n)
		}
		if n := countAddresses(t, db, id); n != 1 {
			t.Errorf("%s 剩余 %d 个点位, want 1（整批拒绝，点位不得被删）", id, n)
		}
	}
}

// TestDeleteDeviceAddressRejectedWhenReferencedByTriggerRule 单条点位删除：
// 条件点位与动作点位都算引用，拒绝后点位必须还在；删掉规则即可删除。
func TestDeleteDeviceAddressRejectedWhenReferencedByTriggerRule(t *testing.T) {
	cases := []struct {
		name string
		rule po.TriggerRule
	}{
		{
			name: "条件点位",
			rule: po.TriggerRule{
				ID: "rule-cond", ActionDeviceID: "dev-2",
				ConditionsJSON: conditionReferencing("a1"),
			},
		},
		{
			name: "动作点位",
			rule: po.TriggerRule{
				ID: "rule-action-addr", ActionDeviceID: "dev-1",
				ActionAddressIDs: `["a1"]`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newDeviceAddressImportTestDB(t)
			svc := NewDeviceAddressService(db, nil)
			ctx := context.Background()

			seedDevices(t, db, "dev-1", "dev-2")
			if err := db.Create(&[]po.DeviceAddress{
				batchDeleteAddr("a1", "dev-1", "D100"),
				batchDeleteAddr("b1", "dev-2", "D200"),
			}).Error; err != nil {
				t.Fatalf("seed addresses: %v", err)
			}
			seedTriggerRule(t, db, tc.rule)

			assertDeviceAddressReferencedByTriggerRule(t, svc.DeleteDeviceAddress(ctx, "a1"))

			if n := countAddresses(t, db, "dev-1"); n != 1 {
				t.Errorf("被引用时 dev-1 剩余 %d 个点位, want 1（必须拒绝删除）", n)
			}

			// 删掉引用它的规则后即可删除。
			if err := db.Where("id = ?", tc.rule.ID).Delete(&po.TriggerRule{}).Error; err != nil {
				t.Fatalf("delete rule: %v", err)
			}
			if err := svc.DeleteDeviceAddress(ctx, "a1"); err != nil {
				t.Fatalf("解除引用后 DeleteDeviceAddress: %v", err)
			}
			if n := countAddresses(t, db, "dev-1"); n != 0 {
				t.Errorf("解除引用后 dev-1 剩余 %d 个点位, want 0", n)
			}
		})
	}
}

// TestBatchDeleteDeviceAddressRejectedWhollyWhenAnyReferenced 批量删点位只要有一台被引用
// 就整批失败，其余点位一个都不删。
func TestBatchDeleteDeviceAddressRejectedWhollyWhenAnyReferenced(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	svc := NewDeviceAddressService(db, nil)
	ctx := context.Background()

	seedDevices(t, db, "dev-1")
	if err := db.Create(&[]po.DeviceAddress{
		batchDeleteAddr("a1", "dev-1", "D100"),
		batchDeleteAddr("a2", "dev-1", "D101"),
	}).Error; err != nil {
		t.Fatalf("seed addresses: %v", err)
	}
	seedTriggerRule(t, db, po.TriggerRule{
		ID: "rule-1", ActionDeviceID: "dev-1",
		ConditionsJSON: conditionReferencing("a1"),
	})

	_, err := svc.BatchDeleteDeviceAddress(ctx, "dev-1", []string{"a1", "a2"})
	assertDeviceAddressReferencedByTriggerRule(t, err)

	if n := countAddresses(t, db, "dev-1"); n != 2 {
		t.Errorf("整批拒绝后 dev-1 剩余 %d 个点位, want 2（一个都不该删）", n)
	}
}

// TestBatchDeleteDeviceAddressIgnoresOtherDevicesReferences 传进来但不属于该设备的 id
// 不参与引用检查：它们本来就不会被删，不能因此拦下这次合法删除。
func TestBatchDeleteDeviceAddressIgnoresOtherDevicesReferences(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	svc := NewDeviceAddressService(db, nil)
	ctx := context.Background()

	seedDevices(t, db, "dev-1", "dev-2")
	if err := db.Create(&[]po.DeviceAddress{
		batchDeleteAddr("a1", "dev-1", "D100"),
		batchDeleteAddr("b1", "dev-2", "D200"),
	}).Error; err != nil {
		t.Fatalf("seed addresses: %v", err)
	}
	seedTriggerRule(t, db, po.TriggerRule{
		ID: "rule-1", ActionDeviceID: "dev-2",
		ConditionsJSON: conditionReferencing("b1"),
	})

	deleted, err := svc.BatchDeleteDeviceAddress(ctx, "dev-1", []string{"a1", "b1"})
	if err != nil {
		t.Fatalf("BatchDeleteDeviceAddress: %v", err)
	}
	if deleted != 1 {
		t.Errorf("删除点位数 = %d, want 1（只有 a1 属于 dev-1）", deleted)
	}
	if n := countAddresses(t, db, "dev-2"); n != 1 {
		t.Errorf("dev-2 剩余 %d 个点位, want 1（b1 不得被误删）", n)
	}
}
