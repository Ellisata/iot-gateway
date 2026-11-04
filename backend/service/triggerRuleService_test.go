// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/testutil/drivertest"
	"iot-gateway/triggerrule"
)

type fakeTriggerReloader struct {
	calls  int
	events []triggerrule.RecentTrigger
}

func (f *fakeTriggerReloader) Refresh() error {
	f.calls++
	return nil
}

func (f *fakeTriggerReloader) RecentTriggers(ruleID string, limit int) []triggerrule.RecentTrigger {
	out := make([]triggerrule.RecentTrigger, 0, len(f.events))
	for _, ev := range f.events {
		if ruleID != "" && ev.RuleID != ruleID {
			continue
		}
		out = append(out, ev)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func newTriggerRuleTestDB(t *testing.T) (*gorm.DB, *fakeTriggerReloader) {
	t.Helper()
	drivertest.EnsureSyntheticProtocol()
	db := newDeviceAddressImportTestDB(t)
	if err := db.AutoMigrate(&po.PushChannel{}, &po.ThingModelProperty{},
		&po.TriggerRule{}, &po.TriggerRuleRecord{}); err != nil {
		t.Fatalf("migrate trigger models: %v", err)
	}

	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: drivertest.TestProtocol}).Error; err != nil {
		t.Fatal(err)
	}
	for _, d := range []po.Device{
		{ID: "dev-1", Name: "dev1", ProtocolID: "proto-1", ProtocolJSON: "{}", Status: 1},
		{ID: "dev-2", Name: "dev2", ProtocolID: "proto-1", ProtocolJSON: "{}", Status: 1},
	} {
		if err := db.Create(&d).Error; err != nil {
			t.Fatal(err)
		}
	}
	addrs := []po.DeviceAddress{
		{ID: "a-float", DeviceID: "dev-1", Name: "F1", Label: "温度", DataType: "float32",
			CommonDataType: "Float", RwPermission: "R", ScanFrequency: 1000, Status: 1},
		{ID: "a-str", DeviceID: "dev-1", Name: "S1", Label: "状态", DataType: "string",
			CommonDataType: "String", RwPermission: "R", ScanFrequency: 1000, Status: 1},
		{ID: "a-unknown", DeviceID: "dev-1", Name: "U1", Label: "未知", DataType: "weird",
			CommonDataType: "Weird", RwPermission: "R", ScanFrequency: 1000, Status: 1},
		{ID: "a-action", DeviceID: "dev-1", Name: "A1", Label: "批次", DataType: "float32",
			CommonDataType: "Float", RwPermission: "R", ScanFrequency: 1000, Status: 1},
		{ID: "a-write", DeviceID: "dev-1", Name: "W1", Label: "只写", DataType: "float32",
			CommonDataType: "Float", RwPermission: "W", ScanFrequency: 1000, Status: 1},
		{ID: "a-other", DeviceID: "dev-2", Name: "O1", Label: "别的设备", DataType: "float32",
			CommonDataType: "Float", RwPermission: "R", ScanFrequency: 1000, Status: 1},
	}
	for _, a := range addrs {
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []po.PushChannel{
		{ID: "c1", Type: "mqtt", Name: "ch1", ConfigJSON: "{}", Status: 1},
		{ID: "c2", Type: "mqtt", Name: "ch2", ConfigJSON: "{}", Status: 1},
	} {
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	// GORM 的 default:1 会把显式 0 当成未设置，停用要单独 update。
	if err := db.Model(&po.PushChannel{}).Where("id = ?", "c2").Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}

	return db, &fakeTriggerReloader{}
}

func validTriggerRuleDTO() *dto.CreateTriggerRuleDTO {
	return &dto.CreateTriggerRuleDTO{TriggerRuleConfigDTO: dto.TriggerRuleConfigDTO{
		Code:             "rule-a",
		Name:             "rule-A",
		ActionDeviceID:   "dev-1",
		ActionAddressIDs: []string{"a-action"},
		TargetChannelID:  "c1",
		ConditionsJSON: json.RawMessage(`{"groups":[{"conditions":[
			{"addressId":"a-float","op":"gt","values":["10"]},
			{"addressId":"a-str","op":"in","values":["RUN","READY"]}
		]}]}`),
		CooldownMs: 1000,
		TimeoutMs:  5000,
	}}
}

func TestCreateTriggerRuleValid(t *testing.T) {
	db, reloader := newTriggerRuleTestDB(t)
	svc := NewTriggerRuleService(db, reloader, reloader)
	ctx := context.Background()

	if err := svc.CreateTriggerRule(ctx, validTriggerRuleDTO()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if reloader.calls != 1 {
		t.Errorf("reload calls = %d，期望 1", reloader.calls)
	}

	var rule po.TriggerRule
	if err := db.Where("name = ?", "rule-A").First(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if rule.Status != 1 || rule.Code != "rule-a" ||
		rule.ActionAddressIDs != `["a-action"]` || rule.TimeoutMs != 5000 {
		t.Fatalf("落库字段 = %+v", rule)
	}

	got, err := svc.GetTriggerRuleByID(ctx, rule.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ActionDeviceName != "dev1" || got.TargetChannelName != "ch1" || got.ConditionCount != 2 {
		t.Fatalf("VO 补全 = %+v", got)
	}
	if len(got.ConditionAddresses) != 2 || got.ConditionAddresses[0].Kind != "float" {
		t.Fatalf("条件点位/ Kind = %+v", got.ConditionAddresses)
	}
}

func TestCreateTriggerRuleDisabledKeepsStatusZero(t *testing.T) {
	db, reloader := newTriggerRuleTestDB(t)
	svc := NewTriggerRuleService(db, reloader, reloader)

	req := validTriggerRuleDTO()
	req.Name = "disabled-rule"
	zero := 0
	req.Status = &zero
	if err := svc.CreateTriggerRule(context.Background(), req); err != nil {
		t.Fatalf("create: %v", err)
	}
	var rule po.TriggerRule
	if err := db.Where("name = ?", "disabled-rule").First(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if rule.Status != 0 {
		t.Fatalf("status = %d，期望 0", rule.Status)
	}
}

func TestCreateTriggerRuleValidation(t *testing.T) {
	db, reloader := newTriggerRuleTestDB(t)
	svc := NewTriggerRuleService(db, reloader, reloader)
	ctx := context.Background()

	tests := []struct {
		name   string
		mutate func(*dto.CreateTriggerRuleDTO)
		want   string
	}{
		{"重名", func(r *dto.CreateTriggerRuleDTO) { r.Name = "dup" }, ""},
		{"编码缺失", func(r *dto.CreateTriggerRuleDTO) { r.Code = "" },
			enums.TriggerRuleCodeInvalidEnum.GetCode()},
		{"编码非法", func(r *dto.CreateTriggerRuleDTO) { r.Code = "1bad" },
			enums.TriggerRuleCodeInvalidEnum.GetCode()},
		{"编码重复", func(r *dto.CreateTriggerRuleDTO) { r.Code = "code-dup" },
			enums.TriggerRuleCodeExistsEnum.GetCode()},
		{"动作点位不存在", func(r *dto.CreateTriggerRuleDTO) { r.ActionAddressIDs = []string{"missing"} },
			enums.TriggerRuleAddressNotFoundEnum.GetCode()},
		{"动作点位跨设备", func(r *dto.CreateTriggerRuleDTO) { r.ActionAddressIDs = []string{"a-other"} },
			enums.TriggerRuleActionDeviceMismatchEnum.GetCode()},
		{"动作点位只写", func(r *dto.CreateTriggerRuleDTO) { r.ActionAddressIDs = []string{"a-write"} },
			enums.TriggerRuleActionAddressNotReadableEnum.GetCode()},
		{"通道不存在", func(r *dto.CreateTriggerRuleDTO) { r.TargetChannelID = "missing" },
			enums.TriggerRuleChannelNotExistsEnum.GetCode()},
		{"通道停用", func(r *dto.CreateTriggerRuleDTO) { r.TargetChannelID = "c2" },
			enums.TriggerRuleChannelNotExistsEnum.GetCode()},
		{"设备不存在", func(r *dto.CreateTriggerRuleDTO) { r.ActionDeviceID = "missing" },
			enums.TriggerRuleDeviceNotExistsEnum.GetCode()},
		{"条件点位不存在", func(r *dto.CreateTriggerRuleDTO) {
			r.ConditionsJSON = json.RawMessage(`{"groups":[{"conditions":[{"addressId":"missing","op":"gt","values":["1"]}]}]}`)
		}, enums.TriggerRuleAddressNotFoundEnum.GetCode()},
		{"未知 Kind", func(r *dto.CreateTriggerRuleDTO) {
			r.ConditionsJSON = json.RawMessage(`{"groups":[{"conditions":[{"addressId":"a-unknown","op":"gt","values":["1"]}]}]}`)
		}, enums.TriggerRuleConditionInvalidEnum.GetCode()},
		{"算子与类型不匹配", func(r *dto.CreateTriggerRuleDTO) {
			r.ConditionsJSON = json.RawMessage(`{"groups":[{"conditions":[{"addressId":"a-str","op":"gt","values":["1"]}]}]}`)
		}, enums.ParamValidEnum.GetCode()},
	}

	// 先建一条同名规则，供"重名"用例使用。
	dup := validTriggerRuleDTO()
	dup.Name = "dup"
	dup.Code = "code-dup"
	if err := svc.CreateTriggerRule(ctx, dup); err != nil {
		t.Fatalf("seed dup: %v", err)
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validTriggerRuleDTO()
			req.Code = fmt.Sprintf("code-%d", i)
			req.Name = "rule-" + tt.name
			tt.mutate(req)
			want := tt.want
			if want == "" {
				want = enums.TriggerRuleExistsEnum.GetCode()
			}
			err := svc.CreateTriggerRule(ctx, req)
			if got := errCode(t, err); got != want {
				t.Fatalf("code = %s，期望 %s（err=%v）", got, want, err)
			}
		})
	}
}

func TestUpdateTriggerRuleNameUniquenessAndStatusPreserved(t *testing.T) {
	db, reloader := newTriggerRuleTestDB(t)
	svc := NewTriggerRuleService(db, reloader, reloader)
	ctx := context.Background()

	req1 := validTriggerRuleDTO()
	req1.Name = "one"
	req1.Code = "one-code"
	if err := svc.CreateTriggerRule(ctx, req1); err != nil {
		t.Fatal(err)
	}
	req2 := validTriggerRuleDTO()
	req2.Name = "two"
	req2.Code = "two-code"
	if err := svc.CreateTriggerRule(ctx, req2); err != nil {
		t.Fatal(err)
	}

	var one po.TriggerRule
	if err := db.Where("name = ?", "one").First(&one).Error; err != nil {
		t.Fatal(err)
	}

	// 编码不可修改：传不同值必须在名称/配置校验之前被拒绝。
	immutable := validTriggerRuleDTO()
	immutable.Code = "other-code"
	err := svc.UpdateTriggerRule(ctx, &dto.UpdateTriggerRuleDTO{
		ID: one.ID, TriggerRuleConfigDTO: immutable.TriggerRuleConfigDTO,
	})
	if got := errCode(t, err); got != enums.TriggerRuleCodeImmutableEnum.GetCode() {
		t.Fatalf("改编码 code = %s，期望 %s", got, enums.TriggerRuleCodeImmutableEnum.GetCode())
	}

	cfg := validTriggerRuleDTO()
	cfg.Code = one.Code
	update := &dto.UpdateTriggerRuleDTO{ID: one.ID, TriggerRuleConfigDTO: cfg.TriggerRuleConfigDTO}
	update.Name = "two"
	err = svc.UpdateTriggerRule(ctx, update)
	if got := errCode(t, err); got != enums.TriggerRuleExistsEnum.GetCode() {
		t.Fatalf("改名撞车 code = %s，期望 %s", got, enums.TriggerRuleExistsEnum.GetCode())
	}

	// status 省略 = 保留原状态；先停用再改配置，不应被改回 1。
	zero := 0
	if err := db.Model(&po.TriggerRule{}).Where("id = ?", one.ID).Update("status", zero).Error; err != nil {
		t.Fatal(err)
	}
	update.Name = "one-renamed"
	update.Status = nil
	if err := svc.UpdateTriggerRule(ctx, update); err != nil {
		t.Fatalf("update: %v", err)
	}
	var got po.TriggerRule
	if err := db.Where("id = ?", one.ID).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != 0 || got.Name != "one-renamed" {
		t.Fatalf("status/name = %d/%q", got.Status, got.Name)
	}
}

func TestDeleteTriggerRule(t *testing.T) {
	db, reloader := newTriggerRuleTestDB(t)
	svc := NewTriggerRuleService(db, reloader, reloader)
	ctx := context.Background()

	req := validTriggerRuleDTO()
	req.Name = "to-delete"
	if err := svc.CreateTriggerRule(ctx, req); err != nil {
		t.Fatal(err)
	}
	var rule po.TriggerRule
	if err := db.Where("name = ?", "to-delete").First(&rule).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.DeleteTriggerRule(ctx, rule.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.DeleteTriggerRule(ctx, rule.ID); errCode(t, err) != enums.TriggerRuleNotExistsEnum.GetCode() {
		t.Fatalf("重复删除 code = %s", errCode(t, err))
	}
	if reloader.calls != 2 {
		t.Errorf("reload calls = %d，期望 2", reloader.calls)
	}
}

func TestRecentTriggersEnrichesNamesAndFilters(t *testing.T) {
	db, reloader := newTriggerRuleTestDB(t)
	svc := NewTriggerRuleService(db, reloader, reloader)
	ctx := context.Background()

	reloader.events = []triggerrule.RecentTrigger{
		{
			Seq: 2, TriggerID: "t2", RuleID: "r1", RuleName: "rule-A",
			FiredAt: time.Date(2026, 11, 3, 12, 0, 0, 0, time.Local),
			Status:  triggerrule.StatusSuccess, DurationMs: 12,
			ActionDeviceID: "dev-1", ChannelID: "c1",
			Records: []triggerrule.RecentActionRecord{{
				AddressID: "a-action", AddressName: "A1", Value: "7",
				Kind: "float", Quality: 0,
			}},
		},
		{
			Seq: 1, TriggerID: "t1", RuleID: "r2", RuleName: "rule-B",
			Status: triggerrule.StatusOffline, ActionDeviceID: "dev-2", ChannelID: "c2",
		},
	}

	got := svc.RecentTriggers(ctx, "", 100)
	if len(got) != 2 {
		t.Fatalf("recent = %d，期望 2", len(got))
	}
	if got[0].ActionDeviceName != "dev1" || got[0].ChannelName != "ch1" {
		t.Fatalf("名称补全 = %q/%q", got[0].ActionDeviceName, got[0].ChannelName)
	}
	if got[0].FiredAt != "2026-11-03 12:00:00.000" {
		t.Errorf("firedAt = %q", got[0].FiredAt)
	}
	if len(got[0].Records) != 1 || got[0].Records[0].Value != "7" {
		t.Errorf("records = %+v", got[0].Records)
	}

	if only := svc.RecentTriggers(ctx, "r1", 100); len(only) != 1 || only[0].TriggerID != "t2" {
		t.Fatalf("按规则过滤 = %+v", only)
	}
	if none := svc.RecentTriggers(ctx, "missing", 100); len(none) != 0 {
		t.Fatalf("不存在规则应返回空，实际 %d", len(none))
	}
}
